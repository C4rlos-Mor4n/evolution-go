package call_handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	call_registry "github.com/evolution-foundation/evolution-go/pkg/call/registry"
	call_service "github.com/evolution-foundation/evolution-go/pkg/call/service"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/gin-gonic/gin"
	"github.com/purpshell/meowcaller"
)

// newRouter mounts the call handlers behind a stand-in for the instance auth
// middleware (header apikey -> instance), mirroring pkg/routes.
func newRouter(t *testing.T, enabled bool) (*gin.Engine, *call_registry.Registry[*meowcaller.Call]) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	reg := call_registry.New[*meowcaller.Call]()
	owner := new(int)
	reg.OpenOwner(owner)
	// A zero-value call reports CallPhaseIdle, i.e. "not ringing".
	if err := reg.Store(owner, "inst-a", "CALL1", &meowcaller.Call{}); err != nil {
		t.Fatal(err)
	}
	svc := call_service.NewCallService(nil, nil, reg, enabled, nil)
	h := NewCallHandler(svc)
	auth := func(c *gin.Context) {
		ids := map[string]string{"token-a": "inst-a", "token-b": "inst-b"}
		id, ok := ids[c.GetHeader("apikey")]
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
			return
		}
		c.Set("instance", &instance_model.Instance{Id: id})
		c.Next()
	}
	r := gin.New()
	g := r.Group("/call", auth)
	g.POST("/answer", h.AnswerCall)
	g.POST("/hangup", h.HangupCall)
	g.GET("/stream/:callId", h.StreamCall)
	return r, reg
}

func post(r *gin.Engine, path, token, body string) int {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("apikey", token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestCallRoutesDisabled(t *testing.T) {
	r, _ := newRouter(t, false)
	for _, p := range []string{"/call/answer", "/call/hangup"} {
		if code := post(r, p, "token-a", `{"callId":"CALL1"}`); code != http.StatusNotFound {
			t.Fatalf("%s with CALLS_ENABLED=false: got %d, want 404", p, code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/call/stream/CALL1", nil)
	req.Header.Set("apikey", "token-a")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("stream with CALLS_ENABLED=false: got %d, want 404", w.Code)
	}
}

func TestCallRoutesRequireAuth(t *testing.T) {
	r, _ := newRouter(t, true)
	for _, p := range []string{"/call/answer", "/call/hangup"} {
		if code := post(r, p, "", `{"callId":"CALL1"}`); code != http.StatusUnauthorized {
			t.Fatalf("%s without apikey: got %d, want 401", p, code)
		}
	}
}

func TestCallRoutesCrossInstance(t *testing.T) {
	r, reg := newRouter(t, true)
	for _, p := range []string{"/call/answer", "/call/hangup"} {
		if code := post(r, p, "token-b", `{"callId":"CALL1"}`); code != http.StatusNotFound {
			t.Fatalf("%s for another instance's call: got %d, want 404", p, code)
		}
	}
	if _, ok := reg.Get("inst-a", "CALL1"); !ok {
		t.Fatal("another instance's request removed the call")
	}
}

func TestAnswerValidation(t *testing.T) {
	r, _ := newRouter(t, true)
	if code := post(r, "/call/answer", "token-a", `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing callId: got %d, want 400", code)
	}
	if code := post(r, "/call/answer", "token-a", `{"callId":"UNKNOWN"}`); code != http.StatusNotFound {
		t.Fatalf("unknown call: got %d, want 404", code)
	}
	if code := post(r, "/call/answer", "token-a", `{"callId":"CALL1"}`); code != http.StatusConflict {
		t.Fatalf("answering a call that is not ringing: got %d, want 409", code)
	}
}
