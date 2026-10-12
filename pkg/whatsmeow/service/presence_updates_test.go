package whatsmeow_service

import (
	"testing"
	"time"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/evolution-foundation/evolution-go/pkg/safemap"
)

func TestPresenceUpdatesSingleGoroutineAndOwnStop(t *testing.T) {
	kill := safemap.New[chan bool]()
	killCh := make(chan bool, 1)
	kill.Set("inst", killCh)
	loggers := logger_wrapper.NewLoggerManager(&config.Config{LogDirectory: t.TempDir(), LogMaxSize: 1})
	mycli := &MyClient{userID: "inst", killChannel: kill, loggerWrapper: loggers}

	mycli.startPresenceUpdates()
	first := mycli.presenceStop
	if first == nil {
		t.Fatal("presence goroutine was not started")
	}
	// Repeated Connected/PushNameSetting events must not start more goroutines.
	mycli.startPresenceUpdates()
	mycli.startPresenceUpdates()
	if mycli.presenceStop != first {
		t.Fatal("a second presence goroutine was started for the same client")
	}

	// The presence loop must not consume the instance kill signal.
	killCh <- true
	time.Sleep(20 * time.Millisecond)
	select {
	case <-killCh:
	default:
		t.Fatal("presence goroutine stole the kill signal")
	}

	mycli.stopPresenceUpdates()
	if mycli.presenceStop != nil {
		t.Fatal("stop did not clear the running marker")
	}
	select {
	case <-first:
	default:
		t.Fatal("stop channel was not closed")
	}
	mycli.stopPresenceUpdates() // idempotent
	time.Sleep(20 * time.Millisecond)
}
