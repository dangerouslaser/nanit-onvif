package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
)

// Revision - marks the version of the structure of a session file. Only files with equal revision will be loaded
// Note: you should increment this whenever you change the Session structure
const Revision = 3

// Session - application session data container
type Session struct {
	Revision            int         `json:"revision"`
	AuthToken           string      `json:"authToken"`
	AuthTime            time.Time   `json:"authTime"`
	Babies              []baby.Baby `json:"babies"`
	RefreshToken        string      `json:"refreshToken"`
	LastSeenMessageTime time.Time   `json:"lastSeenMessageTime"`
}

// Store - application session store context. All reads and writes of Session
// go through Snapshot/Update so the web-login handler and the app goroutines
// can safely share state.
type Store struct {
	Filename string

	mu      sync.Mutex
	session *Session
}

// NewSessionStore - constructor
func NewSessionStore() *Store {
	return &Store{
		session: &Session{Revision: Revision},
	}
}

// Snapshot returns a value copy of the current session. The returned value
// is a stable view even if the store is concurrently updated.
func (store *Store) Snapshot() Session {
	store.mu.Lock()
	defer store.mu.Unlock()
	return *store.session
}

// Update applies fn to the live session under the store lock and persists
// the resulting state to disk (if a filename is configured).
func (store *Store) Update(fn func(*Session)) {
	store.mu.Lock()
	fn(store.session)
	store.saveLocked()
	store.mu.Unlock()
}

// Load - loads previous state from a file
func (store *Store) Load() {
	if _, err := os.Stat(store.Filename); os.IsNotExist(err) {
		log.Info().Str("filename", store.Filename).Msg("No app session file found")
		return
	}

	f, err := os.Open(store.Filename)
	if err != nil {
		log.Fatal().Str("filename", store.Filename).Err(err).Msg("Unable to open app session file")
	}

	defer f.Close()

	session := &Session{}
	jsonErr := json.NewDecoder(f).Decode(session)
	if jsonErr != nil {
		log.Warn().Str("filename", store.Filename).Err(jsonErr).Msg("Unable to decode app session file, starting with fresh session")
		return
	}

	if session.Revision == Revision {
		store.mu.Lock()
		store.session = session
		store.mu.Unlock()
		log.Info().Str("filename", store.Filename).Msg("Loaded app session from the file")
	} else {
		log.Warn().Str("filename", store.Filename).Msg("App session file contains older revision of the state, ignoring")
	}
}

// Save persists the current session to disk.
func (store *Store) Save() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveLocked()
}

func (store *Store) saveLocked() {
	if store.Filename == "" {
		return
	}

	log.Trace().Str("filename", store.Filename).Msg("Storing app session to the file")

	f, err := os.OpenFile(store.Filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Error().Str("filename", store.Filename).Err(err).Msg("Unable to open app session file for writing")
		return
	}

	defer f.Close()

	data, err := json.Marshal(store.session)
	if err != nil {
		log.Error().Str("filename", store.Filename).Err(err).Msg("Unable to marshal contents of app session file")
		return
	}

	if _, err := f.Write(data); err != nil {
		log.Error().Str("filename", store.Filename).Err(err).Msg("Unable to write to app session file")
	}
}

// InitSessionStore - Initializes new application session store
func InitSessionStore(sessionFile string) *Store {
	sessionStore := NewSessionStore()

	// Load previous state of the application from session file
	if sessionFile != "" {

		absFileName, filePathErr := filepath.Abs(sessionFile)
		if filePathErr != nil {
			log.Fatal().Str("path", sessionFile).Err(filePathErr).Msg("Unable to retrieve absolute file path")
		}

		sessionStore.Filename = absFileName
		sessionStore.Load()
	}

	return sessionStore
}
