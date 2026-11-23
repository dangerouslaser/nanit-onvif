package mqtt

import (
	"encoding/json"
	"testing"
)

func discoveryPayload(t *testing.T, commands bool, objectID string) (component string, payload map[string]interface{}) {
	t.Helper()

	for _, c := range discoveryConfigs("nanit", "babyuid1", "Baby", commands) {
		if c.ObjectID != objectID {
			continue
		}
		if err := json.Unmarshal([]byte(c.Payload), &payload); err != nil {
			t.Fatalf("%s discovery payload is not JSON: %v", objectID, err)
		}
		return c.Component, payload
	}

	t.Fatalf("no discovery config for %q", objectID)
	return "", nil
}

// Home Assistant assumes a minimum of 1 when none is given, which made volume 0 unreachable.
func TestWritableNumbersPublishZeroMinimum(t *testing.T) {
	component, payload := discoveryPayload(t, true, "volume")

	if component != "number" {
		t.Fatalf("volume is published as %q, want a number entity", component)
	}

	min, hasMin := payload["min"]
	if !hasMin {
		t.Fatal("volume has no min, so Home Assistant falls back to 1")
	}
	if min != float64(0) {
		t.Fatalf("volume min = %v, want 0", min)
	}
	if max, _ := payload["max"].(float64); max <= 0 {
		t.Fatalf("volume max = %v, want a positive maximum", payload["max"])
	}
}

func TestReadOnlyNumbersHaveNoRange(t *testing.T) {
	component, payload := discoveryPayload(t, false, "volume")

	if component != "sensor" {
		t.Fatalf("volume without commands is published as %q, want a sensor", component)
	}
	for _, key := range []string{"min", "max", "step", "command_topic"} {
		if _, has := payload[key]; has {
			t.Errorf("read-only volume sensor has %q", key)
		}
	}
}

func TestParseCommandTopic(t *testing.T) {
	tests := []struct {
		topic     string
		wantUID   string
		wantField string
		wantOK    bool
	}{
		{"nanit/babies/babyuid1/set/night_light", "babyuid1", "night_light", true},
		{"home/nanit/babies/babyuid1/set/volume", "babyuid1", "volume", true},
		{"nanit/babies/babyuid1/night_light", "", "", false},
		{"nanit/babies/babyuid1/get/night_light", "", "", false},
		{"nanit/cameras/babyuid1/set/night_light", "", "", false},
		{"babies/babyuid1/set/night_light", "", "", false},
	}

	for _, tt := range tests {
		gotUID, gotField, gotOK := parseCommandTopic(tt.topic)
		if gotUID != tt.wantUID || gotField != tt.wantField || gotOK != tt.wantOK {
			t.Errorf("parseCommandTopic(%q) = %q, %q, %v; want %q, %q, %v",
				tt.topic, gotUID, gotField, gotOK, tt.wantUID, tt.wantField, tt.wantOK)
		}
	}
}
