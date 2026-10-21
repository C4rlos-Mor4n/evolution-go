package meowcaller

import (
	"strconv"
	"sync/atomic"
	"time"
)

// KRIONIX PATCH: race-free phase transitions for inbound calls with early media.
//
// With early media the media goroutine (first decoded audio) and Answer (API goroutine) can
// both move an inbound call's phase. Plain setPhase let Answer's Connecting overwrite an
// Active set a moment earlier by the media goroutine, and OnReady could be skipped or doubled.
// These helpers make every transition a compare-and-set under the call lock.

// advancePhase moves the call to next only if its current phase is one of from. It reports
// whether the phase changed, and notifies OnStateChange outside the lock when it did.
func (c *Call) advancePhase(next CallPhase, from ...CallPhase) bool {
	c.mu.Lock()
	allowed := false
	for _, p := range from {
		if c.phase == p {
			allowed = true
			break
		}
	}
	if !allowed || c.phase == next {
		c.mu.Unlock()
		return false
	}
	c.phase = next
	fn := c.onState
	c.mu.Unlock()
	if fn != nil {
		fn(next)
	}
	return true
}

// promoteActive marks the call Active (never from Ended) and fires OnReady exactly once.
// Safe to call from both the media goroutine and Answer.
func (c *Call) promoteActive() {
	c.mu.Lock()
	if c.phase == CallPhaseEnded {
		c.mu.Unlock()
		return
	}
	changed := c.phase != CallPhaseActive
	c.phase = CallPhaseActive
	stateFn := c.onState
	var readyFn func()
	if !c.readyNotified {
		c.readyNotified = true
		readyFn = c.onReady
	}
	c.mu.Unlock()
	if changed && stateFn != nil {
		stateFn(CallPhaseActive)
	}
	if readyFn != nil {
		readyFn()
	}
}

// preLivePhases are the phases a call can be moved to Connecting from (never back from
// Active or Ended).
var preLivePhases = []CallPhase{CallPhaseIdle, CallPhaseCalling, CallPhaseRinging, CallPhaseWaitingRoom}

// audioAllowed reports whether decoded inbound audio may reach the call's sink: always for
// outgoing calls; for inbound calls only after Answer. Audio of a call that is still ringing
// (early media) is decoded only to detect that media flows, and is never played, buffered
// for later playout or exposed to the agent.
func (e *engine) audioAllowed(callID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.calls[callID]
	return m == nil || m.direction != CallDirectionIncoming || m.answered
}

const mediaStatsInterval = 10 * time.Second

// mediaStats are count-only media counters (KRIONIX PATCH). No payloads, no identifiers.
type mediaStats struct {
	rtpTx, rtpRx, audioDecoded, decryptFailed, unknownPayload, preAnswerDropped atomic.Uint64
	lastUnknownPT                                                               atomic.Uint32
}

func (e *engine) emitMediaStats(callID, kind string, s *mediaStats, relayRx uint64) {
	e.mark(callID, "media_stats",
		"kind", kind,
		"relay_rx", strconv.FormatUint(relayRx, 10),
		"rtp_tx", strconv.FormatUint(s.rtpTx.Load(), 10),
		"rtp_rx", strconv.FormatUint(s.rtpRx.Load(), 10),
		"audio_decoded", strconv.FormatUint(s.audioDecoded.Load(), 10),
		"pre_answer_dropped", strconv.FormatUint(s.preAnswerDropped.Load(), 10),
		"decrypt_failed", strconv.FormatUint(s.decryptFailed.Load(), 10),
		"unknown_payload", strconv.FormatUint(s.unknownPayload.Load(), 10),
		"last_unknown_pt", strconv.FormatUint(uint64(s.lastUnknownPT.Load()), 10),
	)
}
