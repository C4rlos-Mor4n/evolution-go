package websocket_producer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/gorilla/websocket"
)

func newTestProducer(t *testing.T) (*websocketProducer, *httptest.Server) {
	t.Helper()
	p := NewWebsocketProducer(logger_wrapper.NewLoggerManager(&config.Config{
		LogDirectory: t.TempDir(), LogMaxSize: 1, LogMaxBackups: 1,
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(w, r, r.URL.Query().Get("instance"), p)
	}))
	t.Cleanup(srv.Close)
	return p, srv
}

func dial(t *testing.T, srv *httptest.Server, instance string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?instance=" + instance
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func waitSubscribers(t *testing.T, p *websocketProducer, instance string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.clientsMux.RLock()
		got := len(p.clients[instance])
		p.clientsMux.RUnlock()
		if got == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d subscribers for %s", n, instance)
}

func readEvent(t *testing.T, c *websocket.Conn) map[string]interface{} {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestProduceDeliversToEverySubscriberAndDropsDeadOnes(t *testing.T) {
	p, srv := newTestProducer(t)
	a := dial(t, srv, "inst-1")
	b := dial(t, srv, "inst-1")
	waitSubscribers(t, p, "inst-1", 2)

	if err := p.Produce("inst-1.Message", []byte(`{"x":1}`), "inst-1", ""); err != nil {
		t.Fatalf("produce: %v", err)
	}
	for _, c := range []*websocket.Conn{a, b} {
		if ev := readEvent(t, c); ev["queue"] != "inst-1.message" {
			t.Fatalf("unexpected event: %v", ev)
		}
	}

	// Break one subscriber server-side: the next Produce must not fail, must
	// drop it, and must still deliver to the healthy one.
	p.clientsMux.RLock()
	dead := p.clients["inst-1"][0]
	p.clientsMux.RUnlock()
	_ = dead.conn.UnderlyingConn().Close()

	if err := p.Produce("inst-1.Message", []byte(`{"x":2}`), "inst-1", ""); err != nil {
		t.Fatalf("produce after failure must return nil, got %v", err)
	}
	waitSubscribers(t, p, "inst-1", 1)
	p.clientsMux.RLock()
	survivor := p.clients["inst-1"][0]
	p.clientsMux.RUnlock()
	if survivor == dead {
		t.Fatal("dead subscriber was kept")
	}
}
