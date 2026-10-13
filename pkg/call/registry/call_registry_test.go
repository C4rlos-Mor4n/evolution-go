package call_registry

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCall struct{ id string }

func newReg() *Registry[*fakeCall] { return New[*fakeCall]() }

func TestStoreAndGetSameInstance(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	c := &fakeCall{"c1"}
	if err := r.Store(owner, "inst-a", "c1", c); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("inst-a", "c1")
	if !ok || got != c {
		t.Fatal("expected the stored call back")
	}
}

func TestCrossInstanceIsolation(t *testing.T) {
	r := newReg()
	ownerA, ownerB := new(int), new(int)
	r.OpenOwner(ownerA)
	r.OpenOwner(ownerB)
	a := &fakeCall{"shared"}
	b := &fakeCall{"shared"}
	if err := r.Store(ownerA, "inst-a", "shared", a); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("inst-b", "shared"); ok {
		t.Fatal("instance B must not see instance A's call")
	}
	if _, _, err := r.AttachStream("inst-b", "shared", func(string) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("instance B must not attach to instance A's call, got %v", err)
	}
	if _, ok := r.Remove("inst-b", "shared", "x"); ok {
		t.Fatal("instance B must not remove instance A's call")
	}
	// Same call ID under another instance is a separate entry, not an overwrite.
	if err := r.Store(ownerB, "inst-b", "shared", b); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get("inst-a", "shared"); got != a {
		t.Fatal("instance A's entry was overwritten by instance B")
	}
	if got, _ := r.Get("inst-b", "shared"); got != b {
		t.Fatal("instance B's entry missing")
	}
	// Closing B's runtime leaves A untouched.
	if calls := r.CloseOwner(ownerB, "x"); len(calls) != 1 || calls[0] != b {
		t.Fatalf("CloseOwner(B) returned %v", calls)
	}
	if _, ok := r.Get("inst-a", "shared"); !ok {
		t.Fatal("closing B removed A's call")
	}
}

func TestStoreRejectsClosedOrUnknownOwner(t *testing.T) {
	r := newReg()
	owner := new(int)
	if err := r.Store(owner, "i", "c", &fakeCall{"c"}); !errors.Is(err, ErrOwnerClosed) {
		t.Fatalf("store under a never-opened owner: %v", err)
	}
	r.OpenOwner(owner)
	r.CloseOwner(owner, "x")
	if err := r.Store(owner, "i", "c", &fakeCall{"c"}); !errors.Is(err, ErrOwnerClosed) {
		t.Fatalf("store after CloseOwner: %v", err)
	}
	if r.Len() != 0 {
		t.Fatal("nothing should be registered")
	}
}

func TestStoreRejectsEmptyIDs(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	if err := r.Store(owner, "", "c", &fakeCall{}); err == nil {
		t.Fatal("empty instance id accepted")
	}
	if err := r.Store(owner, "i", "", &fakeCall{}); err == nil {
		t.Fatal("empty call id accepted")
	}
}

func TestPerInstanceLimit(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	for i := 0; i < MaxCallsPerInstance; i++ {
		id := fmt.Sprintf("c%d", i)
		if err := r.Store(owner, "i", id, &fakeCall{id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Store(owner, "i", "overflow", &fakeCall{"overflow"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("expected ErrLimit, got %v", err)
	}
	// Another instance is not affected by this one's cap.
	if err := r.Store(owner, "j", "c0", &fakeCall{"c0"}); err != nil {
		t.Fatal(err)
	}
}

func TestSingleStreamConsumer(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	c := &fakeCall{"c"}
	_ = r.Store(owner, "i", "c", c)

	got, release, err := r.AttachStream("i", "c", func(string) {})
	if err != nil || got != c {
		t.Fatalf("first attach: %v", err)
	}
	if _, _, err := r.AttachStream("i", "c", func(string) {}); !errors.Is(err, ErrStreamBusy) {
		t.Fatalf("second attach must be rejected, got %v", err)
	}
	release()
	release() // idempotent
	_, release2, err := r.AttachStream("i", "c", func(string) {})
	if err != nil {
		t.Fatalf("attach after release: %v", err)
	}
	// A stale release from the first stream must not free the second one.
	release()
	if _, _, err := r.AttachStream("i", "c", func(string) {}); !errors.Is(err, ErrStreamBusy) {
		t.Fatal("stale release freed the slot of a newer stream")
	}
	release2()
}

func TestRemoveDetachesStream(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	c := &fakeCall{"c"}
	_ = r.Store(owner, "i", "c", c)
	var reason string
	_, release, _ := r.AttachStream("i", "c", func(why string) { reason = why })
	if !r.RemoveCall("i", "c", c, "terminated") {
		t.Fatal("RemoveCall failed")
	}
	if reason != "terminated" {
		t.Fatalf("detach not called with reason, got %q", reason)
	}
	release() // no-op on a removed entry
	if r.Len() != 0 {
		t.Fatal("entry still present")
	}
}

func TestRemoveCallIgnoresDifferentCall(t *testing.T) {
	r := newReg()
	owner := new(int)
	r.OpenOwner(owner)
	old, newer := &fakeCall{"c"}, &fakeCall{"c"}
	_ = r.Store(owner, "i", "c", newer)
	if r.RemoveCall("i", "c", old, "x") {
		t.Fatal("stale end callback removed a newer entry")
	}
	if _, ok := r.Get("i", "c"); !ok {
		t.Fatal("newer entry gone")
	}
}

func TestCloseOwnerRemovesOnlyItsCallsAndDetaches(t *testing.T) {
	r := newReg()
	oldRuntime, newRuntime := new(int), new(int)
	r.OpenOwner(oldRuntime)
	r.OpenOwner(newRuntime)
	_ = r.Store(oldRuntime, "i", "a", &fakeCall{"a"})
	_ = r.Store(oldRuntime, "i", "b", &fakeCall{"b"})
	_ = r.Store(newRuntime, "i", "c", &fakeCall{"c"})
	var detached atomic.Int32
	_, _, _ = r.AttachStream("i", "a", func(string) { detached.Add(1) })

	calls := r.CloseOwner(oldRuntime, "runtime closed")
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls from the old runtime, got %d", len(calls))
	}
	if detached.Load() != 1 {
		t.Fatal("stream of a closed runtime's call was not detached")
	}
	if _, ok := r.Get("i", "c"); !ok {
		t.Fatal("new runtime's call was removed")
	}
	if r.OwnerOpen(oldRuntime) || !r.OwnerOpen(newRuntime) {
		t.Fatal("owner open state wrong")
	}
}

func TestExpire(t *testing.T) {
	r := newReg()
	now := time.Unix(1000, 0)
	r.now = func() time.Time { return now }
	owner := new(int)
	r.OpenOwner(owner)
	_ = r.Store(owner, "i", "ringing", &fakeCall{"ringing"})
	_ = r.Store(owner, "i", "answered", &fakeCall{"answered"})
	r.MarkAnswered("i", "answered")

	now = now.Add(2 * time.Minute)
	if out := r.Expire(3*time.Minute, time.Hour); len(out) != 0 {
		t.Fatalf("nothing should expire yet, got %d", len(out))
	}
	now = now.Add(2 * time.Minute)
	out := r.Expire(3*time.Minute, time.Hour)
	if len(out) != 1 || out[0].CallID != "ringing" || out[0].Answered {
		t.Fatalf("expected only the unanswered call to expire, got %+v", out)
	}
	now = now.Add(time.Hour)
	out = r.Expire(3*time.Minute, time.Hour)
	if len(out) != 1 || out[0].CallID != "answered" || !out[0].Answered {
		t.Fatalf("expected the long answered call to expire, got %+v", out)
	}
	if r.Len() != 0 {
		t.Fatal("registry should be empty")
	}
}

// TestConcurrentAccess is meant for `go test -race`.
func TestConcurrentAccess(t *testing.T) {
	r := newReg()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			owner := new(int)
			r.OpenOwner(owner)
			inst := fmt.Sprintf("inst-%d", w%3)
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-c%d", w, i)
				c := &fakeCall{id}
				_ = r.Store(owner, inst, id, c)
				_, _ = r.Get(inst, id)
				if _, rel, err := r.AttachStream(inst, id, func(string) {}); err == nil {
					rel()
				}
				r.MarkAnswered(inst, id)
				if i%2 == 0 {
					r.RemoveCall(inst, id, c, "x")
				}
				_ = r.Expire(time.Hour, 0)
			}
			r.CloseOwner(owner, "done")
		}(w)
	}
	wg.Wait()
	if r.Len() != 0 {
		t.Fatalf("all owners closed, registry should be empty, has %d", r.Len())
	}
}
