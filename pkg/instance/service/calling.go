package instance_service

import (
	"errors"
	"strings"
	"time"
	"unicode"

	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	whatsmeow_service "github.com/evolution-foundation/evolution-go/pkg/whatsmeow/service"
)

// Calling status values reported to the admin UI (KRIONIX per-instance calling).
const (
	CallingStatusGlobalDisabled = "global_disabled" // CALLS_ENABLED=false on this server
	CallingStatusDisabled       = "disabled"        // switch off, meowcaller not installed
	CallingStatusActive         = "active"          // switch on, meowcaller installed
	CallingStatusPendingApply   = "pending_apply"   // switch and runtime disagree until a reconnect
	CallingStatusOffline        = "offline"         // no runtime; the switch applies on next connect
)

// ErrCallingActiveCalls rejects an apply that would cut live calls.
var ErrCallingActiveCalls = errors.New("instance has active calls")

// CallingInstanceStatus is the admin view of one instance's calling switch. It never
// carries tokens, JIDs or phone numbers.
type CallingInstanceStatus struct {
	InstanceID    string     `json:"instanceId"`
	Name          string     `json:"name"`
	Connected     bool       `json:"connected"`
	CallsEnabled  bool       `json:"callsEnabled"`
	Status        string     `json:"status"`
	OffersHandled bool       `json:"offersHandled"`
	ActiveCalls   int        `json:"activeCalls"`
	RejectCall    bool       `json:"rejectCall"`
	ChangedAt     *time.Time `json:"changedAt,omitempty"`
	ChangedBy     string     `json:"changedBy,omitempty"`
	Notice        string     `json:"notice,omitempty"`
}

// callingStatus derives the effective state from the persisted switch and the runtime.
func callingStatus(global, enabled bool, rt whatsmeow_service.CallingRuntimeState) string {
	switch {
	case !global:
		return CallingStatusGlobalDisabled
	case !rt.Runtime && !rt.Attached:
		if enabled {
			return CallingStatusOffline
		}
		return CallingStatusDisabled
	case enabled && rt.Attached:
		return CallingStatusActive
	case !enabled && !rt.Attached:
		return CallingStatusDisabled
	default:
		return CallingStatusPendingApply
	}
}

func (i instances) callingView(inst *instance_model.Instance) CallingInstanceStatus {
	global := i.config != nil && i.config.CallsEnabled
	rt := i.whatsmeowService.CallingState(inst.Id)
	st := CallingInstanceStatus{
		InstanceID:   inst.Id,
		Name:         inst.Name,
		Connected:    inst.Connected,
		CallsEnabled: inst.CallsEnabled,
		Status:       callingStatus(global, inst.CallsEnabled, rt),
		ActiveCalls:  rt.ActiveCalls,
		RejectCall:   inst.RejectCall,
		ChangedAt:    inst.CallsEnabledChangedAt,
		ChangedBy:    inst.CallsEnabledChangedBy,
	}
	st.OffersHandled = global && inst.CallsEnabled && rt.Attached
	switch {
	case st.Status == CallingStatusPendingApply && inst.CallsEnabled:
		st.Notice = "Reconnect this instance to start receiving calls in Nix360."
	case st.Status == CallingStatusPendingApply:
		st.Notice = "New calls are already left to WhatsApp; reconnect this instance to fully detach the calling engine."
	}
	if st.CallsEnabled && st.RejectCall {
		st.Notice = strings.TrimSpace(st.Notice + " rejectCall is on: incoming calls will be rejected instead of reaching Nix360.")
	}
	return st
}

// ListCalling returns the calling switch of every instance.
func (i instances) ListCalling() ([]CallingInstanceStatus, error) {
	list, err := i.instanceRepository.ListCalling()
	if err != nil {
		return nil, err
	}
	out := make([]CallingInstanceStatus, 0, len(list))
	for _, inst := range list {
		out = append(out, i.callingView(inst))
	}
	return out, nil
}

// GetCalling returns one instance's calling switch.
func (i instances) GetCalling(instanceId string) (CallingInstanceStatus, error) {
	inst, err := i.instanceRepository.GetInstanceByID(instanceId)
	if err != nil {
		return CallingInstanceStatus{}, instance_model.ErrInstanceNotFound
	}
	return i.callingView(inst), nil
}

// SetCalling persists the switch (admin only) and updates the offer gate. Disabling
// takes effect on the next offer; enabling needs ApplyCalling (one reconnect).
func (i instances) SetCalling(instanceId string, enabled bool, actor string, from string) (CallingInstanceStatus, error) {
	changedBy := "global_api_key"
	if a := sanitizeCallingActor(actor); a != "" {
		changedBy += ":" + a
	}
	if err := i.instanceRepository.SetCallsEnabled(instanceId, enabled, changedBy, time.Now().UTC()); err != nil {
		return CallingInstanceStatus{}, err
	}
	i.whatsmeowService.SetCallingGate(instanceId, enabled)
	// Keep the runtime's copy of the instance in sync (best effort, as advanced settings do).
	_ = i.whatsmeowService.UpdateInstanceAdvancedSettings(instanceId)
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	i.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] AUDIT calling %s by %s from %s", instanceId, state, changedBy, from)
	return i.GetCalling(instanceId)
}

// ApplyCalling reconnects only this instance so meowcaller is attached or detached
// to match the switch. Credentials are kept (same device store). It refuses while the
// instance has live calls unless force is set.
func (i instances) ApplyCalling(instanceId string, force bool, from string) (CallingInstanceStatus, error) {
	st, err := i.GetCalling(instanceId)
	if err != nil {
		return st, err
	}
	if st.Status != CallingStatusPendingApply {
		return st, nil
	}
	if st.ActiveCalls > 0 && !force {
		return st, ErrCallingActiveCalls
	}
	i.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] AUDIT calling apply: reconnecting this instance only (from %s)", instanceId, from)
	if err := i.whatsmeowService.ReconnectClient(instanceId); err != nil {
		return st, err
	}
	return i.GetCalling(instanceId)
}

// sanitizeCallingActor keeps an optional operator label printable and short.
func sanitizeCallingActor(actor string) string {
	actor = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '"' && r != '\\' {
			return r
		}
		return -1
	}, strings.TrimSpace(actor))
	if len([]rune(actor)) > 64 {
		actor = string([]rune(actor)[:64])
	}
	return actor
}
