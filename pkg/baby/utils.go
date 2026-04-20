package baby

import (
	"crypto/sha1"
	"fmt"
	"regexp"

	"github.com/rs/zerolog/log"
)

var validUID = regexp.MustCompile(`^[a-z0-9_-]+$`)

// EnsureValidBabyUID - Checks that Baby UID does not contain any bad characters
// This is necessary because we use it as part of file paths
func EnsureValidBabyUID(babyUID string) {
	if !validUID.MatchString(babyUID) {
		log.Fatal().Str("uid", babyUID).Msg("Baby UID contains unsafe characters")
	}
}

// SyntheticMAC returns a deterministic, locally-administered MAC address
// derived from the baby UID. Home Assistant uses the MAC from a device's
// `connections` tuple to merge entities from different integrations (ONVIF
// camera + MQTT sensors) into a single device card.
//
// The first byte is forced to have the U/L bit set (locally administered) and
// the I/G bit cleared (unicast), so the address won't collide with any
// IEEE-assigned OUI.
func SyntheticMAC(babyUID string) string {
	h := sha1.Sum([]byte("nanit-rtsp:" + babyUID))
	b := [6]byte{h[0], h[1], h[2], h[3], h[4], h[5]}
	b[0] = (b[0] & 0xfc) | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}
