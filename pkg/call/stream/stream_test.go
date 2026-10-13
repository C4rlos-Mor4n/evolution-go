package call_stream

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/purpshell/meowcaller"
)

// ---- fakes ----

type fakeCall struct {
	mu      sync.Mutex
	id      string
	state   meowcaller.CallPhase
	sink    meowcaller.AudioSink
	vsink   meowcaller.VideoSink
	src     meowcaller.AudioSource
	hangups int
}

func (f *fakeCall) ID() string    { return f.id }
func (f *fakeCall) IsVideo() bool { return false }
func (f *fakeCall) State() meowcaller.CallPhase {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}
func (f *fakeCall) Receive(s meowcaller.AudioSink) { f.mu.Lock(); f.sink = s; f.mu.Unlock() }
func (f *fakeCall) ReceiveVideo(s meowcaller.VideoSink) {
	f.mu.Lock()
	f.vsink = s
	f.mu.Unlock()
}
func (f *fakeCall) Play(src meowcaller.AudioSource) *meowcaller.Player {
	f.mu.Lock()
	f.src = src
	f.mu.Unlock()
	p := meowcaller.NewPlayer()
	p.Play(src)
	return p
}
func (f *fakeCall) Hangup() error {
	f.mu.Lock()
	f.state = meowcaller.CallPhaseEnded
	f.hangups++
	f.mu.Unlock()
	return nil
}
func (f *fakeCall) getSink() meowcaller.AudioSink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sink
}
func (f *fakeCall) getSrc() meowcaller.AudioSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.src
}
func (f *fakeCall) hangupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hangups
}

type fakeLookup struct {
	enabled bool
	reg     *call_registry.Registry[*fakeCall]
}

func (l *fakeLookup) CallsEnabled() bool { return l.enabled }
func (l *fakeLookup) AttachStream(instanceID, callID string, detach func(string)) (MediaCall, func(), error) {
	c, rel, err := l.reg.AttachStream(instanceID, callID, detach)
	if err != nil {
		return nil, nil, err
	}
	return c, rel, nil
}

// testServer mounts the stream behind a stand-in for the instance auth
// middleware: the "apikey" header/query maps to an instance ID.
func testServer(t *testing.T, lookup Lookup) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tokens := map[string]string{"token-a": "inst-a", "token-b": "inst-b"}
	auth := func(c *gin.Context) {
		tok := c.GetHeader("apikey")
		if tok == "" {
			tok = c.Query("apikey")
		}
		if id, ok := tokens[tok]; ok {
			c.Set("instance", &instance_model.Instance{Id: id})
		}
		c.Next()
	}
	s := NewServer(lookup)
	r.GET("/call/stream/:callId", auth, s.Serve)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T, enabled bool) (*httptest.Server, *call_registry.Registry[*fakeCall], *fakeCall) {
	reg := call_registry.New[*fakeCall]()
	owner := new(int)
	reg.OpenOwner(owner)
	call := &fakeCall{id: "CALL1", state: meowcaller.CallPhaseActive}
	if err := reg.Store(owner, "inst-a", "CALL1", call); err != nil {
		t.Fatal(err)
	}
	return testServer(t, &fakeLookup{enabled: enabled, reg: reg}), reg, call
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func dial(t *testing.T, srv *httptest.Server, path, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if token != "" {
		h.Set("apikey", token)
	}
	return websocket.DefaultDialer.Dial(wsURL(srv, path), h)
}

func readMsg(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m Message
	if err := c.ReadJSON(&m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---- handler / auth tests ----

func TestStreamRequiresCredentials(t *testing.T) {
	srv, _, call := setup(t, true)
	for _, tc := range []struct{ name, path, token string }{
		{"no token", "/call/stream/CALL1", ""},
		{"bad token", "/call/stream/CALL1", "nope"},
		{"empty query token", "/call/stream/CALL1?apikey=", ""},
	} {
		_, resp, err := dial(t, srv, tc.path, tc.token)
		if err == nil {
			t.Fatalf("%s: upgrade must fail without valid credentials", tc.name)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %+v", tc.name, resp)
		}
	}
	if call.getSink() != nil {
		t.Fatal("unauthenticated request attached to the call")
	}
}

func TestStreamDisabledReturns404(t *testing.T) {
	srv, _, _ := setup(t, false)
	_, resp, err := dial(t, srv, "/call/stream/CALL1", "token-a")
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when calls are disabled, got %v %+v", err, resp)
	}
}

func TestStreamCannotAttachToAnotherInstancesCall(t *testing.T) {
	srv, _, call := setup(t, true)
	// token-b authenticates as inst-b; CALL1 belongs to inst-a.
	_, resp, err := dial(t, srv, "/call/stream/CALL1", "token-b")
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for another instance's call, got %v %+v", err, resp)
	}
	_, resp, err = dial(t, srv, "/call/stream/CALL1?apikey=token-b", "")
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for another instance's call via query, got %v %+v", err, resp)
	}
	if call.getSink() != nil || call.hangupCount() != 0 {
		t.Fatal("foreign instance touched the call")
	}
}

func TestStreamRequiresUpgrade(t *testing.T) {
	srv, _, _ := setup(t, true)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/call/stream/CALL1", nil)
	req.Header.Set("apikey", "token-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without upgrade, got %d", resp.StatusCode)
	}
}

func TestStreamSecondConsumerRejected(t *testing.T) {
	srv, _, _ := setup(t, true)
	c1, _, err := dial(t, srv, "/call/stream/CALL1?apikey=token-a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	if m := readMsg(t, c1); m.Event != "start" {
		t.Fatalf("expected start, got %+v", m)
	}
	_, resp, err := dial(t, srv, "/call/stream/CALL1", "token-a")
	if err == nil || resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for a second consumer, got %v %+v", err, resp)
	}
}

func TestStreamAudioBothWaysAndHangupOnClose(t *testing.T) {
	srv, reg, call := setup(t, true)
	c, _, err := dial(t, srv, "/call/stream/CALL1", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	start := readMsg(t, c)
	if start.Event != "start" || start.CallID != "CALL1" || start.SampleRate != 16000 || start.Encoding != "pcm_s16le" {
		t.Fatalf("bad start message %+v", start)
	}
	eventually(t, func() bool { return call.getSink() != nil && call.getSrc() != nil }, "sinks attached")

	// inbound: call -> consumer
	frame := make([]float32, meowcaller.FrameSamples)
	frame[0] = 0.5
	if err := call.getSink().WriteFrame(frame); err != nil {
		t.Fatal(err)
	}
	m := readMsg(t, c)
	if m.Event != "media" || m.Track != "inbound" {
		t.Fatalf("expected inbound media, got %+v", m)
	}
	raw, _ := base64.StdEncoding.DecodeString(m.Payload)
	if len(raw) != meowcaller.FrameSamples*2 || int16(binary.LittleEndian.Uint16(raw)) != 16384 {
		t.Fatalf("bad inbound payload (%d bytes)", len(raw))
	}

	// outbound: consumer -> call, sent in an odd-sized chunk then the rest
	pcm := make([]byte, meowcaller.FrameSamples*2)
	neg := int16(-16384)
	binary.LittleEndian.PutUint16(pcm, uint16(neg))
	send := func(b []byte) {
		if err := c.WriteJSON(Message{Event: "media", Track: "outbound", Payload: base64.StdEncoding.EncodeToString(b)}); err != nil {
			t.Fatal(err)
		}
	}
	send(pcm[:1000])
	send(pcm[1000:])
	var got []float32
	eventually(t, func() bool {
		f, err := call.getSrc().ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if len(f) == meowcaller.FrameSamples {
			got = f
			return true
		}
		return false
	}, "outbound frame")
	if got[0] != -0.5 {
		t.Fatalf("outbound sample = %v", got[0])
	}

	// consumer leaves -> call hung up, slot released, sinks detached
	_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.Close()
	eventually(t, func() bool { return call.hangupCount() == 1 }, "hangup after consumer left")
	eventually(t, func() bool { return call.getSink() == nil }, "sink detached")
	if _, rel, err := reg.AttachStream("inst-a", "CALL1", func(string) {}); err != nil {
		t.Fatalf("slot not released: %v", err)
	} else {
		rel()
	}
}

func TestCallEndClosesStream(t *testing.T) {
	srv, reg, call := setup(t, true)
	c, _, err := dial(t, srv, "/call/stream/CALL1", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = readMsg(t, c)
	// Simulate meowcaller's end-of-call callback removing the call.
	call.mu.Lock()
	call.state = meowcaller.CallPhaseEnded
	call.mu.Unlock()
	if !reg.RemoveCall("inst-a", "CALL1", call, "call_ended") {
		t.Fatal("remove failed")
	}
	m := readMsg(t, c)
	if m.Event != "stop" || m.Reason != "call_ended" {
		t.Fatalf("expected stop, got %+v", m)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("socket should be closed after stop")
	}
	time.Sleep(50 * time.Millisecond)
	if call.hangupCount() != 0 {
		t.Fatal("an already ended call must not be hung up again")
	}
}

func TestSlowConsumerNeverBlocksMedia(t *testing.T) {
	srv, _, call := setup(t, true)
	c, _, err := dial(t, srv, "/call/stream/CALL1", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	eventually(t, func() bool { return call.getSink() != nil }, "sink attached")
	sink := call.getSink()
	frame := make([]float32, meowcaller.FrameSamples)
	start := time.Now()
	// The consumer never reads: far more than the socket + queue can hold.
	for i := 0; i < 20000; i++ {
		_ = sink.WriteFrame(frame)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("WriteFrame blocked on a slow consumer (%s)", d)
	}
	b := sink.(*bridge)
	if b.droppedOut.Load() == 0 {
		t.Fatal("expected frames to be dropped for a slow consumer")
	}
}

// ---- bridge unit tests ----

func TestReadFrameNeverBlocksAndPadsTail(t *testing.T) {
	b := newBridge("c")
	done := make(chan struct{})
	go func() {
		f, err := b.ReadFrame()
		if f != nil || err != nil {
			t.Errorf("empty buffer should yield silence, got %v %v", f, err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReadFrame blocked on an empty buffer")
	}

	b.pushOutbound(make([]int16, 100))
	if f, _ := b.ReadFrame(); f != nil {
		t.Fatal("partial frame should wait one tick")
	}
	f, _ := b.ReadFrame()
	if len(f) != meowcaller.FrameSamples {
		t.Fatalf("partial tail should be flushed padded, got %d", len(f))
	}
	if b.bufferedSamples() != 0 {
		t.Fatal("buffer should be empty")
	}
	b.Close()
	if _, err := b.ReadFrame(); err == nil {
		t.Fatal("closed bridge should return EOF")
	}
}

func TestOutboundBufferCapAndClear(t *testing.T) {
	b := newBridge("c")
	b.pushOutbound(make([]int16, maxBufferedSamples+500))
	if b.bufferedSamples() != maxBufferedSamples || b.droppedIn.Load() != 500 {
		t.Fatalf("cap not enforced: buffered=%d dropped=%d", b.bufferedSamples(), b.droppedIn.Load())
	}
	for i := 0; i < 10; i++ {
		if f, _ := b.ReadFrame(); len(f) != meowcaller.FrameSamples {
			t.Fatal("expected full frames")
		}
	}
	b.clearOutbound()
	if b.bufferedSamples() != 0 {
		t.Fatal("clear did not drop buffered audio")
	}
}

func TestMessageJSONShape(t *testing.T) {
	data, _ := json.Marshal(Message{Event: "media", Track: "inbound", Payload: "AAA="})
	if string(data) != `{"event":"media","track":"inbound","payload":"AAA="}` {
		t.Fatalf("unexpected envelope %s", data)
	}
}
