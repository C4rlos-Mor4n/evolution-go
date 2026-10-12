package whatsmeow_service

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestReconnectGuardAllowsOnlyOneInFlightPerInstance(t *testing.T) {
	const id = "guard-test-instance"
	t.Cleanup(func() { endReconnect(id) })

	var winners int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tryBeginReconnect(id) {
				atomic.AddInt32(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("expected exactly one reconnect to win the guard, got %d", winners)
	}

	if !tryBeginReconnect("guard-test-other-instance") {
		t.Fatal("guard must be per instance")
	}
	endReconnect("guard-test-other-instance")

	endReconnect(id)
	if !tryBeginReconnect(id) {
		t.Fatal("guard must be released after endReconnect")
	}
}
