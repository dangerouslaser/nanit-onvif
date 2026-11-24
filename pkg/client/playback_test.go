package client

import (
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestWhiteNoisePlaybackWire(t *testing.T) {
	for _, tc := range []struct {
		on   bool
		wire string
	}{
		{true, "080010001a130800120f5768697465204e6f6973652e776176"},
		{false, "0801"},
	} {
		encoded, err := proto.Marshal(WhiteNoisePlayback(tc.on))
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(encoded); got != tc.wire {
			t.Fatalf("on=%v: got %s, want %s", tc.on, got, tc.wire)
		}
	}
}

func TestResponsePlayback(t *testing.T) {
	// Fixtures contain valid response headers plus wire field 11 (playback).
	for _, tc := range []struct {
		name string
		wire string
		want *Playback_Status
	}{
		{"started", "0801101318c8015a020800", Playback_STARTED.Enum()},
		{"stopped", "0801101318c8015a020801", Playback_STOPPED.Enum()},
		{"missing", "0801101318c801", nil},
		{"other unknown field", "0801101318c80162005a020801", Playback_STOPPED.Enum()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := hex.DecodeString(tc.wire)
			if err != nil {
				t.Fatal(err)
			}
			response := &Response{}
			if err := proto.Unmarshal(wire, response); err != nil {
				t.Fatal(err)
			}
			playback, err := ResponsePlayback(response)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if playback != nil {
					t.Fatal("absent report must not become off")
				}
			} else if playback == nil || playback.Status == nil || *playback.Status != *tc.want {
				t.Fatalf("unexpected playback: %v", playback)
			}
		})
	}
}

func TestResponsePlaybackRejectsMalformedFields(t *testing.T) {
	for _, wire := range []string{"5a030800", "5a00", "80"} {
		fields, err := hex.DecodeString(wire)
		if err != nil {
			t.Fatal(err)
		}
		response := &Response{}
		response.ProtoReflect().SetUnknown(fields)
		if _, err := ResponsePlayback(response); err == nil {
			t.Fatalf("accepted malformed playback: %s", wire)
		}
	}
}
