package meowcaller

// KRIONIX PATCH tests: the callee <accept> must be sent even when the call is answered
// after the caller's first <mute_v2> (human answer) or when no <mute_v2> arrives.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

type sentNodes struct {
	mu    sync.Mutex
	nodes []waBinary.Node
}

func (s *sentNodes) accepts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, node := range s.nodes {
		for _, kid := range node.GetChildren() {
			if kid.Tag == "accept" {
				n++
			}
		}
	}
	return n
}

var (
	testCaller  = types.NewJID("593900000001", types.DefaultUserServer)
	testDevice  = types.JID{User: "593900000001", Device: 0, Server: types.DefaultUserServer}
	testCallID  = "CALLTEST01"
	silentLogFn = zerolog.Nop()
)

func incomingTestEngine(t *testing.T, relayKnown bool) (*engine, *Call, *sentNodes) {
	t.Helper()
	sent := &sentNodes{}
	eng := &engine{c: &Client{log: silentLogFn}, calls: map[string]*engineCall{}}
	eng.sendCallNode = func(_ context.Context, node waBinary.Node) error {
		sent.mu.Lock()
		sent.nodes = append(sent.nodes, node)
		sent.mu.Unlock()
		return nil
	}
	call := &Call{eng: eng, id: testCallID, peer: testCaller, phase: CallPhaseRinging}
	m := eng.entry(testCallID)
	m.call = call
	m.callKey = make([]byte, 32)
	m.direction = CallDirectionIncoming
	m.from = testDevice
	m.creator = testCaller
	m.started = true // keep maybeStartMedia from opening a real relay connection
	if relayKnown {
		m.relay = &relayData{}
	}
	return eng, call, sent
}

func muteV2Node(muted string) *waBinary.Node {
	return &waBinary.Node{
		Tag:   "call",
		Attrs: waBinary.Attrs{"from": testDevice, "id": "M1"},
		Content: []waBinary.Node{{
			Tag:   "mute_v2",
			Attrs: waBinary.Attrs{"call-id": testCallID, "call-creator": testCaller, "mute-state": muted},
		}},
	}
}

func shortFallback(t *testing.T) {
	t.Helper()
	prevDelay, prevTimeout := acceptFallbackDelay, acceptFallbackTimeout
	acceptFallbackDelay, acceptFallbackTimeout = 20*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { acceptFallbackDelay, acceptFallbackTimeout = prevDelay, prevTimeout })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The bug: mute_v2 arrives ~1s after the offer, the human answers later -> no accept ever.
func TestAnswerAfterMuteV2SendsAcceptImmediately(t *testing.T) {
	shortFallback(t)
	eng, call, sent := incomingTestEngine(t, true)
	eng.onCallRaw(muteV2Node("0")) // before Answer: not pending, must be remembered
	if got := sent.accepts(); got != 0 {
		t.Fatalf("accept sent before answer: %d", got)
	}
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	if got := sent.accepts(); got != 1 {
		t.Fatalf("want 1 accept right after answer, got %d", got)
	}
	time.Sleep(60 * time.Millisecond) // the fallback must not send a second one
	if got := sent.accepts(); got != 1 {
		t.Fatalf("want exactly 1 accept, got %d", got)
	}
}

// Original upstream flow (auto-answer, mute_v2 after Answer) keeps working, once.
func TestMuteV2AfterAnswerSendsAcceptOnce(t *testing.T) {
	shortFallback(t)
	eng, call, sent := incomingTestEngine(t, false)
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	eng.onCallRaw(muteV2Node("0"))
	if got := sent.accepts(); got != 1 {
		t.Fatalf("want 1 accept after mute_v2, got %d", got)
	}
	eng.onCallRaw(muteV2Node("1")) // later mute changes must not re-accept
	time.Sleep(60 * time.Millisecond)
	if got := sent.accepts(); got != 1 {
		t.Fatalf("want exactly 1 accept, got %d", got)
	}
}

// No mute_v2 at all: the fallback accepts once the relay is known.
func TestAnswerWithoutMuteV2FallsBackToAccept(t *testing.T) {
	shortFallback(t)
	eng, call, sent := incomingTestEngine(t, true)
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sent.accepts() == 1 })
	if got := sent.accepts(); got != 1 {
		t.Fatalf("want 1 fallback accept, got %d", got)
	}
}

// Fallback waits for the relay; if the call ends first it never sends.
func TestFallbackStopsWhenCallEnds(t *testing.T) {
	shortFallback(t)
	eng, call, sent := incomingTestEngine(t, false)
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	eng.mu.Lock()
	delete(eng.calls, testCallID)
	eng.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	if got := sent.accepts(); got != 0 {
		t.Fatalf("accept sent for an ended call: %d", got)
	}
}

// A failed <accept> send must stay pending and be retried (fallback), exactly one success.
func TestAcceptSendFailureIsRetried(t *testing.T) {
	shortFallback(t)
	eng, call, sent := incomingTestEngine(t, true)
	var mu sync.Mutex
	failures := 0
	inner := eng.sendCallNode
	eng.sendCallNode = func(ctx context.Context, node waBinary.Node) error {
		isAccept := false
		for _, kid := range node.GetChildren() {
			if kid.Tag == "accept" {
				isAccept = true
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if isAccept && failures == 0 {
			failures++
			return context.DeadlineExceeded
		}
		return inner(ctx, node)
	}
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	eng.onCallRaw(muteV2Node("0")) // first accept attempt fails
	waitFor(t, func() bool { return sent.accepts() == 1 })
	mu.Lock()
	defer mu.Unlock()
	if failures != 1 || sent.accepts() != 1 {
		t.Fatalf("want 1 failed + 1 successful accept, got failures=%d accepts=%d", failures, sent.accepts())
	}
}

// Milestones: order for the human-answer path, and no identifiers in attributes.
func TestMilestonesForAnswerAfterMuteV2(t *testing.T) {
	shortFallback(t)
	eng, call, _ := incomingTestEngine(t, true)
	var mu sync.Mutex
	var names []string
	var attrsSeen []map[string]string
	eng.c.milestone = func(callID, name string, attrs map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		if callID != testCallID {
			t.Errorf("milestone for unexpected call %q", callID)
		}
		names = append(names, name)
		attrsSeen = append(attrsSeen, attrs)
	}
	eng.onCallRaw(muteV2Node("0"))
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"mute_v2", "answer_called", "accept_trigger", "accept_sent"}
	if len(names) < len(want) {
		t.Fatalf("milestones = %v, want prefix %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("milestones = %v, want prefix %v", names, want)
		}
	}
	if attrsSeen[1]["mute_v2_already_seen"] != "true" || attrsSeen[2]["trigger"] != "answer_after_mute_v2" {
		t.Fatalf("unexpected attrs: %v", attrsSeen)
	}
	for _, attrs := range attrsSeen {
		for k, v := range attrs {
			if strings.Contains(v, "@") || strings.Contains(v, testCaller.User) {
				t.Fatalf("milestone attr %s leaks an identifier: %q", k, v)
			}
		}
	}
}

// Early media: the relay session starts while ringing but the call stays Ringing (the API
// only answers Ringing calls) until Answer moves it on.
func TestEarlyMediaKeepsRingingUntilAnswer(t *testing.T) {
	shortFallback(t)
	eng, call, _ := incomingTestEngine(t, true)
	eng.c.early = true
	var mu sync.Mutex
	var mediaStart map[string]string
	eng.c.milestone = func(_ string, name string, attrs map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		if name == "media_start" {
			mediaStart = attrs
		}
	}
	eng.mu.Lock()
	eng.calls[testCallID].started = false // let maybeStartMedia run (no endpoints: no network)
	eng.mu.Unlock()

	eng.maybeStartMedia(testCallID) // what onOffer does right after the preaccept
	if got := call.State(); got != CallPhaseRinging {
		t.Fatalf("early media changed the phase to %v before answer", got)
	}
	mu.Lock()
	if mediaStart["before_answer"] != "true" {
		t.Fatalf("media_start attrs = %v", mediaStart)
	}
	mu.Unlock()

	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	if got := call.State(); got != CallPhaseConnecting && got != CallPhaseEnded {
		t.Fatalf("after answer phase = %v, want connecting", got)
	}
}

// Audio decoded while ringing must not make the call Active before Answer.
func TestEarlyAudioIsPromotedOnAnswer(t *testing.T) {
	shortFallback(t)
	eng, call, _ := incomingTestEngine(t, true)
	if eng.markAudioFlowing(testCallID) {
		t.Fatal("inbound call must not become active before answer")
	}
	readyFired := make(chan struct{}, 1)
	call.OnReady(func() { readyFired <- struct{}{} })
	if err := eng.answer(call); err != nil {
		t.Fatal(err)
	}
	if got := call.State(); got != CallPhaseActive {
		t.Fatalf("after answer with early audio phase = %v, want active", got)
	}
	select {
	case <-readyFired:
	case <-time.After(time.Second):
		t.Fatal("OnReady not fired on answer")
	}
	if !eng.markAudioFlowing(testCallID) {
		t.Fatal("answered call must be allowed to become active")
	}
}
