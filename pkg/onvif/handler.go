package onvif

import (
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
)

// ServerConfig configures the ONVIF HTTP handler.
type ServerConfig struct {
	RTSPPort  string             // e.g. "8554"
	GetBabies func() []baby.Baby // returns current baby list
}

// NewHandler returns an http.Handler that serves ONVIF SOAP requests.
// Each baby UID maps to one ONVIF profile (ProfileToken = baby UID).
func NewHandler(config ServerConfig) http.Handler {
	mux := http.NewServeMux()

	handle := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		action := GetRequestAction(b)
		if action == "" {
			log.Debug().Str("path", r.URL.Path).Msg("ONVIF: empty action")
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		log.Debug().Str("action", action).Msg("ONVIF request")

		host := r.Host

		var resp []byte

		switch action {
		case DeviceGetCapabilities:
			resp = GetCapabilitiesResponse(host)

		case DeviceGetServices:
			resp = GetServicesResponse(host)

		case DeviceGetDeviceInformation:
			resp = GetDeviceInformationResponse("Nanit", "Baby Monitor", "1.0", UUID())

		case MediaGetProfiles:
			resp = GetProfilesResponse(babyNames(config))

		case MediaGetProfile:
			token := FindTagValue(b, "ProfileToken")
			if token == "" {
				babies := config.GetBabies()
				if len(babies) > 0 {
					token = babies[0].UID
				}
			}
			resp = GetProfileResponse(token)

		case MediaGetVideoSources:
			resp = GetVideoSourcesResponse(babyNames(config))

		case MediaGetVideoSourceConfigurations:
			resp = GetVideoSourceConfigurationsResponse(babyNames(config))

		case MediaGetVideoSourceConfiguration:
			token := FindTagValue(b, "ConfigurationToken")
			if token == "" {
				babies := config.GetBabies()
				if len(babies) > 0 {
					token = babies[0].UID
				}
			}
			resp = GetVideoSourceConfigurationResponse(token)

		case MediaGetStreamUri:
			token := FindTagValue(b, "ProfileToken")
			if token == "" {
				babies := config.GetBabies()
				if len(babies) > 0 {
					token = babies[0].UID
				}
			}
			// Construct RTSP URL using the request host's IP and configured RTSP port
			rtspHost := stripPort(host)
			uri := fmt.Sprintf("rtsp://%s:%s/local/%s", rtspHost, config.RTSPPort, token)
			resp = GetStreamUriResponse(uri)

		case MediaGetSnapshotUri:
			// Snapshots not supported
			resp = GetSnapshotUriResponse("")

		default:
			resp = StaticResponse(action)
		}

		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.Write(resp)
	}

	mux.HandleFunc("/onvif/device_service", handle)
	mux.HandleFunc("/onvif/media_service", handle)
	// Some clients probe the root path
	mux.HandleFunc("/onvif/", handle)

	return mux
}

func babyNames(config ServerConfig) []string {
	babies := config.GetBabies()
	names := make([]string, len(babies))
	for i, b := range babies {
		names[i] = b.UID
	}
	return names
}

// stripPort removes the port from a host:port string.
func stripPort(hostPort string) string {
	for i := len(hostPort) - 1; i >= 0; i-- {
		if hostPort[i] == ':' {
			return hostPort[:i]
		}
		if hostPort[i] < '0' || hostPort[i] > '9' {
			break
		}
	}
	return hostPort
}
