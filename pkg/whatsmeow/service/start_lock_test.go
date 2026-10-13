package whatsmeow_service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLockInstanceStartSerializesPerInstance(t *testing.T) {
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockInstanceStart("inst-serial")
			defer unlock()
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("expected at most 1 concurrent StartClient setup per instance, got %d", maxInside)
	}
}

func TestLockInstanceStartUnlockIsIdempotentAndPerInstance(t *testing.T) {
	unlockA := lockInstanceStart("inst-a")
	unlockA()
	unlockA() // second call must be a no-op, not a double unlock panic

	done := make(chan struct{})
	unlockB := lockInstanceStart("inst-b")
	go func() {
		u := lockInstanceStart("inst-c") // another instance is not blocked by inst-b
		u()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lock of one instance blocked another instance")
	}
	unlockB()
}

func TestDeliverKillSignal(t *testing.T) {
	if deliverKillSignal(nil, 10*time.Millisecond) {
		t.Fatal("nil channel must not report delivery")
	}
	if deliverKillSignal(make(chan bool), 10*time.Millisecond) {
		t.Fatal("unattended channel must time out")
	}
	ch := make(chan bool)
	go func() { <-ch }()
	if !deliverKillSignal(ch, time.Second) {
		t.Fatal("listening runtime must receive the signal")
	}
}
