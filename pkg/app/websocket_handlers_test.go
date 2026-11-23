package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sacOO7/gowebsocket"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/utils"
)

const (
	testGrace      = 2 * time.Minute
	testMaxBackoff = 10 * time.Minute
)

func TestPublisherWatchdogRerequestsWhenNoPublisherConnects(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	watchdog := newPublisherWatchdog(start, testGrace, testMaxBackoff)

	// The request was acknowledged a second into the session, and then nothing happened.
	ack := start.Add(time.Second)

	if watchdog.shouldRerequest(ack.Add(testGrace-time.Second), false, false, ack) {
		t.Fatal("re-requested before the grace period was over")
	}
	if !watchdog.shouldRerequest(ack.Add(testGrace), false, false, ack) {
		t.Fatal("did not re-request once the grace period was over")
	}
}

func TestPublisherWatchdogLeavesAliveStreamAlone(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	watchdog := newPublisherWatchdog(start, testGrace, testMaxBackoff)

	for elapsed := time.Duration(0); elapsed < 24*time.Hour; elapsed += time.Hour {
		if watchdog.shouldRerequest(start.Add(elapsed), true, false, start) {
			t.Fatalf("re-requested an alive stream after %v", elapsed)
		}
	}
}

func TestPublisherWatchdogWaitsForRequestInFlight(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	watchdog := newPublisherWatchdog(start, testGrace, testMaxBackoff)

	// E.g. the request loop is sitting out a "too many app connections" backoff.
	if watchdog.shouldRerequest(start.Add(time.Hour), false, true, start) {
		t.Fatal("re-requested while another request was in flight")
	}

	// That request then finishes without a publisher showing up: the grace period counts from its end.
	finished := start.Add(time.Hour)
	if watchdog.shouldRerequest(finished.Add(testGrace-time.Second), false, false, finished) {
		t.Fatal("re-requested before the grace period after the finished request was over")
	}
	if !watchdog.shouldRerequest(finished.Add(testGrace), false, false, finished) {
		t.Fatal("did not re-request after the in-flight request finished without a publisher")
	}
}

func TestPublisherWatchdogGivesFreshlyDroppedStreamAGracePeriod(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	watchdog := newPublisherWatchdog(start, testGrace, testMaxBackoff)

	// The stream has been alive for hours on the back of a request made at session start...
	lastAlive := start.Add(10 * time.Hour)
	watchdog.shouldRerequest(lastAlive, true, false, start)

	// ...and then drops. The regular Unhealthy handler re-requests; the watchdog must not race it just
	// because the last request is ancient.
	if watchdog.shouldRerequest(lastAlive.Add(15*time.Second), false, false, start) {
		t.Fatal("re-requested right after the stream dropped")
	}
	if !watchdog.shouldRerequest(lastAlive.Add(testGrace), false, false, start) {
		t.Fatal("did not re-request a stream that stayed down for the whole grace period")
	}
}

func TestPublisherWatchdogBacksOffAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	watchdog := newPublisherWatchdog(now, testGrace, testMaxBackoff)
	lastAttempt := now

	// Each re-request is acknowledged instantly and still no publisher connects.
	wantWaits := []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, want := range wantWaits {
		if watchdog.shouldRerequest(lastAttempt.Add(want-time.Second), false, false, lastAttempt) {
			t.Fatalf("re-request %d came earlier than %v after the previous one", i, want)
		}
		now = lastAttempt.Add(want)
		if !watchdog.shouldRerequest(now, false, false, lastAttempt) {
			t.Fatalf("re-request %d did not come %v after the previous one", i, want)
		}
		lastAttempt = now
	}

	// The publisher finally connects, then drops again later: back to the short grace period.
	now = now.Add(time.Hour)
	watchdog.shouldRerequest(now, true, false, lastAttempt)
	if !watchdog.shouldRerequest(now.Add(testGrace), false, false, lastAttempt) {
		t.Fatal("backoff did not reset after the stream came back")
	}
}

func TestStreamRequestTracker(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	tracker := newStreamRequestTracker(start)

	if tracker.isInFlight() {
		t.Fatal("new tracker reports a request in flight")
	}
	if !tracker.lastAttemptAt().Equal(start) {
		t.Fatalf("new tracker's last attempt = %v, want the session start %v", tracker.lastAttemptAt(), start)
	}

	tracker.track(func() {
		if !tracker.isInFlight() {
			t.Fatal("tracker does not report the running request as in flight")
		}
	})

	if tracker.isInFlight() {
		t.Fatal("tracker still reports a request in flight after it finished")
	}
	if time.Since(tracker.lastAttemptAt()) > time.Minute {
		t.Fatalf("last attempt was not updated when the request finished: %v", tracker.lastAttemptAt())
	}
}

func TestRunPublisherWatchdogStopsWithSession(t *testing.T) {
	ctx, endSession := newTestSession()

	stopped := make(chan struct{})
	go func() {
		runPublisherWatchdog(ctx, "baby", baby.NewStateManager(), newStreamRequestTracker(time.Now()), func() {
			t.Error("watchdog re-requested within the grace period")
		})
		close(stopped)
	}()

	endSession()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog kept running after its websocket session ended")
	}
}

// A websocket session ends and the manager reconnects: the per-baby "websocket alive" flag is true again,
// but a request loop left over from the old session still holds the old, closed socket. It must stop
// rather than time out and retry on that socket forever.
func TestRequestLocalStreamingStopsWhenSessionEnds(t *testing.T) {
	oldTimeout := streamRequestTimeout
	streamRequestTimeout = 20 * time.Millisecond
	defer func() { streamRequestTimeout = oldTimeout }()

	conn := newDeadTestConnection(t)

	stateManager := baby.NewStateManager()
	stateManager.Update("baby", *baby.NewState().SetWebsocketAlive(true))

	ctx, endSession := newTestSession()

	returned := make(chan struct{})
	go func() {
		requestLocalStreaming(ctx, "baby", "rtmp://127.0.0.1:1935/local/baby", client.Streaming_STARTED, conn, stateManager)
		close(returned)
	}()

	// While the session is alive, timeouts are retried.
	select {
	case <-returned:
		t.Fatal("request loop gave up while its session was still alive")
	case <-time.After(10 * streamRequestTimeout):
	}

	endSession()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("request loop kept retrying on a dead connection after its session ended")
	}
}

// newTestSession returns a context standing in for a websocket session's, and a function that ends it.
func newTestSession() (utils.GracefulContext, func()) {
	ctxC := make(chan utils.GracefulContext, 1)
	runner := utils.RunWithGracefulCancel(func(ctx utils.GracefulContext) {
		ctxC <- ctx
		<-ctx.Done()
	})

	return <-ctxC, runner.Cancel
}

// newDeadTestConnection returns a connection whose socket was connected once and has since been closed.
func newDeadTestConnection(t *testing.T) *client.WebsocketConnection {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer serverConn.Close()

		for {
			if _, _, err := serverConn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	connected := false
	socket := gowebsocket.New("ws" + strings.TrimPrefix(server.URL, "http"))
	socket.OnConnected = func(gowebsocket.Socket) { connected = true }
	socket.Connect() // dials synchronously
	if !connected {
		t.Fatal("could not connect to the test websocket server")
	}
	socket.Close()

	return client.NewWebsocketConnection(&socket)
}
