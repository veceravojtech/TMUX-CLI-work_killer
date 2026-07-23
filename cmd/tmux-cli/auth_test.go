package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunLogin_PrintsLinesAndSaves(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/device/code":
			_ = json.NewEncoder(w).Encode(auth.DeviceCode{
				DeviceCode:      "dev-code-abc",
				UserCode:        "WDJB-MJHT",
				VerificationURI: srv0(r) + "/device",
				ExpiresIn:       900,
				Interval:        5,
			})
		case "/api/v1/auth/device/token":
			_ = json.NewEncoder(w).Encode(auth.Token{
				AccessToken:  "jwt",
				RefreshToken: "refresh-1",
				ExpiresIn:    3600,
				Scopes:       []string{"tasks:write", "artifacts:write", "telemetry:write"},
				Account:      "user@example.test",
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	// Stub the browser opener so no real xdg-open is spawned.
	orig := browserOpener
	var opened string
	browserOpener = func(url string) error { opened = url; return nil }
	defer func() { browserOpener = orig }()

	store := auth.NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	var out bytes.Buffer
	err := runLogin(context.Background(), &out, auth.NewClient(srv.URL), store, srv.URL, "0.1.0")
	require.NoError(t, err)

	got := out.String()
	assert.Contains(t, got, "To authorize this machine, open: "+srv.URL+"/device")
	assert.Contains(t, got, "Enter code: WDJB-MJHT")
	assert.Contains(t, got, "Logged in as user@example.test (3 scopes)")
	assert.Equal(t, srv.URL+"/device", opened, "login attempts to open the verification URI")

	saved, err := store.Load()
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "user@example.test", saved.Account)
	assert.Equal(t, "refresh-1", saved.RefreshToken)
}

func TestRunLogout_Idempotent(t *testing.T) {
	store := auth.NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	var out bytes.Buffer
	// Logout with no store present must still succeed and confirm.
	require.NoError(t, runLogout(&out, store))
	assert.Contains(t, out.String(), "Logged out.")
}

func TestRunWhoami_NotLoggedIn(t *testing.T) {
	store := auth.NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	var out, errOut bytes.Buffer
	code := runWhoami(context.Background(), &out, &errOut, auth.NewClient("http://unused.test"), store)
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "Not logged in — run: tmux-cli login")
}

func TestRunWhoami_LoggedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/auth/whoami", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(auth.Whoami{
			Account:     "user@example.test",
			Scopes:      []string{"tasks:write", "telemetry:write"},
			DeviceLabel: "laptop (abcdef012345)",
			CreatedAt:   "2026-07-22T16:00:00Z",
		})
	}))
	defer srv.Close()

	store := auth.NewStoreAt(filepath.Join(t.TempDir(), "tmux-cli", "auth.json"))
	require.NoError(t, store.Save(&auth.Auth{
		APIURL:       srv.URL,
		Account:      "user@example.test",
		AccessToken:  "jwt-abc",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour), // fresh: no refresh needed
		Scopes:       []string{"tasks:write", "telemetry:write"},
	}))

	var out, errOut bytes.Buffer
	code := runWhoami(context.Background(), &out, &errOut, auth.NewClient(srv.URL), store)
	require.Equal(t, 0, code, "stderr: %s", errOut.String())

	got := out.String()
	assert.Contains(t, got, "Account: user@example.test")
	assert.Contains(t, got, "Device:  laptop (abcdef012345)")
	assert.Contains(t, got, "Scopes:  tasks:write, telemetry:write")
}

// srv0 reconstructs the server's own base URL from a request, so the device-code
// handler can advertise a verification_uri on the same test server.
func srv0(r *http.Request) string {
	return "http://" + r.Host
}
