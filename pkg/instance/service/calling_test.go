package instance_service

import (
	"strings"
	"testing"

	whatsmeow_service "github.com/evolution-foundation/evolution-go/pkg/whatsmeow/service"
)

func TestCallingStatus(t *testing.T) {
	rt := func(runtime, attached bool) whatsmeow_service.CallingRuntimeState {
		return whatsmeow_service.CallingRuntimeState{Runtime: runtime, Attached: attached}
	}
	for _, tt := range []struct {
		name            string
		global, enabled bool
		state           whatsmeow_service.CallingRuntimeState
		want            string
	}{
		{"global_off", false, true, rt(true, true), CallingStatusGlobalDisabled},
		{"default_off", true, false, rt(true, false), CallingStatusDisabled},
		{"active", true, true, rt(true, true), CallingStatusActive},
		{"enabled_needs_reconnect", true, true, rt(true, false), CallingStatusPendingApply},
		{"disabled_still_attached", true, false, rt(true, true), CallingStatusPendingApply},
		{"enabled_offline", true, true, rt(false, false), CallingStatusOffline},
		{"disabled_offline", true, false, rt(false, false), CallingStatusDisabled},
	} {
		if got := callingStatus(tt.global, tt.enabled, tt.state); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestSanitizeCallingActor(t *testing.T) {
	if got := sanitizeCallingActor("  ops\n\"x\\ "); got != "opsx" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeCallingActor(strings.Repeat("a", 100)); len(got) != 64 {
		t.Fatalf("len=%d, want 64", len(got))
	}
}
