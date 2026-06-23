package taskvisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/console/tmux-cli/internal/tmux"
)

const (
	ansiClearScreen = "\033[2J\033[H"
	ansiReset       = "\033[0m"
	ansiGreen       = "\033[32m"
	ansiRed         = "\033[31m"
	ansiYellow      = "\033[33m"
	ansiDim         = "\033[2m"
	ansiBold        = "\033[1m"
	ansiCyan        = "\033[36m"
)

func formatElapsed(startedAt, finishedAt string) string {
	if startedAt == "" {
		return "—"
	}

	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return "—"
	}

	var elapsed time.Duration
	if finishedAt == "" {
		elapsed = time.Since(start)
	} else {
		finish, err := time.Parse(time.RFC3339, finishedAt)
		if err != nil {
			return "—"
		}
		elapsed = finish.Sub(start)
	}

	totalSeconds := int(math.Round(elapsed.Seconds()))
	if totalSeconds < 0 {
		totalSeconds = 0
	}

	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func goalStatusColor(status string) string {
	switch status {
	case GoalDone:
		return ansiGreen
	case GoalFailed:
		return ansiRed
	case GoalRunning:
		return ansiYellow
	case GoalPending:
		return ansiDim
	default:
		return ""
	}
}

func (d *Daemon) renderDashboard(w io.Writer) error {
	if _, err := fmt.Fprint(w, ansiClearScreen); err != nil {
		return err
	}

	if d.mode == modeIdle {
		if _, err := fmt.Fprintf(w, "%s%sTASKVISOR%s  %sIDLE%s — waiting for start signal  %s%s%s\n",
			ansiBold, ansiCyan, ansiReset, ansiDim, ansiReset, ansiDim, d.vcsRevision, ansiReset); err != nil {
			return err
		}
		// A daemon-level halt (P3 wall-clock ceiling) surfaces its reason as a loud
		// bold-red banner so a halted run is unmistakable on the idle surface.
		if d.haltReason != "" {
			if _, err := fmt.Fprintf(w, "%s%s%s%s\n", ansiBold, ansiRed, d.haltReason, ansiReset); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Poll interval: %s\n", d.pollInterval); err != nil {
			return err
		}
		return nil
	}

	phaseName := "NONE"
	switch d.runtime(d.currentGoal).phase {
	case phaseSupervising:
		phaseName = "SUPERVISING"
	case phaseValidating:
		phaseName = "VALIDATING"
	}

	if _, err := fmt.Fprintf(w, "%s%sTASKVISOR%s  %s%sACTIVE%s / %s%s%s  %s%s%s\n",
		ansiBold, ansiCyan, ansiReset,
		ansiBold, ansiGreen, ansiReset,
		ansiBold, phaseName, ansiReset,
		ansiDim, d.vcsRevision, ansiReset); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Poll: %s  Dispatch timeout: %s  Validate timeout: %s\n",
		d.pollInterval, d.dispatchTimeout, d.validateTimeout); err != nil {
		return err
	}
	if d.staleBanner != "" {
		if _, err := fmt.Fprintf(w, "%s%s%s%s\n", ansiBold, ansiYellow, d.staleBanner, ansiReset); err != nil {
			return err
		}
	}
	if d.specRepairs > 0 {
		if _, err := fmt.Fprintf(w, "spec repairs: %d\n", d.specRepairs); err != nil {
			return err
		}
	}
	if d.depWarningCount > 0 {
		if _, err := fmt.Fprintf(w, "%sdep warnings: %d%s\n", ansiYellow, d.depWarningCount, ansiReset); err != nil {
			return err
		}
	}
	if d.stackGateSkips > 0 {
		if _, err := fmt.Fprintf(w, "%sstack-gated: %d%s\n", ansiYellow, d.stackGateSkips, ansiReset); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "\n"); err != nil {
		return err
	}

	goals, err := LoadGoals(d.workDir)
	if err != nil || goals == nil || len(goals.Goals) == 0 {
		return nil
	}

	// Single-pass scan: the Prio column is shown only when some goal carries a
	// non-default priority. All-zero (the common case) renders byte-identically
	// to the pre-Prio dashboard.
	anyPriority := false
	for _, g := range goals.Goals {
		if g.Priority != 0 {
			anyPriority = true
			break
		}
	}

	if anyPriority {
		if _, err := fmt.Fprintf(w, "%s%-4s  %-12s  %-30s  %-4s  %-10s  %-8s  %s%s\n",
			ansiDim, "#", "ID", "Description", "Prio", "Status", "Retries", "Elapsed", ansiReset); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "%s%-4s  %-12s  %-30s  %-10s  %-8s  %s%s\n",
			ansiDim, "#", "ID", "Description", "Status", "Retries", "Elapsed", ansiReset); err != nil {
			return err
		}
	}

	for i, g := range goals.Goals {
		color := goalStatusColor(g.Status)
		elapsed := formatElapsed(g.StartedAt, g.FinishedAt)

		desc := g.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}

		current := " "
		if g.ID == goals.CurrentGoal {
			current = ">"
		}

		if anyPriority {
			if _, err := fmt.Fprintf(w, "%s%s%-3d  %-12s  %-30s  %-4d  %-10s  %d/%-6d  %s%s\n",
				color, current, i+1, g.ID, desc, g.Priority, g.Status, g.Retries, g.MaxRetries, elapsed, ansiReset); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "%s%s%-3d  %-12s  %-30s  %-10s  %d/%-6d  %s%s\n",
				color, current, i+1, g.ID, desc, g.Status, g.Retries, g.MaxRetries, elapsed, ansiReset); err != nil {
				return err
			}
		}
	}

	return nil
}

// ── Read-only dashboard board renderer ─────────────────────────────────────────
//
// RenderBoard/WatchBoard are daemon-INDEPENDENT: they read only atomically-renamed
// files (goals.yaml / task-goals.yaml / taskvisor.log), classify live tmux windows
// via an injected tmux.TmuxExecutor, and make at most one short-timeout best-effort
// API call (cached). The ONLY write performed is the dashboard-private queue cache
// (.tmux-cli/dashboard-queue-cache.json) via temp-file + os.Rename. Every missing
// or disabled source degrades to an inline placeholder — never an error, never a
// panic. See goal-025 spec (research/execute-025-1-dashboard-board-renderer.md).

// queueCounts is the aggregated, cacheable view of the backend task queue. It is
// the JSON shape of .tmux-cli/dashboard-queue-cache.json. Total is the backend's
// full filtered total; Sampled is len(Tasks) from the single ListTasks page;
// ByStatus/BySeverity are client-side aggregates of that page. SampledAt stamps
// the snapshot for the cache-age annotation.
type queueCounts struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"by_status"`
	BySeverity map[string]int `json:"by_severity"`
	Sampled    int            `json:"sampled"`
	SampledAt  string         `json:"sampled_at"`
}

// RenderBoard writes ONE read-only snapshot of the board to w. projectRoot is the
// base project dir (the dir containing .tmux-cli); exec discovers the session
// (TMUX_CLI_PROJECT_PATH env marker) and censuses worker windows — pass nil to skip
// the census. Returns a non-nil error ONLY on a fatal write to w; missing/disabled
// sources degrade to inline placeholders. Emits NO ansiClearScreen prefix (pipe/
// test friendly); the caller clears. The whole board is built in a bytes.Buffer and
// flushed with a single w.Write so a mid-render write error can never leave a
// half-painted screen.
func RenderBoard(w io.Writer, projectRoot string, exec tmux.TmuxExecutor) error {
	var buf bytes.Buffer

	// Section 5's log read also feeds section 1's cycle column — do it once.
	lastTransition, lastCounters, cycleByGoal := tailLog(projectRoot)

	fmt.Fprintf(&buf, "%s%sTASKVISOR BOARD%s  %s%s%s\n\n",
		ansiBold, ansiCyan, ansiReset, ansiDim, time.Now().Format("2006-01-02 15:04:05"), ansiReset)

	renderGoalsSection(&buf, projectRoot, cycleByGoal)
	buf.WriteByte('\n')
	renderMappingsSection(&buf, projectRoot)
	buf.WriteByte('\n')
	renderQueueSection(&buf, projectRoot)
	buf.WriteByte('\n')
	renderCensusSection(&buf, exec, projectRoot)
	buf.WriteByte('\n')
	renderLogSection(&buf, projectRoot, lastTransition, lastCounters)

	_, err := w.Write(buf.Bytes())
	return err
}

// WatchBoard paints the board (ansiClearScreen + RenderBoard) ONCE immediately,
// then every interval, until ctx is cancelled. interval <= 0 ⇒ 5s. ZERO input
// handling — NOT a TUI. Returns nil on ctx cancellation; a write error from w
// aborts the loop and is returned.
func WatchBoard(ctx context.Context, w io.Writer, projectRoot string, exec tmux.TmuxExecutor, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := paintBoard(w, projectRoot, exec); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := paintBoard(w, projectRoot, exec); err != nil {
				return err
			}
		}
	}
}

// paintBoard clears the screen then renders one board snapshot. Shared by
// WatchBoard and (d *Daemon).renderBoard so the foreground daemon repaint and the
// standalone watch loop are byte-identical.
func paintBoard(w io.Writer, projectRoot string, exec tmux.TmuxExecutor) error {
	if _, err := io.WriteString(w, ansiClearScreen); err != nil {
		return err
	}
	return RenderBoard(w, projectRoot, exec)
}

// renderBoard is the daemon's thin foreground repaint, replacing the four
// renderDashboard(os.Stdout) call-sites. A board render error is logged (to
// taskvisor.log) and swallowed so a flaky tmux/API/file read can never escape into
// the poll loop.
//
// It passes nil for the census executor ON PURPOSE: the worker-window census is the
// one board section that issues live tmux calls (FindSessionByEnvironment +
// ListWindows), and this repaint fires on EVERY poll tick / activate / deactivate /
// completion — the daemon's fragile, single-threaded loop, which Prior Learnings
// (and setupDaemon's "byte-identical tick / no extra executor calls" contract)
// keep free of incidental executor traffic so a blocking/flaky tmux call can never
// stall dispatch. The census is fully delivered by the STANDALONE surface
// (`tmux-cli taskvisor dashboard`, execute-2): that path calls RenderBoard/
// WatchBoard with a real executor and runs even with the daemon DOWN — the feature's
// stated primary use. The daemon already owns/knows its own windows, so census in
// the daemon's own foreground view is the lowest-value section; a nil executor
// degrades it to the inline "(no tmux session — census unavailable)" placeholder,
// exactly as the contract specifies for exec==nil. The other four sections render
// in full at every call-site.
func (d *Daemon) renderBoard() {
	if err := paintBoard(os.Stdout, d.workDir, nil); err != nil {
		log.Printf("board render error: %v", err)
	}
}

// ── Section 1: goals ───────────────────────────────────────────────────────────

// collectGoals reads goals.yaml read-only and classifies the result into a render
// note: "" (render the table), or one of the three placeholder strings.
func collectGoals(projectRoot string) ([]Goal, string) {
	gf, err := LoadGoals(projectRoot)
	if err != nil {
		return nil, "(goals.yaml unreadable)"
	}
	if gf == nil {
		return nil, "(no goals.yaml — daemon has not run)"
	}
	if len(gf.Goals) == 0 {
		return nil, "(no goals)"
	}
	return gf.Goals, ""
}

func renderGoalsSection(buf *bytes.Buffer, projectRoot string, cycleByGoal map[string]string) {
	fmt.Fprintf(buf, "%sGOALS%s\n", ansiBold, ansiReset)
	goals, note := collectGoals(projectRoot)
	if note != "" {
		fmt.Fprintf(buf, "  %s%s%s\n", ansiDim, note, ansiReset)
		return
	}
	// Render rows in effective dispatch/run order: running first, then the pending
	// bucket sorted by Priority DESC (ties: dependency-satisfied/runnable first,
	// then id asc), then terminal goals. This mirrors the daemon's NextPendingGoal/
	// RunnableCandidates selection (Priority > comparator) so the board reads top-to-
	// bottom as execution order. Display-only: reorders the local collectGoals slice,
	// which is discarded after this render — goals.yaml is never reordered.
	rank := func(s string) int {
		switch s {
		case GoalRunning:
			return 0
		case GoalPending:
			return 1
		default: // done/failed/blocked
			return 2
		}
	}
	sort.SliceStable(goals, func(i, j int) bool {
		a, b := &goals[i], &goals[j]
		if ra, rb := rank(a.Status), rank(b.Status); ra != rb {
			return ra < rb
		}
		if a.Status == GoalPending { // dispatch order within the pending bucket
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			as, bs := a.DependsOnSatisfied(goals), b.DependsOnSatisfied(goals)
			if as != bs {
				return as // dependency-satisfied (runnable) first
			}
			return a.ID < b.ID
		}
		return false // SliceStable keeps file order in the running/terminal buckets
	})
	fmt.Fprintf(buf, "  %s%-12s  %-5s  %-28s  %-9s  %-12s  %-5s  %-5s  %-8s  %-10s  %s%s\n",
		ansiDim, "ID", "Prio", "Description", "Status", "Phase", "Lane", "Cycle", "c/s/v", "Wall", "Window", ansiReset)
	for i := range goals {
		g := &goals[i]
		color := goalStatusColor(g.Status)
		phase := g.Phase
		if phase == "" {
			phase = "—"
		}
		cycle := cycleByGoal[g.ID]
		if cycle == "" {
			cycle = "—"
		}
		csv := fmt.Sprintf("%d/%d/%d", g.CodeRetries, g.SpecRetries, g.ValidationRetries)
		fmt.Fprintf(buf, "  %s%-12s  %-5d  %-28s  %-9s  %-12s  %-5s  %-5s  %-8s  %-10s  %s%s\n",
			color, g.ID, g.Priority, truncate(g.Description, 28), g.Status, phase, g.LaneOrFull(),
			cycle, csv, formatElapsed(g.StartedAt, g.FinishedAt), SupervisorWindowForGoal(g.ID), ansiReset)
	}
}

// ── Section 2: task → goal mappings ────────────────────────────────────────────

// collectMappings reads the task↔goal ledger read-only; nil-safe (absent file ⇒
// empty slice).
func collectMappings(projectRoot string) []TaskGoalMapping {
	tgf, err := LoadTaskGoals(projectRoot)
	if err != nil || tgf == nil {
		return nil
	}
	return tgf.Mappings
}

func renderMappingsSection(buf *bytes.Buffer, projectRoot string) {
	fmt.Fprintf(buf, "%sTASK → GOAL MAPPINGS%s\n", ansiBold, ansiReset)
	mappings := collectMappings(projectRoot)
	if len(mappings) == 0 {
		fmt.Fprintf(buf, "  %s(no in-flight task→goal mappings)%s\n", ansiDim, ansiReset)
		return
	}
	for _, m := range mappings {
		fmt.Fprintf(buf, "  %s -> %s  %s%s%s\n",
			m.TaskID, m.GoalID, ansiDim, truncate(m.Title, 40), ansiReset)
	}
}

// ── Section 3: backend queue counts ────────────────────────────────────────────

// collectQueueCounts gates on producer.LoadConfig (read-only), then makes at most
// one short-deadline best-effort ListTasks call. Every disabled/unavailable/
// unreachable branch degrades to the cache (with an age annotation) when one
// exists. The live branch rewrites the cache atomically, tolerating a write error
// silently. Returns (counts-or-nil, human note).
func collectQueueCounts(projectRoot string) (*queueCounts, string) {
	cfg, err := producer.LoadConfig(projectRoot)
	if err != nil || !cfg.APIEnabled {
		return cacheOr(projectRoot, "api disabled")
	}
	client := producer.New(cfg)
	if client == nil {
		return cacheOr(projectRoot, "api unavailable")
	}
	// 2s deadline OFF context.Background() (not a daemon ctx) so it never blocks
	// shutdown > 2s and is never cancelled by an unrelated parent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := client.ListTasks(ctx, producer.ListTasksParams{Limit: 200})
	if err != nil || list == nil {
		return cacheOr(projectRoot, "api unreachable")
	}
	counts := aggregateTaskList(list)
	// Cache-write failure must never surface: render live, skip caching.
	_ = writeQueueCache(projectRoot, counts)
	note := fmt.Sprintf("total %d", counts.Total)
	if counts.Total > counts.Sampled {
		note = fmt.Sprintf("total %d (sampled %d of %d)", counts.Total, counts.Sampled, counts.Total)
	}
	return counts, note
}

// cacheOr returns the cached counts (annotated with reason + age) when a cache
// file exists, else nil counts with the bare reason. Centralizes the
// disabled/unavailable/unreachable fallback so all three branches behave alike.
func cacheOr(projectRoot, reason string) (*queueCounts, string) {
	if cached, ok := readQueueCache(projectRoot); ok {
		return cached, fmt.Sprintf("%s (cached %s)", reason, cacheAge(cached))
	}
	return nil, reason
}

func aggregateTaskList(list *producer.TaskList) *queueCounts {
	qc := &queueCounts{
		Total:      list.Total,
		ByStatus:   map[string]int{},
		BySeverity: map[string]int{},
		Sampled:    len(list.Tasks),
		SampledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	for _, t := range list.Tasks {
		status := t.Status
		if status == "" {
			status = "unknown"
		}
		qc.ByStatus[status]++
		sev := t.Severity
		if sev == "" {
			sev = "unknown"
		}
		qc.BySeverity[sev]++
	}
	return qc
}

func renderQueueSection(buf *bytes.Buffer, projectRoot string) {
	fmt.Fprintf(buf, "%sBACKEND QUEUE%s\n", ansiBold, ansiReset)
	counts, note := collectQueueCounts(projectRoot)
	fmt.Fprintf(buf, "  queue: %s\n", note)
	if counts == nil {
		return
	}
	if len(counts.ByStatus) > 0 {
		fmt.Fprintf(buf, "  by status:   %s\n", formatCountMap(counts.ByStatus))
	}
	if len(counts.BySeverity) > 0 {
		fmt.Fprintf(buf, "  by severity: %s\n", formatCountMap(counts.BySeverity))
	}
}

// formatCountMap renders a key→count map as "k1 n1, k2 n2" with keys sorted so the
// output is deterministic (stable across repaints and test runs).
func formatCountMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// ── Section 4: worker-window census ────────────────────────────────────────────

// classifyWindow maps a tmux window name to its goal-worker class, or "" when the
// window is not a goal worker. The trailing "-" in every prefix is what excludes
// the bare singletons: window-0 "supervisor" (the human's interactive window) and
// the bare "validator" one-release fallback are deliberately NOT counted.
func classifyWindow(name string) string {
	switch {
	case strings.HasPrefix(name, "supervisor-"):
		return "supervisor"
	case strings.HasPrefix(name, "execute-"):
		return "execute"
	case strings.HasPrefix(name, "validator-"):
		return "validator"
	case strings.HasPrefix(name, "investigator-"):
		return "investigator"
	case strings.HasPrefix(name, "plan-audit-"):
		return "plan-audit"
	default:
		return ""
	}
}

// censusOrder fixes the census column order so the line is deterministic.
var censusOrder = []string{"supervisor", "execute", "validator", "investigator", "plan-audit"}

// collectCensus discovers the session via the env marker and classifies its live
// windows. exec==nil, an empty/erroring session lookup, or a ListWindows error all
// degrade to the same placeholder note (census unavailable) — never an error.
func collectCensus(exec tmux.TmuxExecutor, projectRoot string) (map[string]int, string) {
	if exec == nil {
		return nil, "(no tmux session — census unavailable)"
	}
	sessionID, err := exec.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", projectRoot)
	if err != nil || sessionID == "" {
		return nil, "(no tmux session — census unavailable)"
	}
	windows, err := exec.ListWindows(sessionID)
	if err != nil {
		return nil, "(no tmux session — census unavailable)"
	}
	counts := map[string]int{}
	for _, win := range windows {
		if kind := classifyWindow(win.Name); kind != "" {
			counts[kind]++
		}
	}
	return counts, ""
}

func renderCensusSection(buf *bytes.Buffer, exec tmux.TmuxExecutor, projectRoot string) {
	fmt.Fprintf(buf, "%sWORKER WINDOWS%s\n", ansiBold, ansiReset)
	counts, note := collectCensus(exec, projectRoot)
	if note != "" {
		fmt.Fprintf(buf, "  %s%s%s\n", ansiDim, note, ansiReset)
		return
	}
	parts := make([]string, 0, len(censusOrder))
	for _, k := range censusOrder {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	fmt.Fprintf(buf, "  %s\n", strings.Join(parts, " / "))
}

// ── Section 5: log tail (last transition + last COUNTERS) ──────────────────────

func taskvisorLogPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "logs", "taskvisor.log")
}

// tailLog reads the last ≤64 KiB of taskvisor.log and does one forward pass: the
// last line containing " -> " is the last transition, the last line containing
// "COUNTERS " is the last COUNTERS line, and each COUNTERS line's goal=/cycle=
// tokens populate cycleByGoal. The read is bounded (os.Stat size → ReadAt) because
// the daemon log grows unbounded. A possibly-partial first line after a non-zero
// seek is dropped. An absent/unreadable file returns ("", "", nil) — never errors.
func tailLog(projectRoot string) (lastTransition, lastCounters string, cycleByGoal map[string]string) {
	path := taskvisorLogPath(projectRoot)
	info, err := os.Stat(path)
	if err != nil {
		return "", "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()

	const maxTail = 64 * 1024
	size := info.Size()
	start := int64(0)
	if size > maxTail {
		start = size - maxTail
	}
	data := make([]byte, size-start)
	n, err := f.ReadAt(data, start)
	if err != nil && err != io.EOF {
		return "", "", nil
	}
	data = data[:n]

	lines := strings.Split(string(data), "\n")
	if start > 0 && len(lines) > 0 {
		// Drop the possibly-partial first line produced by the mid-file seek.
		lines = lines[1:]
	}
	for _, line := range lines {
		if strings.Contains(line, " -> ") {
			lastTransition = line
		}
		if strings.Contains(line, "COUNTERS ") {
			lastCounters = line
			tokens := parseCounters(line)
			if goalID, cyc := tokens["goal"], tokens["cycle"]; goalID != "" && cyc != "" {
				if cycleByGoal == nil {
					cycleByGoal = map[string]string{}
				}
				cycleByGoal[goalID] = cyc
			}
		}
	}
	return lastTransition, lastCounters, cycleByGoal
}

// parseCounters splits a COUNTERS line into key→value tokens: split on whitespace,
// then each token on its FIRST '='. Non-kv tokens (a leading log timestamp, the
// bare "COUNTERS" word) have no '=' and are skipped.
func parseCounters(line string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Fields(line) {
		if i := strings.IndexByte(tok, '='); i >= 0 {
			out[tok[:i]] = tok[i+1:]
		}
	}
	return out
}

func renderLogSection(buf *bytes.Buffer, projectRoot, lastTransition, lastCounters string) {
	fmt.Fprintf(buf, "%sRECENT ACTIVITY%s\n", ansiBold, ansiReset)
	if _, err := os.Stat(taskvisorLogPath(projectRoot)); err != nil {
		fmt.Fprintf(buf, "  %s(no taskvisor.log)%s\n", ansiDim, ansiReset)
		return
	}
	transition := lastTransition
	if transition == "" {
		transition = "—"
	}
	counters := lastCounters
	if counters == "" {
		counters = "—"
	}
	fmt.Fprintf(buf, "  last transition: %s\n", transition)
	fmt.Fprintf(buf, "  last COUNTERS:   %s\n", counters)
}

// ── Queue cache (the sole permitted write) ─────────────────────────────────────

func dashboardQueueCachePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "dashboard-queue-cache.json")
}

// readQueueCache loads the cached queue counts; ok is false on any read/parse
// error (treated as "no cache").
func readQueueCache(projectRoot string) (*queueCounts, bool) {
	data, err := os.ReadFile(dashboardQueueCachePath(projectRoot))
	if err != nil {
		return nil, false
	}
	var qc queueCounts
	if err := json.Unmarshal(data, &qc); err != nil {
		return nil, false
	}
	return &qc, true
}

// writeQueueCache atomically rewrites the cache via temp-file + os.Rename, the only
// write the board performs. Mirrors goals.go's atomicWrite contract.
func writeQueueCache(projectRoot string, qc *queueCounts) error {
	path := dashboardQueueCachePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(qc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// cacheAge renders the human age of a cached snapshot ("42s ago"/"5m ago"/"2h
// ago") from its SampledAt stamp; "age ?" when the stamp is missing/unparseable.
func cacheAge(qc *queueCounts) string {
	if qc.SampledAt == "" {
		return "age ?"
	}
	t, err := time.Parse(time.RFC3339, qc.SampledAt)
	if err != nil {
		return "age ?"
	}
	secs := int(time.Since(t).Seconds())
	if secs < 0 {
		secs = 0
	}
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds ago", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm ago", secs/60)
	default:
		return fmt.Sprintf("%dh ago", secs/3600)
	}
}

// truncate shortens s to at most max bytes, appending "..." when it had to cut.
// Byte-slicing matches the pre-existing renderDashboard truncation idiom; goal
// descriptions are ASCII titles (≤120 chars, AGENTS.md invariant), so no rune is
// split in practice.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
