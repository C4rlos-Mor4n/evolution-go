package managerext

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInjectPlacesScriptOnceBeforeHead(t *testing.T) {
	in := []byte("<html><head><title>x</title></head><body></body></html>")
	out := inject(in)
	if bytes.Count(out, scriptTag) != 1 || bytes.Index(out, scriptTag) > bytes.Index(out, []byte("</head>")) {
		t.Fatalf("script not injected before </head>: %s", out)
	}
	if !bytes.Equal(inject(out), out) {
		t.Fatal("inject must be idempotent")
	}
}

func TestServeManagerServesIndexAndScript(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte("<html><head></head><body>v1</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	h := ServeManager(index)
	r.GET("/manager/*any", h)
	r.GET("/manager", h)

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for _, p := range []string{"/manager", "/manager/instances/abc/settings"} {
		w := get(p)
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), scriptTag) || !bytes.Contains(w.Body.Bytes(), []byte("v1")) {
			t.Fatalf("%s: code=%d body=%s", p, w.Code, w.Body)
		}
	}
	w := get(ScriptPath)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("kx-calling-card")) || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("script: code=%d type=%q", w.Code, w.Header().Get("Content-Type"))
	}

	// A replaced upstream build is picked up (cache keyed on mtime/size).
	if err := os.WriteFile(index, []byte("<html><head></head><body>v2-longer</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := get("/manager"); !bytes.Contains(w.Body.Bytes(), []byte("v2-longer")) {
		t.Fatalf("stale index served: %s", w.Body)
	}
}
