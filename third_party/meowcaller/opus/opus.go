// Package opus is a minimal libopus binding for WhatsApp 1:1 calls negotiated on Opus.
//
// KRIONIX PATCH: when the caller's account is outside the use_mlow_codec_v1 rollout,
// WhatsApp carries standard RFC 6716 Opus (16 kHz mono, 60 ms) in the same RTP payload
// type as MLow. The peer decodes with libopus and expects Opus back, so both directions
// must switch codec; feeding those bodies to the MLow decoder yields noise/silence.
// Source of truth: https://github.com/oxidezap/whatsapp-rust/pull/1111
package opus

/*
#cgo pkg-config: opus
#include <opus.h>

static int kx_enc_init(OpusEncoder *e, int bitrate) {
	int r;
	if ((r = opus_encoder_ctl(e, OPUS_SET_BITRATE(bitrate))) != OPUS_OK) return r;
	if ((r = opus_encoder_ctl(e, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE))) != OPUS_OK) return r;
	if ((r = opus_encoder_ctl(e, OPUS_SET_MAX_BANDWIDTH(OPUS_BANDWIDTH_WIDEBAND))) != OPUS_OK) return r;
	return opus_encoder_ctl(e, OPUS_SET_COMPLEXITY(5));
}
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"unsafe"
)

const (
	// SampleRate is the WhatsApp call audio rate.
	SampleRate = 16000
	// FrameSamples is one 60 ms frame at 16 kHz, the negotiated packet duration.
	FrameSamples = 960
	// maxPacketSamples bounds one decoded packet (120 ms, the longest Opus allows here).
	maxPacketSamples = 1920
	maxPacketBytes   = 1500
	// DefaultBitrate matches the voip_settings target_bitrate seen on 1:1 calls.
	DefaultBitrate = 24000
)

// Encoder encodes 60 ms mono frames into Opus packets.
type Encoder struct {
	st  *C.OpusEncoder
	buf []byte
}

// NewEncoder creates a VOIP-tuned wideband encoder.
func NewEncoder(bitrate int) (*Encoder, error) {
	if bitrate <= 0 {
		bitrate = DefaultBitrate
	}
	var cerr C.int
	st := C.opus_encoder_create(SampleRate, 1, C.OPUS_APPLICATION_VOIP, &cerr)
	if cerr != C.OPUS_OK || st == nil {
		return nil, fmt.Errorf("opus: encoder create failed: %d", int(cerr))
	}
	if r := C.kx_enc_init(st, C.int(bitrate)); r != C.OPUS_OK {
		C.opus_encoder_destroy(st)
		return nil, fmt.Errorf("opus: encoder configure failed: %d", int(r))
	}
	e := &Encoder{st: st, buf: make([]byte, maxPacketBytes)}
	// Released by the GC: the media goroutines that share it stop on their own schedule,
	// so an explicit Close at call end could free it under a concurrent Encode.
	runtime.SetFinalizer(e, (*Encoder).Close)
	return e, nil
}

// Encode turns one 960-sample frame (float in [-1, 1]) into an Opus packet.
func (e *Encoder) Encode(pcm []float32) ([]byte, error) {
	if e == nil || e.st == nil {
		return nil, errors.New("opus: encoder closed")
	}
	if len(pcm) != FrameSamples {
		return nil, fmt.Errorf("opus: expected %d samples, got %d", FrameSamples, len(pcm))
	}
	clean := make([]float32, FrameSamples)
	for i, s := range pcm {
		switch {
		case math.IsNaN(float64(s)):
			s = 0
		case s < -1:
			s = -1
		case s > 1:
			s = 1
		}
		clean[i] = s
	}
	n := C.opus_encode_float(e.st, (*C.float)(unsafe.Pointer(&clean[0])), FrameSamples,
		(*C.uchar)(unsafe.Pointer(&e.buf[0])), C.opus_int32(len(e.buf)))
	if n < 0 {
		return nil, fmt.Errorf("opus: encode failed: %d", int(n))
	}
	return append([]byte(nil), e.buf[:n]...), nil
}

// Close releases the encoder.
func (e *Encoder) Close() {
	if e != nil && e.st != nil {
		C.opus_encoder_destroy(e.st)
		e.st = nil
	}
}

// Decoder decodes Opus packets into 16 kHz mono PCM.
type Decoder struct {
	st  *C.OpusDecoder
	pcm []float32
	// Failed counts packets libopus rejected (concealed as one silent frame each).
	Failed uint64
}

// NewDecoder creates a 16 kHz mono decoder.
func NewDecoder() (*Decoder, error) {
	var cerr C.int
	st := C.opus_decoder_create(SampleRate, 1, &cerr)
	if cerr != C.OPUS_OK || st == nil {
		return nil, fmt.Errorf("opus: decoder create failed: %d", int(cerr))
	}
	d := &Decoder{st: st, pcm: make([]float32, maxPacketSamples)}
	runtime.SetFinalizer(d, (*Decoder).Close)
	return d, nil
}

// Decode decodes one packet. An empty or rejected packet yields one 60 ms silent frame so
// playout keeps its cadence.
func (d *Decoder) Decode(payload []byte) []float32 {
	if d == nil || d.st == nil || len(payload) == 0 {
		return make([]float32, FrameSamples)
	}
	n := C.opus_decode_float(d.st, (*C.uchar)(unsafe.Pointer(&payload[0])), C.opus_int32(len(payload)),
		(*C.float)(unsafe.Pointer(&d.pcm[0])), maxPacketSamples, 0)
	if n <= 0 {
		d.Failed++
		return make([]float32, FrameSamples)
	}
	return append([]float32(nil), d.pcm[:n]...)
}

// Close releases the decoder.
func (d *Decoder) Close() {
	if d != nil && d.st != nil {
		C.opus_decoder_destroy(d.st)
		d.st = nil
	}
}
