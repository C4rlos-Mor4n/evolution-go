package whatsmeow_service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/purpshell/meowcaller"
	"go.mau.fi/whatsmeow"
)

// CallRegistry is the process-wide registry of answerable inbound calls.
type CallRegistry = call_registry.Registry[*meowcaller.Call]

// NewCallRegistry returns an empty call registry.
func NewCallRegistry() *CallRegistry {
	return call_registry.New[*meowcaller.Call]()
}

const (
	// callRingTTL drops calls that were never answered (their terminate stanza
	// was lost). WhatsApp stops ringing after ~60s; with early media an unanswered
	// call keeps a relay session open, so keep this bound tight.
	callRingTTL = 90 * time.Second
	// callMaxDuration hangs up answered calls that are still registered after
	// this long; it is only a safety net against leaked calls.
	callMaxDuration = 6 * time.Hour
	// callTeardownWait bounds how long a runtime teardown waits for its calls'
	// hangups before moving on (hangup sends a stanza; media is always torn
	// down locally first).
	callTeardownWait = 2 * time.Second
)

// attachCalling constructs this runtime's meowcaller client when both
// CALLS_ENABLED=true and the instance has calling enabled (callsEnabled, off by
// default). Instances without it never get meowcaller: no preaccept, no early
// media, WhatsApp keeps its normal call behaviour on the account's other devices.
// It must run after whatsmeow.NewClient and before client.Connect (meowcaller
// installs its raw <call>/<ack> node hooks then).
//
// The returned release function (idempotent) unregisters the incoming-call
// listener, removes every call this runtime registered (closing their media
// streams) and hangs them up. StartClient calls it on every exit path of the
// runtime, so a replaced/killed runtime never leaves calls, streams or media
// goroutines behind. When calls are disabled nothing is constructed and the
// release is a no-op.
func (w whatsmeowService) attachCalling(instance *instance_model.Instance, client *whatsmeow.Client) func() {
	if w.config == nil || !w.config.CallsEnabled || w.callRegistry == nil || instance == nil {
		return func() {}
	}
	instanceID := instance.Id
	if w.callingGate != nil {
		w.callingGate.Set(instanceID, instance.CallsEnabled)
	}
	if !instance.CallsEnabled {
		return func() {}
	}
	registry := w.callRegistry
	// Sanitized logger (identifiers stripped, warn by default, never debug): without it
	// meowcaller's setup/media errors were invisible.
	mc := meowcaller.NewClient(client,
		meowcaller.WithLogger(meowcallerLogger(w.config.CallsLogLevel)),
		meowcaller.WithMilestones(w.callMilestoneLogger(instanceID)),
		meowcaller.WithEarlyMedia(w.config.CallsEarlyMedia),
		// Disabling calling for the instance takes effect on the next offer, without
		// waiting for the reconnect that detaches meowcaller.
		meowcaller.WithOfferGate(func(string) bool { return w.callingAllowed(instanceID) }),
	)
	owner := mc
	registry.OpenOwner(owner)
	w.callingAttached.attach(instanceID, owner)

	mc.OnIncomingCall(func(call *meowcaller.Call) {
		callID := call.ID()
		// Instances with "reject calls": meowcaller already pre-accepted the
		// offer, so reject through it (which also ends the call inside
		// meowcaller instead of leaving it there until a terminate that may
		// never come). myEventHandler, which runs right after this handler,
		// then skips its own whatsmeow reject but still sends the reject message.
		if mycli := w.myClientPointer.Get(instanceID); mycli != nil && mycli.Instance != nil && mycli.Instance.RejectCall {
			if err := call.Reject(); err == nil {
				w.markRejectedByMeowcaller(instanceID, callID)
			}
			return
		}
		// Register the end listener before storing, then re-check the state,
		// so a call that ends concurrently can never stay registered.
		call.OnEnd(func(reason string) {
			registry.RemoveCall(instanceID, callID, call, "call_ended")
		})
		if err := registry.Store(owner, instanceID, callID, call); err != nil {
			// Not answerable through the API (runtime closing, or too many
			// calls): decline it so it does not linger inside meowcaller.
			w.loggerWrapper.GetLogger(instanceID).LogWarn("[%s] incoming call %s not registered, rejecting: %v", instanceID, callID, err)
			_ = call.Reject()
			return
		}
		if call.State() == meowcaller.CallPhaseEnded {
			registry.RemoveCall(instanceID, callID, call, "call_ended")
			return
		}
		w.loggerWrapper.GetLogger(instanceID).LogInfo("[%s] incoming call %s registered (video=%v)", instanceID, callID, call.IsVideo())
		// Lifecycle diagnostics (phases only, no identifiers/media): shows whether an
		// answered call ever reached "active" (media flowing) or stalled in "connecting".
		started := time.Now()
		call.OnStateChange(func(p meowcaller.CallPhase) {
			w.loggerWrapper.GetLogger(instanceID).LogInfo("[%s] call %s phase=%s after=%s", instanceID, callID, callPhaseName(p), time.Since(started).Round(time.Millisecond))
		})
		call.OnReady(func() {
			w.loggerWrapper.GetLogger(instanceID).LogInfo("[%s] call %s media ready after=%s", instanceID, callID, time.Since(started).Round(time.Millisecond))
		})
	})

	var once sync.Once
	return func() {
		once.Do(func() {
			w.callingAttached.detach(instanceID, owner)
			mc.OnIncomingCall(nil)
			calls := registry.CloseOwner(owner, "runtime_closed")
			if len(calls) == 0 {
				return
			}
			w.loggerWrapper.GetLogger(instanceID).LogInfo("[%s] closing %d call(s) of the stopped runtime", instanceID, len(calls))
			hangupCalls(calls, callTeardownWait)
		})
	}
}

// callingAllowed is the offer gate: the persisted per-instance switch as last
// set on this process (attach or admin toggle).
func (w whatsmeowService) callingAllowed(instanceID string) bool {
	if w.config == nil || !w.config.CallsEnabled || w.callingGate == nil {
		return false
	}
	return w.callingGate.Get(instanceID)
}

// SetCallingGate updates the per-instance switch used by the offer gate. Turning it
// off stops meowcaller from engaging new offers immediately; turning it on only
// matters once a reconnect has attached meowcaller to the instance.
func (w whatsmeowService) SetCallingGate(instanceID string, enabled bool) {
	if w.callingGate != nil {
		w.callingGate.Set(instanceID, enabled)
	}
}

// CallingRuntimeState is the effective calling state of one instance on this process.
type CallingRuntimeState struct {
	// Runtime reports whether the instance has a live WhatsApp runtime.
	Runtime bool
	// Attached reports whether meowcaller is installed on that runtime.
	Attached bool
	// ActiveCalls counts calls currently registered for the instance.
	ActiveCalls int
}

// CallingState reports the instance's runtime calling state.
func (w whatsmeowService) CallingState(instanceID string) CallingRuntimeState {
	st := CallingRuntimeState{}
	if w.myClientPointer != nil {
		_, st.Runtime = w.myClientPointer.Lookup(instanceID)
	}
	st.Attached = w.callingAttached.attached(instanceID)
	if w.callRegistry != nil {
		st.ActiveCalls = w.callRegistry.Count(instanceID)
	}
	return st
}

// callingAttachments tracks the meowcaller owner installed per instance, so a
// replaced runtime's release never clears the attachment of its successor.
type callingAttachments struct {
	mu     sync.Mutex
	owners map[string]any
}

func newCallingAttachments() *callingAttachments {
	return &callingAttachments{owners: map[string]any{}}
}

func (a *callingAttachments) attach(instanceID string, owner any) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.owners[instanceID] = owner
	a.mu.Unlock()
}

func (a *callingAttachments) detach(instanceID string, owner any) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.owners[instanceID] == owner {
		delete(a.owners, instanceID)
	}
	a.mu.Unlock()
}

func (a *callingAttachments) attached(instanceID string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.owners[instanceID]
	return ok
}

// markRejectedByMeowcaller records that an auto-rejected offer was already
// rejected through meowcaller (consumed by myEventHandler).
func (w whatsmeowService) markRejectedByMeowcaller(instanceID, callID string) {
	if w.meowRejected != nil {
		w.meowRejected.Store(instanceID+"/"+callID, struct{}{})
	}
}

// takeRejectedByMeowcaller reports (and forgets) whether the offer was already
// rejected through meowcaller.
func (w *whatsmeowService) takeRejectedByMeowcaller(instanceID, callID string) bool {
	if w == nil || w.meowRejected == nil {
		return false
	}
	_, ok := w.meowRejected.LoadAndDelete(instanceID + "/" + callID)
	return ok
}

// hangupCalls ends calls in the background, waiting at most wait for them.
func hangupCalls(calls []*meowcaller.Call, wait time.Duration) {
	endCalls(calls, wait, (*meowcaller.Call).Hangup)
}

// rejectCalls declines ringing calls in the background, waiting at most wait.
func rejectCalls(calls []*meowcaller.Call, wait time.Duration) {
	endCalls(calls, wait, (*meowcaller.Call).Reject)
}

func endCalls(calls []*meowcaller.Call, wait time.Duration, end func(*meowcaller.Call) error) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, c := range calls {
			if c != nil && c.State() != meowcaller.CallPhaseEnded {
				_ = end(c)
			}
		}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// RunCallJanitor periodically expires calls whose end was never observed.
// It returns when ctx is cancelled. Only started when CALLS_ENABLED=true.
func RunCallJanitor(ctx context.Context, registry *CallRegistry, interval time.Duration) {
	if registry == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var answered, ringing []*meowcaller.Call
			for _, e := range registry.Expire(callRingTTL, callMaxDuration) {
				if e.Answered {
					answered = append(answered, e.Call)
				} else {
					ringing = append(ringing, e.Call)
				}
			}
			if len(answered) > 0 {
				hangupCalls(answered, callTeardownWait)
			}
			if len(ringing) > 0 {
				// Ends them inside meowcaller too (its state would otherwise
				// stay until a terminate stanza that evidently never came).
				rejectCalls(ringing, callTeardownWait)
			}
		}
	}
}

// callPhaseName renders a meowcaller call phase for logs.
func callPhaseName(p meowcaller.CallPhase) string {
	switch p {
	case meowcaller.CallPhaseIdle:
		return "idle"
	case meowcaller.CallPhaseCalling:
		return "calling"
	case meowcaller.CallPhaseRinging:
		return "ringing"
	case meowcaller.CallPhaseConnecting:
		return "connecting"
	case meowcaller.CallPhaseActive:
		return "active"
	case meowcaller.CallPhaseEnded:
		return "ended"
	case meowcaller.CallPhaseWaitingRoom:
		return "waiting_room"
	}
	return "unknown"
}

// callMilestoneLogger logs meowcaller's sanitized call milestones at INFO, independent of
// CALLS_LOG_LEVEL, with the time elapsed since the offer. They separate the failure classes
// of a call: signaling (preaccept/answer/mute_v2/accept), relay transport (connect, first
// bytes, silent) and media (first RTP sent/received, decrypt failure, first decoded audio).
// Attributes never contain identifiers, keys, addresses of the people on the call or media.
func (w whatsmeowService) callMilestoneLogger(instanceID string) meowcaller.MilestoneFunc {
	var starts sync.Map // callID -> time.Time (first milestone)
	return func(callID, name string, attrs map[string]string) {
		now := time.Now()
		first, _ := starts.LoadOrStore(callID, now)
		elapsed := now.Sub(first.(time.Time)).Round(time.Millisecond)
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			v := strings.ReplaceAll(attrs[k], "\n", " ")
			if len(v) > 160 {
				v = v[:160] + "..."
			}
			fmt.Fprintf(&b, " %s=%q", k, v)
		}
		w.loggerWrapper.GetLogger(instanceID).LogInfo("[%s] call %s milestone=%s t+%s%s", instanceID, callID, name, elapsed, b.String())
		if name == "media_ended" {
			starts.Delete(callID)
		}
	}
}
