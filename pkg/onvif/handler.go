package onvif

import (
	"crypto/sha1"
	"encoding/base64"
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
	Username  string             // ONVIF auth username (empty = no auth)
	Password  string             // ONVIF auth password (empty = no auth)
}

// NewHandler returns an http.Handler that serves ONVIF SOAP requests.
// Each baby UID maps to one ONVIF profile (ProfileToken = baby UID).
func NewHandler(config ServerConfig) http.Handler {
	mux := http.NewServeMux()

	authRequired := config.Username != "" && config.Password != ""

	handle := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		action := GetRequestAction(b)
		if action == "" {
			log.Debug().Str("path", r.URL.Path).Msg("ONVIF: empty action")
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		log.Debug().Str("action", action).Msg("ONVIF request")

		// GetSystemDateAndTime is always unauthenticated per ONVIF spec
		if authRequired && action != DeviceGetSystemDateAndTime {
			if !validateWSSecurityAuth(b, config.Username, config.Password) {
				log.Debug().Str("action", action).Msg("ONVIF: auth failed")
				w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write(soapAuthFault())
				return
			}
		}

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

// validateWSSecurityAuth checks the WS-Security UsernameToken from a SOAP request.
// Supports PasswordDigest (nonce + created + password hashed with SHA-1) and PasswordText.
func validateWSSecurityAuth(body []byte, expectedUser, expectedPass string) bool {
	username := FindTagValue(body, "Username")
	if username != expectedUser {
		return false
	}

	password := FindTagValue(body, "Password")
	if password == "" {
		return false
	}

	nonce64 := FindTagValue(body, "Nonce")
	created := FindTagValue(body, "Created")

	// If nonce and created are present, treat as PasswordDigest
	if nonce64 != "" && created != "" {
		nonce, err := base64.StdEncoding.DecodeString(nonce64)
		if err != nil {
			return false
		}
		h := sha1.New()
		h.Write(nonce)
		h.Write([]byte(created))
		h.Write([]byte(expectedPass))
		expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
		return password == expected
	}

	// Fallback: PasswordText
	return password == expectedPass
}

func soapAuthFault() []byte {
	e := NewEnvelope()
	e.Append(`<s:Fault>
	<s:Code><s:Value>s:Sender</s:Value>
		<s:Subcode><s:Value>wsse:FailedAuthentication</s:Value></s:Subcode>
	</s:Code>
	<s:Reason><s:Text xml:lang="en">Authentication failed</s:Text></s:Reason>
</s:Fault>`)
	return e.Bytes()
}
