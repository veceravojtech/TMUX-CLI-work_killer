package producer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient builds a *Client signing with a freshly generated key, pointed
// at srv. It returns the client plus the public key so a test can verify the
// recorded signature over the exact bytes the backend would reconstruct.
func newTestClient(t *testing.T, srv *httptest.Server) (*Client, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return newClient(srv.URL, priv, srv.Client()), pub
}

// writeTempFile writes content to a file in t.TempDir() and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestUploadArtifact_HappyPath(t *testing.T) {
	const fileContent = "hello artifact payload\n"
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotFileBytes   []byte
		gotFilename    string
		gotRole        string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")

		mediaType, params, err := mime.ParseMediaType(gotContentType)
		require.NoError(t, err)
		require.Equal(t, "multipart/form-data", mediaType)
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			b, _ := io.ReadAll(part)
			switch part.FormName() {
			case "file":
				gotFileBytes = b
				gotFilename = part.FileName()
			case "role":
				gotRole = string(b)
			}
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":7,"filename":"log.txt","sha256":"abc","size":23,"role":"log","mimeType":"text/plain","createdAt":"2026-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv)
	path := writeTempFile(t, "log.txt", fileContent)

	art, err := c.UploadArtifact(context.Background(), "42", path, "log")
	require.NoError(t, err)
	require.NotNil(t, art)
	assert.Equal(t, "7", art.ID.String())
	assert.Equal(t, "abc", art.Sha256)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/tasks/42/artifacts", gotPath)
	assert.True(t, strings.HasPrefix(gotContentType, "multipart/form-data; boundary="), "content type must carry the multipart boundary, got %q", gotContentType)
	assert.Equal(t, fileContent, string(gotFileBytes), "the file field must carry the raw file bytes")
	assert.Equal(t, "log.txt", gotFilename, "the multipart filename must be the base name")
	assert.Equal(t, "log", gotRole)
}

// TestUploadArtifact_SignsContentDigest pins the upload's signing contract to the
// deployed backend (goal-010): the client advertises X-Content-SHA256 = lowercase
// hex SHA-256 of the FILE bytes, and the signature verifies over X-Timestamp +
// that digest (NOT the empty body and NOT the multipart bytes). The multipart body
// is still sent in full so the backend can re-hash $_FILES and confirm the match.
func TestUploadArtifact_SignsContentDigest(t *testing.T) {
	const fileContent = "the artifact bytes whose digest is signed\n"
	var (
		gotBody   []byte
		gotSig    string
		gotTS     string
		gotDigest string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Signature")
		gotTS = r.Header.Get("X-Timestamp")
		gotDigest = r.Header.Get("X-Content-SHA256")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":1,"sha256":"x"}`)
	}))
	defer srv.Close()

	c, pub := newTestClient(t, srv)
	path := writeTempFile(t, "data.bin", fileContent)

	_, err := c.UploadArtifact(context.Background(), "9", path, "")
	require.NoError(t, err)

	// The digest header is the lowercase-hex SHA-256 of the file content.
	wantSum := sha256.Sum256([]byte(fileContent))
	wantDigest := hex.EncodeToString(wantSum[:])
	assert.Equal(t, wantDigest, gotDigest, "X-Content-SHA256 must be hex SHA-256 of the file bytes")

	sig, err := base64.StdEncoding.DecodeString(gotSig)
	require.NoError(t, err)
	// Verifies over timestamp+digest (the backend's reconstruction when the header is present)...
	assert.True(t, ed25519.Verify(pub, []byte(gotTS+gotDigest), sig),
		"X-Signature must verify over X-Timestamp + X-Content-SHA256 digest")
	// ...and must NOT verify over the multipart bytes (the old, broken signing).
	assert.False(t, ed25519.Verify(pub, []byte(gotTS+string(gotBody)), sig),
		"X-Signature must NOT cover the multipart body")
	// The multipart body is nonetheless sent in full.
	assert.Contains(t, string(gotBody), "Content-Disposition: form-data; name=\"file\"")
}

func TestUploadArtifact_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	path := writeTempFile(t, "big.bin", "x")
	_, err := c.UploadArtifact(context.Background(), "1", path, "")
	assert.ErrorIs(t, err, ErrArtifactTooLarge)
}

func TestUploadArtifact_UnsupportedType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnsupportedMediaType)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	path := writeTempFile(t, "bad.exe", "x")
	_, err := c.UploadArtifact(context.Background(), "1", path, "")
	assert.ErrorIs(t, err, ErrUnsupportedArtifactType)
}

func TestUploadArtifact_FileMissing(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	_, err := c.UploadArtifact(context.Background(), "1", filepath.Join(t.TempDir(), "nope.txt"), "")
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err), "a missing file must surface the os open error before any wire call")
	assert.False(t, called, "no request may be sent when the file cannot be opened")
}

func TestUploadArtifact_NilReceiver(t *testing.T) {
	var c *Client
	art, err := c.UploadArtifact(context.Background(), "1", "whatever", "")
	require.NoError(t, err)
	assert.Nil(t, art)
}

func TestListArtifacts_HappyPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"artifacts":[{"id":3,"filename":"a.log","sha256":"s","size":10,"role":"log","mimeType":"text/plain","createdAt":"t"}],"total":1}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)

	list, err := c.ListArtifacts(context.Background(), "7")
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/tasks/7/artifacts", gotPath)
	assert.Equal(t, 1, list.Total)
	require.Len(t, list.Artifacts, 1)
	assert.Equal(t, "3", list.Artifacts[0].ID.String())
	assert.Equal(t, "a.log", list.Artifacts[0].Filename)
	assert.Equal(t, "text/plain", list.Artifacts[0].MimeType)
}

func TestListArtifacts_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	_, err := c.ListArtifacts(context.Background(), "404")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestGetArtifact_HappyPath(t *testing.T) {
	const body = "downloaded artifact contents\n"
	sum := sha256.Sum256([]byte(body))
	hexSum := hex.EncodeToString(sum[:])
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("X-Artifact-Sha256", hexSum)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)

	dest := filepath.Join(t.TempDir(), "out.bin")
	res, err := c.GetArtifact(context.Background(), "7", "3", dest)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "/api/v1/tasks/7/artifacts/3", gotPath)
	assert.Equal(t, dest, res.Path)
	assert.Equal(t, hexSum, res.Sha256)
	assert.Equal(t, int64(len(body)), res.Size)

	written, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, string(written))
}

func TestGetArtifact_ChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Artifact-Sha256", "deadbeef") // does not match the body
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "actual bytes")
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)

	destDir := t.TempDir()
	dest := filepath.Join(destDir, "out.bin")
	_, err := c.GetArtifact(context.Background(), "7", "3", dest)
	assert.ErrorIs(t, err, ErrChecksumMismatch)

	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr), "a checksum mismatch must not leave a file at dest")

	// And no leftover temp .part file pollutes the directory.
	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the temp download must be discarded on mismatch")
}

func TestGetArtifact_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	dest := filepath.Join(t.TempDir(), "out.bin")
	_, err := c.GetArtifact(context.Background(), "7", "404", dest)
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

// TestDoSigned_JSONUnchanged guards the doSignedRaw extraction: doSigned must
// still set Content-Type: application/json and produce a signature that verifies
// over X-Timestamp + the exact JSON body — i.e. the JSON path is byte-identical
// to before the refactor.
func TestDoSigned_JSONUnchanged(t *testing.T) {
	const jsonBody = `{"hello":"world"}`
	var (
		gotCT   string
		gotBody []byte
		gotSig  string
		gotTS   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Signature")
		gotTS = r.Header.Get("X-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, pub := newTestClient(t, srv)

	resp, err := c.doSigned(context.Background(), http.MethodPost, "/api/v1/tasks", nil, []byte(jsonBody))
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, jsonBody, string(gotBody))
	sig, err := base64.StdEncoding.DecodeString(gotSig)
	require.NoError(t, err)
	assert.True(t, ed25519.Verify(pub, []byte(gotTS+string(gotBody)), sig))
	// The timestamp is a recent unix second (sanity-checks the signing input shape).
	ts, err := strconv.ParseInt(gotTS, 10, 64)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), time.Unix(ts, 0), 10*time.Second)
}

// TestDoSigned_NilBodyGET guards that a nil-body GET sets no Content-Type and
// signs over the timestamp alone — unchanged by the extraction.
func TestDoSigned_NilBodyGET(t *testing.T) {
	var hadCT bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadCT = r.Header["Content-Type"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	resp, err := c.doSigned(context.Background(), http.MethodGet, "/api/v1/tasks", nil, nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.False(t, hadCT, "a nil-body GET must not set Content-Type")
}
