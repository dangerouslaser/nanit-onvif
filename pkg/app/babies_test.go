package app

import (
	"testing"
	"time"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/session"
	"github.com/gregory-m/nanit/pkg/utils"
)

// A web login starts the baby handlers again. The set that is already running must be stopped first,
// otherwise every login adds a websocket and a poller per baby.
func TestStartBabiesReplacesRunningHandlers(t *testing.T) {
	store := session.NewSessionStore()
	store.Update(func(s *session.Session) {
		s.Babies = []baby.Baby{{UID: "babyuid1"}, {UID: "babyuid2"}}
	})

	// No RTMP and no MQTT: the handlers do nothing but wait for their context.
	app := &App{SessionStore: store, BabyStateManager: baby.NewStateManager()}

	ctx, stop := newTestSession()
	defer stop()

	app.startBabies(ctx)
	first := map[string]utils.GracefulRunner{}
	for uid, runner := range app.babyRunners {
		first[uid] = runner
	}
	if len(first) != 2 {
		t.Fatalf("started handlers for %d babies, want 2", len(first))
	}

	app.startBabies(ctx)
	if len(app.babyRunners) != 2 {
		t.Fatalf("%d sets of handlers after a second start, want 2", len(app.babyRunners))
	}

	for uid, runner := range first {
		if app.babyRunners[uid] == runner {
			t.Errorf("handlers of %s were not restarted", uid)
		}

		stopped := make(chan bool, 1)
		go func() {
			cancelled, _ := runner.Wait()
			stopped <- cancelled
		}()

		select {
		case cancelled := <-stopped:
			if !cancelled {
				t.Errorf("first handlers of %s ended without being cancelled", uid)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("first handlers of %s are still running after the restart", uid)
		}
	}
}

// isAlert is optional in the protocol: a sensor update carrying only a timestamp must not crash.
func TestProcessSensorDataWithoutIsAlert(t *testing.T) {
	stateManager := baby.NewStateManager()
	timestamp := int32(1_700_000_000)
	isAlert := true

	for _, sensorType := range []client.SensorType{client.SensorType_MOTION, client.SensorType_SOUND} {
		st := sensorType
		processSensorData("baby", []*client.SensorData{{SensorType: &st, Timestamp: &timestamp}}, stateManager)
	}

	state := stateManager.GetBabyState("baby")
	if state.MotionTimestamp != nil || state.SoundTimestamp != nil {
		t.Fatalf("an update without isAlert set an event timestamp: %+v", state)
	}

	motion := client.SensorType_MOTION
	processSensorData("baby", []*client.SensorData{{SensorType: &motion, Timestamp: &timestamp, IsAlert: &isAlert}}, stateManager)

	state = stateManager.GetBabyState("baby")
	if state.MotionTimestamp == nil || *state.MotionTimestamp != timestamp || state.MotionDetected == nil || !*state.MotionDetected {
		t.Fatalf("an alert did not set the motion state: %+v", state)
	}
}
