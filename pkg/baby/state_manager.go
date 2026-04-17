package baby

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// subscriberWorker owns a buffered queue and a single goroutine that drains
// it. One slow subscriber (e.g. MQTT publishing to an unreachable broker)
// can fall behind on its own queue without stalling other subscribers or
// the publisher side.
type subscriberWorker struct {
	queue    chan stateEvent
	callback func(babyUID string, state State)
	dropped  atomic.Uint64
	done     chan struct{}
}

type stateEvent struct {
	babyUID string
	state   State
}

// StateManager - state manager context
type StateManager struct {
	babiesByUID      map[string]State
	subscribers      map[*subscriberWorker]struct{}
	stateMutex       sync.RWMutex
	subscribersMutex sync.RWMutex
}

// NewStateManager - state manager constructor
func NewStateManager() *StateManager {
	return &StateManager{
		babiesByUID: make(map[string]State),
		subscribers: make(map[*subscriberWorker]struct{}),
	}
}

// Update - updates baby info in thread safe manner
func (manager *StateManager) Update(babyUID string, stateUpdate State) {
	var updatedState *State

	manager.stateMutex.Lock()

	if babyState, ok := manager.babiesByUID[babyUID]; ok {
		updatedState = babyState.Merge(&stateUpdate)
		if updatedState == &babyState {
			manager.stateMutex.Unlock()
			return
		}
	} else {
		updatedState = NewState().Merge(&stateUpdate)
	}

	manager.babiesByUID[babyUID] = *updatedState
	stateUpdate.EnhanceLogEvent(log.Debug().Str("baby_uid", babyUID)).Msg("Baby state updated")
	manager.stateMutex.Unlock()

	manager.notifySubscribers(babyUID, stateUpdate)
}

// Subscribe - registers function to be called on every update.
// Returns unsubscribe function. Each subscriber runs on its own goroutine
// with a bounded queue; events are dropped (with a warn log) if a
// subscriber falls behind.
func (manager *StateManager) Subscribe(callback func(babyUID string, state State)) func() {
	worker := &subscriberWorker{
		queue:    make(chan stateEvent, 64),
		callback: callback,
		done:     make(chan struct{}),
	}

	go func() {
		for {
			select {
			case <-worker.done:
				return
			case evt := <-worker.queue:
				worker.callback(evt.babyUID, evt.state)
			}
		}
	}()

	manager.subscribersMutex.Lock()
	manager.subscribers[worker] = struct{}{}
	manager.subscribersMutex.Unlock()

	// Prime the new subscriber with current state for each baby.
	manager.stateMutex.RLock()
	for babyUID, babyState := range manager.babiesByUID {
		enqueue(worker, stateEvent{babyUID, babyState})
	}
	manager.stateMutex.RUnlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			manager.subscribersMutex.Lock()
			delete(manager.subscribers, worker)
			manager.subscribersMutex.Unlock()
			close(worker.done)
		})
	}
}

// GetBabyState - returns current state of a baby
func (manager *StateManager) GetBabyState(babyUID string) *State {
	manager.stateMutex.RLock()
	babyState := manager.babiesByUID[babyUID]
	manager.stateMutex.RUnlock()

	return &babyState
}

func (manager *StateManager) NotifyMotionSubscribers(babyUID string, time time.Time) {
	timestamp := new(int32)
	*timestamp = int32(time.Unix())
	var state = State{MotionTimestamp: timestamp}

	manager.notifySubscribers(babyUID, state)
}

func (manager *StateManager) NotifySoundSubscribers(babyUID string, time time.Time) {
	timestamp := new(int32)
	*timestamp = int32(time.Unix())
	var state = State{SoundTimestamp: timestamp}

	manager.notifySubscribers(babyUID, state)
}

func (manager *StateManager) notifySubscribers(babyUID string, state State) {
	evt := stateEvent{babyUID, state}

	manager.subscribersMutex.RLock()
	workers := make([]*subscriberWorker, 0, len(manager.subscribers))
	for w := range manager.subscribers {
		workers = append(workers, w)
	}
	manager.subscribersMutex.RUnlock()

	for _, w := range workers {
		enqueue(w, evt)
	}
}

func enqueue(w *subscriberWorker, evt stateEvent) {
	select {
	case w.queue <- evt:
	case <-w.done:
	default:
		n := w.dropped.Add(1)
		if n == 1 || n%50 == 0 {
			log.Warn().Uint64("dropped", n).Msg("State subscriber queue full; dropping event")
		}
	}
}
