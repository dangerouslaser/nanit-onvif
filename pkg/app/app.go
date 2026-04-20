package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/message"
	"github.com/gregory-m/nanit/pkg/mqtt"
	"github.com/gregory-m/nanit/pkg/onvif"
	"github.com/gregory-m/nanit/pkg/rtmpserver"
	"github.com/gregory-m/nanit/pkg/rtspserver"
	"github.com/gregory-m/nanit/pkg/session"
	"github.com/gregory-m/nanit/pkg/snapshot"
	"github.com/gregory-m/nanit/pkg/utils"
	"github.com/gregory-m/nanit/pkg/web"
)

// App - application container
type App struct {
	Opts             Opts
	SessionStore     *session.Store
	BabyStateManager *baby.StateManager
	RestClient       *client.NanitClient
	MQTTConnection   *mqtt.Connection

	commandersMu sync.RWMutex
	commanders   map[string]*babyCommander
}

// NewApp - constructor
func NewApp(opts Opts) *App {
	sessionStore := session.InitSessionStore(opts.SessionFile)

	instance := &App{
		Opts:             opts,
		BabyStateManager: baby.NewStateManager(),
		SessionStore:     sessionStore,
		RestClient: &client.NanitClient{
			RefreshToken: opts.NanitCredentials.RefreshToken,
			SessionStore: sessionStore,
		},
	}

	if opts.MQTT != nil {
		instance.MQTTConnection = mqtt.NewConnection(*opts.MQTT)
	}

	return instance
}

// Run - application main loop
func (app *App) Run(ctx utils.GracefulContext) {
	// Web UI dashboard — start first so login is available even without a session
	if app.Opts.Web != nil {
		webConfig := web.Config{
			SessionStore:     app.SessionStore,
			BabyStateManager: app.BabyStateManager,
			RestClient:       app.RestClient,
		}
		if app.Opts.RTSP != nil {
			webConfig.RTSPAddr = app.Opts.RTSP.ListenAddr
		}
		if app.Opts.RTMP != nil {
			webConfig.RTMPAddr = app.Opts.RTMP.PublicAddr
		}
		if app.Opts.ONVIF != nil {
			webConfig.ONVIFAddr = app.Opts.ONVIF.ListenAddr
			webConfig.ONVIFUser = app.Opts.ONVIF.Username
		}
		if app.Opts.MQTT != nil {
			webConfig.MQTTBroker = app.Opts.MQTT.BrokerURL
		}
		webConfig.OnLogin = func() {
			for _, babyInfo := range app.SessionStore.Snapshot().Babies {
				_babyInfo := babyInfo
				ctx.RunAsChild(func(childCtx utils.GracefulContext) {
					app.handleBaby(_babyInfo, childCtx)
				})
			}
		}
		webHandler := web.NewHandler(webConfig)
		webServer := &http.Server{
			Addr:    app.Opts.Web.ListenAddr,
			Handler: webHandler,
		}
		go func() {
			log.Info().Str("addr", app.Opts.Web.ListenAddr).Msg("Web UI server started")
			if err := webServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("Web UI server error")
			}
		}()
		defer webServer.Close()
	}

	// Reauthorize if we don't have a token or we assume it is invalid.
	// A failure here isn't fatal — the web UI login flow can recover.
	if err := app.RestClient.MaybeAuthorize(false); err != nil {
		log.Warn().Err(err).Msg("Initial authorization failed; web UI login required")
	}

	// Fetches babies info if they are not present in session
	// Skip if no auth token (web login will handle initial auth)
	if app.SessionStore.Snapshot().AuthToken != "" {
		if _, err := app.RestClient.EnsureBabies(); err != nil {
			log.Warn().Err(err).Msg("Failed to fetch babies; will proceed without them")
		}
	} else {
		log.Warn().Msg("No auth token available. Use the web UI to log in or set NANIT_REFRESH_TOKEN.")
	}

	// RTSP
	var rtspSrv *rtspserver.RTSPServer
	if app.Opts.RTSP != nil {
		rtspSrv = rtspserver.NewRTSPServer(app.Opts.RTSP.ListenAddr)
		if err := rtspSrv.Start(); err != nil {
			log.Fatal().Err(err).Msg("Failed to start RTSP server")
		}
		defer rtspSrv.Close()
	}

	// ONVIF (requires RTSP to be enabled — ONVIF points clients to RTSP URLs)
	if app.Opts.ONVIF != nil && rtspSrv != nil {
		_, rtspPort, err := net.SplitHostPort(app.Opts.RTSP.ListenAddr)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to parse RTSP listen address for ONVIF")
		}
		snapshotGen := snapshot.NewGenerator(rtspSrv)

		var eventMgr *onvif.EventManager
		if app.Opts.ONVIF.EventsEnabled {
			eventMgr = onvif.NewEventManager(app.Opts.ONVIF.EventHold)
			eventMgr.Start(app.BabyStateManager)
			defer eventMgr.Stop()
		}

		handler := onvif.NewHandler(onvif.ServerConfig{
			RTSPPort: rtspPort,
			GetBabies: func() []baby.Baby {
				return app.SessionStore.Snapshot().Babies
			},
			Username: app.Opts.ONVIF.Username,
			Password: app.Opts.ONVIF.Password,
			GetSnapshot: func(babyUID string) ([]byte, error) {
				return snapshotGen.Generate(context.Background(), babyUID)
			},
			Events: eventMgr,
		})
		onvifServer := &http.Server{
			Addr:    app.Opts.ONVIF.ListenAddr,
			Handler: handler,
		}
		go func() {
			log.Info().Str("addr", app.Opts.ONVIF.ListenAddr).Msg("ONVIF server started")
			if err := onvifServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("ONVIF server error")
			}
		}()
		defer onvifServer.Close()
	}

	// RTMP
	if app.Opts.RTMP != nil {
		go rtmpserver.StartRTMPServer(app.Opts.RTMP.ListenAddr, app.BabyStateManager, rtspSrv)
	}

	// MQTT
	if app.MQTTConnection != nil {
		getBabies := func() []baby.Baby {
			return app.SessionStore.Snapshot().Babies
		}
		app.MQTTConnection.CommandHandler = app.HandleMQTTCommand
		ctx.RunAsChild(func(childCtx utils.GracefulContext) {
			app.MQTTConnection.Run(app.BabyStateManager, getBabies, childCtx)
		})
	}

	// Start reading the data from the stream
	babies := app.SessionStore.Snapshot().Babies
	for _, babyInfo := range babies {
		_babyInfo := babyInfo
		ctx.RunAsChild(func(childCtx utils.GracefulContext) {
			app.handleBaby(_babyInfo, childCtx)
		})
	}

	// Start serving content over HTTP
	if app.Opts.HTTPEnabled {
		go serve(babies, app.Opts.DataDirectories)
	}

	<-ctx.Done()
}

func (app *App) handleBaby(baby baby.Baby, ctx utils.GracefulContext) {
	if app.Opts.RTMP != nil || app.MQTTConnection != nil {
		// Websocket connection
		ws := client.NewWebsocketConnectionManager(baby.UID, baby.CameraUID, app.SessionStore, app.RestClient, app.BabyStateManager)

		ws.WithReadyConnection(func(conn *client.WebsocketConnection, childCtx utils.GracefulContext) {
			app.runWebsocket(baby.UID, conn, childCtx)
		})

		if app.Opts.EventPolling.Enabled {
			ctx.RunAsChild(func(childCtx utils.GracefulContext) {
				app.pollMessages(baby.UID, app.BabyStateManager, childCtx)
			})
		}

		ctx.RunAsChild(func(childCtx utils.GracefulContext) {
			ws.RunWithinContext(childCtx)
		})
	}

	<-ctx.Done()
}

func (app *App) pollMessages(babyUID string, babyStateManager *baby.StateManager, ctx utils.GracefulContext) {
	hold := app.Opts.EventPolling.DetectedHold
	if hold <= 0 {
		hold = 30 * time.Second
	}

	// Per-field auto-clear timers so MotionDetected/SoundDetected flip back to
	// false after `hold`, and re-triggers during the hold extend the window.
	var timerMu sync.Mutex
	timers := map[string]*time.Timer{}

	trigger := func(field string, ts time.Time, setTrue func() baby.State, setFalse func() baby.State) {
		babyStateManager.Update(babyUID, setTrue())

		key := babyUID + ":" + field
		timerMu.Lock()
		if existing, ok := timers[key]; ok {
			existing.Stop()
		}
		timers[key] = time.AfterFunc(hold, func() {
			babyStateManager.Update(babyUID, setFalse())
			timerMu.Lock()
			delete(timers, key)
			timerMu.Unlock()
		})
		timerMu.Unlock()
	}

	poll := func() {
		newMessages, err := app.RestClient.FetchNewMessages(babyUID, app.Opts.EventPolling.MessageTimeout)
		if err != nil {
			log.Warn().Str("baby_uid", babyUID).Err(err).Msg("Event polling: fetch failed; will retry next tick")
			return
		}
		for _, msg := range newMessages {
			ts := time.Time(msg.Time)
			switch msg.Type {
			case message.SoundEventMessageType:
				log.Info().Str("baby_uid", babyUID).Time("at", ts).Msg("Sound event detected")
				trigger("sound", ts,
					func() baby.State {
						return *baby.NewState().SetSoundTimestamp(int32(ts.Unix())).SetSoundDetected(true)
					},
					func() baby.State { return *baby.NewState().SetSoundDetected(false) },
				)
			case message.MotionEventMessageType:
				log.Info().Str("baby_uid", babyUID).Time("at", ts).Msg("Motion event detected")
				trigger("motion", ts,
					func() baby.State {
						return *baby.NewState().SetMotionTimestamp(int32(ts.Unix())).SetMotionDetected(true)
					},
					func() baby.State { return *baby.NewState().SetMotionDetected(false) },
				)
			}
		}
	}

	ticker := time.NewTicker(app.Opts.EventPolling.PollingInterval)
	defer ticker.Stop()

	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

func (app *App) runWebsocket(babyUID string, conn *client.WebsocketConnection, childCtx utils.GracefulContext) {
	// Register this connection as the current one for inbound commands.
	commander := app.ensureCommander(babyUID)
	commander.setConn(conn)
	defer commander.setConn(nil)

	// Reading sensor data, settings, control, and status
	conn.RegisterMessageHandler(func(m *client.Message, conn *client.WebsocketConnection) {
		// Responses to requests we (or another client) initiated
		if *m.Type == client.Message_RESPONSE && m.Response != nil {
			switch m.Response.GetRequestType() {
			case client.RequestType_GET_SENSOR_DATA:
				if len(m.Response.SensorData) > 0 {
					processSensorData(babyUID, m.Response.SensorData, app.BabyStateManager)
				}
			case client.RequestType_GET_SETTINGS, client.RequestType_PUT_SETTINGS:
				processSettings(babyUID, m.Response.Settings, app.BabyStateManager)
			case client.RequestType_GET_STATUS, client.RequestType_PUT_STATUS:
				processStatus(babyUID, m.Response.Status, app.BabyStateManager)
			}
		}

		// Communication initiated from a cam
		// Note: it sends the updates periodically on its own + whenever some significant change occurs
		if *m.Type == client.Message_REQUEST && m.Request != nil {
			switch m.Request.GetType() {
			case client.RequestType_PUT_SENSOR_DATA:
				if len(m.Request.SensorData_) > 0 {
					processSensorData(babyUID, m.Request.SensorData_, app.BabyStateManager)
				}
			case client.RequestType_PUT_CONTROL:
				processControl(babyUID, m.Request.Control, app.BabyStateManager)
			case client.RequestType_PUT_SETTINGS:
				processSettings(babyUID, m.Request.Settings, app.BabyStateManager)
			case client.RequestType_PUT_STATUS:
				processStatus(babyUID, m.Request.Status, app.BabyStateManager)
			}
		}
	})

	// Ask for sensor data, settings, and status (initial snapshot)
	conn.SendRequest(client.RequestType_GET_SENSOR_DATA, &client.Request{
		GetSensorData: &client.GetSensorData{
			All: utils.ConstRefBool(true),
		},
	})

	conn.SendRequest(client.RequestType_GET_SETTINGS, &client.Request{})

	conn.SendRequest(client.RequestType_GET_STATUS, &client.Request{
		GetStatus_: &client.GetStatus{
			All: utils.ConstRefBool(true),
		},
	})

	var cleanup func()

	// Local streaming
	if app.Opts.RTMP != nil {
		initializeLocalStreaming := func() {
			requestLocalStreaming(babyUID, app.getLocalStreamURL(babyUID), client.Streaming_STARTED, conn, app.BabyStateManager)
		}

		// Watch for stream liveness change
		unsubscribe := app.BabyStateManager.Subscribe(func(updatedBabyUID string, stateUpdate baby.State) {
			// Do another streaming request if stream just turned unhealthy
			if updatedBabyUID == babyUID && stateUpdate.StreamState != nil && *stateUpdate.StreamState == baby.StreamState_Unhealthy {
				// Prevent duplicate request if we already received failure
				if app.BabyStateManager.GetBabyState(babyUID).GetStreamRequestState() != baby.StreamRequestState_RequestFailed {
					go initializeLocalStreaming()
				}
			}
		})

		cleanup = func() {
			// Stop listening for stream liveness change
			unsubscribe()

			// Stop local streaming
			state := app.BabyStateManager.GetBabyState(babyUID)
			if state.GetIsWebsocketAlive() && state.GetStreamState() == baby.StreamState_Alive {
				requestLocalStreaming(babyUID, app.getLocalStreamURL(babyUID), client.Streaming_STOPPED, conn, app.BabyStateManager)
			}
		}

		// Initialize local streaming upon connection if we know that the stream is not alive
		babyState := app.BabyStateManager.GetBabyState(babyUID)
		if babyState.GetStreamState() != baby.StreamState_Alive {
			if babyState.GetStreamRequestState() != baby.StreamRequestState_Requested || babyState.GetStreamState() == baby.StreamState_Unhealthy {
				go initializeLocalStreaming()
			}
		}
	}

	<-childCtx.Done()
	if cleanup != nil {
		cleanup()
	}
}

func (app *App) getRemoteStreamURL(babyUID string) string {
	return fmt.Sprintf("rtmps://media-secured.nanit.com/nanit/%v.%v", babyUID, app.SessionStore.Snapshot().AuthToken)
}

func (app *App) getLocalStreamURL(babyUID string) string {
	if app.Opts.RTMP != nil {
		tpl := "rtmp://{publicAddr}{path}/{key}"
		key := babyUID
		if app.Opts.RTMP.Key != "" {
			key = app.Opts.RTMP.Key
		}
		return strings.NewReplacer("{publicAddr}", app.Opts.RTMP.PublicAddr, "{path}", app.Opts.RTMP.Path, "{key}", key).Replace(tpl)
	}

	return ""
}
