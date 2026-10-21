package instance_handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	"github.com/gin-gonic/gin"
)

// An instance token may echo callsEnabled (the Manager re-sends what it read) but
// never change it: enabling calling is an administrative action.
func TestInstanceTokenCannotChangeCallsEnabled(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		updates    int
	}{
		{"enable_forbidden", `{"callsEnabled":true}`, http.StatusForbidden, 0},
		{"echo_current_allowed", `{"callsEnabled":false,"alwaysOnline":true}`, http.StatusOK, 1},
		{"omitted_allowed", `{"alwaysOnline":true}`, http.StatusOK, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &settingsServiceStub{}
			req := httptest.NewRequest(http.MethodPut, "/instance/"+settingsInstanceID+"/advanced-settings", strings.NewReader(tt.body))
			req.Header.Set("apikey", "instance-token")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			settingsTestRouter(svc).ServeHTTP(response, req)
			if response.Code != tt.status || svc.updateCalls != tt.updates {
				t.Fatalf("status=%d updates=%d body=%s, want %d/%d", response.Code, svc.updateCalls, response.Body, tt.status, tt.updates)
			}
			if svc.updated != nil && svc.updated.CallsEnabled != nil {
				t.Fatal("callsEnabled must never reach the advanced-settings update")
			}
		})
	}
}

type callingServiceStub struct {
	settingsServiceStub
	setCalls   int
	lastActor  string
	lastEnable bool
}

func (s *callingServiceStub) SetCalling(id string, enabled bool, actor, from string) (instance_service.CallingInstanceStatus, error) {
	s.setCalls++
	s.lastActor, s.lastEnable = actor, enabled
	return instance_service.CallingInstanceStatus{InstanceID: id, CallsEnabled: enabled, Status: instance_service.CallingStatusPendingApply}, nil
}

func TestSetCallingValidatesBody(t *testing.T) {
	svc := &callingServiceStub{}
	router := gin.New()
	handler := NewInstanceHandler(svc, nil)
	router.PUT("/calling/instances/:instanceId", handler.SetCalling)
	for _, tt := range []struct {
		name, id, body string
		status, sets   int
	}{
		{"missing_enabled", settingsInstanceID, `{}`, http.StatusBadRequest, 0},
		{"wrong_type", settingsInstanceID, `{"enabled":"yes"}`, http.StatusBadRequest, 0},
		{"bad_id", "nope", `{"enabled":true}`, http.StatusBadRequest, 0},
		{"enable", settingsInstanceID, `{"enabled":true,"actor":"ops"}`, http.StatusOK, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc.setCalls = 0
			req := httptest.NewRequest(http.MethodPut, "/calling/instances/"+tt.id, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tt.status || svc.setCalls != tt.sets {
				t.Fatalf("status=%d sets=%d body=%s", response.Code, svc.setCalls, response.Body)
			}
		})
	}
	if !svc.lastEnable || svc.lastActor != "ops" {
		t.Fatalf("service got enabled=%v actor=%q", svc.lastEnable, svc.lastActor)
	}
}
