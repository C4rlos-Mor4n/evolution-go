package auth_middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	"github.com/gin-gonic/gin"
)

// fakeInstances implements only GetInstanceByToken; any other method panics.
type fakeInstances struct {
	instance_service.InstanceService
	lookups []string
}

func (f *fakeInstances) GetInstanceByToken(token string) (*instance_model.Instance, error) {
	f.lookups = append(f.lookups, token)
	switch token {
	case "token-a":
		return &instance_model.Instance{Id: "inst-a", Token: token}, nil
	case "token-b":
		return &instance_model.Instance{Id: "inst-b", Token: token}, nil
	}
	return nil, errors.New("record not found")
}

func runAuth(t *testing.T, mw gin.HandlerFunc, header, rawQuery string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	reached := ""
	r.GET("/x", mw, func(c *gin.Context) {
		reached = c.MustGet("instance").(*instance_model.Instance).Id
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x?"+rawQuery, nil)
	if header != "" {
		req.Header.Set("apikey", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, reached
}

func TestAuthHeaderOrQuery(t *testing.T) {
	cases := []struct {
		name, header, query string
		code                int
		instance            string
	}{
		{"no credentials", "", "", http.StatusUnauthorized, ""},
		{"empty query token", "", "apikey=", http.StatusUnauthorized, ""},
		{"invalid header", "nope", "", http.StatusUnauthorized, ""},
		{"invalid query", "", "apikey=nope", http.StatusUnauthorized, ""},
		{"global-style token param is not accepted", "", "token=token-a", http.StatusUnauthorized, ""},
		{"header", "token-a", "", http.StatusOK, "inst-a"},
		{"query", "", "apikey=token-b", http.StatusOK, "inst-b"},
		{"header wins over query", "token-a", "apikey=token-b", http.StatusOK, "inst-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInstances{}
			m := NewMiddleware(&config.Config{GlobalApiKey: "global"}, fake)
			code, inst := runAuth(t, m.AuthHeaderOrQuery, tc.header, tc.query)
			if code != tc.code || inst != tc.instance {
				t.Fatalf("got %d/%q, want %d/%q", code, inst, tc.code, tc.instance)
			}
			if tc.header == "" && (tc.query == "" || tc.query == "apikey=") && len(fake.lookups) != 0 {
				t.Fatal("an empty token must be rejected without a database lookup")
			}
		})
	}
}

func TestAuthStillHeaderOnly(t *testing.T) {
	m := NewMiddleware(&config.Config{}, &fakeInstances{})
	if code, _ := runAuth(t, m.Auth, "", "apikey=token-a"); code != http.StatusUnauthorized {
		t.Fatalf("regular routes must keep header-only auth, got %d", code)
	}
}
