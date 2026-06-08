package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/console/tmux-cli/internal/identity"
	"github.com/console/tmux-cli/internal/producer"
)

// newProducerClient is the construction seam the task-report tool uses to build
// a producer client. It defaults to producer.New and is overridden by tests.
// CRITICAL: it returns the concrete *producer.Client (never an interface) so the
// `client == nil` "reporting disabled" check in TaskReport is not defeated by the
// typed-nil-interface trap (a nil *Client boxed in an interface is != nil).
var newProducerClient = func(cfg producer.Config) *producer.Client {
	return producer.New(cfg)
}

// TaskReport files a structured task report to the backend synchronously. Unlike
// the daemon's fire-and-forget reportFailure (which coerces invalid enums), this
// agent-facing tool hard-rejects any missing/stub human field and any out-of-enum
// category or severity with NO coercion, collects SystemInfo server-side (an
// agent cannot spoof it), and returns the backend's {id,status}. It builds its own
// producer client because the MCP server is a separate process from the daemon and
// cannot reach the daemon's in-memory client.
func (s *Server) TaskReport(ctx context.Context, in TaskReportInput) (*TaskReportOutput, error) {
	// 1. All six human fields are required; name every empty/whitespace one.
	required := []struct {
		name, value string
	}{
		{"category", in.Category},
		{"severity", in.Severity},
		{"title", in.Title},
		{"description", in.Description},
		{"proposed_fix", in.ProposedFix},
		{"expected_green_state", in.ExpectedGreenState},
	}
	var missing []string
	for _, f := range required {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing required field(s): %s", ErrInvalidInput, strings.Join(missing, ", "))
	}

	// 2. proposed_fix must carry actionable content, not a contentless stub.
	if c := strings.ToLower(strings.TrimSpace(in.ProposedFix)); contentlessCorrections[c] {
		return nil, fmt.Errorf("%w: proposed_fix is a contentless stub (%q); provide a concrete remediation", ErrInvalidInput, in.ProposedFix)
	}

	// 3. category — reject (NO coercion), listing the allowed values.
	if !producer.ValidCategories[in.Category] {
		return nil, fmt.Errorf("%w: invalid category %q; allowed: %s", ErrInvalidInput, in.Category, sortedKeys(producer.ValidCategories))
	}

	// 4. severity — reject (NO coercion), listing the allowed values.
	if !producer.ValidSeverities[in.Severity] {
		return nil, fmt.Errorf("%w: invalid severity %q; allowed: %s", ErrInvalidInput, in.Severity, sortedKeys(producer.ValidSeverities))
	}

	// 5. Load the producer config (TWO returns — handle err before constructing).
	cfg, err := producer.LoadConfig(s.workingDir)
	if err != nil {
		return nil, err
	}

	// 6. Construct the client. A nil *Client means reporting is disabled.
	client := newProducerClient(cfg)
	if client == nil {
		return nil, fmt.Errorf("%w: task reporting is disabled (enable api in .tmux-cli/setting.yaml)", ErrInvalidInput)
	}

	// 7. Build the request; SystemInfo is collected server-side only.
	req := producer.TaskRequest{
		Category:           in.Category,
		Severity:           in.Severity,
		Title:              in.Title,
		Description:        in.Description,
		ProposedFix:        in.ProposedFix,
		ExpectedGreenState: in.ExpectedGreenState,
		SystemInfo:         identity.CollectSystemInfo(s.version),
		Payload:            in.Payload,
	}

	// 8. Submit synchronously.
	resp, err := client.SubmitTask(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("%w: task submission returned no response", ErrInvalidInput)
	}

	// 9. Return the backend's id/status.
	return &TaskReportOutput{ID: resp.ID.String(), Status: resp.Status}, nil
}

// TaskReportHandler is the MCP tool handler for the task-report operation.
func (s *Server) TaskReportHandler(ctx context.Context, req *sdkmcp.CallToolRequest, input TaskReportInput) (
	*sdkmcp.CallToolResult,
	TaskReportOutput,
	error,
) {
	output, err := s.TaskReport(ctx, input)
	if err != nil {
		return nil, TaskReportOutput{}, err
	}
	result, out := prependStaleWarning(*output)
	return result, out, nil
}

// sortedKeys renders a set's keys as a deterministic comma-joined string for
// error messages (so the listed allowed values are stable across calls).
func sortedKeys(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
