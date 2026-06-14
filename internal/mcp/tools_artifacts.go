package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/producer"
)

// This file adds the task *artifact* tools (task-artifact-upload,
// task-artifact-list, task-artifact-get) that let an agent attach support
// material (logs, screenshots, specs, diffs) to a backend task and fetch it back
// — instead of cramming bytes into the bounded text payload or referencing local
// paths the consumer never has. Each is a thin wrapper over a producer.Client
// method, mirroring the task-query tool stack: validate-before-wire via
// ErrInvalidInput, taskClient(), prependStaleWarning.

// ArtifactView is the agent-facing projection of producer.Artifact (list rows).
type ArtifactView struct {
	ID        string `json:"id"`
	Filename  string `json:"filename,omitempty"`
	Sha256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size"`
	Role      string `json:"role,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func toArtifactView(a producer.Artifact) ArtifactView {
	return ArtifactView{
		ID:        a.ID.String(),
		Filename:  a.Filename,
		Sha256:    a.Sha256,
		Size:      a.Size,
		Role:      a.Role,
		MimeType:  a.MimeType,
		CreatedAt: a.CreatedAt,
	}
}

// ----------------------------------------------------------------------------
// task-artifact-upload
// ----------------------------------------------------------------------------

// TaskArtifactUploadInput defines the input schema for the task-artifact-upload
// tool. id and path are required; role is an optional free-form label.
type TaskArtifactUploadInput struct {
	ID   string `json:"id" jsonschema:"Backend task id to attach the artifact to; required"`
	Path string `json:"path" jsonschema:"Path to a readable local file to upload (its bytes are sent, never the path); required"`
	Role string `json:"role,omitempty" jsonschema:"Optional free-form role/label for the artifact (e.g. log, screenshot, diff, spec)"`
}

// TaskArtifactUploadOutput is the stored artifact's identity and integrity hash.
type TaskArtifactUploadOutput struct {
	ArtifactID string `json:"artifact_id"`
	Sha256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Filename   string `json:"filename"`
	Role       string `json:"role,omitempty"`
}

// TaskArtifactUpload uploads a local file as a multipart attachment on a task.
// All input is validated BEFORE the client is built (invalid input never hits
// the wire): id and path are required, and path must stat to a readable regular
// file.
func (s *Server) TaskArtifactUpload(ctx context.Context, in TaskArtifactUploadInput) (*TaskArtifactUploadOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, fmt.Errorf("%w: missing required field: path", ErrInvalidInput)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read file %q: %v", ErrInvalidInput, path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: path %q is a directory, not a file", ErrInvalidInput, path)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	art, err := client.UploadArtifact(ctx, id, path, strings.TrimSpace(in.Role))
	if err != nil {
		return nil, err
	}
	return &TaskArtifactUploadOutput{
		ArtifactID: art.ID.String(),
		Sha256:     art.Sha256,
		Size:       art.Size,
		Filename:   art.Filename,
		Role:       art.Role,
	}, nil
}

// TaskArtifactUploadHandler is the MCP tool handler for task-artifact-upload.
func (s *Server) TaskArtifactUploadHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskArtifactUploadInput) (
	*sdkmcp.CallToolResult, TaskArtifactUploadOutput, error,
) {
	output, err := s.TaskArtifactUpload(ctx, input)
	if err != nil {
		return nil, TaskArtifactUploadOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-artifact-list
// ----------------------------------------------------------------------------

// TaskArtifactListInput defines the input schema for the task-artifact-list tool.
type TaskArtifactListInput struct {
	ID string `json:"id" jsonschema:"Backend task id whose artifacts to list; required"`
}

// TaskArtifactListOutput is the task's artifacts plus their total.
type TaskArtifactListOutput struct {
	Artifacts []ArtifactView `json:"artifacts"`
	Total     int            `json:"total"`
}

// TaskArtifactList lists the artifacts attached to a task. id is validated before
// the client is built.
func (s *Server) TaskArtifactList(ctx context.Context, in TaskArtifactListInput) (*TaskArtifactListOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	list, err := client.ListArtifacts(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &TaskArtifactListOutput{Total: list.Total}
	out.Artifacts = make([]ArtifactView, 0, len(list.Artifacts))
	for _, a := range list.Artifacts {
		out.Artifacts = append(out.Artifacts, toArtifactView(a))
	}
	return out, nil
}

// TaskArtifactListHandler is the MCP tool handler for task-artifact-list.
func (s *Server) TaskArtifactListHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskArtifactListInput) (
	*sdkmcp.CallToolResult, TaskArtifactListOutput, error,
) {
	output, err := s.TaskArtifactList(ctx, input)
	if err != nil {
		return nil, TaskArtifactListOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// ----------------------------------------------------------------------------
// task-artifact-get
// ----------------------------------------------------------------------------

// TaskArtifactGetInput defines the input schema for the task-artifact-get tool.
// id, artifact_id and dest are required; sha256 is an optional expected hash that
// is cross-checked against the downloaded content.
type TaskArtifactGetInput struct {
	ID         string `json:"id" jsonschema:"Backend task id that owns the artifact; required"`
	ArtifactID string `json:"artifact_id" jsonschema:"Artifact id to download; required"`
	Dest       string `json:"dest" jsonschema:"Local destination file path to write the bytes to; required (its parent directory must exist and be writable)"`
	Sha256     string `json:"sha256,omitempty" jsonschema:"Optional expected sha256 (e.g. the value upload/list returned); cross-checked against the downloaded content, mismatch errors"`
}

// TaskArtifactGetOutput reports where the verified bytes were written, their
// sha256 and size, and that verification passed.
type TaskArtifactGetOutput struct {
	Path     string `json:"path"`
	Sha256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Verified bool   `json:"verified"`
}

// TaskArtifactGet downloads an artifact to dest, verifying the written content's
// sha256 against the backend-advertised header (in the producer method) and, when
// provided, the caller's expected sha256. All input is validated BEFORE the
// client is built: id, artifact_id and dest are required, and dest's parent
// directory must exist (the temp download lands there before an atomic rename).
func (s *Server) TaskArtifactGet(ctx context.Context, in TaskArtifactGetInput) (*TaskArtifactGetOutput, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, fmt.Errorf("%w: missing required field: id", ErrInvalidInput)
	}
	artifactID := strings.TrimSpace(in.ArtifactID)
	if artifactID == "" {
		return nil, fmt.Errorf("%w: missing required field: artifact_id", ErrInvalidInput)
	}
	dest := strings.TrimSpace(in.Dest)
	if dest == "" {
		return nil, fmt.Errorf("%w: missing required field: dest", ErrInvalidInput)
	}
	destDir := filepath.Dir(dest)
	if info, err := os.Stat(destDir); err != nil {
		return nil, fmt.Errorf("%w: destination directory not accessible: %v", ErrInvalidInput, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%w: destination parent %q is not a directory", ErrInvalidInput, destDir)
	}

	client, err := s.taskClient()
	if err != nil {
		return nil, err
	}

	res, err := client.GetArtifact(ctx, id, artifactID, dest)
	if err != nil {
		return nil, err
	}
	// Cross-check the caller-supplied expected hash against the verified content.
	if expected := strings.TrimSpace(in.Sha256); expected != "" && expected != res.Sha256 {
		return nil, fmt.Errorf("%w: expected %s, downloaded %s", producer.ErrChecksumMismatch, expected, res.Sha256)
	}
	return &TaskArtifactGetOutput{Path: res.Path, Sha256: res.Sha256, Size: res.Size, Verified: true}, nil
}

// TaskArtifactGetHandler is the MCP tool handler for task-artifact-get.
func (s *Server) TaskArtifactGetHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskArtifactGetInput) (
	*sdkmcp.CallToolResult, TaskArtifactGetOutput, error,
) {
	output, err := s.TaskArtifactGet(ctx, input)
	if err != nil {
		return nil, TaskArtifactGetOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}
