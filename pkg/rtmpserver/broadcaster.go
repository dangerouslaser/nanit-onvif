package rtmpserver

import (
	"sync"
	"sync/atomic"

	"github.com/notedit/rtmp/av"
	"github.com/rs/zerolog/log"
)

type subscriber struct {
	initialized bool
	pktC        chan av.Packet
	done        chan struct{}
	closeOnce   sync.Once
	dropped     atomic.Uint64
}

func (s *subscriber) signalClose() {
	s.closeOnce.Do(func() { close(s.done) })
}

type broadcaster struct {
	mu          sync.Mutex
	headerPkts  []av.Packet
	subscribers map[*subscriber]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subscribers: make(map[*subscriber]struct{})}
}

func (b *broadcaster) newSubscriber() *subscriber {
	sub := &subscriber{
		pktC: make(chan av.Packet, 64),
		done: make(chan struct{}),
	}

	b.mu.Lock()
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

func (b *broadcaster) unsubscribe(sub *subscriber) {
	b.mu.Lock()
	delete(b.subscribers, sub)
	b.mu.Unlock()
	sub.signalClose()
}

func (b *broadcaster) broadcast(pkt av.Packet) {
	b.mu.Lock()
	if pkt.Type > 2 {
		// Header packets: accumulate for replay to late subscribers.
		b.headerPkts = append(b.headerPkts, pkt)
		b.mu.Unlock()
		return
	}
	// Snapshot so we can release the lock before channel sends.
	subs := make([]*subscriber, 0, len(b.subscribers))
	for s := range b.subscribers {
		subs = append(subs, s)
	}
	headers := b.headerPkts
	b.mu.Unlock()

	for _, sub := range subs {
		deliver(sub, pkt, headers)
	}
}

func deliver(sub *subscriber, pkt av.Packet, headers []av.Packet) {
	if !sub.initialized {
		sub.initialized = true
		// Header packets block (with done fallback): a subscriber can't decode
		// anything without SPS/PPS/config, so we'd rather wait than skip these.
		for _, h := range headers {
			select {
			case sub.pktC <- h:
			case <-sub.done:
				return
			}
		}
	}
	// Data packets are non-blocking: a slow subscriber drops packets rather
	// than stalling the publisher's read loop (which would back up the camera).
	select {
	case sub.pktC <- pkt:
	case <-sub.done:
	default:
		n := sub.dropped.Add(1)
		if n == 1 || n%300 == 0 {
			log.Warn().Uint64("dropped", n).Msg("RTMP subscriber buffer full; dropping packets")
		}
	}
}

func (b *broadcaster) closeSubscribers() {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subscribers))
	for s := range b.subscribers {
		subs = append(subs, s)
	}
	b.subscribers = make(map[*subscriber]struct{})
	b.mu.Unlock()

	for _, s := range subs {
		s.signalClose()
	}
}
