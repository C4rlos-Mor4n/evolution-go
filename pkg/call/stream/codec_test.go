package call_stream

import "testing"

func TestPCM16RoundTrip(t *testing.T) {
	frame := []float32{0, 0.5, -0.5, 1, -1, 0.25}
	pcm := pcm16FromFloat32(frame)
	if len(pcm) != len(frame)*2 {
		t.Fatalf("expected %d bytes, got %d", len(frame)*2, len(pcm))
	}
	samples := int16FromPCM16(pcm)
	back := make([]float32, len(samples))
	float32FromInt16(back, samples)
	for i, want := range frame {
		diff := back[i] - want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.001 {
			t.Errorf("sample %d: want %v, got %v", i, want, back[i])
		}
	}
}

func TestPCM16ClampsOutOfRange(t *testing.T) {
	samples := int16FromPCM16(pcm16FromFloat32([]float32{2.0, -2.0}))
	if samples[0] != 32767 || samples[1] != -32768 {
		t.Fatalf("expected clamping, got %v", samples)
	}
}

func TestInt16FromPCM16IgnoresOddByte(t *testing.T) {
	if got := int16FromPCM16([]byte{1, 0, 2}); len(got) != 1 || got[0] != 1 {
		t.Fatalf("unexpected %v", got)
	}
}
