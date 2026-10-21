package meowcaller

import "testing"

// KRIONIX PATCH: the relay fanout delivers one packet per bound relay, so the replay
// filter must pass the first copy and drop the rest without confusing distinct streams.
func TestRtpReplayFilterDropsCrossRelayDuplicates(t *testing.T) {
	f := newRtpReplayFilter()
	if f.Duplicate(0x1111, 10) {
		t.Fatal("first copy reported as duplicate")
	}
	if !f.Duplicate(0x1111, 10) {
		t.Fatal("second copy of the same packet not dropped")
	}
	if f.Duplicate(0x2222, 10) {
		t.Fatal("same seq on another SSRC treated as duplicate")
	}
	if f.Duplicate(0x1111, 11) {
		t.Fatal("next seq treated as duplicate")
	}
}

func TestRtpReplayFilterSurvivesSequenceWrap(t *testing.T) {
	f := newRtpReplayFilter()
	for seq := 0; seq < 70000; seq++ {
		if f.Duplicate(0x1111, uint16(seq)) {
			t.Fatalf("fresh packet seq=%d (wrapped=%d) reported as duplicate", seq, uint16(seq))
		}
	}
}
