package app

import (
	"testing"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/rtmpserver"
	"github.com/gregory-m/nanit/pkg/session"
)

func newStreamURLTestApp(rtmp RTMPOpts, babyUIDs ...string) *App {
	store := session.NewSessionStore()
	store.Update(func(s *session.Session) {
		for _, uid := range babyUIDs {
			s.Babies = append(s.Babies, baby.Baby{UID: uid})
		}
	})

	return &App{
		Opts:             Opts{RTMP: &rtmp},
		SessionStore:     store,
		BabyStateManager: baby.NewStateManager(),
	}
}

func TestLocalStreamURL(t *testing.T) {
	tests := []struct {
		name     string
		rtmp     RTMPOpts
		babyUIDs []string
		wantURLs map[string]string
	}{
		{
			name:     "defaults",
			rtmp:     RTMPOpts{PublicAddr: "192.0.2.1:1935", Path: "/local"},
			babyUIDs: []string{"babyuid1"},
			wantURLs: map[string]string{"babyuid1": "rtmp://192.0.2.1:1935/local/babyuid1"},
		},
		{
			name:     "custom path and key",
			rtmp:     RTMPOpts{PublicAddr: "192.0.2.1:1935", Path: "/camera", Key: "Abc123"},
			babyUIDs: []string{"babyuid1"},
			wantURLs: map[string]string{"babyuid1": "rtmp://192.0.2.1:1935/camera/Abc123"},
		},
		{
			name:     "custom key with several cameras falls back to baby UIDs",
			rtmp:     RTMPOpts{PublicAddr: "192.0.2.1:1935", Path: "/local", Key: "abc123"},
			babyUIDs: []string{"babyuid1", "babyuid2"},
			wantURLs: map[string]string{
				"babyuid1": "rtmp://192.0.2.1:1935/local/babyuid1",
				"babyuid2": "rtmp://192.0.2.1:1935/local/babyuid2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newStreamURLTestApp(tt.rtmp, tt.babyUIDs...)

			for babyUID, wantURL := range tt.wantURLs {
				if got := app.getLocalStreamURL(babyUID); got != wantURL {
					t.Errorf("getLocalStreamURL(%q) = %q, want %q", babyUID, got, wantURL)
				}

				// Whatever key the camera is told to publish under has to lead back to the same baby.
				gotUID, ok := app.babyUIDForStreamKey(app.streamKeyForBaby(babyUID))
				if !ok || gotUID != babyUID {
					t.Errorf("babyUIDForStreamKey(streamKeyForBaby(%q)) = %q, %v", babyUID, gotUID, ok)
				}
			}
		})
	}
}

func TestBabyUIDForStreamKeyRejectsOtherKeys(t *testing.T) {
	app := newStreamURLTestApp(RTMPOpts{Path: "/local", Key: "abc123"}, "babyuid1")

	for _, streamKey := range []string{"babyuid1", "abc1234", "ABC123", ""} {
		if uid, ok := app.babyUIDForStreamKey(streamKey); ok {
			t.Errorf("babyUIDForStreamKey(%q) = %q, want it rejected", streamKey, uid)
		}
	}
}

// The compile-time check that the app's resolver fits the RTMP server's option.
var _ = rtmpserver.Opts{ResolveKey: (&App{}).babyUIDForStreamKey}
