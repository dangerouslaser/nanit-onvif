package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/session"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Config holds dependencies for the web handler.
type Config struct {
	SessionStore     *session.Store
	BabyStateManager *baby.StateManager
	RestClient       *client.NanitClient
	RTSPAddr         string // e.g. ":8554"
	RTMPAddr         string // public RTMP addr
	ONVIFAddr        string // e.g. ":8089"
	ONVIFUser        string
	MQTTBroker       string
	OnLogin          func() // called after successful web login to start baby handlers
}

func mustParsePageTemplate(name string) *template.Template {
	layoutBytes, _ := templateFS.ReadFile("templates/layout.html")
	pageBytes, _ := templateFS.ReadFile("templates/" + name + ".html")
	t := template.Must(template.New("layout").Parse(string(layoutBytes)))
	template.Must(t.New(name).Parse(string(pageBytes)))
	return t
}

// NewHandler returns an http.Handler that serves the web dashboard.
func NewHandler(config Config) http.Handler {
	pages := map[string]*template.Template{
		"dashboard": mustParsePageTemplate("dashboard"),
		"login":     mustParsePageTemplate("login"),
		"mfa":       mustParsePageTemplate("mfa"),
	}

	mux := http.NewServeMux()

	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderDashboard(w, r, pages["dashboard"], config)
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			renderLogin(w, pages["login"], "", "")
			return
		}
		handleLogin(w, r, pages, config)
	})

	mux.HandleFunc("/login/mfa", func(w http.ResponseWriter, r *http.Request) {
		handleMFA(w, r, pages, config)
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		handleAPIStatus(w, config)
	})

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		handleSSE(w, r, config)
	})

	return mux
}

// Dashboard data

type dashboardData struct {
	AuthActive bool
	Babies     []babyCard
	RTMPAddr   string
	RTSPAddr   string
	ONVIFAddr  string
	ONVIFUser  string
	MQTTBroker string
}

type babyCard struct {
	UID            string
	Name           string
	StreamState    string
	StreamClass    string
	WebsocketAlive bool
	Temperature    string
	Humidity       string
	IsNight        string
	LastMotion     string
	LastSound      string
	RTSPURL        string
}

func formatTimestamp(ts *int32) string {
	if ts == nil || *ts == 0 {
		return ""
	}
	return time.Unix(int64(*ts), 0).Format("3:04 PM")
}

func renderDashboard(w http.ResponseWriter, _ *http.Request, t *template.Template, config Config) {
	sess := config.SessionStore.Snapshot()
	authActive := sess.AuthToken != "" && sess.RefreshToken != ""

	// Use the IP from RTMP public addr (user-configured server IP)
	serverIP := ""
	if config.RTMPAddr != "" {
		serverIP, _, _ = net.SplitHostPort(config.RTMPAddr)
	}

	var cards []babyCard
	for _, b := range sess.Babies {
		state := config.BabyStateManager.GetBabyState(b.UID)

		streamState := "Unknown"
		streamClass := "unknown"
		switch state.GetStreamState() {
		case baby.StreamState_Alive:
			streamState = "Alive"
			streamClass = "alive"
		case baby.StreamState_Unhealthy:
			streamState = "Unhealthy"
			streamClass = "unhealthy"
		}

		var temp, hum string
		if state.TemperatureMilli != nil {
			temp = fmt.Sprintf("%.1f\u00b0F", state.GetTemperature()*9.0/5.0+32.0)
		}
		if state.HumidityMilli != nil {
			hum = fmt.Sprintf("%.1f%%", state.GetHumidity())
		}

		var nightMode string
		if state.IsNight != nil {
			if *state.IsNight {
				nightMode = "Yes"
			} else {
				nightMode = "No"
			}
		}

		lastMotion := formatTimestamp(state.MotionTimestamp)
		lastSound := formatTimestamp(state.SoundTimestamp)

		var rtspURL string
		if config.RTSPAddr != "" && serverIP != "" {
			_, port, _ := net.SplitHostPort(config.RTSPAddr)
			rtspURL = fmt.Sprintf("rtsp://%s:%s/local/%s", serverIP, port, b.UID)
		}

		cards = append(cards, babyCard{
			UID:            b.UID,
			Name:           b.Name,
			StreamState:    streamState,
			StreamClass:    streamClass,
			WebsocketAlive: state.GetIsWebsocketAlive(),
			Temperature:    temp,
			Humidity:       hum,
			IsNight:        nightMode,
			LastMotion:     lastMotion,
			LastSound:      lastSound,
			RTSPURL:        rtspURL,
		})
	}

	rtspDisplay := config.RTSPAddr
	onvifDisplay := config.ONVIFAddr
	if serverIP != "" {
		if config.RTSPAddr != "" {
			_, port, _ := net.SplitHostPort(config.RTSPAddr)
			rtspDisplay = serverIP + ":" + port
		}
		if config.ONVIFAddr != "" {
			_, port, _ := net.SplitHostPort(config.ONVIFAddr)
			onvifDisplay = serverIP + ":" + port
		}
	}

	data := dashboardData{
		AuthActive: authActive,
		Babies:     cards,
		RTMPAddr:   config.RTMPAddr,
		RTSPAddr:   rtspDisplay,
		ONVIFAddr:  onvifDisplay,
		ONVIFUser:  config.ONVIFUser,
		MQTTBroker: config.MQTTBroker,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Error().Err(err).Msg("Web: template render error")
	}
}

// Login

type loginData struct {
	Email string
	Error string
}

func renderLogin(w http.ResponseWriter, t *template.Template, email, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", loginData{Email: email, Error: errMsg}); err != nil {
		log.Error().Err(err).Msg("Web: template render error")
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request, pages map[string]*template.Template, config Config) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	c := &client.NanitClient{SessionStore: config.SessionStore}
	_, refreshToken, err := c.Login(&client.AuthRequestPayload{
		Email:    email,
		Password: password,
		Channel:  "sms",
	})

	var mfaErr *client.MFARequiredError
	if errors.As(err, &mfaErr) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pages["mfa"].ExecuteTemplate(w, "layout", mfaData{
			Email:    email,
			Password: password,
			MFAToken: mfaErr.MFAToken,
			Channel:  "SMS",
		})
		return
	}

	if err != nil {
		renderLogin(w, pages["login"], email, err.Error())
		return
	}

	// Store the tokens in the session
	config.SessionStore.Update(func(s *session.Session) {
		s.RefreshToken = refreshToken
	})

	// Also update the rest client's refresh token so it can authorize
	config.RestClient.RefreshToken = refreshToken
	if err := config.RestClient.MaybeAuthorize(true); err != nil {
		renderLogin(w, pages["login"], email, err.Error())
		return
	}
	if _, err := config.RestClient.EnsureBabies(); err != nil {
		renderLogin(w, pages["login"], email, err.Error())
		return
	}

	if config.OnLogin != nil {
		config.OnLogin()
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// MFA

type mfaData struct {
	Email    string
	Password string
	MFAToken string
	Channel  string
	Error    string
}

func handleMFA(w http.ResponseWriter, r *http.Request, pages map[string]*template.Template, config Config) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	mfaToken := r.FormValue("mfa_token")
	mfaCode := r.FormValue("mfa_code")

	c := &client.NanitClient{SessionStore: config.SessionStore}
	_, refreshToken, err := c.Login(&client.AuthRequestPayload{
		Email:    email,
		Password: password,
		MFAToken: mfaToken,
		MFACode:  mfaCode,
	})

	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pages["mfa"].ExecuteTemplate(w, "layout", mfaData{
			Email:    email,
			Password: password,
			MFAToken: mfaToken,
			Channel:  "SMS",
			Error:    err.Error(),
		})
		return
	}

	config.SessionStore.Update(func(s *session.Session) {
		s.RefreshToken = refreshToken
	})

	config.RestClient.RefreshToken = refreshToken
	renderMFAError := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pages["mfa"].ExecuteTemplate(w, "layout", mfaData{
			Email:    email,
			Password: password,
			MFAToken: mfaToken,
			Channel:  "SMS",
			Error:    msg,
		})
	}
	if err := config.RestClient.MaybeAuthorize(true); err != nil {
		renderMFAError(err.Error())
		return
	}
	if _, err := config.RestClient.EnsureBabies(); err != nil {
		renderMFAError(err.Error())
		return
	}

	if config.OnLogin != nil {
		config.OnLogin()
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// API: JSON status

type statusResponse struct {
	AuthActive bool               `json:"auth_active"`
	Babies     []babyStatusEntry  `json:"babies"`
	Servers    serverStatusEntry  `json:"servers"`
}

type babyStatusEntry struct {
	UID            string  `json:"uid"`
	Name           string  `json:"name"`
	StreamState    string  `json:"stream_state"`
	WebsocketAlive bool    `json:"websocket_alive"`
	Temperature    float64 `json:"temperature,omitempty"`
	Humidity       float64 `json:"humidity,omitempty"`
	IsNight        *bool   `json:"is_night,omitempty"`
	MotionTimestamp int32  `json:"motion_timestamp,omitempty"`
	SoundTimestamp  int32  `json:"sound_timestamp,omitempty"`
}

type serverStatusEntry struct {
	RTMP  string `json:"rtmp,omitempty"`
	RTSP  string `json:"rtsp,omitempty"`
	ONVIF string `json:"onvif,omitempty"`
}

func handleAPIStatus(w http.ResponseWriter, config Config) {
	sess := config.SessionStore.Snapshot()
	var babies []babyStatusEntry
	for _, b := range sess.Babies {
		state := config.BabyStateManager.GetBabyState(b.UID)
		streamState := "Unknown"
		switch state.GetStreamState() {
		case baby.StreamState_Alive:
			streamState = "Alive"
		case baby.StreamState_Unhealthy:
			streamState = "Unhealthy"
		}
		entry := babyStatusEntry{
			UID:            b.UID,
			Name:           b.Name,
			StreamState:    streamState,
			WebsocketAlive: state.GetIsWebsocketAlive(),
			Temperature:    state.GetTemperature(),
			Humidity:       state.GetHumidity(),
			IsNight:        state.IsNight,
		}
		if state.MotionTimestamp != nil {
			entry.MotionTimestamp = *state.MotionTimestamp
		}
		if state.SoundTimestamp != nil {
			entry.SoundTimestamp = *state.SoundTimestamp
		}
		babies = append(babies, entry)
	}

	resp := statusResponse{
		AuthActive: sess.AuthToken != "" && sess.RefreshToken != "",
		Babies:     babies,
		Servers: serverStatusEntry{
			RTMP:  config.RTMPAddr,
			RTSP:  config.RTSPAddr,
			ONVIF: config.ONVIFAddr,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// SSE: real-time state updates

type sseEvent struct {
	BabyUID         string  `json:"baby_uid"`
	StreamState     string  `json:"stream_state"`
	WebsocketAlive  bool    `json:"websocket_alive"`
	Temperature     float64 `json:"temperature,omitempty"`
	Humidity        float64 `json:"humidity,omitempty"`
	IsNight         *bool   `json:"is_night,omitempty"`
	MotionTimestamp int32   `json:"motion_timestamp,omitempty"`
	SoundTimestamp  int32   `json:"sound_timestamp,omitempty"`
}

func handleSSE(w http.ResponseWriter, r *http.Request, config Config) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	events := make(chan sseEvent, 16)

	unsubscribe := config.BabyStateManager.Subscribe(func(babyUID string, _ baby.State) {
		// Fetch full current state, not the partial delta
		full := config.BabyStateManager.GetBabyState(babyUID)

		streamState := "Unknown"
		switch full.GetStreamState() {
		case baby.StreamState_Alive:
			streamState = "Alive"
		case baby.StreamState_Unhealthy:
			streamState = "Unhealthy"
		}

		evt := sseEvent{
			BabyUID:        babyUID,
			StreamState:    streamState,
			WebsocketAlive: full.GetIsWebsocketAlive(),
			Temperature:    full.GetTemperature(),
			Humidity:       full.GetHumidity(),
			IsNight:        full.IsNight,
		}
		if full.MotionTimestamp != nil {
			evt.MotionTimestamp = *full.MotionTimestamp
		}
		if full.SoundTimestamp != nil {
			evt.SoundTimestamp = *full.SoundTimestamp
		}

		select {
		case events <- evt:
		default:
			// Drop event if client is too slow
		}
	})
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-events:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
