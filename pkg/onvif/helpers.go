package onvif

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Utility functions ported from go2rtc (MIT License).
// Client-side discovery functions (DiscoveryStreamingDevices, etc.) removed — not needed for server.

// FindTagValue extracts the text content of an XML tag, supporting namespace prefixes.
func FindTagValue(b []byte, tag string) string {
	re := regexp.MustCompile(`(?s)<(?:\w+:)?` + tag + `\b[^>]*>([^<]+)`)
	m := re.FindSubmatch(b)
	if len(m) != 2 {
		return ""
	}
	return string(m[1])
}

// UUID generates an RFC4122-style UUID using crypto/rand.
func UUID() string {
	s := randHexString(32)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func randHexString(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return i
}

// GetPosixTZ converts the current time zone to POSIX TZ format.
func GetPosixTZ(current time.Time) string {
	_, offset := current.Zone()

	if current.IsDST() {
		_, end := current.ZoneBounds()
		endPlus1 := end.Add(time.Hour * 25)
		_, offset = endPlus1.Zone()
	}

	var prefix string
	if offset < 0 {
		prefix = "GMT+"
		offset = -offset / 60
	} else {
		prefix = "GMT-"
		offset = offset / 60
	}

	return prefix + fmt.Sprintf("%02d:%02d", offset/60, offset%60)
}

// GetPath extracts the path from a URL string. Returns defPath on failure.
func GetPath(urlOrPath, defPath string) string {
	if urlOrPath == "" || urlOrPath[0] == '/' {
		return defPath
	}
	u, err := url.Parse(urlOrPath)
	if err != nil {
		return defPath
	}
	return GetPath(u.Path, defPath)
}
