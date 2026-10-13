package auth_middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAccessLogRedactsQueryCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	r := gin.New()
	r.Use(RedactQueryCredentials, gin.LoggerWithWriter(&logs), RestoreQueryCredentials)
	var seenAPIKey, seenToken, seenOther string
	r.GET("/x", func(c *gin.Context) {
		seenAPIKey, seenToken, seenOther = c.Query("apikey"), c.Query("token"), c.Query("instanceId")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x?apikey=secret-instance-token&token=secret-global&instanceId=abc", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seenAPIKey != "secret-instance-token" || seenToken != "secret-global" || seenOther != "abc" {
		t.Fatalf("handler must see the original query, got %q %q %q", seenAPIKey, seenToken, seenOther)
	}
	out := logs.String()
	if strings.Contains(out, "secret") {
		t.Fatalf("credential leaked into the access log: %s", out)
	}
	if !strings.Contains(out, "REDACTED") || !strings.Contains(out, "instanceId=abc") {
		t.Fatalf("unexpected log line: %s", out)
	}
}

func TestAccessLogUntouchedWithoutCredentials(t *testing.T) {
	if got, changed := redactQuery("a=1&b=2"); changed || got != "a=1&b=2" {
		t.Fatalf("query without credentials must not change, got %q", got)
	}
}
