package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWritesOwnerOnlyFileAndLoadsBack(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "session.json")

	// A session file left behind by an older version: world-readable.
	if err := os.WriteFile(filename, []byte(`{"revision":3,"refreshToken":"old"}`), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewSessionStore()
	store.Filename = filename
	store.Load()
	store.Update(func(s *Session) {
		s.AuthToken = "auth"
		s.RefreshToken = "refresh"
	})

	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("session file mode = %o, want 600", mode)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files left next to the session file: %v", entries)
	}

	reloaded := NewSessionStore()
	reloaded.Filename = filename
	reloaded.Load()
	if got := reloaded.Snapshot(); got.AuthToken != "auth" || got.RefreshToken != "refresh" {
		t.Fatalf("reloaded session = %+v", got)
	}
}

// When the file cannot be replaced by a rename (here: its directory is read-only, as with a single-file
// bind mount), the session is still saved in place.
func TestSaveFallsBackToWritingInPlace(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("directory permissions do not restrict root")
	}

	dir := t.TempDir()
	filename := filepath.Join(dir, "session.json")
	if err := os.WriteFile(filename, []byte(`{"revision":3}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)

	store := NewSessionStore()
	store.Filename = filename
	store.Update(func(s *Session) { s.RefreshToken = "refresh" })

	reloaded := NewSessionStore()
	reloaded.Filename = filename
	reloaded.Load()
	if got := reloaded.Snapshot().RefreshToken; got != "refresh" {
		t.Fatalf("refresh token after in-place save = %q, want %q", got, "refresh")
	}
}
