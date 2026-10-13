package whatsmeow_service

import (
	"reflect"
	"sync"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	"github.com/evolution-foundation/evolution-go/pkg/safemap"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

// hasAckHook reports whether meowcaller's raw <ack> node hook was installed on
// the client (whatsmeow itself has no "ack" node handler).
func hasAckHook(t *testing.T, cli *whatsmeow.Client) bool {
	t.Helper()
	field := reflect.ValueOf(cli).Elem().FieldByName("nodeHandlers")
	if !field.IsValid() {
		t.Fatal("whatsmeow.Client.nodeHandlers not found")
	}
	return field.MapIndex(reflect.ValueOf("ack")).IsValid()
}

func TestAttachCallingDisabledIsInert(t *testing.T) {
	reg := NewCallRegistry()
	w := whatsmeowService{
		config:          &config.Config{CallsEnabled: false},
		callRegistry:    reg,
		myClientPointer: safemap.New[*MyClient](),
	}
	cli := whatsmeow.NewClient(&store.Device{}, nil)
	release := w.attachCalling("inst-a", cli)
	if hasAckHook(t, cli) {
		t.Fatal("meowcaller must not be constructed when CALLS_ENABLED=false")
	}
	if reg.OpenOwners() != 0 {
		t.Fatal("no runtime should be registered when calls are disabled")
	}
	release()
}

func TestAttachCallingEnabledAndReleased(t *testing.T) {
	reg := NewCallRegistry()
	w := whatsmeowService{
		config:          &config.Config{CallsEnabled: true},
		callRegistry:    reg,
		myClientPointer: safemap.New[*MyClient](),
	}
	oldCli := whatsmeow.NewClient(&store.Device{}, nil)
	releaseOld := w.attachCalling("inst-a", oldCli)
	if !hasAckHook(t, oldCli) {
		t.Fatal("meowcaller hook not installed when CALLS_ENABLED=true")
	}
	if reg.OpenOwners() != 1 {
		t.Fatalf("expected 1 open runtime, got %d", reg.OpenOwners())
	}

	// A replacement runtime for the same instance gets its own owner; closing
	// the old one leaves the new one open.
	newCli := whatsmeow.NewClient(&store.Device{}, nil)
	releaseNew := w.attachCalling("inst-a", newCli)
	if reg.OpenOwners() != 2 {
		t.Fatalf("expected 2 open runtimes, got %d", reg.OpenOwners())
	}
	releaseOld()
	releaseOld() // idempotent
	if reg.OpenOwners() != 1 {
		t.Fatalf("old runtime not closed, open=%d", reg.OpenOwners())
	}
	releaseNew()
	if reg.OpenOwners() != 0 {
		t.Fatalf("expected no open runtimes, got %d", reg.OpenOwners())
	}
}

func TestRejectedByMeowcallerHandoff(t *testing.T) {
	w := &whatsmeowService{meowRejected: &sync.Map{}}
	if w.takeRejectedByMeowcaller("inst-a", "c1") {
		t.Fatal("nothing marked yet")
	}
	w.markRejectedByMeowcaller("inst-a", "c1")
	if w.takeRejectedByMeowcaller("inst-b", "c1") {
		t.Fatal("handoff must be scoped by instance")
	}
	if !w.takeRejectedByMeowcaller("inst-a", "c1") {
		t.Fatal("marked call not reported")
	}
	if w.takeRejectedByMeowcaller("inst-a", "c1") {
		t.Fatal("handoff must be consumed once")
	}
	// A service without the map (CALLS disabled paths/tests) is safe.
	var empty whatsmeowService
	empty.markRejectedByMeowcaller("i", "c")
	if empty.takeRejectedByMeowcaller("i", "c") {
		t.Fatal("nil map must report false")
	}
}
