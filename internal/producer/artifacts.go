package producer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Artifact-path sentinel errors so the MCP artifact tools can map an HTTP status
// (or a failed integrity check) to a precise, user-facing message instead of a
// bare "status N": 413 the file is over the backend's size limit, 415 the type
// is not allowed, and a checksum mismatch when the downloaded bytes do not hash
// to the advertised value.
var (
	ErrArtifactTooLarge        = errors.New("producer: artifact exceeds backend size limit")
	ErrUnsupportedArtifactType = errors.New("producer: artifact type not allowed")
	ErrChecksumMismatch        = errors.New("producer: artifact checksum mismatch")
)

// Artifact is the serialized metadata the backend returns for one task artifact
// (from the upload and list endpoints). IDs use FlexibleID because the backend
// emits them as JSON numbers; tags are camelCase to match the backend DTO.
type Artifact struct {
	ID        FlexibleID `json:"id"`
	Filename  string     `json:"filename"`
	Sha256    string     `json:"sha256"`
	Size      int64      `json:"size"`
	Role      string     `json:"role"`
	MimeType  string     `json:"mimeType"`
	CreatedAt string     `json:"createdAt"`
}

// ArtifactList is the GET /api/v1/tasks/{id}/artifacts reply: the artifacts plus
// their total.
type ArtifactList struct {
	Artifacts []Artifact `json:"artifacts"`
	Total     int        `json:"total"`
}

// DownloadResult is the verified outcome of GetArtifact: the path the bytes were
// written to, their computed sha256 (hex), and their size in bytes.
type DownloadResult struct {
	Path   string
	Sha256 string
	Size   int64
}

// UploadArtifact uploads filePath as a multipart attachment on task taskID,
// optionally tagged with role. A nil receiver is a no-op returning (nil, nil),
// matching the other producer methods.
//
// Signature note: PHP parses a multipart/form-data POST into $_FILES and leaves
// php://input empty, so the backend cannot reconstruct the raw body to verify a
// signature over it (signing the multipart bytes is the cause of the upload 401).
// Instead the backend's authenticator binds the signature to an X-Content-SHA256
// digest header: this client computes the lowercase-hex SHA-256 of the FILE bytes
// (the exact bytes the controller re-hashes from $_FILES), sends it as
// X-Content-SHA256, and signs over timestamp+digest (signBody=digest). The
// multipart body is still sent in full so the controller can read $_FILES and
// confirm the bytes hash to the advertised digest. The Content-Type carries the
// random boundary via w.FormDataContentType(), which is why this cannot reuse
// doSigned's hardcoded application/json (hence doSignedRaw). It maps 413 ->
// ErrArtifactTooLarge, 415 -> ErrUnsupportedArtifactType, 404 -> ErrTaskNotFound.
func (c *Client) UploadArtifact(ctx context.Context, taskID, filePath, role string) (*Artifact, error) {
	if c == nil {
		return nil, nil
	}
	// Read the file fully so its content can be hashed (for the X-Content-SHA256
	// digest) and written into the multipart body from the same bytes — the digest
	// MUST match what the backend re-hashes from the received file.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:]) // lowercase hex, matching PHP hash('sha256', ...)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	// role is free-form (not enum-gated); send the field only when non-empty.
	if role != "" {
		if err := w.WriteField("role", role); err != nil {
			return nil, err
		}
	}
	// Close BEFORE reading buf.Bytes(): this flushes the closing boundary so the
	// buffered bytes are the complete, well-formed multipart body.
	if err := w.Close(); err != nil {
		return nil, err
	}

	// Sign over timestamp+digest and advertise the digest header; the backend
	// verifies the signature against the header and re-hashes the uploaded bytes to
	// confirm they match (see the signature note above).
	resp, err := c.doSignedRaw(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(taskID)+"/artifacts", nil, buf.Bytes(), w.FormDataContentType(), []byte(digest), map[string]string{"X-Content-SHA256": digest})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "artifact upload", map[int]error{
		http.StatusNotFound:              ErrTaskNotFound,
		http.StatusRequestEntityTooLarge: ErrArtifactTooLarge,        // 413
		http.StatusUnsupportedMediaType:  ErrUnsupportedArtifactType, // 415
	}); err != nil {
		return nil, err
	}
	var out Artifact
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListArtifacts fetches the artifacts attached to task taskID. It maps 404 ->
// ErrTaskNotFound. A nil receiver is a no-op returning (nil, nil).
func (c *Client) ListArtifacts(ctx context.Context, taskID string) (*ArtifactList, error) {
	if c == nil {
		return nil, nil
	}
	resp, err := c.doSigned(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/artifacts", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "artifact list", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}
	var out ArtifactList
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetArtifact downloads artifact artifactID of task taskID to destPath and
// verifies its integrity. It maps 404 -> ErrTaskNotFound. A nil receiver is a
// no-op returning (nil, nil).
//
// The binary body is NOT routed through decode: it is streamed via
// io.TeeReader(resp.Body, sha256) into a temp file in destPath's directory while
// hashing. If the backend advertises X-Artifact-Sha256 and it does not match the
// computed hash, the temp file is discarded and ErrChecksumMismatch is returned
// — so a mismatch or partial download never leaves a truncated file at destPath.
// On success the temp file is os.Rename'd into place (atomic on the same dir).
func (c *Client) GetArtifact(ctx context.Context, taskID, artifactID, destPath string) (*DownloadResult, error) {
	if c == nil {
		return nil, nil
	}
	resp, err := c.doSigned(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(taskID)+"/artifacts/"+url.PathEscape(artifactID), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect2xx(resp, "artifact get", map[int]error{http.StatusNotFound: ErrTaskNotFound}); err != nil {
		return nil, err
	}

	// Stream to a temp file in the destination's directory so os.Rename below is
	// an atomic same-filesystem move; a discarded temp never pollutes destPath.
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".artifact-*.part")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		tmp.Close()
		if !renamed {
			os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	size, err := io.Copy(tmp, io.TeeReader(resp.Body, hasher))
	if err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	computed := hex.EncodeToString(hasher.Sum(nil))

	if advertised := resp.Header.Get("X-Artifact-Sha256"); advertised != "" && advertised != computed {
		return nil, fmt.Errorf("%w: server advertised %s, downloaded %s", ErrChecksumMismatch, advertised, computed)
	}

	if err := os.Rename(tmpName, destPath); err != nil {
		return nil, err
	}
	renamed = true
	return &DownloadResult{Path: destPath, Sha256: computed, Size: size}, nil
}
