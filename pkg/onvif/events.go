package onvif

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
)

// ONVIF event topics we emit.
const (
	topicMotion = "tns1:RuleEngine/CellMotionDetector/Motion"
	topicSound  = "tns1:AudioAnalytics/Audio/DetectedSound"

	dataKeyMotion = "IsMotion"
	dataKeySound  = "IsSoundDetected"
)

// EventManager bridges baby state changes to ONVIF PullPoint subscribers.
//
// Nanit surfaces motion/sound two ways: (1) live websocket SensorData with
// isAlert booleans, and (2) REST-polled event timestamps. Websocket clears
// drive event=false directly; for REST-polled events we auto-clear after
// HoldDuration so NVRs see a discrete trigger pulse.
type EventManager struct {
	HoldDuration time.Duration

	mu   sync.RWMutex
	subs map[string]*subscription

	clearsMu sync.Mutex
	clears   map[string]*time.Timer // "{babyUID}:{topic}" -> timer

	lastStateMu sync.Mutex
	// lastMotion/lastSound hold the most recent timestamp we've converted into
	// an event. Pointer-valued: nil means "not yet seen" — the first
	// observation records the baseline without firing, so the subscribe-prime
	// replay of stale timestamps after a restart doesn't re-trigger.
	lastMotion map[string]*int32
	lastSound  map[string]*int32

	stateUnsub func()
}

type subscription struct {
	id          string
	terminateAt time.Time

	mu      sync.Mutex
	queue   []notification
	waiters []chan struct{}
}

type notification struct {
	time   time.Time
	topic  string
	source string
	key    string
	value  bool
}

// NewEventManager creates an EventManager. Call Start() once the baby state
// manager is ready; call Stop() to release subscriptions.
func NewEventManager(hold time.Duration) *EventManager {
	if hold <= 0 {
		hold = 30 * time.Second
	}
	return &EventManager{
		HoldDuration: hold,
		subs:         make(map[string]*subscription),
		clears:       make(map[string]*time.Timer),
		lastMotion:   make(map[string]*int32),
		lastSound:    make(map[string]*int32),
	}
}

// Start subscribes to the baby state manager and begins emitting events.
func (em *EventManager) Start(stateManager *baby.StateManager) {
	em.stateUnsub = stateManager.Subscribe(func(babyUID string, state baby.State) {
		em.handleState(babyUID, state)
	})
	go em.janitor()
}

// Stop releases resources.
func (em *EventManager) Stop() {
	if em.stateUnsub != nil {
		em.stateUnsub()
	}
	em.clearsMu.Lock()
	for _, t := range em.clears {
		t.Stop()
	}
	em.clears = make(map[string]*time.Timer)
	em.clearsMu.Unlock()
}

// janitor periodically evicts expired subscriptions.
func (em *EventManager) janitor() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		em.mu.Lock()
		for id, s := range em.subs {
			if now.After(s.terminateAt) {
				delete(em.subs, id)
				log.Debug().Str("subscription", id).Msg("ONVIF subscription expired")
			}
		}
		em.mu.Unlock()
	}
}

func (em *EventManager) handleState(babyUID string, state baby.State) {
	// Live websocket alert booleans — authoritative when present.
	if state.MotionDetected != nil {
		em.emit(babyUID, topicMotion, dataKeyMotion, *state.MotionDetected)
		em.cancelClear(babyUID, topicMotion)
	}
	if state.SoundDetected != nil {
		em.emit(babyUID, topicSound, dataKeySound, *state.SoundDetected)
		em.cancelClear(babyUID, topicSound)
	}

	// Timestamp-driven triggers (REST-polled). Only fire on advance past the
	// last observed value; on the first sighting, record the baseline without
	// firing (handles subscribe-prime replay after a restart).
	if state.MotionTimestamp != nil && state.MotionDetected == nil {
		if em.advanceTS(em.lastMotion, babyUID, *state.MotionTimestamp) {
			em.emit(babyUID, topicMotion, dataKeyMotion, true)
			em.scheduleClear(babyUID, topicMotion, dataKeyMotion)
		}
	}
	if state.SoundTimestamp != nil && state.SoundDetected == nil {
		if em.advanceTS(em.lastSound, babyUID, *state.SoundTimestamp) {
			em.emit(babyUID, topicSound, dataKeySound, true)
			em.scheduleClear(babyUID, topicSound, dataKeySound)
		}
	}
}

// advanceTS returns true if ts is a real advance over the previously-recorded
// value for babyUID. First sighting records the baseline and returns false.
func (em *EventManager) advanceTS(m map[string]*int32, babyUID string, ts int32) bool {
	em.lastStateMu.Lock()
	defer em.lastStateMu.Unlock()
	prev, seen := m[babyUID]
	v := ts
	m[babyUID] = &v
	if !seen {
		return false
	}
	return ts > *prev
}

func (em *EventManager) emit(babyUID, topic, key string, value bool) {
	msg := notification{
		time:   time.Now().UTC(),
		topic:  topic,
		source: babyUID,
		key:    key,
		value:  value,
	}
	em.mu.RLock()
	subs := make([]*subscription, 0, len(em.subs))
	for _, s := range em.subs {
		subs = append(subs, s)
	}
	em.mu.RUnlock()
	for _, s := range subs {
		s.enqueue(msg)
	}
	log.Debug().Str("baby_uid", babyUID).Str("topic", topic).Bool("value", value).Int("subs", len(subs)).Msg("ONVIF event emitted")
}

func (em *EventManager) scheduleClear(babyUID, topic, key string) {
	clearKey := babyUID + ":" + topic
	em.clearsMu.Lock()
	if existing, ok := em.clears[clearKey]; ok {
		existing.Stop()
	}
	em.clears[clearKey] = time.AfterFunc(em.HoldDuration, func() {
		em.emit(babyUID, topic, key, false)
		em.clearsMu.Lock()
		delete(em.clears, clearKey)
		em.clearsMu.Unlock()
	})
	em.clearsMu.Unlock()
}

func (em *EventManager) cancelClear(babyUID, topic string) {
	clearKey := babyUID + ":" + topic
	em.clearsMu.Lock()
	if t, ok := em.clears[clearKey]; ok {
		t.Stop()
		delete(em.clears, clearKey)
	}
	em.clearsMu.Unlock()
}

func (s *subscription) enqueue(msg notification) {
	s.mu.Lock()
	s.queue = append(s.queue, msg)
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, w := range waiters {
		close(w)
	}
}

// drain removes up to limit messages. If timeout > 0 and queue is empty,
// blocks up to timeout waiting for one.
func (s *subscription) drain(limit int, timeout time.Duration) []notification {
	s.mu.Lock()
	if len(s.queue) == 0 && timeout > 0 {
		wait := make(chan struct{})
		s.waiters = append(s.waiters, wait)
		s.mu.Unlock()

		select {
		case <-wait:
		case <-time.After(timeout):
		}

		s.mu.Lock()
	}
	n := len(s.queue)
	if limit > 0 && n > limit {
		n = limit
	}
	out := make([]notification, n)
	copy(out, s.queue[:n])
	s.queue = s.queue[n:]
	s.mu.Unlock()
	return out
}

// --- HTTP handlers ---

const (
	EventsCreatePullPointSubscription = "CreatePullPointSubscription"
	EventsGetEventProperties          = "GetEventProperties"
	EventsPullMessages                = "PullMessages"
	EventsRenew                       = "Renew"
	EventsUnsubscribe                 = "Unsubscribe"
)

var subscriptionPathRX = regexp.MustCompile(`^/onvif/events/subscription/([a-f0-9]+)/?$`)

// HandleEventsService handles the main events_service endpoint —
// CreatePullPointSubscription and GetEventProperties.
func (em *EventManager) HandleEventsService(w http.ResponseWriter, r *http.Request, host string) {
	b, _ := io.ReadAll(r.Body)
	action := GetRequestAction(b)

	log.Debug().Str("action", action).Msg("ONVIF events request")

	var resp []byte
	switch action {
	case EventsCreatePullPointSubscription:
		// Optional: the client may pass InitialTerminationTime; default to 10 min.
		termSec := int64(600)
		if s := FindTagValue(b, "InitialTerminationTime"); s != "" {
			if d, err := parseISODuration(s); err == nil {
				termSec = int64(d.Seconds())
			}
		}
		sub := em.createSubscription(time.Duration(termSec) * time.Second)
		addr := fmt.Sprintf("http://%s/onvif/events/subscription/%s", host, sub.id)
		resp = createPullPointSubscriptionResponse(addr, sub.terminateAt)
	case EventsGetEventProperties:
		resp = getEventPropertiesResponse()
	case ServiceGetServiceCapabilities:
		resp = eventsServiceCapabilitiesResponse()
	default:
		log.Debug().Str("action", action).Msg("ONVIF events: unhandled action")
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Write(resp)
}

// HandleSubscription handles per-subscription operations: PullMessages, Renew, Unsubscribe.
func (em *EventManager) HandleSubscription(w http.ResponseWriter, r *http.Request) {
	m := subscriptionPathRX.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	id := m[1]

	em.mu.RLock()
	sub, ok := em.subs[id]
	em.mu.RUnlock()
	if !ok {
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	}

	b, _ := io.ReadAll(r.Body)
	action := GetRequestAction(b)
	log.Debug().Str("action", action).Str("subscription", id).Msg("ONVIF subscription request")

	var resp []byte
	switch action {
	case EventsPullMessages:
		timeout := 60 * time.Second
		if s := FindTagValue(b, "Timeout"); s != "" {
			if d, err := parseISODuration(s); err == nil {
				timeout = d
			}
		}
		limit := 100
		if s := FindTagValue(b, "MessageLimit"); s != "" {
			var n int
			fmt.Sscanf(s, "%d", &n)
			if n > 0 {
				limit = n
			}
		}
		msgs := sub.drain(limit, timeout)
		resp = pullMessagesResponse(sub.terminateAt, msgs)
	case EventsRenew:
		termSec := int64(600)
		if s := FindTagValue(b, "TerminationTime"); s != "" {
			if d, err := parseISODuration(s); err == nil {
				termSec = int64(d.Seconds())
			}
		}
		em.mu.Lock()
		sub.terminateAt = time.Now().Add(time.Duration(termSec) * time.Second)
		em.mu.Unlock()
		resp = renewResponse(sub.terminateAt)
	case EventsUnsubscribe:
		em.mu.Lock()
		delete(em.subs, id)
		em.mu.Unlock()
		resp = unsubscribeResponse()
	default:
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Write(resp)
}

func (em *EventManager) createSubscription(ttl time.Duration) *subscription {
	buf := make([]byte, 8)
	rand.Read(buf)
	id := hex.EncodeToString(buf)
	sub := &subscription{
		id:          id,
		terminateAt: time.Now().Add(ttl),
	}
	em.mu.Lock()
	em.subs[id] = sub
	em.mu.Unlock()
	log.Info().Str("subscription", id).Dur("ttl", ttl).Msg("ONVIF PullPoint subscription created")
	return sub
}

// --- SOAP response builders ---

func createPullPointSubscriptionResponse(addr string, termAt time.Time) []byte {
	current := time.Now().UTC().Format(time.RFC3339)
	term := termAt.UTC().Format(time.RFC3339)
	e := NewEventsEnvelope()
	e.Appendf(`<tev:CreatePullPointSubscriptionResponse>
	<tev:SubscriptionReference>
		<wsa:Address>%s</wsa:Address>
	</tev:SubscriptionReference>
	<wsnt:CurrentTime>%s</wsnt:CurrentTime>
	<wsnt:TerminationTime>%s</wsnt:TerminationTime>
</tev:CreatePullPointSubscriptionResponse>`, escXML(addr), current, term)
	return e.Bytes()
}

func pullMessagesResponse(termAt time.Time, msgs []notification) []byte {
	current := time.Now().UTC().Format(time.RFC3339)
	term := termAt.UTC().Format(time.RFC3339)
	e := NewEventsEnvelope()
	e.Appendf(`<tev:PullMessagesResponse>
	<tev:CurrentTime>%s</tev:CurrentTime>
	<tev:TerminationTime>%s</tev:TerminationTime>`, current, term)
	for _, m := range msgs {
		ts := m.time.Format(time.RFC3339Nano)
		state := "false"
		if m.value {
			state = "true"
		}
		e.Appendf(`<wsnt:NotificationMessage>
		<wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">%s</wsnt:Topic>
		<wsnt:Message>
			<tt:Message UtcTime="%s" PropertyOperation="Changed">
				<tt:Source>
					<tt:SimpleItem Name="VideoSourceConfigurationToken" Value="%s"/>
				</tt:Source>
				<tt:Data>
					<tt:SimpleItem Name="%s" Value="%s"/>
				</tt:Data>
			</tt:Message>
		</wsnt:Message>
	</wsnt:NotificationMessage>`, m.topic, ts, escXML(m.source), m.key, state)
	}
	e.Append(`</tev:PullMessagesResponse>`)
	return e.Bytes()
}

func renewResponse(termAt time.Time) []byte {
	current := time.Now().UTC().Format(time.RFC3339)
	term := termAt.UTC().Format(time.RFC3339)
	e := NewEventsEnvelope()
	e.Appendf(`<wsnt:RenewResponse>
	<wsnt:TerminationTime>%s</wsnt:TerminationTime>
	<wsnt:CurrentTime>%s</wsnt:CurrentTime>
</wsnt:RenewResponse>`, term, current)
	return e.Bytes()
}

func unsubscribeResponse() []byte {
	e := NewEventsEnvelope()
	e.Append(`<wsnt:UnsubscribeResponse/>`)
	return e.Bytes()
}

func eventsServiceCapabilitiesResponse() []byte {
	e := NewEventsEnvelope()
	e.Append(`<tev:GetServiceCapabilitiesResponse>
	<tev:Capabilities WSSubscriptionPolicySupport="false" WSPullPointSupport="true" WSPausableSubscriptionManagerInterfaceSupport="false" MaxNotificationProducers="1" MaxPullPoints="16" PersistentNotificationStorage="false"/>
</tev:GetServiceCapabilitiesResponse>`)
	return e.Bytes()
}

func getEventPropertiesResponse() []byte {
	e := NewEventsEnvelope()
	e.Append(`<tev:GetEventPropertiesResponse>
	<tev:TopicNamespaceLocation>http://www.onvif.org/onvif/ver10/topics/topicns.xml</tev:TopicNamespaceLocation>
	<wsnt:FixedTopicSet>true</wsnt:FixedTopicSet>
	<wstop:TopicSet xmlns:wstop="http://docs.oasis-open.org/wsn/t-1">
		<tns1:RuleEngine wstop:topic="false">
			<CellMotionDetector wstop:topic="false">
				<Motion wstop:topic="true">
					<tt:MessageDescription IsProperty="true">
						<tt:Source>
							<tt:SimpleItemDescription Name="VideoSourceConfigurationToken" Type="tt:ReferenceToken"/>
						</tt:Source>
						<tt:Data>
							<tt:SimpleItemDescription Name="IsMotion" Type="xs:boolean"/>
						</tt:Data>
					</tt:MessageDescription>
				</Motion>
			</CellMotionDetector>
		</tns1:RuleEngine>
		<tns1:AudioAnalytics wstop:topic="false">
			<Audio wstop:topic="false">
				<DetectedSound wstop:topic="true">
					<tt:MessageDescription IsProperty="true">
						<tt:Source>
							<tt:SimpleItemDescription Name="VideoSourceConfigurationToken" Type="tt:ReferenceToken"/>
						</tt:Source>
						<tt:Data>
							<tt:SimpleItemDescription Name="IsSoundDetected" Type="xs:boolean"/>
						</tt:Data>
					</tt:MessageDescription>
				</DetectedSound>
			</Audio>
		</tns1:AudioAnalytics>
	</wstop:TopicSet>
	<wsnt:TopicExpressionDialect>http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet</wsnt:TopicExpressionDialect>
	<tev:MessageContentFilterDialect>http://www.onvif.org/ver10/tev/messageContentFilter/ItemFilter</tev:MessageContentFilterDialect>
	<tev:MessageContentSchemaLocation>http://www.onvif.org/ver10/schema/onvif.xsd</tev:MessageContentSchemaLocation>
</tev:GetEventPropertiesResponse>`)
	return e.Bytes()
}

// parseISODuration accepts ISO8601 durations like "PT60S", "PT1M30S", "PT2H".
// Only seconds/minutes/hours are supported.
var isoDurationRX = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$`)

func parseISODuration(s string) (time.Duration, error) {
	m := isoDurationRX.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var d time.Duration
	var n int
	if m[1] != "" {
		fmt.Sscanf(m[1], "%d", &n)
		d += time.Duration(n) * time.Hour
	}
	if m[2] != "" {
		fmt.Sscanf(m[2], "%d", &n)
		d += time.Duration(n) * time.Minute
	}
	if m[3] != "" {
		var f float64
		fmt.Sscanf(m[3], "%f", &f)
		d += time.Duration(f * float64(time.Second))
	}
	return d, nil
}

func escXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
