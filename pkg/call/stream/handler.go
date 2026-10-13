package call_stream

import (
	"errors"
	"net/http"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/gin-gonic/gin"
	"github.com/gomessguii/logger"
	"github.com/gorilla/websocket"
	"github.com/purpshell/meowcaller"
)

// MediaCall is the subset of *meowcaller.Call the stream needs.
type MediaCall interface {
	ID() string
	IsVideo() bool
	State() meowcaller.CallPhase
	Receive(sink meowcaller.AudioSink)
	ReceiveVideo(sink meowcaller.VideoSink)
	Play(src meowcaller.AudioSource) *meowcaller.Player
	Hangup() error
}

// Lookup resolves calls for the stream. AttachStream must only return calls
// registered for instanceID and must allow at most one attached stream per
// call (call_registry.ErrNotFound / call_registry.ErrStreamBusy otherwise).
type Lookup interface {
	CallsEnabled() bool
	AttachStream(instanceID, callID string, detach func(reason string)) (MediaCall, func(), error)
}

// Server serves GET /call/stream/:callId. It must be mounted behind the
// instance auth middleware, which puts the authenticated *Instance in the gin
// context under "instance".
type Server struct {
	lookup   Lookup
	upgrader websocket.Upgrader
}

// NewServer returns a stream server backed by lookup.
func NewServer(lookup Lookup) *Server {
	return &Server{
		lookup: lookup,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Auth is an explicit per-instance token (header or query), never a
			// cookie, so a cross-origin page cannot ride an existing session.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// ErrDisabledMessage is returned by every call endpoint while CALLS_ENABLED=false.
const ErrDisabledMessage = "calls feature is disabled on this server (CALLS_ENABLED=false)"

// Serve upgrades the request and bridges the call's media to the socket. All
// checks (auth, feature flag, ownership, single consumer) happen BEFORE the
// upgrade, so a rejected request gets a plain HTTP error and no socket.
func (s *Server) Serve(ctx *gin.Context) {
	instance, ok := ctx.Value("instance").(*instance_model.Instance)
	if !ok || instance == nil || instance.Id == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}
	if !s.lookup.CallsEnabled() {
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": ErrDisabledMessage})
		return
	}
	if !websocket.IsWebSocketUpgrade(ctx.Request) {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "websocket upgrade required"})
		return
	}

	callID := ctx.Param("callId")
	b := newBridge(callID)
	call, release, err := s.lookup.AttachStream(instance.Id, callID, b.closeWith)
	switch {
	case errors.Is(err, call_registry.ErrStreamBusy):
		ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	case err != nil:
		ctx.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": call_registry.ErrNotFound.Error()})
		return
	}
	defer release()

	conn, err := s.upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		// The upgrader already answered the request with an HTTP error.
		return
	}
	b.conn = conn
	logger.LogInfo("[%s] call stream attached callId=%s", instance.Id, callID)

	go b.writeLoop()
	b.enqueue(Message{
		Event:        "start",
		CallID:       callID,
		SampleRate:   meowcaller.SampleRate,
		Encoding:     "pcm_s16le",
		Channels:     1,
		FrameSamples: meowcaller.FrameSamples,
		Video:        call.IsVideo(),
	})
	call.Receive(b)
	call.ReceiveVideo(b)
	player := call.Play(b)

	b.readLoop() // blocks until the consumer leaves or the bridge is closed
	b.closeWith("consumer_closed")
	<-b.writerDone

	// Detach before hanging up so the media goroutines stop touching the bridge.
	call.Receive(nil)
	call.ReceiveVideo(nil)
	if player != nil {
		player.Stop()
	}
	if call.State() != meowcaller.CallPhaseEnded {
		// The stream is the call's only media path: once the consumer is gone
		// nobody is listening or speaking, so end the call instead of leaving
		// a silent call open.
		if err := call.Hangup(); err != nil {
			logger.LogWarn("[%s] call stream: hangup after consumer left failed callId=%s: %v", instance.Id, callID, err)
		}
	}
	reason, _ := b.reason.Load().(string)
	logger.LogInfo("[%s] call stream closed callId=%s reason=%s droppedOut=%d droppedIn=%d",
		instance.Id, callID, reason, b.droppedOut.Load(), b.droppedIn.Load())
}
