package mqtt

import (
	"fmt"
	"strings"
	"sync"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/utils"
)

// BabyGetter resolves baby metadata (for HA discovery friendly names).
type BabyGetter func() []baby.Baby

// Connection - MQTT context
type Connection struct {
	Opts         Opts
	StateManager *baby.StateManager
	GetBabies    BabyGetter

	// CommandHandler, if set, is invoked for inbound commands on
	// nanit/babies/{uid}/set/{field}. See Phase 3.
	CommandHandler func(babyUID, field, payload string)
}

// NewConnection - constructor
func NewConnection(opts Opts) *Connection {
	return &Connection{
		Opts: opts,
	}
}

// Run - runs the mqtt connection handler. getBabies may be nil; if set, HA
// discovery uses it to resolve friendly names.
func (conn *Connection) Run(manager *baby.StateManager, getBabies BabyGetter, ctx utils.GracefulContext) {
	conn.StateManager = manager
	conn.GetBabies = getBabies

	utils.RunWithPerseverance(func(attempt utils.AttemptContext) {
		runMqtt(conn, attempt)
	}, ctx, utils.PerseverenceOpts{
		RunnerID:       "mqtt",
		ResetThreshold: 2 * time.Second,
		Cooldown: []time.Duration{
			2 * time.Second,
			10 * time.Second,
			1 * time.Minute,
		},
	})
}

func runMqtt(conn *Connection, attempt utils.AttemptContext) {
	opts := MQTT.NewClientOptions()
	opts.AddBroker(conn.Opts.BrokerURL)
	clientID := conn.Opts.ClientID
	if clientID == "" {
		clientID = conn.Opts.TopicPrefix
	}
	opts.SetClientID(clientID)
	opts.SetUsername(conn.Opts.Username)
	opts.SetPassword(conn.Opts.Password)
	opts.SetCleanSession(false)

	client := MQTT.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Error().Str("broker_url", conn.Opts.BrokerURL).Err(token.Error()).Msg("Unable to connect to MQTT broker")
		attempt.Fail(token.Error())
		return
	}

	log.Info().Str("broker_url", conn.Opts.BrokerURL).Msg("Successfully connected to MQTT broker")

	// Subscribe to command topics. Topic shape: {prefix}/babies/{uid}/set/{field}
	if conn.Opts.Commands && conn.CommandHandler != nil {
		cmdTopic := fmt.Sprintf("%s/babies/+/set/+", conn.Opts.TopicPrefix)
		token := client.Subscribe(cmdTopic, 0, func(_ MQTT.Client, msg MQTT.Message) {
			parts := strings.Split(msg.Topic(), "/")
			// Expected layout: [prefix, "babies", uid, "set", field]
			if len(parts) < 5 {
				return
			}
			if parts[len(parts)-4] != "babies" || parts[len(parts)-2] != "set" {
				return
			}
			babyUID := parts[len(parts)-3]
			field := parts[len(parts)-1]
			conn.CommandHandler(babyUID, field, string(msg.Payload()))
		})
		if token.Wait(); token.Error() != nil {
			log.Error().Err(token.Error()).Str("topic", cmdTopic).Msg("Failed to subscribe to MQTT command topics")
		} else {
			log.Info().Str("topic", cmdTopic).Msg("MQTT command subscription active")
		}
	}

	publish := func(key string, value interface{}, babyUID string) {
		topic := fmt.Sprintf("%v/babies/%v/%v", conn.Opts.TopicPrefix, babyUID, key)
		log.Trace().Str("topic", topic).Interface("value", value).Msg("MQTT publish")
		token := client.Publish(topic, 0, false, fmt.Sprintf("%v", value))
		if token.Wait(); token.Error() != nil {
			log.Error().Err(token.Error()).Msgf("Unable to publish %v update", key)
		}
	}

	publishRetained := func(topic, payload string) {
		log.Trace().Str("topic", topic).Msg("MQTT publish (retained)")
		token := client.Publish(topic, 0, true, payload)
		if token.Wait(); token.Error() != nil {
			log.Error().Err(token.Error()).Msgf("Unable to publish retained %v", topic)
		}
	}

	// Per-baby HA-discovery state: publish once when we first learn the baby's
	// friendly name. Guarded by discoveredMu so multiple parallel state events
	// for the same baby can't race.
	discovered := make(map[string]bool)
	var discoveredMu sync.Mutex

	babyName := func(babyUID string) (string, bool) {
		if conn.GetBabies == nil {
			return "", false
		}
		for _, b := range conn.GetBabies() {
			if b.UID == babyUID {
				name := b.Name
				if name == "" {
					name = babyUID
				}
				return name, true
			}
		}
		return "", false
	}

	maybePublishDiscovery := func(babyUID string) {
		if !conn.Opts.HADiscovery {
			return
		}
		discoveredMu.Lock()
		if discovered[babyUID] {
			discoveredMu.Unlock()
			return
		}
		name, ok := babyName(babyUID)
		if !ok {
			discoveredMu.Unlock()
			return
		}
		discovered[babyUID] = true
		discoveredMu.Unlock()

		prefix := conn.Opts.HADiscoveryPrefix
		if prefix == "" {
			prefix = "homeassistant"
		}

		cfgs := discoveryConfigs(conn.Opts.TopicPrefix, babyUID, name, conn.Opts.Commands)
		for _, c := range cfgs {
			topic := fmt.Sprintf("%s/%s/%s_%s/%s/config", prefix, c.Component, conn.Opts.TopicPrefix, babyUID, c.ObjectID)
			publishRetained(topic, c.Payload)
		}
		log.Info().Str("baby_uid", babyUID).Str("name", name).Int("entities", len(cfgs)).Msg("Published HA MQTT discovery")
	}

	unsubscribe := conn.StateManager.Subscribe(func(babyUID string, state baby.State) {
		maybePublishDiscovery(babyUID)

		for key, value := range state.AsMap(false) {
			publish(key, value, babyUID)
		}

		if state.StreamState != nil && *state.StreamState != baby.StreamState_Unknown {
			publish("is_stream_alive", *state.StreamState == baby.StreamState_Alive, babyUID)
		}

		// Publish websocket liveness as retained availability — HA uses this
		// so entities go 'unavailable' when the camera is offline.
		if state.IsWebsocketAlive != nil {
			topic := fmt.Sprintf("%v/babies/%v/is_websocket_alive", conn.Opts.TopicPrefix, babyUID)
			payload := "false"
			if *state.IsWebsocketAlive {
				payload = "true"
			}
			publishRetained(topic, payload)
		}
	})

	// Wait until interrupt signal is received
	<-attempt.Done()

	log.Debug().Msg("Closing MQTT connection on interrupt")
	unsubscribe()
	client.Disconnect(250)
}
