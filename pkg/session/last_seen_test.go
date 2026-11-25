package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLastSeenMessageIsPerBaby(t *testing.T) {
	s := &Session{}
	a := time.Unix(1_700_000_100, 0)
	b := time.Unix(1_700_000_050, 0)

	if got := s.LastSeenMessage("baby-a"); !got.IsZero() {
		t.Fatalf("last seen of an unknown baby = %v, want zero", got)
	}

	s.SetLastSeenMessage("baby-a", a)
	s.SetLastSeenMessage("baby-b", b)

	if got := s.LastSeenMessage("baby-a"); !got.Equal(a) {
		t.Errorf("baby-a = %v, want %v", got, a)
	}
	if got := s.LastSeenMessage("baby-b"); !got.Equal(b) {
		t.Errorf("baby-b = %v, want %v", got, b)
	}
}

// A session file written by an older version has one time for all babies: it is the starting point for
// each of them until they get their own.
func TestLastSeenMessageStartsFromLegacyTime(t *testing.T) {
	legacy := time.Unix(1_700_000_000, 0).UTC()

	s := &Session{}
	data := []byte(`{"revision":3,"lastSeenMessageTime":"` + legacy.Format(time.RFC3339) + `"}`)
	if err := json.Unmarshal(data, s); err != nil {
		t.Fatal(err)
	}

	if got := s.LastSeenMessage("baby-a"); !got.Equal(legacy) {
		t.Fatalf("baby-a = %v, want the legacy time %v", got, legacy)
	}

	newer := legacy.Add(time.Minute)
	s.SetLastSeenMessage("baby-a", newer)

	if got := s.LastSeenMessage("baby-a"); !got.Equal(newer) {
		t.Errorf("baby-a = %v, want %v", got, newer)
	}
	if got := s.LastSeenMessage("baby-b"); !got.Equal(legacy) {
		t.Errorf("baby-b = %v, want it still at the legacy time %v", got, legacy)
	}
}

// Snapshots share the map with the live session, so updating must not change a snapshot taken earlier.
func TestSetLastSeenMessageDoesNotChangeSnapshots(t *testing.T) {
	store := NewSessionStore()
	first := time.Unix(1_700_000_000, 0)

	store.Update(func(s *Session) { s.SetLastSeenMessage("baby-a", first) })
	snapshot := store.Snapshot()

	store.Update(func(s *Session) { s.SetLastSeenMessage("baby-a", first.Add(time.Hour)) })

	if got := snapshot.LastSeenMessage("baby-a"); !got.Equal(first) {
		t.Fatalf("earlier snapshot changed to %v", got)
	}
}
