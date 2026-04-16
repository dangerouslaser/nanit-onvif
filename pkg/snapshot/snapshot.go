package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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
}

// NewGenerator creates a snapshot Generator that reads keyframes from provider.
func NewGenerator(provider KeyframeProvider) *Generator {
	return &Generator{
		provider: provider,
		timeout:  5 * time.Second,
	}
}

// Generate returns a JPEG-encoded snapshot for the given baby.
func (g *Generator) Generate(ctx context.Context, babyUID string) ([]byte, error) {
	kf, ok := g.provider.GetKeyframe(babyUID)
	if !ok {
		return nil, ErrNoKeyframe
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

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
	cmd.Stdin = bytes.NewReader(kf)
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
