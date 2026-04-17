package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/message"
	"github.com/gregory-m/nanit/pkg/session"
	"github.com/gregory-m/nanit/pkg/utils"
)

var myClient = &http.Client{Timeout: 10 * time.Second}
var ErrExpiredRefreshToken = errors.New("Refresh token has expired. Relogin required.")
var ErrNoAuthToken = errors.New("no auth token available; login required")

type MFARequiredError struct {
	MFAToken string
}

func (e *MFARequiredError) Error() string {
	return "MFA authentication enabled for user account"
}

// ------------------------------------------

type AuthRequestPayload struct {
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
	Channel  string `json:"channel,omitempty"`
	MFAToken string `json:"mfa_token,omitempty"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type authResponsePayload struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"` // We can store this to renew a session, avoiding the need to re-auth with MFA
}

type authMFAEnabledResponsePayload struct {
	MFAToken    string `json:"mfa_token,omitempty"`
	PhoneSuffix string `json:"phone_suffix,omitempty"`
	Channel     string `json:"channel,omitempty"`
}

type babiesResponsePayload struct {
	Babies []baby.Baby `json:"babies"`
}

type messagesResponsePayload struct {
	Messages []message.Message `json:"messages"`
}

// ------------------------------------------

// NanitClient - client context
type NanitClient struct {
	RefreshToken string
	SessionStore *session.Store
}

// MaybeAuthorize - Performs authorization if we don't have token or we assume it is expired
func (c *NanitClient) MaybeAuthorize(force bool) error {
	sess := c.SessionStore.Snapshot()
	if force || sess.AuthToken == "" || time.Since(sess.AuthTime) > AuthTokenTimelife {
		return c.Authorize()
	}
	return nil
}

// Authorize - performs authorization attempt
func (c *NanitClient) Authorize() error {
	// Seed the session refresh token from the CLI-provided one if missing.
	c.SessionStore.Update(func(s *session.Session) {
		if len(s.RefreshToken) == 0 {
			s.RefreshToken = c.RefreshToken
		}
	})

	if len(c.SessionStore.Snapshot().RefreshToken) == 0 {
		return ErrNoAuthToken
	}

	if err := c.RenewSession(); err != nil {
		return fmt.Errorf("refresh session: %w", err)
	}
	return nil
}

// Renews an existing session using a valid refresh token
// If the refresh token has also expired, we need to perform a full re-login
func (c *NanitClient) RenewSession() error {
	refreshToken := c.SessionStore.Snapshot().RefreshToken
	log.Debug().Str("refresh_token", utils.AnonymizeToken(refreshToken, 4)).Msg("Renewing Session")
	requestBody, err := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
	})
	if err != nil {
		return fmt.Errorf("marshal refresh body: %w", err)
	}

	r, err := myClient.Post("https://api.nanit.com/tokens/refresh", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("refresh request: %w", err)
	}
	defer r.Body.Close()

	if r.StatusCode == 404 {
		log.Warn().Msg("Server responded with code 404. This typically means your refresh token has expired.")
		return ErrExpiredRefreshToken
	} else if r.StatusCode > 299 || r.StatusCode < 200 {
		return fmt.Errorf("refresh responded with status %d", r.StatusCode)
	}

	authResponse := new(authResponsePayload)
	if err := json.NewDecoder(r.Body).Decode(authResponse); err != nil {
		return fmt.Errorf("decode refresh response: %w", err)
	}

	log.Info().Str("token", utils.AnonymizeToken(authResponse.AccessToken, 4)).Msg("Authorized")
	log.Info().Str("refresh_token", utils.AnonymizeToken(authResponse.RefreshToken, 4)).Msg("Retreived")
	c.SessionStore.Update(func(s *session.Session) {
		s.AuthToken = authResponse.AccessToken
		s.RefreshToken = authResponse.RefreshToken
		s.AuthTime = time.Now()
	})

	return nil
}

// Login - performs login with MFA support
func (c *NanitClient) Login(authReq *AuthRequestPayload) (accessToken string, refreshToken string, err error) {
	log.Debug().
		Str("email", authReq.Email).
		Str("password", utils.AnonymizeToken(authReq.Password, 0)).
		Str("channel", authReq.Channel).
		Str("mfa_token", utils.AnonymizeToken(authReq.MFAToken, 4)).
		Str("mfa_code", utils.AnonymizeToken(authReq.MFACode, 1)).
		Msg("Authorizing")

	requestBody, err := json.Marshal(authReq)
	if err != nil {
		return "", "", fmt.Errorf("unable to marshal auth body: %q", err)
	}

	req, err := http.NewRequest("POST", "https://api.nanit.com/login", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", "", fmt.Errorf("unable to create request: %q", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("nanit-api-version", "2")
	r, err := myClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("unable to fetch auth token: %q", err)
	}

	defer r.Body.Close()

	if r.StatusCode == 401 {
		return "", "", fmt.Errorf("server responded with code 401, provided credentials has not been accepted by the server")
	} else if r.StatusCode == 482 {
		mfaResponse := new(authMFAEnabledResponsePayload)

		err = json.NewDecoder(r.Body).Decode(mfaResponse)
		if err != nil {
			return "", "", fmt.Errorf("unable to decode MFA response: %q", err)
		}

		log.Debug().Str("mfa_token", utils.AnonymizeToken(mfaResponse.MFAToken, 4)).Msg("MFA Required")
		return "", "", &MFARequiredError{MFAToken: mfaResponse.MFAToken}
	} else if r.StatusCode != 201 {
		return "", "", fmt.Errorf("server responded with unexpected status code: %d", r.StatusCode)
	}

	authResponse := new(authResponsePayload)
	if err := json.NewDecoder(r.Body).Decode(authResponse); err != nil {
		return "", "", fmt.Errorf("unable to decode auth response: %w", err)
	}

	log.Debug().Str("access_token", utils.AnonymizeToken(authResponse.AccessToken, 4)).
		Str("refresh_token", utils.AnonymizeToken(authResponse.RefreshToken, 4)).
		Msg("Authorized")

	return authResponse.AccessToken, authResponse.RefreshToken, nil
}

// FetchAuthorized - makes an authorized HTTP request, refreshing the token on 401 once.
func (c *NanitClient) FetchAuthorized(req *http.Request, data interface{}) error {
	for i := 0; i < 2; i++ {
		token := c.SessionStore.Snapshot().AuthToken
		if token == "" {
			if err := c.Authorize(); err != nil {
				return err
			}
			continue
		}

		req.Header.Set("Authorization", token)
		res, err := myClient.Do(req)
		if err != nil {
			return fmt.Errorf("http request failed: %w", err)
		}

		if res.StatusCode == 401 {
			res.Body.Close()
			log.Info().Msg("Token might be expired. Will try to re-authenticate.")
			if err := c.Authorize(); err != nil {
				return err
			}
			continue
		}

		if res.StatusCode != 200 {
			res.Body.Close()
			return fmt.Errorf("server responded with unexpected status %d", res.StatusCode)
		}

		err = json.NewDecoder(res.Body).Decode(data)
		res.Body.Close()
		if err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}

	return errors.New("unable to make request due to failed authorization (2 attempts)")
}

// FetchBabies - fetches baby list
func (c *NanitClient) FetchBabies() ([]baby.Baby, error) {
	log.Info().Msg("Fetching babies list")
	req, err := http.NewRequest("GET", "https://api.nanit.com/babies", nil)
	if err != nil {
		return nil, fmt.Errorf("create babies request: %w", err)
	}

	data := new(babiesResponsePayload)
	if err := c.FetchAuthorized(req, data); err != nil {
		return nil, err
	}

	c.SessionStore.Update(func(s *session.Session) {
		s.Babies = data.Babies
	})
	return data.Babies, nil
}

// FetchMessages - fetches message list
func (c *NanitClient) FetchMessages(babyUID string, limit int) ([]message.Message, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.nanit.com/babies/%s/messages?limit=%d", babyUID, limit), nil)
	if err != nil {
		return nil, fmt.Errorf("create messages request: %w", err)
	}

	data := new(messagesResponsePayload)
	if err := c.FetchAuthorized(req, data); err != nil {
		return nil, err
	}

	return data.Messages, nil
}

// EnsureBabies - fetches baby list if not fetched already
func (c *NanitClient) EnsureBabies() ([]baby.Baby, error) {
	babies := c.SessionStore.Snapshot().Babies
	if len(babies) == 0 {
		return c.FetchBabies()
	}
	return babies, nil
}

// FetchNewMessages - fetches 10 newest messages, ignores any messages which were already fetched or which are older than 5 minutes
func (c *NanitClient) FetchNewMessages(babyUID string, defaultMessageTimeout time.Duration) ([]message.Message, error) {
	fetchedMessages, err := c.FetchMessages(babyUID, 10)
	if err != nil {
		return nil, err
	}

	if len(fetchedMessages) == 0 {
		log.Debug().Msg("No messages fetched")
		return nil, nil
	}

	// sort fetchedMessages starting with most recent
	sort.Slice(fetchedMessages, func(i, j int) bool {
		return fetchedMessages[i].Time.Time().After(fetchedMessages[j].Time.Time())
	})

	lastSeenMessageTime := c.SessionStore.Snapshot().LastSeenMessageTime
	messageTimeoutTime := lastSeenMessageTime
	log.Debug().Msgf("Last seen message time was %s", lastSeenMessageTime)

	// Don't know when last message was, set messageTimeout to default
	if lastSeenMessageTime.IsZero() {
		messageTimeoutTime = time.Now().UTC().Add(-defaultMessageTimeout)
	}

	// lastSeenMessageTime is older than most recent fetchedMessage, or is unset
	newest := fetchedMessages[0].Time.Time()
	if lastSeenMessageTime.Before(newest) {
		c.SessionStore.Update(func(s *session.Session) {
			s.LastSeenMessageTime = newest
		})
	}

	// Only keep messages that are more recent than messageTimeoutTime
	filteredMessages := message.FilterMessages(fetchedMessages, func(m message.Message) bool {
		return m.Time.Time().After(messageTimeoutTime)
	})

	log.Debug().Msgf("Found %d new messages", len(filteredMessages))
	log.Debug().Msgf("%+v\n", filteredMessages)

	return filteredMessages, nil
}
