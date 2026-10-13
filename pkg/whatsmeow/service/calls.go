package whatsmeow_service

import (
	"context"
	"sync"
	"time"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
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
	// was lost). WhatsApp stops ringing well before this.
	callRingTTL = 3 * time.Minute
	// callMaxDuration hangs up answered calls that are still registered after
	// this long; it is only a safety net against leaked calls.
	callMaxDuration = 6 * time.Hour
	// callTeardownWait bounds how long a runtime teardown waits for its calls'
	// hangups before moving on (hangup sends a stanza; media is always torn
	// down locally first).
	callTeardownWait = 2 * time.Second
)

// attachCalling constructs this runtime's meowcaller client when
// CALLS_ENABLED=true. It must run after whatsmeow.NewClient and before
// client.Connect (meowcaller installs its raw <call>/<ack> node hooks then).
//
// The returned release function (idempotent) unregisters the incoming-call
// listener, removes every call this runtime registered (closing their media
// streams) and hangs them up. StartClient calls it on every exit path of the
// runtime, so a replaced/killed runtime never leaves calls, streams or media
// goroutines behind. When calls are disabled nothing is constructed and the
// release is a no-op.
func (w whatsmeowService) attachCalling(instanceID string, client *whatsmeow.Client) func() {
	if w.config == nil || !w.config.CallsEnabled || w.callRegistry == nil {
		return func() {}
	}
	registry := w.callRegistry
	mc := meowcaller.NewClient(client) // no WithLogger: the library stays silent (it can log key material at debug)
	owner := mc
	registry.OpenOwner(owner)

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
	})

	var once sync.Once
	return func() {
		once.Do(func() {
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
