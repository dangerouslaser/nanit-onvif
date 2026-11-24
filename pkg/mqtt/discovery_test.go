package mqtt

import (
	"encoding/json"
	"testing"
)

func TestSoundDiscovery(t *testing.T) {
	for _, commands := range []bool{false, true} {
		found := false
		for _, config := range discoveryConfigs("nanit", "47e311fe", "47e311fe", commands) {
			if config.ObjectID != "playback_on" {
				continue
			}
			found = true
			var entity haEntityBase
			if err := json.Unmarshal([]byte(config.Payload), &entity); err != nil {
				t.Fatal(err)
			}
			if entity.StateTopic != "nanit/babies/47e311fe/playback_on" || entity.UniqueID != "nanit_47e311fe_playback_on" {
				t.Fatalf("incorrect sound state or identity: %+v", entity)
			}
			if commands {
				if entity.DefaultEntityID != "switch.nanit_47e311fe_sound" {
					t.Fatal("sound entity ID must match the dashboard")
				}
				if config.Component != "switch" || entity.CommandTopic != "nanit/babies/47e311fe/set/playback_on" {
					t.Fatalf("missing sound command: %+v", config)
				}
			} else if config.Component != "binary_sensor" || entity.CommandTopic != "" {
				t.Fatal("read-only discovery exposed a command")
			}
		}
		if !found {
			t.Fatal("sound entity was not discovered")
		}
	}
}
