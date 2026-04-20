package app

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/client"
)

// babyCommander sends control/settings writes to a specific baby's websocket.
// The connection swaps in/out as the websocket reconnects — callers always see
// the current one (or nil if the camera is offline).
type babyCommander struct {
	mu   sync.RWMutex
	conn *client.WebsocketConnection
}

func (c *babyCommander) setConn(conn *client.WebsocketConnection) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *babyCommander) currentConn() *client.WebsocketConnection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

func (c *babyCommander) send(reqType client.RequestType, req *client.Request) error {
	conn := c.currentConn()
	if conn == nil {
		return errors.New("websocket not connected")
	}
	await := conn.SendRequest(reqType, req)
	_, err := await(10 * time.Second)
	return err
}

// parseBool accepts the MQTT-ish truthy/falsy payloads HA typically sends.
func parseBool(payload string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(payload)) {
	case "true", "on", "1", "yes":
		return true, true
	case "false", "off", "0", "no":
		return false, true
	}
	return false, false
}

func (app *App) ensureCommander(babyUID string) *babyCommander {
	app.commandersMu.Lock()
	defer app.commandersMu.Unlock()
	if app.commanders == nil {
		app.commanders = make(map[string]*babyCommander)
	}
	if c, ok := app.commanders[babyUID]; ok {
		return c
	}
	c := &babyCommander{}
	app.commanders[babyUID] = c
	return c
}

// HandleMQTTCommand routes an inbound MQTT command to the right baby's
// websocket. Invoked by the MQTT client on {prefix}/babies/{uid}/set/{field}.
func (app *App) HandleMQTTCommand(babyUID, field, payload string) {
	app.commandersMu.RLock()
	c, ok := app.commanders[babyUID]
	app.commandersMu.RUnlock()
	if !ok {
		log.Warn().Str("baby_uid", babyUID).Str("field", field).Msg("MQTT command for unknown baby")
		return
	}

	log.Info().Str("baby_uid", babyUID).Str("field", field).Str("payload", payload).Msg("MQTT command received")

	var err error
	switch field {
	case "night_light_on":
		if on, ok := parseBool(payload); ok {
			enum := client.Control_LIGHT_OFF
			if on {
				enum = client.Control_LIGHT_ON
			}
			err = c.send(client.RequestType_PUT_CONTROL, &client.Request{
				Control: &client.Control{NightLight: &enum},
			})
		}
	case "night_light_timeout":
		if n, e := strconv.ParseInt(strings.TrimSpace(payload), 10, 32); e == nil {
			v := int32(n)
			err = c.send(client.RequestType_PUT_CONTROL, &client.Request{
				Control: &client.Control{NightLightTimeout: &v},
			})
		}
	case "volume":
		if n, e := strconv.ParseInt(strings.TrimSpace(payload), 10, 32); e == nil {
			v := int32(n)
			err = c.send(client.RequestType_PUT_SETTINGS, &client.Request{
				Settings: &client.Settings{Volume: &v},
			})
		}
	case "mic_mute_on":
		if on, ok := parseBool(payload); ok {
			err = c.send(client.RequestType_PUT_SETTINGS, &client.Request{
				Settings: &client.Settings{MicMuteOn: &on},
			})
		}
	case "sleep_mode":
		if on, ok := parseBool(payload); ok {
			err = c.send(client.RequestType_PUT_SETTINGS, &client.Request{
				Settings: &client.Settings{SleepMode: &on},
			})
		}
	case "status_light_on":
		if on, ok := parseBool(payload); ok {
			err = c.send(client.RequestType_PUT_SETTINGS, &client.Request{
				Settings: &client.Settings{StatusLightOn: &on},
			})
		}
	case "night_vision":
		if on, ok := parseBool(payload); ok {
			err = c.send(client.RequestType_PUT_SETTINGS, &client.Request{
				Settings: &client.Settings{NightVision: &on},
			})
		}
	default:
		log.Warn().Str("baby_uid", babyUID).Str("field", field).Msg("MQTT command: unsupported field")
		return
	}

	if err != nil {
		log.Error().Err(err).Str("baby_uid", babyUID).Str("field", field).Msg("MQTT command failed")
	}
}
