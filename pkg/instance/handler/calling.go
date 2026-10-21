package instance_handler

import (
	"errors"
	"net/http"

	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// setCallingRequest is the body of PUT /calling/instances/:instanceId.
type setCallingRequest struct {
	Enabled *bool `json:"enabled"`
	// Actor is an optional operator label stored in the audit fields.
	Actor string `json:"actor"`
}

// ListCalling lists every instance's calling switch (global API key).
// @Summary List per-instance calling switches
// @Tags Calling
// @Produce json
// @Success 200 {object} gin.H
// @Failure 401 {object} gin.H "Not authorized"
// @Router /calling/instances [get]
func (h *instanceHandler) ListCalling(c *gin.Context) {
	list, err := h.instanceService.ListCalling()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list instances"})
		return
	}
	globalEnabled := h.config != nil && h.config.CallsEnabled
	c.JSON(http.StatusOK, gin.H{"globalCallsEnabled": globalEnabled, "instances": list})
}

// SetCalling turns calling on or off for one instance (global API key).
// @Summary Enable or disable calling for one instance
// @Description Disabling takes effect on the next incoming call. Enabling needs POST /calling/instances/{instanceId}/apply (reconnects only that instance).
// @Tags Calling
// @Accept json
// @Produce json
// @Param instanceId path string true "Instance ID"
// @Param body body setCallingRequest true "enabled (required), actor (optional operator label)"
// @Success 200 {object} instance_service.CallingInstanceStatus
// @Failure 400 {object} gin.H "Invalid request"
// @Failure 401 {object} gin.H "Not authorized"
// @Failure 404 {object} gin.H "Instance not found"
// @Router /calling/instances/{instanceId} [put]
func (h *instanceHandler) SetCalling(c *gin.Context) {
	instanceId, ok := callingInstanceID(c)
	if !ok {
		return
	}
	var req setCallingRequest
	if err := bindInstanceSettingsJSON(c, &req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": `body must be {"enabled": true|false}`})
		return
	}
	st, err := h.instanceService.SetCalling(instanceId, *req.Enabled, req.Actor, c.ClientIP())
	if err != nil {
		writeCallingError(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

// ApplyCalling reconnects only this instance so the calling switch takes effect.
// @Summary Apply the calling switch (reconnects only this instance)
// @Description Keeps the WhatsApp session. Refused with 409 while the instance has active calls unless ?force=true.
// @Tags Calling
// @Produce json
// @Param instanceId path string true "Instance ID"
// @Param force query bool false "Apply even with active calls (they are hung up)"
// @Success 200 {object} instance_service.CallingInstanceStatus
// @Failure 401 {object} gin.H "Not authorized"
// @Failure 404 {object} gin.H "Instance not found"
// @Failure 409 {object} gin.H "Active calls"
// @Router /calling/instances/{instanceId}/apply [post]
func (h *instanceHandler) ApplyCalling(c *gin.Context) {
	instanceId, ok := callingInstanceID(c)
	if !ok {
		return
	}
	st, err := h.instanceService.ApplyCalling(instanceId, c.Query("force") == "true", c.ClientIP())
	if err != nil {
		if errors.Is(err, instance_service.ErrCallingActiveCalls) {
			c.JSON(http.StatusConflict, gin.H{"error": "instance has active calls; retry with ?force=true to hang them up", "status": st})
			return
		}
		writeCallingError(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func callingInstanceID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("instanceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instanceId"})
		return "", false
	}
	return id.String(), true
}

func writeCallingError(c *gin.Context, err error) {
	if errors.Is(err, instance_model.ErrInstanceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update calling"})
}

// errCallingNeedsAdmin is returned when an instance token tries to change callsEnabled.
const errCallingNeedsAdmin = "callsEnabled can only be changed with the global API key (PUT /calling/instances/{instanceId})"

// guardCallsEnabled lets an instance-token PUT echo the current callsEnabled (the
// Manager sends the settings it read) but never change it.
func (h *instanceHandler) guardCallsEnabled(c *gin.Context, instanceId string, settings *instance_model.AdvancedSettings) bool {
	if settings.CallsEnabled == nil {
		return true
	}
	current, err := h.instanceService.GetAdvancedSettings(instanceId)
	if err != nil {
		writeInstanceSettingsError(c, err)
		return false
	}
	currentEnabled := current.CallsEnabled != nil && *current.CallsEnabled
	if currentEnabled != *settings.CallsEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": errCallingNeedsAdmin})
		return false
	}
	settings.CallsEnabled = nil
	return true
}
