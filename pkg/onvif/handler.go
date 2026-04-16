package onvif

import (
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
)

// ServerConfig configures the ONVIF HTTP handler.
type ServerConfig struct {
	RTSPPort  string             // e.g. "8554"
	GetBabies func() []baby.Baby // returns current baby list
	Username  string             // ONVIF auth username (empty = no auth)
	Password  string             // ONVIF auth password (empty = no auth)

	// GetSnapshot returns a JPEG-encoded snapshot for the given baby.
	// If nil, snapshot support is disabled.
	GetSnapshot func(babyUID string) ([]byte, error)
}

const snapshotPathPrefix = "/onvif/snapshot/"

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
			if config.GetSnapshot == nil {
				resp = GetSnapshotUriResponse("")
			} else {
				token := FindTagValue(b, "ProfileToken")
				if token == "" {
					babies := config.GetBabies()
					if len(babies) > 0 {
						token = babies[0].UID
					}
				}
				uri := fmt.Sprintf("http://%s%s%s.jpg", host, snapshotPathPrefix, token)
				resp = GetSnapshotUriResponse(uri)
			}

		default:
			resp = StaticResponse(action)
		}

		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.Write(resp)
	}

	mux.HandleFunc("/onvif/device_service", handle)
	mux.HandleFunc("/onvif/media_service", handle)

	if config.GetSnapshot != nil {
		mux.HandleFunc(snapshotPathPrefix, func(w http.ResponseWriter, r *http.Request) {
			handleSnapshot(w, r, config, authRequired)
		})
	}

	// Some clients probe the root path
	mux.HandleFunc("/onvif/", handle)

	return mux
}

func handleSnapshot(w http.ResponseWriter, r *http.Request, config ServerConfig, authRequired bool) {
	if authRequired && !validateBasicAuth(r, config.Username, config.Password) {
		w.Header().Set("WWW-Authenticate", `Basic realm="nanit-onvif"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, snapshotPathPrefix)
	name = strings.TrimSuffix(name, ".jpg")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	jpeg, err := config.GetSnapshot(name)
	if err != nil {
		log.Warn().Err(err).Str("baby_uid", name).Msg("ONVIF snapshot failed")
		http.Error(w, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(jpeg)
}

func validateBasicAuth(r *http.Request, user, pass string) bool {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
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
