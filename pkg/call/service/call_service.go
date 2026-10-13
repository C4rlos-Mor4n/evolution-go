package call_service

import (
	"context"
	"errors"
	"sync"
	"time"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	call_stream "github.com/evolution-foundation/evolution-go/pkg/call/stream"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/evolution-foundation/evolution-go/pkg/safemap"
	whatsmeow_service "github.com/evolution-foundation/evolution-go/pkg/whatsmeow/service"
	"github.com/gomessguii/logger"
	"github.com/purpshell/meowcaller"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

type CallService interface {
	RejectCall(data *RejectCallStruct, instance *instance_model.Instance) error
	AnswerCall(data *AnswerCallStruct, instance *instance_model.Instance) error
	HangupCall(data *HangupCallStruct, instance *instance_model.Instance) error

	// CallsEnabled and AttachStream back the GET /call/stream/:callId socket
	// (call_stream.Lookup).
	CallsEnabled() bool
	AttachStream(instanceID, callID string, detach func(reason string)) (call_stream.MediaCall, func(), error)
}

var (
	// ErrCallsDisabled is returned by answer/hangup/stream while CALLS_ENABLED=false.
	ErrCallsDisabled = errors.New(call_stream.ErrDisabledMessage)
	// ErrInvalidState is returned when the call is not in a state that allows
	// the operation (e.g. answering a call that is no longer ringing).
	ErrInvalidState = errors.New("call is not in a state that allows this operation")
	// ErrMissingCallID is returned when the request has no callId.
	ErrMissingCallID = errors.New("callId is required")
)

type callService struct {
	clientPointer    *safemap.Map[*whatsmeow.Client]
	whatsmeowService whatsmeow_service.WhatsmeowService
	callRegistry     *call_registry.Registry[*meowcaller.Call]
	callsEnabled     bool
	loggerWrapper    *logger_wrapper.LoggerManager
	// inflight serializes answer/reject/hangup per (instance, call) so two
	// concurrent requests cannot both act on the same call.
	inflight sync.Map
}

type RejectCallStruct struct {
	CallCreator types.JID `json:"callCreator"`
	CallID      string    `json:"callId"`
}

// AnswerCallStruct is the body of POST /call/answer.
type AnswerCallStruct struct {
	CallID string `json:"callId"`
}

// HangupCallStruct is the body of POST /call/hangup.
type HangupCallStruct struct {
	CallID string `json:"callId"`
}

func (c *callService) ensureClientConnected(instanceId string) (*whatsmeow.Client, error) {
	client := c.clientPointer.Get(instanceId)
	c.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] Checking client connection status - Client exists: %v", instanceId, client != nil)

	if client == nil {
		c.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] No client found, attempting to start new instance", instanceId)
		err := c.whatsmeowService.StartInstance(instanceId)
		if err != nil {
			c.loggerWrapper.GetLogger(instanceId).LogError("[%s] Failed to start instance: %v", instanceId, err)
			return nil, errors.New("no active session found")
		}

		c.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] Instance started, waiting 2 seconds...", instanceId)
		time.Sleep(2 * time.Second)

		client = c.clientPointer.Get(instanceId)
		c.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] Checking new client - Exists: %v, Connected: %v",
			instanceId,
			client != nil,
			client != nil && client.IsConnected())

		if client == nil || !client.IsConnected() {
			c.loggerWrapper.GetLogger(instanceId).LogError("[%s] New client validation failed - Exists: %v, Connected: %v",
				instanceId,
				client != nil,
				client != nil && client.IsConnected())
			return nil, errors.New("no active session found")
		}
	} else if !client.IsConnected() {
		c.loggerWrapper.GetLogger(instanceId).LogError("[%s] Existing client is disconnected - Connected status: %v",
			instanceId,
			client.IsConnected())
		return nil, errors.New("client disconnected")
	}

	c.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] Client successfully validated - Connected: %v", instanceId, client.IsConnected())
	return client, nil
}

func (c *callService) CallsEnabled() bool {
	return c.callsEnabled && c.callRegistry != nil
}

// begin claims the per-call operation slot; the returned func releases it.
func (c *callService) begin(instanceID, callID string) (func(), bool) {
	k := instanceID + "/" + callID
	if _, busy := c.inflight.LoadOrStore(k, struct{}{}); busy {
		return nil, false
	}
	return func() { c.inflight.Delete(k) }, true
}

// AnswerCall answers a ringing inbound call registered for this instance.
// Answer negotiates whatever media the offer declared (audio, or audio+video);
// it deliberately does NOT call AcceptVideo, which is only for accepting a
// mid-call audio->video upgrade and errors on calls that already have video.
func (c *callService) AnswerCall(data *AnswerCallStruct, instance *instance_model.Instance) error {
	if !c.CallsEnabled() {
		return ErrCallsDisabled
	}
	if data == nil || data.CallID == "" {
		return ErrMissingCallID
	}
	call, ok := c.callRegistry.Get(instance.Id, data.CallID)
	if !ok {
		return call_registry.ErrNotFound
	}
	done, ok := c.begin(instance.Id, data.CallID)
	if !ok {
		return ErrInvalidState
	}
	defer done()
	if call.State() != meowcaller.CallPhaseRinging {
		return ErrInvalidState
	}
	if err := call.Answer(); err != nil {
		c.loggerWrapper.GetLogger(instance.Id).LogError("[%s] error answering call %s: %v", instance.Id, data.CallID, err)
		return err
	}
	c.callRegistry.MarkAnswered(instance.Id, data.CallID)
	c.loggerWrapper.GetLogger(instance.Id).LogInfo("[%s] call %s answered", instance.Id, data.CallID)
	return nil
}

// HangupCall ends a registered call (ringing or active) of this instance.
func (c *callService) HangupCall(data *HangupCallStruct, instance *instance_model.Instance) error {
	if !c.CallsEnabled() {
		return ErrCallsDisabled
	}
	if data == nil || data.CallID == "" {
		return ErrMissingCallID
	}
	call, ok := c.callRegistry.Get(instance.Id, data.CallID)
	if !ok {
		return call_registry.ErrNotFound
	}
	done, ok := c.begin(instance.Id, data.CallID)
	if !ok {
		return ErrInvalidState
	}
	defer done()
	if call.State() == meowcaller.CallPhaseEnded {
		c.callRegistry.RemoveCall(instance.Id, data.CallID, call, "ended")
		return ErrInvalidState
	}
	err := call.Hangup()
	c.callRegistry.RemoveCall(instance.Id, data.CallID, call, "hangup")
	if err != nil {
		c.loggerWrapper.GetLogger(instance.Id).LogError("[%s] error hanging up call %s: %v", instance.Id, data.CallID, err)
		return err
	}
	c.loggerWrapper.GetLogger(instance.Id).LogInfo("[%s] call %s hung up", instance.Id, data.CallID)
	return nil
}

// AttachStream implements call_stream.Lookup.
func (c *callService) AttachStream(instanceID, callID string, detach func(reason string)) (call_stream.MediaCall, func(), error) {
	if !c.CallsEnabled() {
		return nil, nil, ErrCallsDisabled
	}
	call, release, err := c.callRegistry.AttachStream(instanceID, callID, detach)
	if err != nil {
		return nil, nil, err
	}
	return call, release, nil
}

// RejectCall rejects an inbound call. With CALLS_ENABLED=true and the call
// captured by meowcaller, it goes through meowcaller's Call.Reject, which
// reliably stops the caller's phone ringing when sent from a companion device.
// Otherwise it falls back to the original whatsmeow reject, unchanged.
func (c *callService) RejectCall(data *RejectCallStruct, instance *instance_model.Instance) error {
	if c.CallsEnabled() && data != nil && data.CallID != "" {
		if call, ok := c.callRegistry.Get(instance.Id, data.CallID); ok {
			done, ok := c.begin(instance.Id, data.CallID)
			if !ok {
				return ErrInvalidState
			}
			defer done()
			if call.State() != meowcaller.CallPhaseRinging {
				return ErrInvalidState
			}
			err := call.Reject()
			c.callRegistry.RemoveCall(instance.Id, data.CallID, call, "rejected")
			if err != nil {
				logger.LogError("[%s] error reject call: %v", instance.Id, err)
				return err
			}
			return nil
		}
	}

	client, err := c.ensureClientConnected(instance.Id)
	if err != nil {
		return err
	}

	err = client.RejectCall(context.Background(), data.CallCreator, data.CallID)
	if err != nil {
		logger.LogError("[%s] error reject call: %v", instance.Id, err)
		return err
	}

	return nil
}

func NewCallService(
	clientPointer *safemap.Map[*whatsmeow.Client],
	whatsmeowService whatsmeow_service.WhatsmeowService,
	callRegistry *call_registry.Registry[*meowcaller.Call],
	callsEnabled bool,
	loggerWrapper *logger_wrapper.LoggerManager,
) CallService {
	return &callService{
		clientPointer:    clientPointer,
		whatsmeowService: whatsmeowService,
		callRegistry:     callRegistry,
		callsEnabled:     callsEnabled,
		loggerWrapper:    loggerWrapper,
	}
}
