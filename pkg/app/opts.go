package app

import (
	"time"

	"github.com/gregory-m/nanit/pkg/mqtt"
)

// Opts - application run options
type Opts struct {
	NanitCredentials NanitCredentials
	SessionFile      string
	DataDirectories  DataDirectories
	HTTPEnabled      bool
	MQTT             *mqtt.Opts
	RTMP             *RTMPOpts
	RTSP             *RTSPOpts
	ONVIF            *ONVIFOpts
	Web              *WebOpts
	EventPolling     EventPollingOpts
}

// NanitCredentials - user credentials for Nanit account
type NanitCredentials struct {
	Email        string
	Password     string
	RefreshToken string
}

// DataDirectories - dictionary of dir paths
type DataDirectories struct {
	BaseDir  string
	VideoDir string
	LogDir   string
}

// RTMPOpts - options for RTMP streaming
type RTMPOpts struct {
	// IP:Port of the interface on which we should listen
	ListenAddr string

	// IP:Port under which can Cam reach the RTMP server
	PublicAddr string

	// Path where the cam can reach the RTMP server
	Path string

	// Key for this stream
	Key string
}

// RTSPOpts - options for RTSP streaming
type RTSPOpts struct {
	// IP:Port of the interface on which we should listen
	ListenAddr string
}

// ONVIFOpts - options for ONVIF server
type ONVIFOpts struct {
	// IP:Port of the interface on which the ONVIF HTTP server should listen
	ListenAddr string
	Username   string
	Password   string

	// EventsEnabled toggles the ONVIF Events (PullPoint) service.
	EventsEnabled bool
	// EventHold is how long motion/sound stays "active" after a REST-polled
	// trigger. Websocket isAlert=false clears immediately regardless.
	EventHold time.Duration
}

// WebOpts - options for the web UI dashboard
type WebOpts struct {
	// IP:Port of the interface on which the web UI should listen
	ListenAddr string
}

type EventPollingOpts struct {
	Enabled         bool
	PollingInterval time.Duration
	MessageTimeout  time.Duration
	// DetectedHold is how long motion_detected / sound_detected stay true
	// after a REST-polled event before auto-clearing to false.
	DetectedHold time.Duration
}
