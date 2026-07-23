package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleAuth() *Auth {
	return &Auth{
		APIURL:       "https://example.test",
		Account:      "user@example.test",
		AccessToken:  "access-xyz",
		RefreshToken: "refresh-abc",
		ExpiresAt:    time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		Scopes:       []string{"tasks:write", "artifacts:write", "telemetry:write"},
	}
}

func TestDefaultStorePath_XDGOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	got, err := DefaultStorePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "tmux-cli", "auth.json"), got)
}

func TestStoreSave_Enforces0600(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	require.NoError(t, s.Save(sampleAuth()))

	info, err := os.Stat(s.Path())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "auth.json must be 0600")
}

func TestStoreSaveLoad_RoundTrip(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	want := sampleAuth()
	require.NoError(t, s.Save(want))

	got, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.Account, got.Account)
	assert.Equal(t, want.AccessToken, got.AccessToken)
	assert.Equal(t, want.RefreshToken, got.RefreshToken)
	assert.True(t, want.ExpiresAt.Equal(got.ExpiresAt))
	assert.Equal(t, want.Scopes, got.Scopes)
}

func TestStoreSave_AtomicNoTempLeftBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmux-cli")
	s := NewStoreAt(filepath.Join(dir, "auth.json"))
	require.NoError(t, s.Save(sampleAuth()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"auth.json"}, names, "no temp file should survive an atomic write")
}

func TestStoreLoad_MissingIsLoggedOut(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	got, err := s.Load()
	require.NoError(t, err)
	assert.Nil(t, got, "missing store must read as logged out")
}

func TestStoreLoad_CorruptIsLoggedOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	s := NewStoreAt(path)

	got, err := s.Load()
	require.NoError(t, err, "corrupt store must not error")
	assert.Nil(t, got, "corrupt store must read as logged out")
}

func TestStoreLoad_InsecurePermsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"account":"x"}`), 0o644))
	s := NewStoreAt(path)

	got, err := s.Load()
	require.NoError(t, err)
	assert.Nil(t, got, "group/world-readable store must be ignored as logged out")
}

func TestStoreDelete_Idempotent(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	require.NoError(t, s.Save(sampleAuth()))
	require.NoError(t, s.Delete())
	// Second delete on an already-absent file is a no-op.
	require.NoError(t, s.Delete())

	got, err := s.Load()
	require.NoError(t, err)
	assert.Nil(t, got)
}
