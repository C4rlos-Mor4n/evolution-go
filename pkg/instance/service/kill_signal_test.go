package instance_service

import (
	"testing"
	"time"
)

func TestSendKillSignalNeverBlocks(t *testing.T) {
	start := time.Now()
	if sendKillSignal(nil, time.Hour) {
		t.Fatal("nil channel must report not delivered")
	}
	if time.Since(start) > time.Second {
		t.Fatal("nil channel must not wait for the timeout")
	}

	unread := make(chan bool)
	if sendKillSignal(unread, 20*time.Millisecond) {
		t.Fatal("unread channel must time out")
	}

	ch := make(chan bool, 1)
	if !sendKillSignal(ch, time.Second) || !<-ch {
		t.Fatal("signal must be delivered to a ready channel")
	}
}
