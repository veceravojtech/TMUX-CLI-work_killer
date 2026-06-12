package producer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKeypair generates a fresh Ed25519 keypair for tests so no real
// keys/private.pem is ever required.
func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func sampleRequest() TaskRequest {
	return TaskRequest{
		Category:           "build",
		Severity:           "high",
		Title:              "broken pipeline",
		Description:        "the build is red",
		ProposedFix:        "pin the dependency",
		ExpectedGreenState: "build passes",
		Payload:            map[string]any{"k": "v"},
	}
}

func TestSubmitTask_SignsAndPostsHappyPath(t *testing.T) {
	pub, priv := testKeypair(t)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tasks", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("X-Fingerprint"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = body

		ts := r.Header.Get("X-Timestamp")
		assert.NotEmpty(t, ts)

		sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
		require.NoError(t, err)

		// Reconstruct the signed message exactly as the backend verifier would:
		// the decimal timestamp string immediately followed by the raw body.
		msg := []byte(ts + string(body))
		assert.True(t, ed25519.Verify(pub, msg, sig), "signature must verify over <ts><body>")

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"task-123","status":"queued","extra":"ignored"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "task-123", resp.ID.String())
	assert.Equal(t, "queued", resp.Status)
	assert.NotEmpty(t, gotBody, "server must have received a JSON body")
}

func TestSubmitTask_NilReceiverNoOp(t *testing.T) {
	var c *Client
	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	assert.NoError(t, err)
}

func TestNew_DisabledReturnsNil(t *testing.T) {
	c := New(Config{APIEnabled: false})
	assert.Nil(t, c)

	// A nil client must remain a safe no-op.
	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	assert.NoError(t, err)
}

func TestSubmitTask_Non2xxReturnsError(t *testing.T) {
	_, priv := testKeypair(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "nope")
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

// TestSubmitTask_Non2xxIncludesBody: a rejection body (e.g. a Symfony 422
// violation list) is surfaced in the error so the rejection cause is
// diagnosable instead of a bare status line.
func TestSubmitTask_Non2xxIncludesBody(t *testing.T) {
	_, priv := testKeypair(t)
	violation := `{"violations":[{"propertyPath":"proposedFix","title":"This value should not be blank."}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, violation)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	resp, err := c.SubmitTask(context.Background(), sampleRequest())
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "should not be blank", "the violation body must be surfaced")
}

// TestSubmitTask_Non2xxBodyBounded: the surfaced body slice is capped at 2048
// bytes no matter how large the response is.
func TestSubmitTask_Non2xxBodyBounded(t *testing.T) {
	_, priv := testKeypair(t)
	big := strings.Repeat("x", 5000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.SubmitTask(context.Background(), sampleRequest())
	require.Error(t, err)

	prefix := "producer: task submission returned status 422: "
	require.True(t, strings.HasPrefix(err.Error(), prefix), "err=%q", err.Error())
	assert.LessOrEqual(t, len(err.Error())-len(prefix), 2048, "body slice must be capped at 2048 bytes")
}

// TestSubmitTask_Non2xxEmptyBody: an empty rejection body preserves the
// status-only error format (no trailing ": ").
func TestSubmitTask_Non2xxEmptyBody(t *testing.T) {
	_, priv := testKeypair(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClient(srv.URL, priv, srv.Client())
	_, err := c.SubmitTask(context.Background(), sampleRequest())
	require.Error(t, err)
	assert.Equal(t, "producer: task submission returned status 500", err.Error())
}

func TestSubmitTask_TransportTimeout(t *testing.T) {
	_, priv := testKeypair(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Inject a client with a timeout shorter than the server's delay so the
	// call returns an error promptly instead of hanging.
	hc := &http.Client{Timeout: 20 * time.Millisecond}
	c := newClient(srv.URL, priv, hc)

	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.SubmitTask(context.Background(), sampleRequest())
		close(done)
	}()
	select {
	case <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitTask blocked past the client timeout")
	}
}

func TestNew_URLResolutionPrecedence(t *testing.T) {
	// cfg URL beats env beats default.
	t.Setenv("TMUX_CLI_API_URL", "https://env.example")
	assert.Equal(t, "https://cfg.example", resolveBaseURL("https://cfg.example"))

	// env beats default when cfg is empty.
	assert.Equal(t, "https://env.example", resolveBaseURL(""))

	// default when neither cfg nor env is set.
	os.Unsetenv("TMUX_CLI_API_URL")
	assert.Equal(t, defaultAPIURL, resolveBaseURL(""))
}

func TestSign_MessageLayout(t *testing.T) {
	pub, priv := testKeypair(t)
	c := newClient("https://unused.example", priv, &http.Client{})

	var ts int64 = 1700000000
	body := []byte(`{"title":"x"}`)

	encoded := c.sign(ts, body)
	sig, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	// The signed message is the exact bytes fmt.Sprintf("%d", ts)+string(body),
	// with no separator and no re-encoding — this locks the byte layout the PHP
	// verifier reproduces.
	msg := []byte(fmt.Sprintf("%d", ts) + string(body))
	assert.True(t, ed25519.Verify(pub, msg, sig))

	// A perturbed message must NOT verify.
	assert.False(t, ed25519.Verify(pub, append([]byte("x"), msg...), sig))
}

func TestLoadConfig_FlatAndNested(t *testing.T) {
	write := func(t *testing.T, content string) string {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, ".tmux-cli")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "setting.yaml"), []byte(content), 0o644))
		return root
	}

	t.Run("flat keys resolve", func(t *testing.T) {
		root := write(t, "apiUrl: https://flat.example\napiEnabled: true\n")
		cfg, err := LoadConfig(root)
		require.NoError(t, err)
		assert.Equal(t, "https://flat.example", cfg.APIURL)
		assert.True(t, cfg.APIEnabled)
	})

	t.Run("nested keys resolve when flat absent", func(t *testing.T) {
		root := write(t, "api:\n  url: https://nested.example\n  enabled: true\n")
		cfg, err := LoadConfig(root)
		require.NoError(t, err)
		assert.Equal(t, "https://nested.example", cfg.APIURL)
		assert.True(t, cfg.APIEnabled)
	})

	t.Run("flat wins when both present", func(t *testing.T) {
		root := write(t, "apiUrl: https://flat.example\napiEnabled: false\napi:\n  url: https://nested.example\n  enabled: true\n")
		cfg, err := LoadConfig(root)
		require.NoError(t, err)
		assert.Equal(t, "https://flat.example", cfg.APIURL)
		assert.False(t, cfg.APIEnabled, "explicit flat false must win over nested true")
	})

	t.Run("missing file yields zero-value config and no error", func(t *testing.T) {
		root := t.TempDir() // no .tmux-cli/setting.yaml
		cfg, err := LoadConfig(root)
		require.NoError(t, err)
		assert.Equal(t, Config{}, cfg)
	})
}

// TestParsePrivateKey covers the two interchangeable encodings loadPrivateKey
// accepts: PKCS#8 PEM (openssl/Go) and base64 of a raw Ed25519 key (the
// libsodium shape the web GenerateKeypairCommand emits, either 64-byte secret
// key or 32-byte seed). All three must yield a key that produces a signature
// verifiable by the matching public key.
func TestParsePrivateKey(t *testing.T) {
	pub, priv := testKeypair(t)
	seed := priv.Seed()

	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	cases := map[string][]byte{
		"pkcs8 pem":             pemBytes,
		"base64 secret key 64b": []byte(base64.StdEncoding.EncodeToString(priv)),
		"base64 seed 32b":       []byte(base64.StdEncoding.EncodeToString(seed)),
		// Trailing newline must be tolerated (a printed credential usually has one).
		"base64 with whitespace": []byte(base64.StdEncoding.EncodeToString(priv) + "\n"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parsePrivateKey(in)
			require.NoError(t, err)
			msg := []byte("verify me")
			require.True(t, ed25519.Verify(pub, msg, ed25519.Sign(got, msg)),
				"signature from parsed key must verify against the original public key")
		})
	}

	t.Run("garbage is rejected", func(t *testing.T) {
		_, err := parsePrivateKey([]byte("not a key at all !!!"))
		require.Error(t, err)
	})

	t.Run("wrong-length base64 is rejected", func(t *testing.T) {
		_, err := parsePrivateKey([]byte(base64.StdEncoding.EncodeToString([]byte("tooshort"))))
		require.Error(t, err)
	})
}
