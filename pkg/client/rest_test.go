package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gregory-m/nanit/pkg/session"
)

// fakeNanitAPI stands in for api.nanit.com. Like the real one, it hands out single-use refresh tokens:
// refreshing replaces both tokens, and a refresh token presented a second time is rejected.
type fakeNanitAPI struct {
	mu           sync.Mutex
	generation   int
	refreshCalls int
	messages     map[string][]int64 // baby UID -> message times (unix seconds)
}

func (api *fakeNanitAPI) authToken() string    { return fmt.Sprintf("auth-%d", api.generation) }
func (api *fakeNanitAPI) refreshToken() string { return fmt.Sprintf("refresh-%d", api.generation) }

func (api *fakeNanitAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	api.mu.Lock()
	defer api.mu.Unlock()

	respond := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}

	if req.URL.Path == "/tokens/refresh" {
		api.refreshCalls++

		var payload map[string]string
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return respond(400, `{}`)
		}
		if payload["refresh_token"] != api.refreshToken() {
			return respond(404, `{}`)
		}

		api.generation++
		return respond(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":%q}`, api.authToken(), api.refreshToken()))
	}

	if req.Header.Get("Authorization") != api.authToken() {
		return respond(401, `{}`)
	}

	if babyUID, ok := strings.CutPrefix(req.URL.Path, "/babies/"); ok && strings.HasSuffix(babyUID, "/messages") {
		babyUID = strings.TrimSuffix(babyUID, "/messages")

		var body bytes.Buffer
		body.WriteString(`{"messages":[`)
		for i, unixTime := range api.messages[babyUID] {
			if i > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, `{"id":%d,"baby_uid":%q,"type":"MOTION","time":%d}`, unixTime, babyUID, unixTime)
		}
		body.WriteString(`]}`)
		return respond(200, body.String())
	}

	return respond(200, `{"babies":[]}`)
}

// newTestClient returns a client talking to a fake API, whose session holds the given tokens.
func newTestClient(t *testing.T, api *fakeNanitAPI, authToken string, refreshToken string) *NanitClient {
	t.Helper()

	realClient := myClient
	myClient = &http.Client{Transport: api}
	t.Cleanup(func() { myClient = realClient })

	store := session.NewSessionStore()
	store.Update(func(s *session.Session) {
		s.AuthToken = authToken
		s.RefreshToken = refreshToken
		s.AuthTime = time.Now()
	})

	return &NanitClient{SessionStore: store}
}

// Several goroutines (one websocket manager and one event poller per baby) notice the expired auth token
// at the same moment. The refresh token is single-use, so only one refresh may be made with it.
func TestConcurrentRequestsRefreshTheTokenOnce(t *testing.T) {
	api := &fakeNanitAPI{generation: 1}
	c := newTestClient(t, api, "auth-0", "refresh-1") // auth token expired, refresh token still good

	const requests = 8
	errs := make(chan error, requests)
	start := make(chan struct{})

	for i := 0; i < requests; i++ {
		go func() {
			<-start
			req, _ := http.NewRequest("GET", "https://api.nanit.com/babies", nil)
			errs <- c.FetchAuthorized(req, new(babiesResponsePayload))
		}()
	}
	close(start)

	for i := 0; i < requests; i++ {
		if err := <-errs; err != nil {
			t.Errorf("request failed: %v", err)
		}
	}

	if api.refreshCalls != 1 {
		t.Errorf("token was refreshed %d times, want 1", api.refreshCalls)
	}
	if got := c.SessionStore.Snapshot(); got.AuthToken != "auth-2" || got.RefreshToken != "refresh-2" {
		t.Errorf("session tokens = %q / %q, want auth-2 / refresh-2", got.AuthToken, got.RefreshToken)
	}
}

func TestForcedAuthorizeAlwaysRefreshes(t *testing.T) {
	api := &fakeNanitAPI{generation: 1}
	c := newTestClient(t, api, "auth-1", "refresh-1")

	if err := c.MaybeAuthorize(false); err != nil || api.refreshCalls != 0 {
		t.Fatalf("MaybeAuthorize(false) with a fresh token: err = %v, refreshes = %d, want none", err, api.refreshCalls)
	}

	for want := 1; want <= 2; want++ {
		if err := c.MaybeAuthorize(true); err != nil {
			t.Fatal(err)
		}
		if api.refreshCalls != want {
			t.Fatalf("after %d forced authorizations the token was refreshed %d times", want, api.refreshCalls)
		}
	}
}

// With two cameras, a recent message of one baby must not hide older, not yet seen messages of the other.
func TestFetchNewMessagesTracksEachBabySeparately(t *testing.T) {
	now := time.Now().Unix()
	api := &fakeNanitAPI{generation: 1, messages: map[string][]int64{
		"baby-a": {now - 10},
		"baby-b": {now - 40},
	}}
	c := newTestClient(t, api, "auth-1", "refresh-1")

	fetch := func(babyUID string) []int64 {
		t.Helper()
		messages, err := c.FetchNewMessages(babyUID, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var times []int64
		for _, m := range messages {
			times = append(times, m.Time.Unix())
		}
		return times
	}

	if got := fetch("baby-a"); len(got) != 1 || got[0] != now-10 {
		t.Fatalf("baby-a first poll = %v, want its one message", got)
	}
	// Older than baby-a's message, but new for baby-b.
	if got := fetch("baby-b"); len(got) != 1 || got[0] != now-40 {
		t.Fatalf("baby-b first poll = %v, want its one message", got)
	}

	// Nothing new on the next poll of either.
	if got := fetch("baby-a"); len(got) != 0 {
		t.Fatalf("baby-a second poll = %v, want nothing new", got)
	}
	if got := fetch("baby-b"); len(got) != 0 {
		t.Fatalf("baby-b second poll = %v, want nothing new", got)
	}

	// A new message for baby-b shows up on its own.
	api.mu.Lock()
	api.messages["baby-b"] = []int64{now - 40, now - 20}
	api.mu.Unlock()
	if got := fetch("baby-b"); len(got) != 1 || got[0] != now-20 {
		t.Fatalf("baby-b third poll = %v, want only the new message", got)
	}
}
