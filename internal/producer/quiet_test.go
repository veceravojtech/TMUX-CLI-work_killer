package producer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr to a pipe for the duration of fn and returns
// everything written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

func newTransportFailClient(t *testing.T, quiet bool) *Client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	c := newClient("http://127.0.0.1:0", priv, &http.Client{})
	if quiet {
		c = c.Quiet()
	}
	return c
}

// TestClientQuiet_SuppressesTransportErrorStderr is the headline guarantee: a
// best-effort (quiet) client never leaks the "producer: request failed" line a
// transport error (e.g. the board's transient 2s deadline-exceeded) would
// otherwise print — while a normal client still surfaces it.
func TestClientQuiet_SuppressesTransportErrorStderr(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel() // force the transport to fail immediately, like a blown deadline

	loud := captureStderr(t, func() {
		_, _ = newTransportFailClient(t, false).ListTasks(cancelled, ListTasksParams{Limit: 1})
	})
	if !strings.Contains(loud, "producer: request failed") {
		t.Fatalf("non-quiet client should print the transport error; stderr = %q", loud)
	}

	quiet := captureStderr(t, func() {
		_, _ = newTransportFailClient(t, true).ListTasks(cancelled, ListTasksParams{Limit: 1})
	})
	if strings.Contains(quiet, "producer: request failed") {
		t.Fatalf("quiet client must NOT print the transport error; stderr = %q", quiet)
	}
}

// TestClientQuiet_NilSafe documents that Quiet() on a nil no-op client (New
// returned nil) is safe and stays nil, so `producer.New(cfg).Quiet()` is valid.
func TestClientQuiet_NilSafe(t *testing.T) {
	var c *Client
	if got := c.Quiet(); got != nil {
		t.Fatalf("(*Client)(nil).Quiet() = %v, want nil", got)
	}
}
