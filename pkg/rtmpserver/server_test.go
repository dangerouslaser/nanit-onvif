package rtmpserver

import (
	"testing"

	"github.com/gregory-m/nanit/pkg/baby"
)

func TestResolveBabyUID(t *testing.T) {
	customKey := func(streamKey string) (string, bool) {
		if streamKey == "abc123" {
			return "babyuid1", true
		}
		return "", false
	}

	tests := []struct {
		name       string
		path       string
		resolveKey func(string) (string, bool)
		urlPath    string
		wantUID    string
		wantOK     bool
	}{
		{"default path, baby UID as key", "/local", nil, "/local/babyuid1", "babyuid1", true},
		{"custom path", "/camera", nil, "/camera/babyuid1", "babyuid1", true},
		{"custom path with trailing slash", "/camera/", nil, "/camera/babyuid1", "babyuid1", true},
		{"nested custom path", "/a/b", nil, "/a/b/babyuid1", "babyuid1", true},
		{"old default path once the path is customised", "/camera", nil, "/local/babyuid1", "", false},
		{"path only", "/local", nil, "/local", "", false},
		{"empty key", "/local", nil, "/local/", "", false},
		{"extra segment", "/local", nil, "/local/babyuid1/extra", "", false},
		{"path that merely starts the same", "/local", nil, "/localx/babyuid1", "", false},
		{"key that cannot be a baby UID", "/local", nil, "/local/BabyUID1", "", false},
		{"custom key", "/local", customKey, "/local/abc123", "babyuid1", true},
		{"baby UID when a custom key is set", "/local", customKey, "/local/babyuid1", "", false},
		{"custom path and key", "/camera", customKey, "/camera/abc123", "babyuid1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newRtmpHandler(Opts{Path: tt.path, ResolveKey: tt.resolveKey}, baby.NewStateManager(), nil)

			gotUID, gotOK := handler.resolveBabyUID(tt.urlPath)
			if gotUID != tt.wantUID || gotOK != tt.wantOK {
				t.Fatalf("resolveBabyUID(%q) = %q, %v; want %q, %v", tt.urlPath, gotUID, gotOK, tt.wantUID, tt.wantOK)
			}
		})
	}
}

// The camera reconnects and republishes before the connection it replaced has errored out. When that old
// connection finally does, it must not take the new publisher's stream down with it.
func TestStalePublisherDoesNotTearDownItsReplacement(t *testing.T) {
	stateManager := baby.NewStateManager()
	handler := newRtmpHandler(Opts{Path: "/local"}, stateManager, nil)
	alive := *baby.NewState().SetStreamState(baby.StreamState_Alive)

	oldPublisher := handler.getNewPublisher("baby")
	stateManager.Update("baby", alive)

	newPublisher := handler.getNewPublisher("baby")
	stateManager.Update("baby", alive)

	handler.closePublisher("baby", oldPublisher)

	if got := stateManager.GetBabyState("baby").GetStreamState(); got != baby.StreamState_Alive {
		t.Fatalf("stream state after the replaced publisher closed = %v, want alive", got)
	}
	if subscriber, _ := handler.getNewSubscriber("baby"); subscriber == nil {
		t.Fatal("the current publisher was removed when the replaced one closed")
	}

	handler.closePublisher("baby", newPublisher)

	if got := stateManager.GetBabyState("baby").GetStreamState(); got != baby.StreamState_Unhealthy {
		t.Fatalf("stream state after the current publisher closed = %v, want unhealthy", got)
	}
	if subscriber, _ := handler.getNewSubscriber("baby"); subscriber != nil {
		t.Fatal("the publisher is still registered after it closed")
	}
}
