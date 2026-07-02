package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/e2e"
	"github.com/spf13/cobra"
)

// errBootstrapFailed is the sentinel returned after a prologue gate fails. The
// ok:false JSON is already printed to stdout, so the command silences error
// reprinting and just needs a non-zero exit (ExitGeneralError).
var errBootstrapFailed = errors.New("e2e-bootstrap prologue gate failed")

// e2e-bootstrap / e2e-teardown automate the deterministic prologue and reap of
// the /tmux:e2e-evaluator conductor (design §5/§10). The conductor LLM calls
// e2e-bootstrap once per cycle and proceeds straight to DRIVE on ok:true — no
// hand-driven PROVISION/BOOTSTRAP/HANDSHAKE tool ping-pong.

var (
	e2eProject       string
	e2eResume        bool
	e2eNoHumanView   bool
	e2eOrchPane      string
	e2eMaxCycles     int
	e2eKeepOnFailure bool
	e2eHandshakeWait int
	e2eModel         string
	e2eTeardownDir   string
)

var e2eBootstrapCmd = &cobra.Command{
	Use:   "e2e-bootstrap <scenario>",
	Short: "Provision+bootstrap+handshake a disposable e2e-evaluator target (prologue automation)",
	Long: `Run the entire deterministic e2e-evaluator prologue in one command and emit a
JSON context the conductor reads once before DRIVE:

  preconditions → reap stale tmux-cli-tmp-* sessions → resolve/git-init target dir
  → seed ~/.claude.json trust → tmux-cli start (detached) → attach human view
  → verify bypass+idle → pipe-pane log → init-prompt HANDSHAKE (notify-orchestrator
  proven live)

Every step is a hard gate: on failure it prints {"ok":false,"stage":...,"error":...}
to stdout, self-tears-down what it created (unless --keep-on-failure), and exits
non-zero. On success it prints {"ok":true, session, target_pane, orchestrator_pane,
target_dir, log_path, state_file, cycle, ...}. Human-readable progress goes to stderr
so stdout stays a single pure JSON line.

Fresh-from-scratch by default: clears prior state + reports for the scenario and
reaps stale test sessions. --resume continues an in-progress run's cycle instead.`,
	Args:          cobra.MinimumNArgs(1),
	RunE:          runE2EBootstrap,
	SilenceErrors: true, // ok:false JSON is the error surface; just exit non-zero
	SilenceUsage:  true,
}

var e2eTeardownCmd = &cobra.Command{
	Use:   "e2e-teardown <session>",
	Short: "Ordered reap of an e2e-evaluator target session (daemon→compose→worktrees→kill→rm)",
	Long: `Reap everything a disposable e2e-evaluator target spawned, IN ORDER, continuing
past a single step's failure (design §10):

  1. stop the target's taskvisor daemon
  2. docker compose down every stack it created
  3. remove its git worktrees + branches
  4. tmux kill-session by EXACT name
  5. rm -rf its /tmp dir

NEVER uses pkill -f (it would SIGTERM the teardown itself). The /tmp dir is derived
from the session's pane path when --dir is omitted; only dirs under /tmp are removed.`,
	Args: cobra.ExactArgs(1),
	RunE: runE2ETeardown,
}

func init() {
	e2eBootstrapCmd.Flags().StringVar(&e2eProject, "project", "", "Pin the target dir instead of /tmp/<scenario>-<UTCstamp>")
	e2eBootstrapCmd.Flags().BoolVar(&e2eResume, "resume", false, "Continue an in-progress run's cycle instead of clearing fresh")
	e2eBootstrapCmd.Flags().BoolVar(&e2eNoHumanView, "no-human-view", false, "Never attach a native terminal (force headless)")
	e2eBootstrapCmd.Flags().StringVar(&e2eOrchPane, "orchestrator-pane", "", "Orchestrator pane id (default: $TMUX_CLI_ORCHESTRATOR_PANE or current pane)")
	e2eBootstrapCmd.Flags().IntVar(&e2eMaxCycles, "max-cycles", e2e.DefaultMaxCycles, "Self-heal cycle budget recorded in fresh state")
	e2eBootstrapCmd.Flags().BoolVar(&e2eKeepOnFailure, "keep-on-failure", false, "Do not self-teardown the target on a prologue failure (for debugging)")
	e2eBootstrapCmd.Flags().IntVar(&e2eHandshakeWait, "handshake-wait", 30, "Seconds to wait for the handshake token before retry/abort")
	// The e2e-evaluator is a self-test harness — it must exercise the flow on the
	// STRONG model, never a cheaper account-default (e.g. Fable 5). Default the
	// target session (and thus every daemon window/worker, via TMUX_CLI_MODEL) to
	// Opus 4.8; pass --model "" to fall back to the account default.
	e2eBootstrapCmd.Flags().StringVar(&e2eModel, "model", "claude-opus-4-8", "Claude model for the target session + all daemon windows/workers (default Opus 4.8; \"\" = account default)")

	e2eTeardownCmd.Flags().StringVar(&e2eTeardownDir, "dir", "", "The /tmp target dir to rm (default: derived from the session's pane path)")

	rootCmd.AddCommand(e2eBootstrapCmd)
	rootCmd.AddCommand(e2eTeardownCmd)
}

// progress writes a human-readable prologue step line to stderr (stdout is the
// pure JSON contract).
func progress(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "[e2e-bootstrap] "+format+"\n", a...)
}

// tmuxOut runs a tmux command and returns trimmed stdout.
func tmuxOut(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// failBootstrap prints the ok:false result, optionally self-tears-down, and
// returns a SilentError so cobra exits non-zero without re-printing.
func failBootstrap(r e2e.BootstrapResult, stage, msg string) error {
	r.Ok = false
	r.Stage = stage
	r.Error = msg
	progress("FAILED at %s: %s", stage, msg)
	if !e2eKeepOnFailure && r.Session != "" {
		progress("self-teardown of %s", r.Session)
		e2eReap(r.Session, r.TargetDir)
	}
	fmt.Println(r.JSON())
	return errBootstrapFailed
}

func runE2EBootstrap(cmd *cobra.Command, args []string) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	scenario := e2e.SlugifyScenario(strings.Join(args, " "))
	res := e2e.BootstrapResult{Scenario: scenario, MaxCycles: e2eMaxCycles}

	// ── Step 1: preconditions ───────────────────────────────────────────────
	for _, bin := range []string{"tmux", "docker", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			return failBootstrap(res, "precondition", fmt.Sprintf("%s not found on PATH", bin))
		}
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		return failBootstrap(res, "precondition", "`docker compose` not available")
	}

	// ── Orchestrator pane resolution ────────────────────────────────────────
	orchPane := strings.TrimSpace(e2eOrchPane)
	if orchPane == "" {
		orchPane = strings.TrimSpace(os.Getenv("TMUX_CLI_ORCHESTRATOR_PANE"))
	}
	if orchPane == "" {
		if p, err := tmuxOut("display-message", "-p", "#{pane_id}"); err == nil {
			orchPane = strings.TrimSpace(p)
		}
	}
	if !strings.HasPrefix(orchPane, "%") {
		return failBootstrap(res, "orchestrator-pane", fmt.Sprintf("could not resolve an orchestrator pane id (got %q); pass --orchestrator-pane %%N", orchPane))
	}
	res.OrchestratorPane = orchPane

	// ── Step 1b: resume-or-clear state ──────────────────────────────────────
	stateFile := e2e.StateFilePath(repoRoot, scenario)
	res.StateFile = mustRel(repoRoot, stateFile)
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		return failBootstrap(res, "state", fmt.Sprintf("mkdir state dir: %v", err))
	}
	cycle, err := resolveCycle(repoRoot, scenario)
	if err != nil {
		return failBootstrap(res, "state", err.Error())
	}
	res.Cycle = cycle
	// On --resume, surface the ledger's pending fix-verification so the
	// conductor knows it is entering a confirm-fix cycle (self-update handoff).
	if e2eResume {
		if v := readLedgerVerify(stateFile); v != nil {
			res.VerifySignature = v.Signature
			res.VerifyTaskID = v.TaskID
		}
	}

	// ── Step 2: reap stale tmux-cli-tmp-* sessions from past runs ────────────
	res.ReapedSessions = reapStaleSessions()

	// ── Step 2: resolve + provision the target dir ──────────────────────────
	stamp := time.Now().UTC().Format("20060102T150405Z")
	targetDir, err := e2e.ResolveTargetDir(scenario, e2eProject, stamp)
	if err != nil {
		return failBootstrap(res, "provision", err.Error())
	}
	res.TargetDir = targetDir
	if isNonEmptyDir(targetDir) {
		return failBootstrap(res, "provision", fmt.Sprintf("target dir already exists and is non-empty: %s (pristine-every-cycle)", targetDir))
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return failBootstrap(res, "provision", fmt.Sprintf("mkdir target dir: %v", err))
	}
	// Mark the dir disposable NOW (session recorded post-start): the orphan
	// reaper selects dirs by this marker, never by name pattern.
	if err := writeDisposableMarker(targetDir, scenario, stamp); err != nil {
		return failBootstrap(res, "provision", fmt.Sprintf("write disposable marker: %v", err))
	}
	if err := runIn(targetDir, "git", "init", "-q"); err != nil {
		return failBootstrap(res, "provision", fmt.Sprintf("git init: %v", err))
	}
	_ = runIn(targetDir, "git", "config", "user.email", "e2e@evaluator.local")
	_ = runIn(targetDir, "git", "config", "user.name", "e2e-evaluator")
	// Born HEAD before the daemon ticks: `git init` leaves an unborn HEAD, so the
	// pipelined daemon's `git worktree add … HEAD` for goal-001 fails (exit 128,
	// "invalid reference: HEAD") and the goal poll-wedges to failed (backend task
	// 317). Seed an --allow-empty baseline commit (identity is configured above)
	// so HEAD is born before startTarget launches the daemon.
	if err := runIn(targetDir, "git", "commit", "--allow-empty", "-m", "e2e-baseline"); err != nil {
		return failBootstrap(res, "provision", fmt.Sprintf("git commit baseline: %v", err))
	}
	progress("target dir %s (cycle %d)", targetDir, cycle)

	// ── Step 2: seed ~/.claude.json trust BEFORE any claude launch ──────────
	if err := seedTrust(targetDir); err != nil {
		return failBootstrap(res, "trust-seed", err.Error())
	}
	progress("seeded trust for %s", targetDir)

	// ── Step 2/3: inject orchestrator pane + notify receipt into the tmux
	// server env, then start. Both MUST land before startTarget so the
	// auto-launched claude inherits them. The receipt is keyed by the run's
	// own identity (<scenario>-<stamp>, the target-dir basename) because the
	// session name embeds a start-time timestamp that does not exist yet.
	if _, err := tmuxOut("setenv", "-g", "TMUX_CLI_ORCHESTRATOR_PANE", orchPane); err != nil {
		return failBootstrap(res, "bootstrap", fmt.Sprintf("setenv -g orchestrator pane: %v", err))
	}
	receiptPath := e2e.ReceiptPath(repoRoot, scenario+"-"+stamp)
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		return failBootstrap(res, "bootstrap", fmt.Sprintf("mkdir receipt dir: %v", err))
	}
	if _, err := tmuxOut("setenv", "-g", "TMUX_CLI_NOTIFY_RECEIPT", receiptPath); err != nil {
		return failBootstrap(res, "bootstrap", fmt.Sprintf("setenv -g notify receipt: %v", err))
	}
	res.ReceiptPath = receiptPath
	session, err := startTarget(targetDir, e2eModel)
	if err != nil {
		return failBootstrap(res, "start", err.Error())
	}
	res.Session = session
	if err := recordDisposableSession(targetDir, session); err != nil {
		return failBootstrap(res, "start", fmt.Sprintf("record session in disposable marker: %v", err))
	}
	progress("started detached session %s", session)

	pane, err := tmuxOut("list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil || pane == "" {
		return failBootstrap(res, "start", fmt.Sprintf("resolve target pane: %v", err))
	}
	pane = strings.SplitN(pane, "\n", 2)[0]
	res.TargetPane = pane
	// Note: the orchestrator-pane env is injected via `setenv -g` BEFORE start so
	// the auto-launched claude inherits it. We do NOT re-check via
	// `show-environment -t <session>` (that reads only session-scoped overrides,
	// not inherited globals — a false negative). The HANDSHAKE below is the real
	// proof: notify-orchestrator fails loudly without the var, so a landed token
	// inherently confirms it propagated.

	// ── Step 2: human view (mandatory when a GUI exists) ────────────────────
	res.HumanView = attachHumanView(session)

	// ── Step 3: wait for claude idle, then pipe-pane BEFORE any drive ───────
	if !waitForIdlePrompt(pane, 40*time.Second) {
		return failBootstrap(res, "bootstrap", "target never reached a stable idle prompt — Claude Code TUI markers (idle ❯ / busy-hint words) may have changed and this probe may be stale")
	}
	logPath := e2e.LogPath(repoRoot, session)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return failBootstrap(res, "pipe-pane", fmt.Sprintf("mkdir log dir: %v", err))
	}
	if _, err := tmuxOut("pipe-pane", "-o", "-t", pane, fmt.Sprintf("cat >> %q", logPath)); err != nil {
		return failBootstrap(res, "pipe-pane", fmt.Sprintf("start pipe-pane: %v", err))
	}
	res.LogPath = logPath
	progress("pipe-pane logging → %s", logPath)

	// ── Step 3b: init-prompt HANDSHAKE — prove notify-orchestrator live ─────
	if !doHandshake(pane, session, logPath, receiptPath, time.Duration(e2eHandshakeWait)*time.Second) {
		return failBootstrap(res, "handshake", "notify-orchestrator channel did not prove live (handshake token never landed)")
	}
	res.Handshake = "ok"
	progress("HANDSHAKE ok — notify-orchestrator channel live")

	res.Ok = true
	fmt.Println(res.JSON())
	return nil
}

// resolveCycle implements fresh-from-scratch-by-default vs --resume (step 1b).
// It derives the ledger path itself (e2e.StateFilePath) so its reads stay
// coherent with writeE2ELedger's writes, and routes every ledger write through
// writeE2ELedger so the <scenario>.state.md rendering — the self-update
// resume-handoff artifact — never goes stale beside state.json.
func resolveCycle(repoRoot, scenario string) (int, error) {
	stateFile := e2e.StateFilePath(repoRoot, scenario)
	if !e2eResume {
		// Clear prior artifacts and start fresh at cycle 1.
		_ = os.Remove(stateFile)
		clearReports(repoRoot, scenario)
		clearRunArtifacts(repoRoot, scenario)
		st := e2e.NewState(scenario, e2eMaxCycles)
		if err := writeE2ELedger(repoRoot, scenario, st); err != nil {
			return 0, fmt.Errorf("init fresh state: %w", err)
		}
		return 1, nil
	}
	// --resume: read and continue.
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		// Missing on resume == fresh cycle 1 (not an error).
		st := e2e.NewState(scenario, e2eMaxCycles)
		if werr := writeE2ELedger(repoRoot, scenario, st); werr != nil {
			return 0, fmt.Errorf("init state on resume: %w", werr)
		}
		return 1, nil
	}
	st, err := e2e.ParseState(raw)
	if err != nil {
		return 0, err
	}
	if st.Status != e2e.StatusInProgress {
		// Already terminal — leave the ledger byte-identical.
		return 0, fmt.Errorf("run already terminal (status=%s); nothing to resume", st.Status)
	}
	if st.Cycle > st.MaxCycles {
		// Flip the ledger terminal BEFORE erroring — otherwise it stays
		// in-progress forever and e2e-evaluator.xml step 1b keeps resuming it.
		if werr := writeE2ELedger(repoRoot, scenario, st.MarkExhausted()); werr != nil {
			return 0, fmt.Errorf("self-heal budget exhausted (cycle %d > max %d); also failed to mark ledger exhausted: %v", st.Cycle, st.MaxCycles, werr)
		}
		return 0, fmt.Errorf("self-heal budget exhausted (cycle %d > max %d); ledger marked exhausted", st.Cycle, st.MaxCycles)
	}
	return st.Cycle, nil
}

// clearRunArtifacts sweeps THIS scenario's stale run artifacts before a fresh
// (non-resume) bootstrap: the <scenario>.state.md resume-handoff plus the
// scenario's receipts and pipe-pane logs under logs/. Matching is tighter than
// a bare <scenario>-* glob: the slug must be immediately followed by a run
// stamp, so a scenario whose slug extends this one ("scn-two" vs "scn") is
// never swept.
func clearRunArtifacts(repoRoot, scenario string) {
	_ = os.Remove(e2e.StateMDPath(repoRoot, scenario))
	q := regexp.QuoteMeta(scenario)
	// The bootstrap's UTC run stamp (20060102T150405Z); tmux session names
	// carry it lowercased (GenerateSessionID sanitizes the target dir path).
	const stamp = `[0-9]{8}[Tt][0-9]{6}[Zz]`
	// Receipts are named <scenario>-<stamp>.receipt (e2e.ReceiptPath).
	receiptRe := regexp.MustCompile(`^` + q + `-` + stamp)
	// Pipe-pane logs are named after the target session
	// (tmux-cli-…-tmp-<scenario>-<stamp>-<started>.log, e2e.LogPath), so the
	// slug sits mid-name behind a tmp- marker; a <scenario>-<stamp> prefix is
	// also accepted for spec-shaped names.
	logRe := regexp.MustCompile(`^(?:.*-)?tmp-` + q + `-` + stamp + `|^` + q + `-` + stamp)
	logsDir := filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", "logs")
	entries, _ := os.ReadDir(logsDir)
	for _, e := range entries {
		name := e.Name()
		matched := (strings.HasSuffix(name, ".receipt") && receiptRe.MatchString(name)) ||
			(strings.HasSuffix(name, ".log") && logRe.MatchString(name))
		if matched {
			_ = os.Remove(filepath.Join(logsDir, name))
		}
	}
}

// clearReports sweeps ONLY this scenario's per-cycle reports before a fresh
// (non-resume) bootstrap — e2e.IsScenarioReport anchors the slug with the
// following "-cycle-", so a sibling scenario is never cross-swept. Legacy
// unscoped e2e-report-cycle-<n>.md files predate the scenario-scoped naming
// and belong to no scenario; they are removed too (one-time orphan migration).
func clearReports(repoRoot, scenario string) {
	dir := filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e2e.IsScenarioReport(e.Name(), scenario) || e2e.IsLegacyReport(e.Name()) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// readLedgerVerify extracts the pending fix-verification from a ledger file,
// tolerantly: a missing/corrupt file or an absent verify field yields nil
// (resolveCycle already gated the strict parse on the resume path).
func readLedgerVerify(stateFile string) *e2e.VerifyState {
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}
	st, err := e2e.ParseState(raw)
	if err != nil {
		return nil
	}
	return st.Verify
}

func writeStateAtomic(stateFile string, st e2e.State) error {
	b, err := st.Marshal()
	if err != nil {
		return err
	}
	tmp := stateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, stateFile)
}

// seedTrust seeds ~/.claude.json trust for targetDir with a
// read-transform-rename cycle that re-reads the file IMMEDIATELY before the
// rename (SeedTrustConfig is pure and idempotent, so re-applying to fresh
// bytes is free), verifies the three seeded keys after the rename, and
// retries the whole cycle once on a verify miss.
//
// Residual race (documented, not eliminable here): claude honors no lock on
// ~/.claude.json, so a concurrent claude process can still rewrite the file
// between our post-rename verify and the target's own config load. The fresh
// read + verify + single retry only shrinks that clobber window.
func seedTrust(targetDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	p := filepath.Join(home, ".claude.json")
	attempt := func() error {
		raw, _ := os.ReadFile(p) // fresh read right before the rename; missing → seed from empty
		out, err := e2e.SeedTrustConfig(raw, targetDir)
		if err != nil {
			return err
		}
		tmp := p + ".e2e.tmp"
		if err := os.WriteFile(tmp, out, 0o600); err != nil {
			return fmt.Errorf("write trust config: %w", err)
		}
		if err := os.Rename(tmp, p); err != nil {
			return fmt.Errorf("rename trust config: %w", err)
		}
		back, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read back trust config: %w", err)
		}
		if !e2e.TrustSeeded(back, targetDir) {
			return fmt.Errorf("trust keys missing after seed of %s (concurrent ~/.claude.json writer?)", targetDir)
		}
		return nil
	}
	if err := attempt(); err != nil {
		progress("trust seed verify failed (%v) — retrying once", err)
		return attempt()
	}
	return nil
}

// startTarget runs `tmux-cli start --print-json` in the target dir
// (auto-launching claude with the seeded trust + bypass) and returns the
// exact session name from the machine contract: stdout is exactly one
// compact JSON line {"session":...,"created":true|false}, human output on
// stderr. There is deliberately no human-output fallback parse.
func startTarget(targetDir, model string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "tmux-cli"
	}
	args := []string{"start", "--print-json"}
	if model != "" {
		// Propagates as TMUX_CLI_MODEL to every window + worker (session.go).
		args = append(args, "--model", model)
	}
	c := exec.Command(self, args...)
	c.Dir = targetDir
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("tmux-cli start: %v: %s", err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	out, err := e2e.ParseStartOutput(stdout.String())
	if err != nil {
		return "", err
	}
	return out.Session, nil
}

// attachHumanView attaches a native terminal when a GUI exists and verifies a
// client landed; returns "attached" | "headless" | "headless (warn: ...)".
func attachHumanView(session string) string {
	if e2eNoHumanView {
		return "headless"
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		return "headless" // genuine CI/SSH path
	}
	term := findNativeTerminal()
	if term == "" {
		return "headless" // no native terminal binary
	}
	attach := func() {
		args := []string{}
		if filepath.Base(term) == "konsole" {
			args = append(args, "--separate")
		}
		args = append(args, "-e", "tmux", "attach-session", "-t", session)
		c := exec.Command("setsid", append([]string{term}, args...)...)
		c.Stdout, c.Stderr = nil, nil
		_ = c.Start()
	}
	attach()
	for i := 0; i < 2; i++ {
		time.Sleep(2 * time.Second)
		if clients, _ := tmuxOut("list-clients", "-t", session); strings.TrimSpace(clients) != "" {
			progress("human view attached via %s", filepath.Base(term))
			return "attached"
		}
		if i == 0 {
			attach() // one retry
		}
	}
	progress("WARN: %s found but no client attached — running headless (human cannot see progress)", filepath.Base(term))
	return "headless (warn: attach failed)"
}

// findNativeTerminal resolves the first available native terminal (Konsole
// first), excluding the sandboxed Flatpak Konsole (no host TTY).
func findNativeTerminal() string {
	for _, t := range []string{"konsole", "gnome-terminal", "xterm", "alacritty", "kitty", "wezterm"} {
		if p, err := exec.LookPath(t); err == nil && !strings.Contains(p, "flatpak") {
			return p
		}
	}
	return ""
}

// ── Claude Code TUI probe constants ─────────────────────────────────────────
// Deliberately TUI-coupled: these mirror Claude Code's terminal rendering and
// are the ONLY place that coupling lives. idlePromptMarker is the input-box
// prompt glyph; busyHintWords are spinner/status words that mean "definitely
// busy" (they only short-circuit the stability wait — idleness itself is
// decided structurally by two identical snapshots). If Claude Code's TUI
// changes, update this block and nothing else.
const (
	idlePromptMarker   = "❯"
	idleStableInterval = 2 * time.Second
)

var busyHintWords = []string{"Cogitating", "Germinating", "Gitifying", "Transfiguring", "Brewed", "esc to interrupt"}

// writeDisposableMarker drops the provision-time disposable marker in dir.
func writeDisposableMarker(dir, scenario, stamp string) error {
	return os.WriteFile(filepath.Join(dir, e2e.DisposableMarkerName), []byte(e2e.NewMarker(scenario, stamp)), 0o644)
}

// recordDisposableSession rewrites dir's marker with the started session name.
// A marker that cannot be updated is a hard error: without a session line the
// next bootstrap's orphan scan would reap this (live) run's dir.
func recordDisposableSession(dir, session string) error {
	p := filepath.Join(dir, e2e.DisposableMarkerName)
	raw, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read disposable marker: %w", err)
	}
	return os.WriteFile(p, []byte(e2e.MarkerWithSession(string(raw), session)), 0o644)
}

// reapOrphanDisposables scans root/*/ for disposable markers and removes every
// marked dir whose recorded session is not live (or was never recorded).
// Unmarked dirs are NEVER touched. Returns the removed dirs.
func reapOrphanDisposables(root string, liveSessions []string) []string {
	markers, _ := filepath.Glob(filepath.Join(root, "*", e2e.DisposableMarkerName))
	var removed []string
	for _, mp := range markers {
		raw, err := os.ReadFile(mp)
		if err != nil {
			continue
		}
		if e2e.ShouldReapDisposable(e2e.ParseMarker(string(raw)), liveSessions) {
			dir := filepath.Dir(mp)
			if os.RemoveAll(dir) == nil {
				removed = append(removed, dir)
			}
		}
	}
	return removed
}

// handshakeSeen verifies the handshake token: receipt file PRIMARY (exact
// token line — notify-orchestrator appends each delivered message verbatim),
// normalized pipe-pane log FALLBACK (still proves the pane saw it when the
// receipt is missing, e.g. an old binary in the target).
func handshakeSeen(receiptPath, logPath, token string) bool {
	if raw, err := os.ReadFile(receiptPath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == token {
				return true
			}
		}
	}
	if raw, err := os.ReadFile(logPath); err == nil {
		n := normalizeLog(string(raw))
		if strings.Contains(n, stripWS(token)) && strings.Contains(n, "Notifiedorchestratorpane") {
			return true
		}
	}
	return false
}

// waitForIdlePrompt polls the target pane until it is structurally idle: two
// consecutive capture-pane snapshots taken idleStableInterval apart are
// IDENTICAL (after trimming) and contain the idle prompt marker. A matched
// busy-hint word only short-circuits the stability compare (definitely busy);
// it is not required for the idle decision, so new spinner words can't cause
// a false "idle" — at worst they cost one extra compare cycle.
func waitForIdlePrompt(pane string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	prev := ""
	for time.Now().Before(deadline) {
		out, _ := tmuxOut("capture-pane", "-p", "-t", pane)
		snap := strings.TrimSpace(out)
		busy := false
		for _, w := range busyHintWords {
			if strings.Contains(snap, w) {
				busy = true
				break
			}
		}
		if busy {
			prev = "" // definitely busy — restart the stability window
		} else {
			if snap != "" && snap == prev && strings.Contains(snap, idlePromptMarker) {
				return true
			}
			prev = snap
		}
		time.Sleep(idleStableInterval)
	}
	return false
}

// doHandshake sends the init prompt (two-step) and verifies the channel via
// handshakeSeen (receipt file primary, normalized log fallback). Retries the
// prompt once before giving up.
func doHandshake(pane, session, logPath, receiptPath string, wait time.Duration) bool {
	token := e2e.HandshakeToken(session)
	prompt := buildInitPrompt(session, token)
	send := func() bool {
		f, err := os.CreateTemp("", "e2e-init-*.txt")
		if err != nil {
			return false
		}
		defer os.Remove(f.Name())
		f.WriteString(prompt)
		f.Close()
		if _, err := tmuxOut("load-buffer", f.Name()); err != nil {
			return false
		}
		if _, err := tmuxOut("paste-buffer", "-t", pane); err != nil {
			return false
		}
		time.Sleep(1 * time.Second)
		_, err = tmuxOut("send-keys", "-t", pane, "Enter")
		return err == nil
	}
	verify := func(deadline time.Time) bool {
		for {
			if handshakeSeen(receiptPath, logPath, token) {
				return true
			}
			if !time.Now().Before(deadline) {
				return false
			}
			time.Sleep(1 * time.Second)
		}
	}

	if !send() {
		return false
	}
	if verify(time.Now().Add(wait)) {
		return true
	}
	progress("handshake token not seen — retrying init prompt once")
	if !waitForIdlePrompt(pane, 20*time.Second) {
		// proceed to re-send anyway; the target may still be reading
	}
	if !send() {
		return false
	}
	return verify(time.Now().Add(wait))
}

// ansiEscapeRe strips ANSI CSI (e.g. cursor moves, colours) and OSC (title)
// escape sequences a TUI emits, plus stray ESC/BEL bytes, so a wrapped success
// line reads as plain text.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|[\x1b\x07]`)

// allWS matches any run of whitespace (incl. NUL the pipe-pane log can carry).
var allWS = regexp.MustCompile(`[\s\x00]+`)

// stripWS removes all whitespace from s.
func stripWS(s string) string { return allWS.ReplaceAllString(s, "") }

// normalizeLog renders a TUI pipe-pane transcript into escape-free, whitespace-
// free text so substring proofs survive line-wrapping and cursor positioning.
func normalizeLog(s string) string { return stripWS(ansiEscapeRe.ReplaceAllString(s, "")) }

func buildInitPrompt(session, token string) string {
	return strings.Join([]string{
		"You are running UNDER the tmux-cli e2e-evaluator orchestrator (an automated self-test harness), not a normal interactive user. For the ENTIRE run, report every milestone by running this shell command via your Bash tool: tmux-cli notify-orchestrator \"<msg>\". The TMUX_CLI_ORCHESTRATOR_PANE env var is already set, so it just works. Do NOT hand-roll tmux send-keys.",
		"",
		"Milestone vocabulary you MUST emit (exact strings, one notify per milestone as you reach it): discovery-done | goal-authored <id> | goal-<id>-done | goal-<id> failed: <reason> | product-complete | app-up. While a goal is in flight, emit the heartbeat: goal-<id>-progress <note>.",
		"",
		fmt.Sprintf("RIGHT NOW, before anything else, prove the channel: run exactly this via Bash: tmux-cli notify-orchestrator %q  -- then STOP and wait for my next instruction. Do not begin any other work yet.", token),
	}, "\n")
}

// ── teardown ────────────────────────────────────────────────────────────────

func runE2ETeardown(cmd *cobra.Command, args []string) error {
	session := args[0]
	dir := strings.TrimSpace(e2eTeardownDir)
	if dir == "" {
		// Derive from the session's pane path before we kill it.
		if p, err := tmuxOut("display-message", "-p", "-t", session, "#{pane_current_path}"); err == nil {
			dir = strings.TrimSpace(strings.SplitN(p, "\n", 2)[0])
		}
	}
	steps := e2eReap(session, dir)
	for _, s := range steps {
		fmt.Println(s)
	}
	return nil
}

// e2eReap performs the ordered, best-effort reap (design §10). Never pkill -f.
func e2eReap(session, dir string) []string {
	var log []string
	add := func(format string, a ...interface{}) { log = append(log, fmt.Sprintf(format, a...)) }

	// 1. Stop the target's taskvisor daemon (best effort).
	if dir != "" && isDir(dir) {
		self, _ := os.Executable()
		if self == "" {
			self = "tmux-cli"
		}
		c := exec.Command(self, "taskvisor", "stop")
		c.Dir = dir
		if err := c.Run(); err != nil {
			add("daemon stop: skipped (%v)", err)
		} else {
			add("daemon stopped")
		}
	}

	// 2. docker compose down every stack the run created.
	if dir != "" && isDir(dir) {
		n := 0
		for _, f := range findComposeFiles(dir) {
			c := exec.Command("docker", "compose", "-f", f, "down", "-v", "--remove-orphans")
			if err := c.Run(); err == nil {
				n++
			}
		}
		add("docker compose down: %d stack(s)", n)
	}

	// 3. Remove git worktrees + branches.
	if dir != "" && isDir(dir) {
		wt := exec.Command("git", "worktree", "list", "--porcelain")
		wt.Dir = dir
		if out, err := wt.CombinedOutput(); err == nil {
			count := 0
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "worktree ") {
					p := strings.TrimPrefix(line, "worktree ")
					if p == dir {
						continue // the main tree
					}
					rm := exec.Command("git", "worktree", "remove", "--force", p)
					rm.Dir = dir
					if rm.Run() == nil {
						count++
					}
				}
			}
			add("removed %d worktree(s)", count)
		}
	}

	// 4. Kill the session by EXACT name.
	if _, err := tmuxOut("kill-session", "-t", session); err == nil {
		add("killed session %s", session)
	} else {
		add("kill-session %s: already gone", session)
	}

	// 5. rm -rf the disposable dir (only under /tmp, for safety).
	if dir != "" && strings.HasPrefix(filepath.Clean(dir), "/tmp/") && isDir(dir) {
		if err := os.RemoveAll(dir); err == nil {
			add("removed %s", dir)
		} else {
			add("rm %s: %v", dir, err)
		}
	} else if dir != "" {
		add("rm skipped (not under /tmp): %s", dir)
	}
	return log
}

// reapStaleSessions kills leftover disposable targets from crashed prior runs
// and removes orphan /tmp dirs by their disposable marker (any scenario, not
// just the current one). Returns the session names reaped.
func reapStaleSessions() []string {
	var reaped []string
	if names, err := tmuxOut("list-sessions", "-F", "#{session_name}"); err == nil {
		for _, s := range e2e.SelectStaleSessions(strings.Split(names, "\n")) {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			dir := ""
			if p, derr := tmuxOut("display-message", "-p", "-t", s, "#{pane_current_path}"); derr == nil {
				dir = strings.TrimSpace(strings.SplitN(p, "\n", 2)[0])
			}
			e2eReap(s, dir)
			reaped = append(reaped, s)
		}
	}
	// Marker-based orphan-dir sweep, AFTER the kills so the live list is
	// current. A tmux-less environment yields an empty live list — every
	// marked dir is an orphan then.
	var live []string
	if names, err := tmuxOut("list-sessions", "-F", "#{session_name}"); err == nil {
		live = strings.Split(names, "\n")
	}
	for _, d := range reapOrphanDisposables("/tmp", live) {
		progress("reaped orphan disposable dir %s", d)
	}
	return reaped
}

// ── small helpers ────────────────────────────────────────────────────────────

func runIn(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isNonEmptyDir(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) > 0
}

func mustRel(base, target string) string {
	if rel, err := filepath.Rel(base, target); err == nil {
		return rel
	}
	return target
}

func findComposeFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if matched := name == "docker-compose.yml" || name == "docker-compose.yaml" ||
			name == "compose.yml" || name == "compose.yaml"; matched {
			files = append(files, path)
		}
		return nil
	})
	return files
}
