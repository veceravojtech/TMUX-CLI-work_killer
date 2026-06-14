package mcp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
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
	"sync"
	"testing"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// artifactBackend is a tiny stateful in-memory backend keyed by task id: it
// accepts a multipart upload, lists, and serves the stored bytes back with the
// matching X-Artifact-Sha256 header — enough to prove the upload->list->get
// round-trip preserves the sha256 without a real backend.
type artifactBackend struct {
	mu    sync.Mutex
	store map[string][]storedArtifact // taskID -> artifacts
	seq   int
}

type storedArtifact struct {
	id       string
	filename string
	role     string
	sha256   string
	size     int64
	bytes    []byte
}

func newArtifactBackend() *artifactBackend {
	return &artifactBackend{store: map[string][]storedArtifact{}}
}

// withArtifactServer stands up the stateful backend, hooks newProducerClient to a
// signed test client pointed at it, and returns a Server.
func withArtifactServer(t *testing.T, be *artifactBackend) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(be.handle))
	t.Cleanup(srv.Close)

	withReportHook(t, func(producer.Config) *producer.Client {
		return producer.NewClientForTest(srv.URL, priv, srv.Client())
	})
	return newReportServer(t)
}

func (be *artifactBackend) handle(w http.ResponseWriter, r *http.Request) {
	// Paths: /api/v1/tasks/{id}/artifacts  and  /api/v1/tasks/{id}/artifacts/{aid}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/"), "/")
	// parts: [id, "artifacts"] or [id, "artifacts", aid]
	taskID := parts[0]

	be.mu.Lock()
	defer be.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && len(parts) == 2:
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var fileBytes []byte
		var filename, role string
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			b, _ := io.ReadAll(part)
			switch part.FormName() {
			case "file":
				fileBytes = b
				filename = part.FileName()
			case "role":
				role = string(b)
			}
		}
		sum := sha256.Sum256(fileBytes)
		hexSum := hex.EncodeToString(sum[:])
		be.seq++
		aid := strconv.Itoa(be.seq)
		be.store[taskID] = append(be.store[taskID], storedArtifact{
			id: aid, filename: filename, role: role, sha256: hexSum, size: int64(len(fileBytes)), bytes: fileBytes,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":`+aid+`,"filename":"`+filename+`","sha256":"`+hexSum+`","size":`+strconv.FormatInt(int64(len(fileBytes)), 10)+`,"role":"`+role+`","mimeType":"application/octet-stream","createdAt":"2026-01-01T00:00:00Z"}`)

	case r.Method == http.MethodGet && len(parts) == 2:
		arts := be.store[taskID]
		var b strings.Builder
		b.WriteString(`{"artifacts":[`)
		for i, a := range arts {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"id":` + a.id + `,"filename":"` + a.filename + `","sha256":"` + a.sha256 + `","size":` + strconv.FormatInt(a.size, 10) + `,"role":"` + a.role + `","mimeType":"application/octet-stream","createdAt":"t"}`)
		}
		b.WriteString(`],"total":` + strconv.Itoa(len(arts)) + `}`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, b.String())

	case r.Method == http.MethodGet && len(parts) == 3:
		aid := parts[2]
		for _, a := range be.store[taskID] {
			if a.id == aid {
				w.Header().Set("X-Artifact-Sha256", a.sha256)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(a.bytes)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestTaskArtifactRoundTrip(t *testing.T) {
	be := newArtifactBackend()
	s := withArtifactServer(t, be)

	const content = "round-trip artifact bytes — logs and such\n"
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])

	src := filepath.Join(t.TempDir(), "evidence.log")
	require.NoError(t, os.WriteFile(src, []byte(content), 0o644))

	// 1. upload
	up, err := s.TaskArtifactUpload(context.Background(), TaskArtifactUploadInput{ID: "55", Path: src, Role: "log"})
	require.NoError(t, err)
	require.NotNil(t, up)
	assert.Equal(t, want, up.Sha256)
	assert.Equal(t, "evidence.log", up.Filename)
	assert.Equal(t, "log", up.Role)
	aid := up.ArtifactID

	// 2. list shows it with the same sha256
	list, err := s.TaskArtifactList(context.Background(), TaskArtifactListInput{ID: "55"})
	require.NoError(t, err)
	require.Len(t, list.Artifacts, 1)
	assert.Equal(t, aid, list.Artifacts[0].ID)
	assert.Equal(t, want, list.Artifacts[0].Sha256)
	assert.Equal(t, "application/octet-stream", list.Artifacts[0].MimeType)

	// 3. get writes the bytes and verifies the sha256 matches
	dest := filepath.Join(t.TempDir(), "out.log")
	got, err := s.TaskArtifactGet(context.Background(), TaskArtifactGetInput{ID: "55", ArtifactID: aid, Dest: dest})
	require.NoError(t, err)
	assert.True(t, got.Verified)
	assert.Equal(t, want, got.Sha256)
	written, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, string(written))
	// The downloaded file's sha256 equals the uploaded file's sha256.
	got2 := sha256.Sum256(written)
	assert.Equal(t, want, hex.EncodeToString(got2[:]))

	// 4. passing the correct expected sha256 succeeds; a wrong one errors.
	dest2 := filepath.Join(t.TempDir(), "out2.log")
	_, err = s.TaskArtifactGet(context.Background(), TaskArtifactGetInput{ID: "55", ArtifactID: aid, Dest: dest2, Sha256: want})
	require.NoError(t, err)

	dest3 := filepath.Join(t.TempDir(), "out3.log")
	_, err = s.TaskArtifactGet(context.Background(), TaskArtifactGetInput{ID: "55", ArtifactID: aid, Dest: dest3, Sha256: "deadbeef"})
	assert.ErrorIs(t, err, producer.ErrChecksumMismatch)
}

func TestTaskArtifactUpload_RejectsBlankIdOrPath(t *testing.T) {
	cases := []struct {
		name string
		in   TaskArtifactUploadInput
		want string
	}{
		{"blank id", TaskArtifactUploadInput{ID: "  ", Path: "x"}, "id"},
		{"blank path", TaskArtifactUploadInput{ID: "1", Path: ""}, "path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t)) // must reject before building a client
			s := newReportServer(t)
			out, err := s.TaskArtifactUpload(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTaskArtifactUpload_RejectsMissingFile(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskArtifactUpload(context.Background(), TaskArtifactUploadInput{
		ID: "1", Path: filepath.Join(t.TempDir(), "does-not-exist.log"),
	})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestTaskArtifactUpload_RejectsDirectory(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskArtifactUpload(context.Background(), TaskArtifactUploadInput{ID: "1", Path: t.TempDir()})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestTaskArtifactList_RejectsBlankId(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskArtifactList(context.Background(), TaskArtifactListInput{ID: "  "})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
}

func TestTaskArtifactGet_RejectsBlankFields(t *testing.T) {
	cases := []struct {
		name string
		in   TaskArtifactGetInput
		want string
	}{
		{"blank id", TaskArtifactGetInput{ID: "", ArtifactID: "a", Dest: "/tmp/x"}, "id"},
		{"blank artifact_id", TaskArtifactGetInput{ID: "1", ArtifactID: " ", Dest: "/tmp/x"}, "artifact_id"},
		{"blank dest", TaskArtifactGetInput{ID: "1", ArtifactID: "a", Dest: ""}, "dest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReportHook(t, failIfCalled(t))
			s := newReportServer(t)
			out, err := s.TaskArtifactGet(context.Background(), tc.in)
			assert.Nil(t, out)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTaskArtifactGet_RejectsMissingDestDir(t *testing.T) {
	withReportHook(t, failIfCalled(t))
	s := newReportServer(t)
	out, err := s.TaskArtifactGet(context.Background(), TaskArtifactGetInput{
		ID: "1", ArtifactID: "a", Dest: filepath.Join(t.TempDir(), "no-such-dir", "out.bin"),
	})
	assert.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestTaskArtifact_ReportingDisabled(t *testing.T) {
	// taskClient() returns nil -> actionable error when reporting is disabled.
	src := filepath.Join(t.TempDir(), "f.log")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))

	t.Run("upload", func(t *testing.T) {
		withReportHook(t, func(producer.Config) *producer.Client { return nil })
		s := newReportServer(t)
		_, err := s.TaskArtifactUpload(context.Background(), TaskArtifactUploadInput{ID: "1", Path: src})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidInput)
	})
	t.Run("list", func(t *testing.T) {
		withReportHook(t, func(producer.Config) *producer.Client { return nil })
		s := newReportServer(t)
		_, err := s.TaskArtifactList(context.Background(), TaskArtifactListInput{ID: "1"})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidInput)
	})
	t.Run("get", func(t *testing.T) {
		withReportHook(t, func(producer.Config) *producer.Client { return nil })
		s := newReportServer(t)
		_, err := s.TaskArtifactGet(context.Background(), TaskArtifactGetInput{ID: "1", ArtifactID: "a", Dest: filepath.Join(t.TempDir(), "out.bin")})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidInput)
	})
}
