package baby_test

import (
	"testing"
	"time"

	"github.com/gregory-m/nanit/pkg/baby"
)

func TestSubscribeChangesSkipsCurrentState(t *testing.T) {
	manager := baby.NewStateManager()
	manager.Update("baby", *baby.NewState().SetStreamState(baby.StreamState_Unhealthy))

	primed := make(chan baby.State, 4)
	changes := make(chan baby.State, 4)

	unsubscribePrimed := manager.Subscribe(func(_ string, state baby.State) { primed <- state })
	defer unsubscribePrimed()
	unsubscribeChanges := manager.SubscribeChanges(func(_ string, state baby.State) { changes <- state })
	defer unsubscribeChanges()

	// Subscribe is told about the state the baby is already in...
	select {
	case state := <-primed:
		if state.GetStreamState() != baby.StreamState_Unhealthy {
			t.Fatalf("primed with stream state %v, want unhealthy", state.GetStreamState())
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe was not sent the current state")
	}

	// ...SubscribeChanges is not, only about what changes from here on.
	manager.Update("baby", *baby.NewState().SetStreamState(baby.StreamState_Alive))

	select {
	case state := <-changes:
		if state.GetStreamState() != baby.StreamState_Alive {
			t.Fatalf("first event had stream state %v, want the change to alive", state.GetStreamState())
		}
	case <-time.After(time.Second):
		t.Fatal("SubscribeChanges was not sent the update")
	}
}
