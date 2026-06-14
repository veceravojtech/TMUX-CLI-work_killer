// Package producer reports task submissions to the tmux-cli backend over HTTP.
//
// Each submission is a JSON POST to /api/v1/tasks signed with an embedded
// Ed25519 private key; the backend verifies the detached signature over
// fmt.Sprintf("%d", timestamp)+body. The package is a side channel and degrades
// gracefully: construction is config-gated and returns a nil *Client (a no-op)
// when reporting is disabled or no usable key is embedded, and a nil-receiver
// SubmitTask returns (nil, nil) so callers never have to nil-check. Network and
// HTTP failures are returned (and logged to stderr) — never panicked, never
// blocking beyond the 10s client timeout.
//
// Dependency direction is producer -> identity only; the leaf identity package
// is reused verbatim and never imported in reverse.
package producer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/identity"
)

// keysFS embeds the keys directory. The all: prefix is required so the
// dot-prefixed .gitkeep placeholder is included, guaranteeing the directory is
// non-empty and the build never breaks when keys/private.pem is gitignored or
// absent. The real key is read from this FS at New time.
//
//go:embed all:keys
var keysFS embed.FS

// defaultAPIURL is the fallback backend base URL when neither cfg.APIURL nor the
// TMUX_CLI_API_URL environment variable is set.
const defaultAPIURL = "https://tmux.vojta.ai"

// errBodyLimit caps how many bytes of a non-2xx response body are read into the
// returned error. Enough for a full Symfony violation list while keeping a
// hostile/huge body from bloating logs.
const errBodyLimit = 2048

// Client signs and POSTs task submissions to the backend. A nil *Client is a
// valid no-op receiver.
type Client struct {
	baseURL     string
	httpClient  *http.Client
	privateKey  ed25519.PrivateKey
	fingerprint string
	// project is the lane applied transparently to every call: stamped on each
	// SubmitTask and used to scope ClaimTask/ListTasks. Empty means unscoped
	// (global) — preserving the pre-lane behavior for clients without a config.
	project string
}

// New builds a Client from cfg. It returns nil (a no-op client) when reporting
// is disabled or no usable Ed25519 key is embedded — the caller treats nil as
// "reporting off" and never has to nil-check before calling SubmitTask. URL
// precedence is cfg.APIURL -> TMUX_CLI_API_URL env -> defaultAPIURL.
func New(cfg Config) *Client {
	if !cfg.APIEnabled {
		return nil
	}
	key, err := loadPrivateKey(keysFS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: reporting disabled, could not load signing key:", err)
		return nil
	}
	c := newClient(resolveBaseURL(cfg.APIURL), key, &http.Client{Timeout: 10 * time.Second})
	c.project = cfg.Project
	return c
}

// newClient is the construction seam shared by New and tests. Tests inject a
// generated key, a test-server base URL, and an *http.Client (e.g. with a short
// timeout) so no real keys/private.pem is required.
func newClient(baseURL string, key ed25519.PrivateKey, hc *http.Client) *Client {
	return &Client{
		baseURL:     baseURL,
		httpClient:  hc,
		privateKey:  key,
		fingerprint: identity.Fingerprint(),
	}
}

// resolveBaseURL applies the documented URL precedence: an explicit cfg URL
// wins, then the TMUX_CLI_API_URL environment variable, then defaultAPIURL.
func resolveBaseURL(cfgURL string) string {
	if cfgURL != "" {
		return cfgURL
	}
	if env := os.Getenv("TMUX_CLI_API_URL"); env != "" {
		return env
	}
	return defaultAPIURL
}

// SubmitTask signs req and POSTs it to {baseURL}/api/v1/tasks. A nil receiver is
// a no-op returning (nil, nil). Transport errors, a non-2xx response, or a
// timeout return (nil, error) — logged to stderr, never panicking and never
// blocking beyond the client's 10s timeout. The exact body bytes that are sent
// are the same bytes that are signed, so the backend's detached-signature check
// matches byte-for-byte.
func (c *Client) SubmitTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	if c == nil {
		return nil, nil
	}

	// Default to the client's own lane only when the caller did not set one
	// explicitly. An explicit req.Project (e.g. a cross-project task-report that
	// targets another lane) is preserved — so a report from any project can route
	// to the project the issue actually belongs to.
	if req.Project == "" {
		req.Project = c.project
	}

	body, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: failed to marshal task request:", err)
		return nil, err
	}

	ts := time.Now().Unix()
	sig := c.sign(ts, body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/tasks", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: failed to build request:", err)
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Signature", sig)
	httpReq.Header.Set("X-Timestamp", fmt.Sprintf("%d", ts))
	httpReq.Header.Set("X-Fingerprint", c.fingerprint)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: task submission failed:", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface a bounded slice of the rejection body (e.g. a 422 validation
		// violation list) so the cause is diagnosable, then drain the remainder
		// so the underlying connection can be reused.
		slice, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		io.Copy(io.Discard, resp.Body)
		err := fmt.Errorf("producer: task submission returned status %d", resp.StatusCode)
		if body := strings.TrimSpace(string(slice)); body != "" {
			err = fmt.Errorf("producer: task submission returned status %d: %s", resp.StatusCode, body)
		}
		fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	var out TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintln(os.Stderr, "producer: failed to decode task response:", err)
		return nil, err
	}
	return &out, nil
}

// sign returns the base64 (std encoding, matching PHP base64_encode) Ed25519
// detached signature over the exact bytes fmt.Sprintf("%d", ts)+string(body).
// The body slice MUST be the same one sent as the request body so the backend
// verifier reproduces the message byte-for-byte.
func (c *Client) sign(ts int64, body []byte) string {
	msg := fmt.Sprintf("%d", ts) + string(body)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(c.privateKey, []byte(msg)))
}

// loadPrivateKey reads keys/private.pem from the embedded FS and parses an
// Ed25519 private key from one of two interchangeable encodings, so the key the
// web GenerateKeypairCommand emits drops in without reformatting:
//
//   - PKCS#8 PEM (the openssl/Go shape): tried first via pem.Decode.
//   - base64 of a raw Ed25519 key (the libsodium shape the web command prints):
//     either the 64-byte secret key (seed||public, byte-identical to
//     ed25519.PrivateKey) or a 32-byte seed.
//
// A missing file (fs.ErrNotExist), or contents that are neither a parseable
// PKCS#8 PEM nor a base64 key of a valid length, return an error so New degrades
// to a nil client rather than crashing.
func loadPrivateKey(fsys embed.FS) (ed25519.PrivateKey, error) {
	keyBytes, err := fsys.ReadFile("keys/private.pem")
	if err != nil {
		// Includes fs.ErrNotExist when the key is gitignored/absent; the caller
		// treats any error here as "reporting disabled".
		return nil, err
	}
	return parsePrivateKey(keyBytes)
}

// parsePrivateKey decodes an Ed25519 private key from PKCS#8 PEM or a base64 raw
// key (see loadPrivateKey for the accepted shapes). Split out from the embed.FS
// read so the format handling is unit-testable without a real keys/private.pem.
func parsePrivateKey(keyBytes []byte) (ed25519.PrivateKey, error) {
	// Preferred: PKCS#8 PEM. pem.Decode returns nil for non-PEM input (e.g. a
	// bare base64 key), in which case we fall through to the base64 path.
	if block, _ := pem.Decode(keyBytes); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("producer: keys/private.pem is not an ed25519 private key")
		}
		return key, nil
	}

	// Fallback: base64-encoded raw key, the libsodium shape the web command emits.
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyBytes)))
	if err != nil {
		return nil, fmt.Errorf("producer: keys/private.pem is neither PKCS#8 PEM nor base64: %w", err)
	}
	switch len(raw) {
	case ed25519.PrivateKeySize: // 64-byte libsodium secret key (seed||public)
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize: // 32-byte seed
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("producer: keys/private.pem base64 decodes to %d bytes, want %d (seed) or %d (secret key)", len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}
