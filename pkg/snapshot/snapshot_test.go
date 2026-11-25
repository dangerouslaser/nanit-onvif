package snapshot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeProvider struct {
	mu        sync.Mutex
	keyframes map[string][]byte
}

func (p *fakeProvider) GetKeyframe(babyUID string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kf, ok := p.keyframes[babyUID]
	return kf, ok
}

func (p *fakeProvider) set(babyUID string, keyframe []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keyframes[babyUID] = keyframe
}

func newTestGenerator(provider KeyframeProvider, encodes *atomic.Int32) *Generator {
	g := NewGenerator(provider)
	g.encode = func(_ context.Context, keyframe []byte) ([]byte, error) {
		encodes.Add(1)
		return append([]byte("jpeg:"), keyframe...), nil
	}
	return g
}

func TestGenerateWithoutKeyframe(t *testing.T) {
	var encodes atomic.Int32
	g := newTestGenerator(&fakeProvider{keyframes: map[string][]byte{}}, &encodes)

	if _, err := g.Generate(context.Background(), "baby"); !errors.Is(err, ErrNoKeyframe) {
		t.Fatalf("err = %v, want ErrNoKeyframe", err)
	}
	if encodes.Load() != 0 {
		t.Fatal("ran the encoder without a keyframe")
	}
}

func TestGenerateDecodesEachKeyframeOnce(t *testing.T) {
	var encodes atomic.Int32
	provider := &fakeProvider{keyframes: map[string][]byte{"baby": []byte("first")}}
	g := newTestGenerator(provider, &encodes)

	// Several clients polling the same keyframe at once share one decode.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jpeg, err := g.Generate(context.Background(), "baby")
			if err != nil || string(jpeg) != "jpeg:first" {
				t.Errorf("Generate = %q, %v", jpeg, err)
			}
		}()
	}
	wg.Wait()

	if got := encodes.Load(); got != 1 {
		t.Fatalf("encoder ran %d times for one keyframe, want 1", got)
	}

	// A new keyframe is decoded again, even one with identical bytes.
	provider.set("baby", []byte("first"))
	if _, err := g.Generate(context.Background(), "baby"); err != nil {
		t.Fatal(err)
	}
	provider.set("baby", []byte("second"))
	jpeg, err := g.Generate(context.Background(), "baby")
	if err != nil || string(jpeg) != "jpeg:second" {
		t.Fatalf("Generate after a new keyframe = %q, %v", jpeg, err)
	}
	if got := encodes.Load(); got != 3 {
		t.Fatalf("encoder ran %d times for three keyframes, want 3", got)
	}
}

func TestGenerateDoesNotCacheFailures(t *testing.T) {
	provider := &fakeProvider{keyframes: map[string][]byte{"baby": []byte("keyframe")}}
	g := NewGenerator(provider)

	fail := true
	g.encode = func(_ context.Context, _ []byte) ([]byte, error) {
		if fail {
			return nil, errors.New("ffmpeg: boom")
		}
		return []byte("jpeg"), nil
	}

	if _, err := g.Generate(context.Background(), "baby"); err == nil {
		t.Fatal("expected the encoder's error")
	}

	fail = false
	jpeg, err := g.Generate(context.Background(), "baby")
	if err != nil || string(jpeg) != "jpeg" {
		t.Fatalf("Generate after a failed attempt = %q, %v", jpeg, err)
	}
}
