package onvif

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Utility functions ported from go2rtc (MIT License).
// Client-side discovery functions (DiscoveryStreamingDevices, etc.) removed — not needed for server.

var tagRegexCache sync.Map // map[string]*regexp.Regexp

// FindTagValue extracts the text content of an XML tag, supporting namespace prefixes.
func FindTagValue(b []byte, tag string) string {
	var re *regexp.Regexp
	if v, ok := tagRegexCache.Load(tag); ok {
		re = v.(*regexp.Regexp)
	} else {
		re = regexp.MustCompile(`(?s)<(?:\w+:)?` + tag + `\b[^>]*>([^<]+)`)
		tagRegexCache.Store(tag, re)
	}
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
