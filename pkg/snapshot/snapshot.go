package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// KeyframeProvider returns the latest H.264 keyframe (annex-B) for a baby.
type KeyframeProvider interface {
	GetKeyframe(babyUID string) ([]byte, bool)
}

// ErrNoKeyframe is returned when no keyframe has been observed yet for a baby.
var ErrNoKeyframe = errors.New("no keyframe available")

// Generator decodes the latest cached H.264 keyframe to a JPEG using ffmpeg.
type Generator struct {
	provider KeyframeProvider
	timeout  time.Duration
	encode   func(ctx context.Context, keyframe []byte) ([]byte, error)

	// mu allows one ffmpeg at a time and guards cache: clients polling the snapshot together decode a
	// keyframe once instead of forking a process each.
	mu    sync.Mutex
	cache map[string]cachedSnapshot
}

type cachedSnapshot struct {
	keyframe []byte
	jpeg     []byte
}

// NewGenerator creates a snapshot Generator that reads keyframes from provider.
func NewGenerator(provider KeyframeProvider) *Generator {
	return &Generator{
		provider: provider,
		timeout:  5 * time.Second,
		encode:   encodeJPEG,
		cache:    make(map[string]cachedSnapshot),
	}
}

// Generate returns a JPEG-encoded snapshot for the given baby.
func (g *Generator) Generate(ctx context.Context, babyUID string) ([]byte, error) {
	kf, ok := g.provider.GetKeyframe(babyUID)
	if !ok || len(kf) == 0 {
		return nil, ErrNoKeyframe
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if cached, ok := g.cache[babyUID]; ok && sameSlice(cached.keyframe, kf) {
		return cached.jpeg, nil
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	jpeg, err := g.encode(ctx, kf)
	if err != nil {
		return nil, err
	}

	g.cache[babyUID] = cachedSnapshot{keyframe: kf, jpeg: jpeg}
	return jpeg, nil
}

// sameSlice reports whether a and b are the very same slice, not merely equal: the provider hands out
// the keyframe it has stored until a new one replaces it.
func sameSlice(a, b []byte) bool {
	return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0]
}

func encodeJPEG(ctx context.Context, keyframe []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "h264",
		"-i", "pipe:0",
		"-frames:v", "1",
		"-c:v", "mjpeg",
		"-f", "image2",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(keyframe)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, stderr.String())
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced empty output: %s", stderr.String())
	}
	return stdout.Bytes(), nil
}
