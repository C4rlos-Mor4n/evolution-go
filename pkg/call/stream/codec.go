package call_stream

import "encoding/binary"

// pcm16FromFloat32 converts one mono PCM frame (as meowcaller delivers it:
// float32 samples in [-1, 1]) into little-endian signed 16-bit PCM bytes,
// clamping out-of-range samples.
func pcm16FromFloat32(frame []float32) []byte {
	out := make([]byte, len(frame)*2)
	for i, s := range frame {
		v := s * 32768.0
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return out
}

// int16FromPCM16 decodes little-endian signed 16-bit PCM bytes. A trailing odd
// byte (a malformed chunk) is ignored.
func int16FromPCM16(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

// float32FromInt16 converts 16-bit samples to float32 in [-1, 1].
func float32FromInt16(dst []float32, src []int16) {
	for i, v := range src {
		dst[i] = float32(v) / 32768.0
	}
}
