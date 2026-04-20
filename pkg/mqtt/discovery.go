package mqtt

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
)

// discoveryConfig is one Home Assistant MQTT discovery entity.
type discoveryConfig struct {
	Component string
	ObjectID  string
	Payload   string
}

type haDevice struct {
	Identifiers  []string   `json:"identifiers"`
	Connections  [][]string `json:"connections,omitempty"`
	Name         string     `json:"name"`
	Manufacturer string     `json:"manufacturer"`
	Model        string     `json:"model"`
}

type haAvailability struct {
	Topic               string `json:"topic"`
	PayloadAvailable    string `json:"payload_available"`
	PayloadNotAvailable string `json:"payload_not_available"`
}

type haEntityBase struct {
	Name              string           `json:"name"`
	UniqueID          string           `json:"unique_id"`
	StateTopic        string           `json:"state_topic"`
	Availability      []haAvailability `json:"availability,omitempty"`
	Device            haDevice         `json:"device"`
	DeviceClass       string           `json:"device_class,omitempty"`
	StateClass        string           `json:"state_class,omitempty"`
	UnitOfMeasurement string           `json:"unit_of_measurement,omitempty"`
	ValueTemplate     string           `json:"value_template,omitempty"`
	Icon              string           `json:"icon,omitempty"`
	EntityCategory    string           `json:"entity_category,omitempty"`
	PayloadOn         string           `json:"payload_on,omitempty"`
	PayloadOff        string           `json:"payload_off,omitempty"`
	// Command-capable fields (switches/numbers); Phase 3 wires these up.
	CommandTopic string  `json:"command_topic,omitempty"`
	Min          float64 `json:"min,omitempty"`
	Max          float64 `json:"max,omitempty"`
	Step         float64 `json:"step,omitempty"`
}

// discoveryConfigs builds the full set of HA discovery entities for one baby.
// When commands is true, writable fields are published as switch/number
// entities with command_topic set instead of read-only binary_sensor/sensor.
func discoveryConfigs(topicPrefix, babyUID, babyName string, commands bool) []discoveryConfig {
	stateTopic := func(key string) string {
		return fmt.Sprintf("%s/babies/%s/%s", topicPrefix, babyUID, key)
	}
	commandTopic := func(key string) string {
		return fmt.Sprintf("%s/babies/%s/set/%s", topicPrefix, babyUID, key)
	}
	device := haDevice{
		Identifiers:  []string{fmt.Sprintf("%s_%s", topicPrefix, babyUID)},
		Connections:  [][]string{{"mac", baby.SyntheticMAC(babyUID)}},
		Name:         fmt.Sprintf("Nanit %s", babyName),
		Manufacturer: "Nanit",
		Model:        "Baby Monitor",
	}
	availability := []haAvailability{{
		Topic:               stateTopic("is_websocket_alive"),
		PayloadAvailable:    "true",
		PayloadNotAvailable: "false",
	}}
	uid := func(key string) string {
		return fmt.Sprintf("%s_%s_%s", topicPrefix, babyUID, key)
	}

	entries := []entry{
		{"sensor", "temperature", haEntityBase{
			Name: "Temperature", UniqueID: uid("temperature"),
			StateTopic:  stateTopic("temperature"),
			DeviceClass: "temperature", StateClass: "measurement",
			UnitOfMeasurement: "°C",
			Availability:      availability, Device: device,
		}},
		{"sensor", "humidity", haEntityBase{
			Name: "Humidity", UniqueID: uid("humidity"),
			StateTopic:  stateTopic("humidity"),
			DeviceClass: "humidity", StateClass: "measurement",
			UnitOfMeasurement: "%",
			Availability:      availability, Device: device,
		}},
		{"sensor", "light_level", haEntityBase{
			Name: "Light level", UniqueID: uid("light_level"),
			StateTopic:  stateTopic("light_level"),
			DeviceClass: "illuminance", StateClass: "measurement",
			UnitOfMeasurement: "lx",
			Availability:      availability, Device: device,
		}},
		{"binary_sensor", "motion_detected", haEntityBase{
			Name: "Motion", UniqueID: uid("motion_detected"),
			StateTopic:  stateTopic("motion_detected"),
			DeviceClass: "motion",
			PayloadOn:   "true", PayloadOff: "false",
			Availability: availability, Device: device,
		}},
		{"binary_sensor", "sound_detected", haEntityBase{
			Name: "Sound", UniqueID: uid("sound_detected"),
			StateTopic:  stateTopic("sound_detected"),
			DeviceClass: "sound",
			PayloadOn:   "true", PayloadOff: "false",
			Availability: availability, Device: device,
		}},
		{"binary_sensor", "is_night", haEntityBase{
			Name: "Night mode", UniqueID: uid("is_night"),
			StateTopic: stateTopic("is_night"),
			PayloadOn:  "true", PayloadOff: "false",
			Icon:         "mdi:weather-night",
			Availability: availability, Device: device,
		}},
		writableSwitch(commands, "night_light_on", "Night light", "mdi:lightbulb", "",
			stateTopic, commandTopic, uid, availability, device),
		writableSwitch(commands, "night_vision", "Night vision", "mdi:weather-night", "diagnostic",
			stateTopic, commandTopic, uid, availability, device),
		writableSwitch(commands, "sleep_mode", "Sleep mode", "mdi:sleep", "diagnostic",
			stateTopic, commandTopic, uid, availability, device),
		writableSwitch(commands, "status_light_on", "Status light", "mdi:led-on", "diagnostic",
			stateTopic, commandTopic, uid, availability, device),
		writableSwitch(commands, "mic_mute_on", "Microphone muted", "mdi:microphone-off", "diagnostic",
			stateTopic, commandTopic, uid, availability, device),
		{"binary_sensor", "is_stream_alive", haEntityBase{
			Name: "Stream", UniqueID: uid("is_stream_alive"),
			StateTopic:  stateTopic("is_stream_alive"),
			DeviceClass: "connectivity",
			PayloadOn:   "true", PayloadOff: "false",
			EntityCategory: "diagnostic",
			Availability:   availability, Device: device,
		}},
		// Note: no is_connected_to_server entity — the camera doesn't reliably
		// emit Status.connectionToServer, so it would just sit on "unknown".
		writableNumber(commands, "volume", "Volume", "%", "mdi:volume-high", "",
			0, 100, 1, stateTopic, commandTopic, uid, availability, device),
		writableNumber(commands, "night_light_timeout", "Night light timeout", "s", "mdi:timer",
			"diagnostic", 0, 3600, 60, stateTopic, commandTopic, uid, availability, device),
		{"sensor", "firmware_version", haEntityBase{
			Name: "Firmware", UniqueID: uid("firmware_version"),
			StateTopic:     stateTopic("firmware_version"),
			Icon:           "mdi:chip",
			EntityCategory: "diagnostic",
			Availability:   availability, Device: device,
		}},
		{"sensor", "hardware_version", haEntityBase{
			Name: "Hardware", UniqueID: uid("hardware_version"),
			StateTopic:     stateTopic("hardware_version"),
			Icon:           "mdi:chip",
			EntityCategory: "diagnostic",
			Availability:   availability, Device: device,
		}},
		{"sensor", "mounting_mode", haEntityBase{
			Name: "Mounting mode", UniqueID: uid("mounting_mode"),
			StateTopic:     stateTopic("mounting_mode"),
			Icon:           "mdi:camera-metering-center",
			EntityCategory: "diagnostic",
			Availability:   availability, Device: device,
		}},
	}

	out := make([]discoveryConfig, 0, len(entries))
	for _, e := range entries {
		payload, err := json.Marshal(e.entity)
		if err != nil {
			log.Error().Err(err).Str("object_id", e.objectID).Msg("Failed to marshal HA discovery config")
			continue
		}
		out = append(out, discoveryConfig{
			Component: e.component,
			ObjectID:  e.objectID,
			Payload:   string(payload),
		})
	}
	return out
}

type entry struct {
	component string
	objectID  string
	entity    haEntityBase
}

func writableSwitch(commands bool, key, name, icon, cat string,
	stateTopic, commandTopic func(string) string,
	uid func(string) string, availability []haAvailability, device haDevice) entry {
	base := haEntityBase{
		Name: name, UniqueID: uid(key),
		StateTopic: stateTopic(key),
		PayloadOn:  "true", PayloadOff: "false",
		Icon:           icon,
		EntityCategory: cat,
		Availability:   availability, Device: device,
	}
	if commands {
		base.CommandTopic = commandTopic(key)
		return entry{"switch", key, base}
	}
	return entry{"binary_sensor", key, base}
}

func writableNumber(commands bool, key, name, unit, icon, cat string,
	min, max, step float64,
	stateTopic, commandTopic func(string) string,
	uid func(string) string, availability []haAvailability, device haDevice) entry {
	base := haEntityBase{
		Name: name, UniqueID: uid(key),
		StateTopic:        stateTopic(key),
		UnitOfMeasurement: unit,
		Icon:              icon,
		EntityCategory:    cat,
		Availability:      availability, Device: device,
	}
	if commands {
		base.CommandTopic = commandTopic(key)
		base.Min = min
		base.Max = max
		base.Step = step
		return entry{"number", key, base}
	}
	return entry{"sensor", key, base}
}
