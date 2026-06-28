package main

import (
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
	e2eBootstrapCmd.Flags().IntVar(&e2eHandshakeWait, "handshake-wait", 60, "Seconds to wait for the handshake token before retry/abort")

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
	cycle, err := resolveCycle(repoRoot, scenario, stateFile)
	if err != nil {
		return failBootstrap(res, "state", err.Error())
	}
	res.Cycle = cycle

	// ── Step 2: reap stale tmux-cli-tmp-* sessions from past runs ────────────
	res.ReapedSessions = reapStaleSessions(scenario)

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
	if err := runIn(targetDir, "git", "init", "-q"); err != nil {
		return failBootstrap(res, "provision", fmt.Sprintf("git init: %v", err))
	}
	_ = runIn(targetDir, "git", "config", "user.email", "e2e@evaluator.local")
	_ = runIn(targetDir, "git", "config", "user.name", "e2e-evaluator")
	progress("target dir %s (cycle %d)", targetDir, cycle)

	// ── Step 2: seed ~/.claude.json trust BEFORE any claude launch ──────────
	if err := seedTrust(targetDir); err != nil {
		return failBootstrap(res, "trust-seed", err.Error())
	}
	progress("seeded trust for %s", targetDir)

	// ── Step 2/3: inject orchestrator pane into the tmux server env, then start ─
	if _, err := tmuxOut("setenv", "-g", "TMUX_CLI_ORCHESTRATOR_PANE", orchPane); err != nil {
		return failBootstrap(res, "bootstrap", fmt.Sprintf("setenv -g orchestrator pane: %v", err))
	}
	session, err := startTarget(targetDir)
	if err != nil {
		return failBootstrap(res, "start", err.Error())
	}
	res.Session = session
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
		return failBootstrap(res, "bootstrap", "target claude did not reach an idle ❯ prompt in time")
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
	if !doHandshake(pane, session, logPath, time.Duration(e2eHandshakeWait)*time.Second) {
		return failBootstrap(res, "handshake", "notify-orchestrator channel did not prove live (handshake token never landed)")
	}
	res.Handshake = "ok"
	progress("HANDSHAKE ok — notify-orchestrator channel live")

	res.Ok = true
	fmt.Println(res.JSON())
	return nil
}

// resolveCycle implements fresh-from-scratch-by-default vs --resume (step 1b).
func resolveCycle(repoRoot, scenario, stateFile string) (int, error) {
	if !e2eResume {
		// Clear prior artifacts and start fresh at cycle 1.
		_ = os.Remove(stateFile)
		clearReports(repoRoot, scenario)
		st := e2e.NewState(scenario, e2eMaxCycles)
		if err := writeStateAtomic(stateFile, st); err != nil {
			return 0, fmt.Errorf("init fresh state: %w", err)
		}
		return 1, nil
	}
	// --resume: read and continue.
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		// Missing on resume == fresh cycle 1 (not an error).
		st := e2e.NewState(scenario, e2eMaxCycles)
		if werr := writeStateAtomic(stateFile, st); werr != nil {
			return 0, fmt.Errorf("init state on resume: %w", werr)
		}
		return 1, nil
	}
	st, err := e2e.ParseState(raw)
	if err != nil {
		return 0, err
	}
	if st.Status != e2e.StatusInProgress {
		return 0, fmt.Errorf("run already terminal (status=%s); nothing to resume", st.Status)
	}
	if st.Cycle > st.MaxCycles {
		return 0, fmt.Errorf("self-heal budget exhausted (cycle %d > max %d)", st.Cycle, st.MaxCycles)
	}
	return st.Cycle, nil
}

func clearReports(repoRoot, scenario string) {
	dir := filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "e2e-report-cycle-") && strings.HasSuffix(e.Name(), ".md") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
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

func seedTrust(targetDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	p := filepath.Join(home, ".claude.json")
	raw, _ := os.ReadFile(p) // missing → seed from empty
	out, err := e2e.SeedTrustConfig(raw, targetDir)
	if err != nil {
		return err
	}
	tmp := p + ".e2e.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write trust config: %w", err)
	}
	return os.Rename(tmp, p)
}

var createdSessionRe = regexp.MustCompile(`Created session '([^']+)'`)

// startTarget runs `tmux-cli start` in the target dir (auto-launching claude
// with the seeded trust + bypass) and returns the exact session name.
func startTarget(targetDir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "tmux-cli"
	}
	c := exec.Command(self, "start")
	c.Dir = targetDir
	out, err := c.CombinedOutput()
	text := string(out)
	if err != nil {
		return "", fmt.Errorf("tmux-cli start: %v: %s", err, strings.TrimSpace(text))
	}
	m := createdSessionRe.FindStringSubmatch(text)
	if m == nil {
		return "", fmt.Errorf("could not parse session name from start output: %s", strings.TrimSpace(text))
	}
	return m[1], nil
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

// waitForIdlePrompt polls the target pane for a stable idle ❯ with no spinner.
func waitForIdlePrompt(pane string, timeout time.Duration) bool {
	spinners := []string{"Cogitating", "Germinating", "Gitifying", "Transfiguring", "Brewed", "esc to interrupt"}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := tmuxOut("capture-pane", "-p", "-t", pane)
		busy := false
		for _, s := range spinners {
			if strings.Contains(out, s) {
				busy = true
				break
			}
		}
		if !busy && strings.Contains(out, "❯") {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// doHandshake sends the init prompt (two-step) and verifies the channel by
// finding the handshake token + notify success line in the target log. Retries
// the prompt once before giving up.
func doHandshake(pane, session, logPath string, wait time.Duration) bool {
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
		for time.Now().Before(deadline) {
			if raw, err := os.ReadFile(logPath); err == nil {
				s := string(raw)
				if strings.Contains(s, token) && strings.Contains(s, "Notified orchestrator pane") {
					return true
				}
			}
			time.Sleep(2 * time.Second)
		}
		return false
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

func buildInitPrompt(session, token string) string {
	return strings.Join([]string{
		"You are running UNDER the tmux-cli e2e-evaluator orchestrator (an automated self-test harness), not a normal interactive user. For the ENTIRE run, report every milestone by running this shell command via your Bash tool: tmux-cli notify-orchestrator \"<msg>\". The TMUX_CLI_ORCHESTRATOR_PANE env var is already set, so it just works. Do NOT hand-roll tmux send-keys.",
		"",
		"Milestone vocabulary you MUST emit (exact strings, one notify per milestone as you reach it): discovery-done | roadmap-generated | preflight-passed | goals-dispatched | goals-done | app-up. On any failure emit: goal-<id> failed: <reason>.",
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
// and removes orphan /tmp dirs. Returns the names reaped.
func reapStaleSessions(scenario string) []string {
	names, err := tmuxOut("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	stale := e2e.SelectStaleSessions(strings.Split(names, "\n"))
	var reaped []string
	for _, s := range stale {
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
	// Orphan /tmp/<scenario>-* dirs whose session is already gone.
	if matches, _ := filepath.Glob(filepath.Join("/tmp", scenario+"-*")); matches != nil {
		for _, d := range matches {
			if isDir(d) {
				_ = os.RemoveAll(d)
			}
		}
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
