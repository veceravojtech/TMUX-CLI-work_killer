package producer

import (
	"crypto/ed25519"
	"net/http"
)

// NewClientForTest is the test-only construction seam: it builds a *Client from
// an explicit base URL, signing key and *http.Client, bypassing the embedded-key
// loading and config gating that New performs. It exists so tests in OTHER
// packages (e.g. internal/mcp's task-report happy path) can inject a generated
// keypair and an httptest.Server URL without a real keys/private.pem on disk —
// New returns nil in a test build because no key is embedded. This keeps the
// internal newClient seam unexported while giving cross-package tests the same
// construction path. It is not meant for production use.
func NewClientForTest(baseURL string, key ed25519.PrivateKey, hc *http.Client) *Client {
	return newClient(baseURL, key, hc)
}
