package web

import "sync"

// Broker is an in-memory pub/sub used to fan out filesystem change events to
// connected SSE clients. The payload carries no data: subscribers react by
// re-fetching the board, so a missed signal is harmless as long as the next
// one arrives.
type Broker struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	closed bool
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan struct{}]struct{})}
}

// Subscribe registers a new subscriber and returns a receive-only channel plus
// an unsubscribe function. The returned channel is buffered to size 1 so a
// slow consumer at most coalesces multiple Publish calls into a single event.
func (b *Broker) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	return ch, unsub
}

// Publish delivers a notification to every subscriber. The send is
// non-blocking: if a subscriber's buffer is full the event is dropped for
// that subscriber, which is fine because consumers re-fetch the full board
// state on any tick.
func (b *Broker) Publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Close releases every subscriber. Safe to call multiple times.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
	}
}
