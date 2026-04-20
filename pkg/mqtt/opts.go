package mqtt

// Opts - holds configuration needed to establish connection to the broker
type Opts struct {
	BrokerURL string
	ClientID  string

	Username string
	Password string

	TopicPrefix string

	// HADiscovery enables Home Assistant MQTT auto-discovery.
	HADiscovery bool
	// HADiscoveryPrefix is the topic prefix HA listens on (default "homeassistant").
	HADiscoveryPrefix string

	// Commands enables inbound control via {prefix}/babies/{uid}/set/{field}.
	// When combined with HADiscovery, writable fields are published as
	// switch/number entities instead of read-only binary_sensor/sensor.
	Commands bool
}
