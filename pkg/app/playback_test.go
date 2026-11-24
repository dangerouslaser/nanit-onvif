package app

import (
	"testing"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
)

func TestProcessPlaybackUsesReportsOnly(t *testing.T) {
	manager := baby.NewStateManager()
	processPlayback("nursery", nil, manager)
	processPlayback("nursery", &client.Playback{}, manager)
	if manager.GetBabyState("nursery").PlaybackOn != nil {
		t.Fatal("absent reports must remain unknown")
	}
	processPlayback("nursery", &client.Playback{Status: client.Playback_STARTED.Enum()}, manager)
	if state := manager.GetBabyState("nursery"); state.PlaybackOn == nil || !*state.PlaybackOn {
		t.Fatal("started report did not turn sound on")
	}
	processPlayback("nursery", &client.Playback{}, manager)
	if !*manager.GetBabyState("nursery").PlaybackOn {
		t.Fatal("missing status changed the last confirmed state")
	}
	processPlayback("nursery", &client.Playback{Status: client.Playback_STOPPED.Enum()}, manager)
	if state := manager.GetBabyState("nursery"); state.PlaybackOn == nil || *state.PlaybackOn {
		t.Fatal("stopped report did not turn sound off")
	}
}
