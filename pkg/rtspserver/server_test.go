package rtspserver

import (
	"testing"
	"time"
)

func TestRTPTimestampAt(t *testing.T) {
	const wrap = int64(1) << 32

	tests := []struct {
		name      string
		pts       time.Duration
		clockRate int64
		want      uint32
	}{
		{"zero", 0, 90000, 0},
		{"one second", time.Second, 90000, 90000},
		{"millisecond precision", 40 * time.Millisecond, 90000, 3600},
		{"rounds down within a tick", 1500 * time.Nanosecond, 90000, 0},
		{"last tick before the 90 kHz wrap", time.Duration(wrap-1) * time.Second / 90000, 90000, uint32(wrap - 2)},
		// 2^32 ticks at 90 kHz is 13h15m21s: a stream publishing for longer than that must keep counting.
		{"wraps instead of saturating", 14 * time.Hour, 90000, uint32(14*3600*90000 - wrap)},
		{"several wraps", 100 * time.Hour, 90000, uint32((100 * 3600 * 90000) % wrap)},
		{"audio clock rate", 30 * time.Hour, 48000, uint32((30 * 3600 * 48000) % wrap)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rtpTimestampAt(tt.pts, tt.clockRate); got != tt.want {
				t.Fatalf("rtpTimestampAt(%v, %d) = %d, want %d", tt.pts, tt.clockRate, got, tt.want)
			}
		})
	}
}

func TestRTPTimestampAtKeepsAdvancingAcrossTheWrap(t *testing.T) {
	frame := 40 * time.Millisecond // 25 fps
	start := 13*time.Hour + 15*time.Minute

	previous := rtpTimestampAt(start, 90000)
	for i := 1; i <= 25*60; i++ {
		current := rtpTimestampAt(start+time.Duration(i)*frame, 90000)
		// Unsigned subtraction gives the distance modulo 2^32, which is what RTP receivers compute.
		if step := current - previous; step != 3600 {
			t.Fatalf("frame %d advanced the timestamp by %d ticks, want 3600", i, step)
		}
		previous = current
	}
}
