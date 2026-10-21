package opus

import (
	"math"
	"testing"
)

// A 440 Hz tone survives an encode/decode round trip at the negotiated 16 kHz / 60 ms shape.
func TestRoundTripKeepsSpeechBandAudio(t *testing.T) {
	enc, err := NewEncoder(DefaultBitrate)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	var energy float64
	for f := 0; f < 10; f++ {
		frame := make([]float32, FrameSamples)
		for i := range frame {
			n := f*FrameSamples + i
			frame[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(n)/SampleRate))
		}
		pkt, err := enc.Encode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) == 0 {
			t.Fatalf("frame %d: empty packet", f)
		}
		pcm := dec.Decode(pkt)
		if len(pcm) != FrameSamples {
			t.Fatalf("frame %d: decoded %d samples, want %d", f, len(pcm), FrameSamples)
		}
		if f >= 3 {
			for _, s := range pcm {
				energy += float64(s) * float64(s)
			}
		}
	}
	if rms := math.Sqrt(energy / float64(7*FrameSamples)); rms < 0.05 {
		t.Fatalf("decoded tone too quiet: rms=%.4f", rms)
	}
	if dec.Failed != 0 {
		t.Fatalf("decoder rejected %d packets", dec.Failed)
	}
}

// The packets WhatsApp peers send on Opus calls open with TOC 0x58 (SILK wideband, 60 ms).
// A voice frame from our encoder must declare the same 60 ms duration so the peer's
// timestamp cadence matches.
func TestEncoderEmitsSixtyMillisecondPackets(t *testing.T) {
	enc, err := NewEncoder(DefaultBitrate)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	frame := make([]float32, FrameSamples)
	for i := range frame {
		frame[i] = float32(0.3 * math.Sin(2*math.Pi*300*float64(i)/SampleRate))
	}
	pkt, err := enc.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	config := pkt[0] >> 3
	// SILK-only configs 0..11: duration = {10,20,40,60}[config%4]; 60 ms is config%4 == 3.
	if config > 11 || config%4 != 3 {
		t.Fatalf("TOC 0x%02x: config %d is not a 60 ms SILK packet", pkt[0], config)
	}
}

func TestRejectedPacketYieldsOneSilentFrame(t *testing.T) {
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pcm := dec.Decode([]byte{0xFF, 0xFF, 0xFF})
	if len(pcm) != FrameSamples {
		t.Fatalf("got %d samples, want %d", len(pcm), FrameSamples)
	}
	if dec.Failed != 1 {
		t.Fatalf("Failed=%d, want 1", dec.Failed)
	}
}

func TestWrongFrameSizeIsRejected(t *testing.T) {
	enc, err := NewEncoder(0)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, err := enc.Encode(make([]float32, 320)); err == nil {
		t.Fatal("expected an error for a 20 ms frame")
	}
}
