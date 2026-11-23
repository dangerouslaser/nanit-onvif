package onvif

import (
	"strings"
	"testing"
)

func TestEventsServiceIsOnlyAdvertisedWhenEnabled(t *testing.T) {
	responses := map[string]func(host string, eventsEnabled bool) []byte{
		"GetCapabilities": GetCapabilitiesResponse,
		"GetServices":     GetServicesResponse,
	}

	for name, response := range responses {
		enabled := string(response("192.0.2.1:8089", true))
		disabled := string(response("192.0.2.1:8089", false))

		if !strings.Contains(enabled, "http://192.0.2.1:8089/onvif/events_service") {
			t.Errorf("%s with events enabled does not advertise the events service", name)
		}
		if strings.Contains(disabled, "events") {
			t.Errorf("%s with events disabled still mentions events:\n%s", name, disabled)
		}

		for _, body := range []string{enabled, disabled} {
			for _, service := range []string{"device_service", "media_service"} {
				if !strings.Contains(body, "http://192.0.2.1:8089/onvif/"+service) {
					t.Errorf("%s does not advertise the %s", name, service)
				}
			}
			if !strings.HasSuffix(body, "</s:Body></s:Envelope>") {
				t.Errorf("%s response is not a closed envelope", name)
			}
		}
	}
}
