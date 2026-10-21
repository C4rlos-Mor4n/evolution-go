package whatsmeow_service

import (
	"reflect"
	"sync"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
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

func callingInstance(id string, enabled bool) *instance_model.Instance {
	return &instance_model.Instance{Id: id, CallsEnabled: enabled}
}

func callingTestService(global bool) whatsmeowService {
	return whatsmeowService{
		config:          &config.Config{CallsEnabled: global},
		callRegistry:    NewCallRegistry(),
		myClientPointer: safemap.New[*MyClient](),
		callingGate:     safemap.New[bool](),
		callingAttached: newCallingAttachments(),
	}
}

func TestAttachCallingDisabledIsInert(t *testing.T) {
	reg := NewCallRegistry()
	w := whatsmeowService{
		config:          &config.Config{CallsEnabled: false},
		callRegistry:    reg,
		myClientPointer: safemap.New[*MyClient](),
	}
	cli := whatsmeow.NewClient(&store.Device{}, nil)
	release := w.attachCalling(callingInstance("inst-a", true), cli)
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
	releaseOld := w.attachCalling(callingInstance("inst-a", true), oldCli)
	if !hasAckHook(t, oldCli) {
		t.Fatal("meowcaller hook not installed when CALLS_ENABLED=true")
	}
	if reg.OpenOwners() != 1 {
		t.Fatalf("expected 1 open runtime, got %d", reg.OpenOwners())
	}

	// A replacement runtime for the same instance gets its own owner; closing
	// the old one leaves the new one open.
	newCli := whatsmeow.NewClient(&store.Device{}, nil)
	releaseNew := w.attachCalling(callingInstance("inst-a", true), newCli)
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

// KRIONIX: an instance without callsEnabled never gets meowcaller, even with the
// global flag on, so WhatsApp keeps its normal call behaviour for that number.
func TestAttachCallingSkipsInstanceWithCallingDisabled(t *testing.T) {
	w := callingTestService(true)
	cli := whatsmeow.NewClient(&store.Device{}, nil)
	release := w.attachCalling(callingInstance("inst-b", false), cli)
	defer release()
	if hasAckHook(t, cli) {
		t.Fatal("meowcaller must not be installed for an instance with calling disabled")
	}
	if w.callRegistry.OpenOwners() != 0 || w.CallingState("inst-b").Attached {
		t.Fatal("no calling runtime may be registered for a disabled instance")
	}
	if w.callingAllowed("inst-b") {
		t.Fatal("offer gate must be closed for a disabled instance")
	}
}

// Two instances on one server: only the enabled one gets meowcaller.
func TestAttachCallingIsPerInstance(t *testing.T) {
	w := callingTestService(true)
	cliA := whatsmeow.NewClient(&store.Device{}, nil)
	cliB := whatsmeow.NewClient(&store.Device{}, nil)
	releaseA := w.attachCalling(callingInstance("inst-a", true), cliA)
	releaseB := w.attachCalling(callingInstance("inst-b", false), cliB)
	if !hasAckHook(t, cliA) || hasAckHook(t, cliB) {
		t.Fatalf("hooks: enabled=%v disabled=%v, want true/false", hasAckHook(t, cliA), hasAckHook(t, cliB))
	}
	if !w.CallingState("inst-a").Attached || w.CallingState("inst-b").Attached {
		t.Fatal("attachment state must follow each instance's switch")
	}
	releaseA()
	releaseB()
	if w.CallingState("inst-a").Attached {
		t.Fatal("release must clear the attachment")
	}
}

// Turning calling off closes the offer gate at once, before any reconnect; a
// replaced runtime's release keeps its successor's attachment.
func TestCallingGateAndAttachmentOwnership(t *testing.T) {
	w := callingTestService(true)
	oldCli := whatsmeow.NewClient(&store.Device{}, nil)
	releaseOld := w.attachCalling(callingInstance("inst-a", true), oldCli)
	if !w.callingAllowed("inst-a") {
		t.Fatal("gate must open for an enabled instance")
	}
	w.SetCallingGate("inst-a", false)
	if w.callingAllowed("inst-a") {
		t.Fatal("disabling must close the gate immediately")
	}
	w.SetCallingGate("inst-a", true)

	newCli := whatsmeow.NewClient(&store.Device{}, nil)
	releaseNew := w.attachCalling(callingInstance("inst-a", true), newCli)
	releaseOld()
	if !w.CallingState("inst-a").Attached {
		t.Fatal("old runtime release cleared the new runtime's attachment")
	}
	releaseNew()
	if w.CallingState("inst-a").Attached {
		t.Fatal("attachment must clear when the current runtime is released")
	}

	// Global flag off: the gate stays closed whatever the instance switch says.
	off := callingTestService(false)
	off.SetCallingGate("inst-a", true)
	if off.callingAllowed("inst-a") {
		t.Fatal("CALLS_ENABLED=false must keep every gate closed")
	}
}
