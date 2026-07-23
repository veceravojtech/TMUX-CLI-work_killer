package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/identity"
	"github.com/console/tmux-cli/internal/producer"
	"github.com/console/tmux-cli/internal/shipper"
	"github.com/spf13/cobra"
)

// Exit codes for the session command group (contract §3):
// 0 ok; 1 usage / no-session-resolved / unknown-session; 2 auth required;
// 3 other backend/transport error.
const (
	sessionExitOK      = 0
	sessionExitUsage   = 1
	sessionExitAuth    = 2
	sessionExitBackend = 3
)

// sessionCmd groups the session-telemetry review subcommands (P4).
var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Inspect shipped telemetry sessions (list, review)",
}

var sessionListJSON bool

var sessionListCmd = &cobra.Command{
	Use:          "list [PROJECT_PATH]",
	Short:        "List this device's telemetry sessions for the project",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		projectPath, err := resolveProjectPath(args)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "session list: %v\n", err)
			os.Exit(sessionExitUsage)
		}
		client, project, err := newSessionClient(projectPath)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "session list: %v\n", err)
			os.Exit(sessionExitBackend)
		}
		code := runTelemetrySessionList(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), client, project, sessionListJSON)
		if code != sessionExitOK {
			os.Exit(code)
		}
	},
}

var (
	sessionReviewProject string
	sessionReviewRefresh bool
	sessionReviewJSON    bool
)

var sessionReviewCmd = &cobra.Command{
	Use:          "review [SESSION_ID]",
	Short:        "Trigger + render the full-session review report",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		var pathArgs []string
		if sessionReviewProject != "" {
			pathArgs = []string{sessionReviewProject}
		}
		projectPath, err := resolveProjectPath(pathArgs)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "session review: %v\n", err)
			os.Exit(sessionExitUsage)
		}
		client, project, err := newSessionClient(projectPath)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "session review: %v\n", err)
			os.Exit(sessionExitBackend)
		}
		sessionID := ""
		if len(args) > 0 {
			sessionID = args[0]
		}
		code := runTelemetrySessionReview(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
			client, project, sessionID, sessionReviewRefresh, sessionReviewJSON)
		if code != sessionExitOK {
			os.Exit(code)
		}
	},
}

func init() {
	sessionListCmd.Flags().BoolVar(&sessionListJSON, "json", false, "print the raw sessions JSON")
	sessionReviewCmd.Flags().StringVar(&sessionReviewProject, "project", "", "project path (default: current directory)")
	sessionReviewCmd.Flags().BoolVar(&sessionReviewRefresh, "refresh", false, "force regeneration instead of returning a cached report")
	sessionReviewCmd.Flags().BoolVar(&sessionReviewJSON, "json", false, "print the raw report JSON")
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionReviewCmd)
	rootCmd.AddCommand(sessionCmd)
}

// newSessionClient wires the auth-backed shipper client for projectPath exactly
// like the ship loop does (auth.LoadAPIURL + identity.Fingerprint + auth.NewStore)
// and derives the project lane name.
func newSessionClient(projectPath string) (*shipper.Client, string, error) {
	store, err := auth.NewStore()
	if err != nil {
		return nil, "", err
	}
	apiURL := auth.LoadAPIURL(projectPath)
	return shipper.NewClient(apiURL, identity.Fingerprint(), store), deriveProject(projectPath), nil
}

// deriveProject resolves the project lane name the same way buildManifest
// (logs.go) does: producer.LoadConfig().Project override, else the basename of
// the project path. Duplicated because logs.go is frozen for this change.
func deriveProject(projectPath string) string {
	if cfg, err := producer.LoadConfig(projectPath); err == nil && cfg.Project != "" {
		return cfg.Project
	}
	if abs, err := filepath.Abs(projectPath); err == nil {
		return filepath.Base(abs)
	}
	return ""
}

// runSessionList fetches and renders the session table (or raw JSON). Factored
// out of the cobra wrapper so tests drive it against an httptest-backed client.
func runTelemetrySessionList(ctx context.Context, out, errOut io.Writer, client *shipper.Client, project string, jsonOut bool) int {
	sessions, err := client.ListSessions(ctx, project)
	if err != nil {
		return reportSessionError(errOut, err)
	}
	if jsonOut {
		return printSessionsJSON(out, errOut, sessions)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(out, "no sessions")
		return sessionExitOK
	}
	w := tabwriter.NewWriter(out, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION_ID\tPROJECT\tSTARTED\tWINDOWS\tEVENTS\tREVIEW")
	for _, s := range sessions {
		review := "-"
		if s.HasReview {
			review = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n",
			s.SessionID, s.Project, s.StartedAt, s.Windows, s.Events, review)
	}
	w.Flush()
	return sessionExitOK
}

// printSessionsJSON re-emits the sessions in the contract §2 wire shape
// {"sessions":[...]} — the struct tags are byte-exact with the wire, so this
// round-trips every pinned field.
func printSessionsJSON(out, errOut io.Writer, sessions []shipper.SessionSummary) int {
	payload := struct {
		Sessions []shipper.SessionSummary `json:"sessions"`
	}{Sessions: sessions}
	if err := json.NewEncoder(out).Encode(payload); err != nil {
		fmt.Fprintf(errOut, "session list: %v\n", err)
		return sessionExitBackend
	}
	return sessionExitOK
}

// runSessionReview resolves the target session (explicit arg wins, else newest
// for this fingerprint+project), triggers the review, and renders it.
func runTelemetrySessionReview(ctx context.Context, out, errOut io.Writer, client *shipper.Client,
	project, sessionID string, refresh, jsonOut bool) int {
	if sessionID == "" {
		sessions, err := client.ListSessions(ctx, project)
		if err != nil {
			return reportSessionError(errOut, err)
		}
		sessionID = newestSessionID(sessions)
		if sessionID == "" {
			fmt.Fprintln(errOut, "no session found — run a session first, or pass a session id")
			return sessionExitUsage
		}
	}
	review, err := client.PostReview(ctx, sessionID, refresh)
	if err != nil {
		if errors.Is(err, shipper.ErrUnknownSession) {
			fmt.Fprintf(errOut, "unknown session: %s\n", sessionID)
			return sessionExitUsage
		}
		return reportSessionError(errOut, err)
	}
	if jsonOut {
		out.Write(review.Raw)
		fmt.Fprintln(out)
		return sessionExitOK
	}
	renderReview(out, review)
	return sessionExitOK
}

// newestSessionID picks the session with the max started_at (RFC3339 UTC sorts
// lexicographically). The server already orders newest-first; scanning keeps the
// pick correct regardless of response order. Empty list → "".
func newestSessionID(sessions []shipper.SessionSummary) string {
	id, newest := "", ""
	for _, s := range sessions {
		if id == "" || s.StartedAt > newest {
			id, newest = s.SessionID, s.StartedAt
		}
	}
	return id
}

// reportSessionError maps a client error onto the contract exit codes and prints
// the operator-facing line.
func reportSessionError(errOut io.Writer, err error) int {
	if errors.Is(err, shipper.ErrLoginRequired) {
		fmt.Fprintln(errOut, "authentication required — run: tmux-cli login")
		return sessionExitAuth
	}
	fmt.Fprintf(errOut, "session: %v\n", err)
	return sessionExitBackend
}

// renderReview prints the human report sections (SUMMARY / AGENTS / PHASES vs
// fleet / ANOMALIES / SUGGESTIONS). A schema_version newer than this CLI
// understands renders summary + raw JSON only (contract §1 forward-compat).
func renderReview(out io.Writer, r shipper.Review) {
	fmt.Fprintf(out, "SESSION %s (%s) — generated %s\n\n", r.SessionID, r.Project, r.GeneratedAt)

	if r.SchemaVersion > shipper.ReviewSchemaVersion {
		fmt.Fprintf(out, "note: report schema_version %d is newer than this CLI supports (%d) — showing summary + raw JSON\n\n",
			r.SchemaVersion, shipper.ReviewSchemaVersion)
		renderReviewSummary(out, r.Summary)
		fmt.Fprintln(out)
		out.Write(r.Raw)
		fmt.Fprintln(out)
		return
	}

	renderReviewSummary(out, r.Summary)

	fmt.Fprintln(out, "\nAGENTS")
	if len(r.Agents) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		w := tabwriter.NewWriter(out, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "  WINDOW\tKIND\tEVENTS\tSEGMENTS\tFIRST\tLAST\tSUMMARY")
		for _, a := range r.Agents {
			fmt.Fprintf(w, "  %s\t%s\t%d\t%d\t%s\t%s\t%s\n",
				a.Window, a.Kind, a.Events, a.TranscriptSegments, a.FirstTS, a.LastTS, a.Summary)
		}
		w.Flush()
	}

	fmt.Fprintln(out, "\nPHASES vs fleet")
	if len(r.Phases) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		w := tabwriter.NewWriter(out, 2, 8, 2, ' ', 0)
		fmt.Fprintln(w, "  PHASE\tCOUNT\tP50s\tP90s\tFLEET_P50s\tFLEET_P90s\tOVER_CEILING")
		for _, p := range r.Phases {
			fmt.Fprintf(w, "  %s\t%d\t%.1f\t%.1f\t%s\t%s\t%v\n",
				p.Phase, p.Count, p.P50Sec, p.P90Sec,
				fleetCell(p.FleetP50Sec), fleetCell(p.FleetP90Sec), p.OverCeiling)
		}
		w.Flush()
	}

	fmt.Fprintln(out, "\nANOMALIES")
	if len(r.Anomalies) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		for _, a := range r.Anomalies {
			line := fmt.Sprintf("  [%s] %s", a.Severity, a.Type)
			if a.Window != "" {
				line += " " + a.Window
			}
			line += ": " + a.Detail
			if len(a.EvidenceEventIDs) > 0 {
				line += fmt.Sprintf(" (events: %v)", a.EvidenceEventIDs)
			}
			fmt.Fprintln(out, line)
		}
	}

	fmt.Fprintln(out, "\nSUGGESTIONS")
	if len(r.Suggestions) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		for _, s := range r.Suggestions {
			fmt.Fprintf(out, "  [%s] %s — %s\n", s.Priority, s.Title, s.Detail)
		}
	}
}

// renderReviewSummary prints the SUMMARY block shared by the normal and
// forward-compat render paths.
func renderReviewSummary(out io.Writer, s shipper.ReviewSummary) {
	fmt.Fprintln(out, "SUMMARY")
	fmt.Fprintf(out, "  started: %s  ended: %s  duration: %.0fs\n", s.StartedAt, s.EndedAt, s.DurationSec)
	fmt.Fprintf(out, "  windows: %d  events: %d  transcript segments: %d\n",
		s.Windows, s.EventsTotal, s.TranscriptSegments)
	fmt.Fprintf(out, "  goals: %d/%d done, %d failed  retries: %d  bounces: %d  escalations: %d\n",
		s.Goals.Done, s.Goals.Total, s.Goals.Failed, s.Retries, s.Bounces, s.Escalations)
}

// fleetCell renders a nullable fleet baseline: "-" when the cross-session
// sample was below the min-sample threshold (contract §1).
func fleetCell(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *v)
}
