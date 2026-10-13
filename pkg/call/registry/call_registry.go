// Package call_registry tracks the inbound WhatsApp calls that can be answered,
// rejected, hung up or streamed through the API.
//
// Every entry is keyed by (instanceID, callID) and tagged with the runtime
// ("owner") that captured it, so:
//
//   - a lookup authenticated as instance B can never see a call of instance A,
//     even if both somehow carried the same call ID;
//   - when an instance runtime is torn down (disconnect, logout, delete,
//     reconnect, replacement by a newer runtime) every call it owned is removed
//     in one step with CloseOwner, and a late offer delivered to that dead
//     runtime can no longer be registered;
//   - at most one media stream consumer can be attached to a call at a time.
//
// The registry is written from the whatsmeow event goroutines and read from the
// HTTP handlers, so every access is mutex-guarded. Callbacks (stream detach
// functions) are always run outside the lock.
package call_registry

import (
	"errors"
	"sync"
	"time"
)

// MaxCallsPerInstance bounds how many calls a single instance can have
// registered at once. WhatsApp only rings one device a handful of times in
// parallel; the cap only exists so a flood of offers cannot grow memory
// without bound.
const MaxCallsPerInstance = 32

var (
	// ErrNotFound is returned when no call with that ID is registered for the
	// requesting instance (including when it belongs to another instance).
	ErrNotFound = errors.New("no call with that id for this instance")
	// ErrStreamBusy is returned when a stream consumer is already attached.
	ErrStreamBusy = errors.New("a stream consumer is already attached to this call")
	// ErrOwnerClosed is returned when the runtime that captured the call has
	// already been torn down.
	ErrOwnerClosed = errors.New("call runtime is closed")
	// ErrLimit is returned when the instance already has MaxCallsPerInstance calls.
	ErrLimit = errors.New("too many registered calls for this instance")
)

type key struct {
	instanceID string
	callID     string
}

type entry[C comparable] struct {
	owner      any
	call       C
	createdAt  time.Time
	answeredAt time.Time
	// streamSeq identifies the attached stream (0 = none); detach is that
	// stream's non-blocking close function.
	streamSeq uint64
	detach    func(reason string)
}

// Registry is a concurrency-safe call registry. C is the call handle type
// (*meowcaller.Call in production, a fake in tests).
type Registry[C comparable] struct {
	mu      sync.Mutex
	entries map[key]*entry[C]
	owners  map[any]struct{}
	seq     uint64
	now     func() time.Time
}

// New returns an empty registry.
func New[C comparable]() *Registry[C] {
	return &Registry[C]{
		entries: make(map[key]*entry[C]),
		owners:  make(map[any]struct{}),
		now:     time.Now,
	}
}

// OpenOwner marks owner (a runtime token, e.g. the per-runtime meowcaller
// client) as live, so calls can be stored under it.
func (r *Registry[C]) OpenOwner(owner any) {
	r.mu.Lock()
	r.owners[owner] = struct{}{}
	r.mu.Unlock()
}

// Store registers call for instanceID under owner. It fails if owner is not
// open (its runtime has already been torn down) or the instance is at its cap.
//
// Storing the same call again is a no-op. Storing a DIFFERENT call under an
// existing key (a re-delivered offer makes meowcaller replace its call object)
// replaces the entry: the stream attached to the stale call is detached with
// reason "replaced" (outside the lock), since that call no longer receives
// media or end notifications.
func (r *Registry[C]) Store(owner any, instanceID, callID string, call C) error {
	if instanceID == "" || callID == "" {
		return ErrNotFound
	}
	r.mu.Lock()
	if _, ok := r.owners[owner]; !ok {
		r.mu.Unlock()
		return ErrOwnerClosed
	}
	k := key{instanceID, callID}
	old, exists := r.entries[k]
	if exists && old.call == call && old.owner == owner {
		r.mu.Unlock()
		return nil
	}
	if !exists && r.countLocked(instanceID) >= MaxCallsPerInstance {
		r.mu.Unlock()
		return ErrLimit
	}
	r.entries[k] = &entry[C]{owner: owner, call: call, createdAt: r.now()}
	var detach func(string)
	if exists {
		detach = old.detach
	}
	r.mu.Unlock()
	if detach != nil {
		detach("replaced")
	}
	return nil
}

func (r *Registry[C]) countLocked(instanceID string) int {
	n := 0
	for k := range r.entries {
		if k.instanceID == instanceID {
			n++
		}
	}
	return n
}

// Get returns the call registered as callID for instanceID only.
func (r *Registry[C]) Get(instanceID, callID string) (C, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key{instanceID, callID}]
	if !ok {
		var zero C
		return zero, false
	}
	return e.call, true
}

// MarkAnswered records that the call was answered (used by Expire).
func (r *Registry[C]) MarkAnswered(instanceID, callID string) {
	r.mu.Lock()
	if e, ok := r.entries[key{instanceID, callID}]; ok && e.answeredAt.IsZero() {
		e.answeredAt = r.now()
	}
	r.mu.Unlock()
}

// Remove deletes the entry for (instanceID, callID) and closes its attached
// stream, if any. It returns the removed call.
func (r *Registry[C]) Remove(instanceID, callID, reason string) (C, bool) {
	r.mu.Lock()
	e, ok := r.entries[key{instanceID, callID}]
	if ok {
		delete(r.entries, key{instanceID, callID})
	}
	r.mu.Unlock()
	if !ok {
		var zero C
		return zero, false
	}
	if e.detach != nil {
		e.detach(reason)
	}
	return e.call, true
}

// RemoveCall deletes the entry only if it still holds exactly call (so a
// late end-of-call callback can never remove a newer entry with the same key).
func (r *Registry[C]) RemoveCall(instanceID, callID string, call C, reason string) bool {
	r.mu.Lock()
	k := key{instanceID, callID}
	e, ok := r.entries[k]
	if ok && e.call == call {
		delete(r.entries, k)
	} else {
		ok = false
	}
	r.mu.Unlock()
	if ok && e.detach != nil {
		e.detach(reason)
	}
	return ok
}

// AttachStream claims the single stream slot of a call. detach is invoked
// (outside the lock, must not block) when the call is removed from the
// registry while the stream is attached. The returned release function frees
// the slot again; it is idempotent and a no-op once the entry is gone.
func (r *Registry[C]) AttachStream(instanceID, callID string, detach func(reason string)) (C, func(), error) {
	var zero C
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key{instanceID, callID}]
	if !ok {
		return zero, nil, ErrNotFound
	}
	if e.streamSeq != 0 {
		return zero, nil, ErrStreamBusy
	}
	r.seq++
	seq := r.seq
	e.streamSeq = seq
	e.detach = detach
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if cur, ok := r.entries[key{instanceID, callID}]; ok && cur.streamSeq == seq {
				cur.streamSeq = 0
				cur.detach = nil
			}
		})
	}
	return e.call, release, nil
}

// CloseOwner marks owner as closed and removes every call it registered,
// closing their streams. The removed calls are returned so the caller can end
// them (hang up) outside any lock.
func (r *Registry[C]) CloseOwner(owner any, reason string) []C {
	r.mu.Lock()
	delete(r.owners, owner)
	var calls []C
	var detaches []func(string)
	for k, e := range r.entries {
		if e.owner != owner {
			continue
		}
		delete(r.entries, k)
		calls = append(calls, e.call)
		if e.detach != nil {
			detaches = append(detaches, e.detach)
		}
	}
	r.mu.Unlock()
	for _, d := range detaches {
		d(reason)
	}
	return calls
}

// Expired is a call removed by Expire.
type Expired[C comparable] struct {
	InstanceID string
	CallID     string
	Call       C
	Answered   bool
}

// Expire removes calls that were never answered within ringTTL, and answered
// calls older than maxAge (0 disables that limit). It is the safety net for
// calls whose terminate stanza was lost; normal calls leave the registry
// through their end callback long before.
func (r *Registry[C]) Expire(ringTTL, maxAge time.Duration) []Expired[C] {
	now := r.now()
	r.mu.Lock()
	var out []Expired[C]
	var detaches []func(string)
	for k, e := range r.entries {
		answered := !e.answeredAt.IsZero()
		stale := (!answered && now.Sub(e.createdAt) > ringTTL) ||
			(answered && maxAge > 0 && now.Sub(e.answeredAt) > maxAge)
		if !stale {
			continue
		}
		delete(r.entries, k)
		out = append(out, Expired[C]{InstanceID: k.instanceID, CallID: k.callID, Call: e.call, Answered: answered})
		if e.detach != nil {
			detaches = append(detaches, e.detach)
		}
	}
	r.mu.Unlock()
	for _, d := range detaches {
		d("expired")
	}
	return out
}

// Len returns the number of registered calls.
func (r *Registry[C]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// OwnerOpen reports whether owner is currently open.
func (r *Registry[C]) OwnerOpen(owner any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.owners[owner]
	return ok
}

// OpenOwners returns how many runtimes are currently open (diagnostics/tests).
func (r *Registry[C]) OpenOwners() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.owners)
}
