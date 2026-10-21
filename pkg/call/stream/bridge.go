package call_stream

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/purpshell/meowcaller"
)

const (
	// writeWait bounds every single socket write, so a stalled consumer can
	// never pin the writer goroutine (same policy as pkg/events/websocket).
	writeWait = 10 * time.Second
	// closeWait bounds the final "stop" message and close frame.
	closeWait = 2 * time.Second
	// pongWait is how long the consumer may stay silent (no message, no pong)
	// before the stream is considered dead.
	pongWait = 60 * time.Second
	// pingInterval must be shorter than pongWait.
	pingInterval = 25 * time.Second
	// maxMessageSize caps one inbound message (~6 s of base64 PCM16 audio).
	maxMessageSize = 256 * 1024
	// outQueueSize is the number of messages buffered towards the consumer
	// (~3.8 s of audio at one 60 ms frame per message). When the consumer
	// cannot keep up, newer frames are dropped instead of blocking the call's
	// media goroutines.
	outQueueSize = 64
	// maxBufferedSamples caps the consumer→call audio buffer (30 s at 16 kHz).
	// Consumers that generate audio faster than real time (TTS) may run up to
	// this far ahead; anything beyond it is dropped.
	maxBufferedSamples = 30 * meowcaller.SampleRate
)

// Message is the JSON envelope carried over the stream socket, modeled on
// Twilio Media Streams (flat payload instead of a nested "media" object).
type Message struct {
	Event        string `json:"event"`
	CallID       string `json:"callId,omitempty"`
	SampleRate   int    `json:"sampleRate,omitempty"`
	Encoding     string `json:"encoding,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	FrameSamples int    `json:"frameSamples,omitempty"`
	Video        bool   `json:"video,omitempty"`
	Track        string `json:"track,omitempty"`
	Payload      string `json:"payload,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// bridge adapts one WebSocket connection to meowcaller's AudioSink
// (Call.Receive), VideoSink (Call.ReceiveVideo) and AudioSource (Call.Play).
//
// Concurrency model: the call's media goroutines only ever enqueue into a
// bounded channel (never block, drop on overflow); a single writer goroutine
// owns all socket writes, each bounded by a deadline; the handler goroutine
// owns all socket reads. Close only signals (closes done) and never blocks, so
// it is safe from whatsmeow event handlers.
type bridge struct {
	callID string
	conn   *websocket.Conn

	out        chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	reason     atomic.Value // string
	writerDone chan struct{}

	audioMu sync.Mutex
	pending []int16 // consumer audio waiting to be played into the call
	head    int     // read offset into pending
	starved bool    // previous ReadFrame found only a partial frame

	droppedOut atomic.Uint64 // frames dropped towards the consumer
	droppedIn  atomic.Uint64 // consumer samples dropped (buffer full)

	// Diagnostics (counts only, never audio): logged when the stream ends.
	framesFromCall  atomic.Uint64 // decoded peer frames delivered by the call (inbound)
	framesIntoCall  atomic.Uint64 // consumer frames handed to the call's send loop (outbound)
	silenceTicks    atomic.Uint64 // send-loop ticks with no consumer audio (silence sent)
	samplesFromUser atomic.Uint64 // consumer samples received over the socket
}

// Stats is a count-only snapshot of the stream for diagnostics.
type Stats struct {
	FramesFromCall, FramesIntoCall, SilenceTicks, SamplesFromUser, DroppedToUser, DroppedFromUser uint64
}

func (b *bridge) stats() Stats {
	return Stats{
		FramesFromCall:  b.framesFromCall.Load(),
		FramesIntoCall:  b.framesIntoCall.Load(),
		SilenceTicks:    b.silenceTicks.Load(),
		SamplesFromUser: b.samplesFromUser.Load(),
		DroppedToUser:   b.droppedOut.Load(),
		DroppedFromUser: b.droppedIn.Load(),
	}
}

func newBridge(callID string) *bridge {
	return &bridge{
		callID:     callID,
		out:        make(chan []byte, outQueueSize),
		done:       make(chan struct{}),
		writerDone: make(chan struct{}),
	}
}

// closeWith ends the stream with reason. Idempotent, non-blocking.
func (b *bridge) closeWith(reason string) {
	b.closeOnce.Do(func() {
		b.reason.Store(reason)
		close(b.done)
	})
}

func (b *bridge) closed() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// enqueue hands msg to the writer without ever blocking.
func (b *bridge) enqueue(msg Message) {
	if b.closed() {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case b.out <- data:
	default:
		b.droppedOut.Add(1)
	}
}

// WriteFrame implements meowcaller.AudioSink: one decoded mono frame from the peer.
func (b *bridge) WriteFrame(frame []float32) error {
	if b.closed() {
		return nil
	}
	b.framesFromCall.Add(1)
	b.enqueue(Message{Event: "media", Track: "inbound", Payload: base64.StdEncoding.EncodeToString(pcm16FromFloat32(frame))})
	return nil
}

// WriteVideo implements meowcaller.VideoSink: one Annex-B H.264 access unit.
func (b *bridge) WriteVideo(accessUnit []byte) error {
	if b.closed() {
		return nil
	}
	b.enqueue(Message{Event: "video", Track: "inbound", Payload: base64.StdEncoding.EncodeToString(accessUnit)})
	return nil
}

// Close implements the shared Close of AudioSink/VideoSink/AudioSource.
// meowcaller calls it when the call ends (sink) or the player stops (source).
func (b *bridge) Close() error {
	b.closeWith("call_ended")
	return nil
}

// ReadFrame implements meowcaller.AudioSource. It is called by the call's
// send loop every 60 ms and MUST NOT block: when the consumer has not sent
// enough audio it returns (nil, nil), which makes the call send silence.
func (b *bridge) ReadFrame() ([]float32, error) {
	if b.closed() {
		return nil, io.EOF
	}
	b.audioMu.Lock()
	defer b.audioMu.Unlock()
	avail := len(b.pending) - b.head
	if avail == 0 {
		b.starved = false
		b.silenceTicks.Add(1)
		return nil, nil
	}
	if avail < meowcaller.FrameSamples && !b.starved {
		// Give the consumer one more tick to complete the frame before
		// flushing the tail padded with silence.
		b.starved = true
		b.silenceTicks.Add(1)
		return nil, nil
	}
	b.framesIntoCall.Add(1)
	b.starved = false
	n := avail
	if n > meowcaller.FrameSamples {
		n = meowcaller.FrameSamples
	}
	frame := make([]float32, meowcaller.FrameSamples) // zero-padded tail
	float32FromInt16(frame[:n], b.pending[b.head:b.head+n])
	b.head += n
	if b.head == len(b.pending) {
		b.pending = b.pending[:0]
		b.head = 0
	} else if b.head > len(b.pending)/2 {
		m := copy(b.pending, b.pending[b.head:])
		b.pending = b.pending[:m]
		b.head = 0
	}
	return frame, nil
}

// pushOutbound appends consumer audio, dropping what exceeds the buffer cap.
func (b *bridge) pushOutbound(samples []int16) {
	b.audioMu.Lock()
	defer b.audioMu.Unlock()
	b.samplesFromUser.Add(uint64(len(samples)))
	room := maxBufferedSamples - (len(b.pending) - b.head)
	if room <= 0 {
		b.droppedIn.Add(uint64(len(samples)))
		return
	}
	if len(samples) > room {
		b.droppedIn.Add(uint64(len(samples) - room))
		samples = samples[:room]
	}
	b.pending = append(b.pending, samples...)
}

// clearOutbound drops all buffered consumer audio (barge-in).
func (b *bridge) clearOutbound() {
	b.audioMu.Lock()
	b.pending = b.pending[:0]
	b.head = 0
	b.starved = false
	b.audioMu.Unlock()
}

func (b *bridge) bufferedSamples() int {
	b.audioMu.Lock()
	defer b.audioMu.Unlock()
	return len(b.pending) - b.head
}

// writeLoop is the only goroutine that writes to the socket. It exits (and
// closes the socket, which unblocks readLoop) when the bridge is closed or a
// write fails.
func (b *bridge) writeLoop() {
	defer close(b.writerDone)
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-b.done:
			b.finish()
			return
		default:
		}
		select {
		case data := <-b.out:
			_ = b.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := b.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				b.closeWith(reasonWriteError)
				_ = b.conn.Close()
				return
			}
		case <-ping.C:
			if err := b.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				b.closeWith(reasonWriteError)
				_ = b.conn.Close()
				return
			}
		case <-b.done:
			b.finish()
			return
		}
	}
}

// finish sends a best-effort "stop" and close frame, then closes the socket.
func (b *bridge) finish() {
	reason, _ := b.reason.Load().(string)
	if data, err := json.Marshal(Message{Event: "stop", CallID: b.callID, Reason: reason}); err == nil {
		_ = b.conn.SetWriteDeadline(time.Now().Add(closeWait))
		_ = b.conn.WriteMessage(websocket.TextMessage, data)
	}
	_ = b.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), time.Now().Add(closeWait))
	_ = b.conn.Close()
}

// readLoop consumes consumer messages until the socket closes, the consumer
// sends "stop", or the consumer goes silent past pongWait. Unknown events are
// ignored so the protocol can grow without breaking old clients.
func (b *bridge) readLoop() {
	b.conn.SetReadLimit(maxMessageSize)
	_ = b.conn.SetReadDeadline(time.Now().Add(pongWait))
	b.conn.SetPongHandler(func(string) error {
		return b.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := b.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = b.conn.SetReadDeadline(time.Now().Add(pongWait))
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Event {
		case "media":
			if msg.Track != "" && msg.Track != "outbound" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(msg.Payload)
			if err != nil || len(raw) < 2 {
				continue
			}
			b.pushOutbound(int16FromPCM16(raw))
		case "clear":
			b.clearOutbound()
		case "stop":
			return
		}
	}
}
