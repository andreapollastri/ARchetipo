package web

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokerPublishDeliversToSubscriber(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	ch, unsub := b.Subscribe()
	defer unsub()

	b.Publish()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected event within 1s")
	}
}

func TestBrokerCoalescesWhenSubscriberSlow(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	ch, unsub := b.Subscribe()
	defer unsub()

	for i := 0; i < 100; i++ {
		b.Publish()
	}
	// Buffer is 1: one event must be readable, and no more than one should
	// accumulate (the rest are dropped by the non-blocking send).
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected at least one event")
	}
	select {
	case <-ch:
		t.Fatal("did not expect a second event after first drain")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrokerUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	ch, unsub := b.Subscribe()
	unsub()
	b.Publish()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("channel should be closed after unsubscribe")
	}
}

func TestBrokerCloseUnblocksSubscribers(t *testing.T) {
	b := NewBroker()
	ch, _ := b.Subscribe()
	b.Close()
	b.Close() // idempotent
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("subscribers should be released on Close")
	}
}

func TestBrokerConcurrentPublishSubscribe(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	const subs = 32
	const pubs = 32
	const each = 50

	var received int64
	var wg sync.WaitGroup
	for i := 0; i < subs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := b.Subscribe()
			defer unsub()
			deadline := time.After(2 * time.Second)
			for {
				select {
				case <-deadline:
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
					atomic.AddInt64(&received, 1)
				}
			}
		}()
	}

	var pwg sync.WaitGroup
	for i := 0; i < pubs; i++ {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			for j := 0; j < each; j++ {
				b.Publish()
			}
		}()
	}
	pwg.Wait()

	// Give subscribers time to drain.
	time.Sleep(200 * time.Millisecond)
	b.Close()
	wg.Wait()

	if atomic.LoadInt64(&received) == 0 {
		t.Fatal("expected subscribers to receive at least one event")
	}
}
