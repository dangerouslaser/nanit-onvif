package app

import (
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/utils"
)

func processSensorData(babyUID string, sensorData []*client.SensorData, stateManager *baby.StateManager) {
	// Parse sensor update
	stateUpdate := baby.State{}
	for _, s := range sensorData {
		if s.SensorType == nil {
			continue
		}
		switch *s.SensorType {
		case client.SensorType_TEMPERATURE:
			if s.ValueMilli != nil {
				stateUpdate.SetTemperatureMilli(*s.ValueMilli)
			}
		case client.SensorType_HUMIDITY:
			if s.ValueMilli != nil {
				stateUpdate.SetHumidityMilli(*s.ValueMilli)
			}
		case client.SensorType_NIGHT:
			if s.Value != nil {
				stateUpdate.SetIsNight(*s.Value == 1)
			}
		case client.SensorType_LIGHT:
			if s.Value != nil {
				stateUpdate.SetLightLevel(*s.Value)
			}
		case client.SensorType_MOTION:
			if s.IsAlert != nil {
				stateUpdate.SetMotionDetected(*s.IsAlert)
			}
			if s.Timestamp != nil && s.GetIsAlert() {
				stateUpdate.SetMotionTimestamp(*s.Timestamp)
			}
		case client.SensorType_SOUND:
			if s.IsAlert != nil {
				stateUpdate.SetSoundDetected(*s.IsAlert)
			}
			if s.Timestamp != nil && s.GetIsAlert() {
				stateUpdate.SetSoundTimestamp(*s.Timestamp)
			}
		}
	}

	stateManager.Update(babyUID, stateUpdate)
}

func processControl(babyUID string, control *client.Control, stateManager *baby.StateManager) {
	if control == nil {
		return
	}
	stateUpdate := baby.State{}
	if control.NightLight != nil {
		stateUpdate.SetNightLightOn(*control.NightLight == client.Control_LIGHT_ON)
	}
	if control.NightLightTimeout != nil {
		stateUpdate.SetNightLightTimeoutSec(*control.NightLightTimeout)
	}
	stateManager.Update(babyUID, stateUpdate)
}

func processSettings(babyUID string, settings *client.Settings, stateManager *baby.StateManager) {
	if settings == nil {
		return
	}
	stateUpdate := baby.State{}
	if settings.NightVision != nil {
		stateUpdate.SetNightVision(*settings.NightVision)
	}
	if settings.SleepMode != nil {
		stateUpdate.SetSleepMode(*settings.SleepMode)
	}
	if settings.StatusLightOn != nil {
		stateUpdate.SetStatusLightOn(*settings.StatusLightOn)
	}
	if settings.MicMuteOn != nil {
		stateUpdate.SetMicMuteOn(*settings.MicMuteOn)
	}
	if settings.Volume != nil {
		stateUpdate.SetVolume(*settings.Volume)
	}
	if settings.MountingMode != nil {
		stateUpdate.SetMountingMode(*settings.MountingMode)
	}
	stateManager.Update(babyUID, stateUpdate)
}

func processStatus(babyUID string, status *client.Status, stateManager *baby.StateManager) {
	if status == nil {
		return
	}
	stateUpdate := baby.State{}
	if status.CurrentVersion != nil {
		stateUpdate.SetFirmwareVersion(*status.CurrentVersion)
	}
	if status.HardwareVersion != nil {
		stateUpdate.SetHardwareVersion(*status.HardwareVersion)
	}
	if status.ConnectionToServer != nil {
		stateUpdate.SetIsConnectedToServer(*status.ConnectionToServer == client.Status_CONNECTED)
	}
	if status.Mode != nil {
		stateUpdate.SetMountingMode(int32(*status.Mode))
	}
	stateManager.Update(babyUID, stateUpdate)
}

// streamRequestTimeout is how long we wait for the response to a single streaming request.
// A variable only so tests can shorten it.
var streamRequestTimeout = 30 * time.Second

// tooManyConnectionsBackoff is how long we wait before retrying after Nanit declines the request because
// the camera already has its maximum number of app connections.
const tooManyConnectionsBackoff = 300 * time.Second

// requestLocalStreaming asks the camera to start, pause or stop pushing its stream to targetURL, retrying
// on timeouts and on "too many app connections". ctx is the context of the websocket session that owns
// conn: once it is done the connection is dead, so we stop retrying instead of looping on a closed socket.
func requestLocalStreaming(ctx utils.GracefulContext, babyUID string, targetURL string, streamingStatus client.Streaming_Status, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	for {
		switch streamingStatus {
		case client.Streaming_STARTED:
			log.Info().Str("target", targetURL).Msg("Requesting local streaming")
		case client.Streaming_PAUSED:
			log.Info().Str("target", targetURL).Msg("Pausing local streaming")
		case client.Streaming_STOPPED:
			log.Info().Str("target", targetURL).Msg("Stopping local streaming")
		}

		awaitResponse := conn.SendRequest(client.RequestType_PUT_STREAMING, &client.Request{
			Streaming: &client.Streaming{
				Id:       client.StreamIdentifier(client.StreamIdentifier_MOBILE).Enum(),
				RtmpUrl:  utils.ConstRefStr(targetURL),
				Status:   client.Streaming_Status(streamingStatus).Enum(),
				Attempts: utils.ConstRefInt32(1),
			},
		})

		_, err := awaitResponse(streamRequestTimeout)

		if err != nil {
			if err.Error() == "Forbidden: Number of Mobile App connections above limit, declining connection" {
				log.Warn().Err(err).Msg("Too many app connections, waiting for local connection to become available...")
				stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				if !sleepWithContext(ctx, tooManyConnectionsBackoff) {
					return
				}
				continue
			} else if err.Error() != "Request timeout" {
				if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Alive {
					log.Info().Err(err).Msg("Failed to request local streaming, but stream seems to be alive from previous run")
				} else if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Unhealthy {
					log.Error().Err(err).Msg("Failed to request local streaming and stream seems to be dead")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				} else {
					log.Warn().Err(err).Msg("Failed to request local streaming, awaiting stream health check")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				}

				return
			}

			// The websocket-alive flag is per baby, not per connection: after a reconnect it is true again
			// while conn is still the old, closed socket. The session context is what tells us about conn.
			if isDone(ctx) || !stateManager.GetBabyState(babyUID).GetIsWebsocketAlive() {
				return
			}

			log.Warn().Msg("Streaming request timeout, trying again")

		} else {
			log.Info().Msg("Local streaming successfully requested")
			stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_Requested))
			return
		}
	}
}

func isDone(ctx utils.GracefulContext) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// sleepWithContext sleeps for d and returns true, or returns false early if ctx is done first.
func sleepWithContext(ctx utils.GracefulContext, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// -----------------------------
// Publisher watchdog
//
// Nanit's cloud acknowledges a streaming request even when the camera never ends up publishing, and a
// request can also fail outright. Either way nothing else retries: the stream only turns Unhealthy when an
// existing publisher drops, so "requested, but no publisher ever connected" would otherwise hang until the
// bridge is restarted. The watchdog is the safety net: one per websocket session, it re-requests local
// streaming whenever the stream is not alive and nobody has tried for a while.

const (
	// publisherGracePeriod is how long the camera gets to connect its RTMP publisher after a request
	// before we ask again.
	publisherGracePeriod = 2 * time.Minute

	// publisherMaxBackoff caps the wait between re-requests. It doubles after every re-request and resets
	// once the stream is alive, so a camera that is unplugged or asleep is not asked every two minutes forever.
	publisherMaxBackoff = 10 * time.Minute

	// publisherCheckInterval is how often the watchdog looks at the stream.
	publisherCheckInterval = 15 * time.Second
)

// streamRequestTracker records the start-streaming requests made within one websocket session, whichever
// code path made them, so the watchdog neither piles onto a request that is still in progress nor fires
// right after one has finished.
type streamRequestTracker struct {
	inFlight    atomic.Int32
	lastAttempt atomic.Int64 // UnixNano of the end of the latest request, or of the session start
}

func newStreamRequestTracker(sessionStart time.Time) *streamRequestTracker {
	tracker := &streamRequestTracker{}
	tracker.lastAttempt.Store(sessionStart.UnixNano())
	return tracker
}

// track runs request and records that it is in flight and when it finished.
func (tracker *streamRequestTracker) track(request func()) {
	tracker.inFlight.Add(1)
	defer func() {
		tracker.lastAttempt.Store(time.Now().UnixNano())
		tracker.inFlight.Add(-1)
	}()

	request()
}

func (tracker *streamRequestTracker) isInFlight() bool {
	return tracker.inFlight.Load() > 0
}

func (tracker *streamRequestTracker) lastAttemptAt() time.Time {
	return time.Unix(0, tracker.lastAttempt.Load())
}

// publisherWatchdog decides when local streaming should be re-requested. It holds no clock and does no
// I/O, so the policy can be tested on its own.
type publisherWatchdog struct {
	grace      time.Duration
	maxBackoff time.Duration

	wait          time.Duration // how long the stream may stay down, with nobody trying, before we re-request
	lastSeenAlive time.Time     // the latest check at which the stream was alive, or when the watchdog started
}

func newPublisherWatchdog(now time.Time, grace time.Duration, maxBackoff time.Duration) *publisherWatchdog {
	return &publisherWatchdog{
		grace:         grace,
		maxBackoff:    maxBackoff,
		wait:          grace,
		lastSeenAlive: now,
	}
}

// shouldRerequest reports whether local streaming should be re-requested now. lastAttempt is when the
// latest request finished, successfully or not.
func (watchdog *publisherWatchdog) shouldRerequest(now time.Time, streamAlive bool, requestInFlight bool, lastAttempt time.Time) bool {
	if streamAlive {
		watchdog.wait = watchdog.grace
		watchdog.lastSeenAlive = now
		return false
	}

	if requestInFlight {
		return false
	}

	// Count from whichever is later: the stream going down or the last request. The first covers a stream
	// that has only just dropped, where the regular re-request has not had a chance to run yet.
	downSince := watchdog.lastSeenAlive
	if lastAttempt.After(downSince) {
		downSince = lastAttempt
	}

	if now.Sub(downSince) < watchdog.wait {
		return false
	}

	watchdog.wait = min(watchdog.wait*2, watchdog.maxBackoff)
	return true
}

// runPublisherWatchdog watches one websocket session until ctx is done, calling rerequest whenever the
// stream has been down for too long. rerequest must go through tracker and may block.
func runPublisherWatchdog(ctx utils.GracefulContext, babyUID string, stateManager *baby.StateManager, tracker *streamRequestTracker, rerequest func()) {
	watchdog := newPublisherWatchdog(time.Now(), publisherGracePeriod, publisherMaxBackoff)

	ticker := time.NewTicker(publisherCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		streamAlive := stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Alive
		lastAttempt := tracker.lastAttemptAt()
		if !watchdog.shouldRerequest(time.Now(), streamAlive, tracker.isInFlight(), lastAttempt) {
			continue
		}

		log.Warn().
			Str("baby_uid", babyUID).
			Dur("since_last_request", time.Since(lastAttempt).Round(time.Second)).
			Msg("No stream publisher connected, re-requesting local streaming")
		rerequest()
	}
}
