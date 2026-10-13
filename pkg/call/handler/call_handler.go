package call_handler

import (
	"errors"
	"net/http"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	call_service "github.com/evolution-foundation/evolution-go/pkg/call/service"
	call_stream "github.com/evolution-foundation/evolution-go/pkg/call/stream"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/gin-gonic/gin"
)

type CallHandler interface {
	RejectCall(ctx *gin.Context)
	AnswerCall(ctx *gin.Context)
	HangupCall(ctx *gin.Context)
	StreamCall(ctx *gin.Context)
}

type callHandler struct {
	callService call_service.CallService
	stream      *call_stream.Server
}

// callErrorStatus maps call service errors to HTTP status codes.
func callErrorStatus(err error) int {
	switch {
	case errors.Is(err, call_service.ErrCallsDisabled), errors.Is(err, call_registry.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, call_service.ErrInvalidState):
		return http.StatusConflict
	case errors.Is(err, call_service.ErrMissingCallID):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// Reject call
// @Summary Reject call
// @Description Reject call
// @Tags Call
// @Accept json
// @Produce json
// @Param message body call_service.RejectCallStruct true "Call data"
// @Success 200 {object} gin.H "success"
// @Failure 500 {object} gin.H "Internal server error"
// @Router /call/reject [post]
func (g *callHandler) RejectCall(ctx *gin.Context) {
	getInstance := ctx.MustGet("instance")

	instance, ok := getInstance.(*instance_model.Instance)
	if !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "instance not found"})
		return
	}

	var data *call_service.RejectCallStruct
	err := ctx.ShouldBindBodyWithJSON(&data)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err = g.callService.RejectCall(data, instance)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, call_service.ErrInvalidState) {
			status = http.StatusConflict
		}
		ctx.JSON(status, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "success"})
}

// Answer call
// @Summary Answer an incoming call
// @Description Answers a ringing inbound call captured for this instance (requires CALLS_ENABLED=true). Audio/video then flows through GET /call/stream/{callId}.
// @Tags Call
// @Accept json
// @Produce json
// @Param message body call_service.AnswerCallStruct true "Call data"
// @Success 200 {object} gin.H "success"
// @Failure 404 {object} gin.H "Calls disabled or call not found for this instance"
// @Failure 409 {object} gin.H "Call is not ringing"
// @Failure 500 {object} gin.H "Internal server error"
// @Router /call/answer [post]
func (g *callHandler) AnswerCall(ctx *gin.Context) {
	instance, ok := ctx.MustGet("instance").(*instance_model.Instance)
	if !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "instance not found"})
		return
	}

	var data *call_service.AnswerCallStruct
	if err := ctx.ShouldBindBodyWithJSON(&data); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := g.callService.AnswerCall(data, instance); err != nil {
		ctx.JSON(callErrorStatus(err), gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "success"})
}

// Hangup call
// @Summary Hang up a call
// @Description Ends a ringing or active call captured for this instance (requires CALLS_ENABLED=true).
// @Tags Call
// @Accept json
// @Produce json
// @Param message body call_service.HangupCallStruct true "Call data"
// @Success 200 {object} gin.H "success"
// @Failure 404 {object} gin.H "Calls disabled or call not found for this instance"
// @Failure 409 {object} gin.H "Call already ended"
// @Failure 500 {object} gin.H "Internal server error"
// @Router /call/hangup [post]
func (g *callHandler) HangupCall(ctx *gin.Context) {
	instance, ok := ctx.MustGet("instance").(*instance_model.Instance)
	if !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "instance not found"})
		return
	}

	var data *call_service.HangupCallStruct
	if err := ctx.ShouldBindBodyWithJSON(&data); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := g.callService.HangupCall(data, instance); err != nil {
		ctx.JSON(callErrorStatus(err), gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "success"})
}

// Stream call media
// @Summary Stream a call's audio over WebSocket
// @Description WebSocket carrying the call's audio (base64 PCM16LE 16 kHz mono) and inbound H.264 video in a Twilio-Media-Streams-like JSON envelope. Authenticate with the instance token in the apikey header (or apikey query parameter). One consumer per call; closing the socket hangs up the call. Requires CALLS_ENABLED=true.
// @Tags Call
// @Param callId path string true "Call ID"
// @Success 101 "Switching Protocols"
// @Failure 401 {object} gin.H "Not authorized"
// @Failure 404 {object} gin.H "Calls disabled or call not found for this instance"
// @Failure 409 {object} gin.H "A consumer is already attached"
// @Router /call/stream/{callId} [get]
func (g *callHandler) StreamCall(ctx *gin.Context) {
	g.stream.Serve(ctx)
}

func NewCallHandler(
	callService call_service.CallService,
) CallHandler {
	return &callHandler{
		callService: callService,
		stream:      call_stream.NewServer(callService),
	}
}
