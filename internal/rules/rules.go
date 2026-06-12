// Package rules resolves which rule packs apply to a project. It is the
// deterministic half of the hybrid design: Go detects stack/capability
// signals and emits the applicable pack file list; the consuming agent reads
// the listed markdown/YAML files. Packs are materialized by setup into
// .tmux-cli/rules/ (see internal/setup.WriteRules) from the embedded
// catalogue; manifest.yaml describes pack conditions.
//
// Matching asymmetry (deliberate): capability signals (run target, database,
// frontend, auth flows) that cannot be detected match CONSERVATIVELY — the
// pack loads with a warning, because a missing safety convention is worse
// than an extra one. Stack signals (lang, framework) must be known to match —
// wrong-stack code rules misdirect rather than protect.
package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tri is a three-valued capability signal: detection may genuinely fail
// (missing discovery doc), and Unknown must stay distinguishable from No so
// the resolver can fall back conservatively.
type Tri int

const (
	TriUnknown Tri = iota
	TriYes
	TriNo
)

// Signals are the per-project facts pack conditions are evaluated against.
// Zero values mean unknown: empty Lang/Framework/RunTarget, NAuthFlows < 0.
type Signals struct {
	Lang        string
	Framework   string
	RunTarget   string // "docker" | "local" | "" (unknown)
	HasDatabase Tri
	HasFrontend Tri
	NAuthFlows  int // -1 = unknown
}

// Condition is a pack's structured `when` clause. All set fields must hold
// (AND). A nil Condition always holds.
type Condition struct {
	RunTarget    string `yaml:"run_target"`
	HasDatabase  *bool  `yaml:"has_database"`
	HasFrontend  *bool  `yaml:"has_frontend"`
	MinAuthFlows *int   `yaml:"min_auth_flows"`
	Lang         string `yaml:"lang"`
	Framework    string `yaml:"framework"`
}

// Pack is one manifest entry. Conventions are planner-binding rule files;
// CodeRules are spec/implementation/review catalogue files.
type Pack struct {
	ID          string     `yaml:"id"`
	When        *Condition `yaml:"when"`
	Conventions []string   `yaml:"conventions"`
	CodeRules   []string   `yaml:"code_rules"`
}

// Manifest is the parsed rules manifest.
type Manifest struct {
	Version int    `yaml:"version"`
	Packs   []Pack `yaml:"packs"`
}

// File kinds emitted by Resolve.
const (
	KindConvention = "convention"
	KindCodeRules  = "code-rules"
)

// ResolvedFile is one applicable rule file. Path is relative to the project
// root (".tmux-cli/rules/<pack>/<file>") so agents can Read it directly.
type ResolvedFile struct {
	Pack string `json:"pack"`
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// RulesDir is the on-disk rules root relative to the project root.
const RulesDir = ".tmux-cli/rules"

// LoadManifest reads and parses manifest.yaml under the project's rules dir.
func LoadManifest(projectRoot string) (*Manifest, error) {
	p := filepath.Join(projectRoot, RulesDir, "manifest.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("rules manifest unreadable (run `tmux-cli project init` to materialize %s): %w", RulesDir, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("rules manifest invalid: %w", err)
	}
	return &m, nil
}

// Resolve evaluates every pack condition against sig and returns the ordered
// applicable file list plus human-readable warnings for conservative matches.
// Project-local rules under .tmux-cli/rules/local/{conventions,code-rules}/
// are always appended last (so they read as overrides).
func Resolve(projectRoot string, m *Manifest, sig Signals) ([]ResolvedFile, []string, error) {
	var files []ResolvedFile
	var warnings []string

	for _, pack := range m.Packs {
		ok, warn := matches(pack.When, sig)
		if warn != "" {
			warnings = append(warnings, fmt.Sprintf("pack %s: %s", pack.ID, warn))
		}
		if !ok {
			continue
		}
		for _, f := range pack.Conventions {
			files = append(files, resolvedFile(pack.ID, KindConvention, f))
		}
		for _, f := range pack.CodeRules {
			files = append(files, resolvedFile(pack.ID, KindCodeRules, f))
		}
	}

	local, err := localFiles(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, local...)

	for _, f := range files {
		if _, err := os.Stat(filepath.Join(projectRoot, f.Path)); err != nil {
			return nil, nil, fmt.Errorf("resolved rule file missing on disk (re-run `tmux-cli project init`): %s", f.Path)
		}
	}
	return files, warnings, nil
}

func resolvedFile(packID, kind, name string) ResolvedFile {
	return ResolvedFile{Pack: packID, Kind: kind, Path: filepath.ToSlash(filepath.Join(RulesDir, packID, name))}
}

// matches reports whether the condition holds for sig, with a non-empty
// warning when it holds only via the conservative unknown-signal fallback.
func matches(c *Condition, sig Signals) (bool, string) {
	if c == nil {
		return true, ""
	}
	var conservative []string

	if c.RunTarget != "" {
		switch sig.RunTarget {
		case c.RunTarget:
		case "":
			conservative = append(conservative, "run target unknown")
		default:
			return false, ""
		}
	}
	if c.HasDatabase != nil {
		switch sig.HasDatabase {
		case TriUnknown:
			conservative = append(conservative, "database presence unknown")
		case TriYes:
			if !*c.HasDatabase {
				return false, ""
			}
		case TriNo:
			if *c.HasDatabase {
				return false, ""
			}
		}
	}
	if c.HasFrontend != nil {
		switch sig.HasFrontend {
		case TriUnknown:
			conservative = append(conservative, "frontend presence unknown")
		case TriYes:
			if !*c.HasFrontend {
				return false, ""
			}
		case TriNo:
			if *c.HasFrontend {
				return false, ""
			}
		}
	}
	if c.MinAuthFlows != nil {
		switch {
		case sig.NAuthFlows < 0:
			conservative = append(conservative, "auth flow count unknown")
		case sig.NAuthFlows < *c.MinAuthFlows:
			return false, ""
		}
	}
	// Stack signals: must be KNOWN to match — no conservative fallback.
	if c.Lang != "" && !strings.EqualFold(sig.Lang, c.Lang) {
		return false, ""
	}
	if c.Framework != "" && !strings.EqualFold(sig.Framework, c.Framework) {
		return false, ""
	}

	if len(conservative) > 0 {
		return true, "included conservatively (" + strings.Join(conservative, ", ") + ")"
	}
	return true, ""
}

// localFiles returns project-local rules: .tmux-cli/rules/local/conventions/
// and .tmux-cli/rules/local/code-rules/. The local tree is user-owned — setup
// never writes or deletes it.
func localFiles(projectRoot string) ([]ResolvedFile, error) {
	var out []ResolvedFile
	for sub, kind := range map[string]string{"conventions": KindConvention, "code-rules": KindCodeRules} {
		dir := filepath.Join(projectRoot, RulesDir, "local", sub)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, ResolvedFile{
				Pack: "local",
				Kind: kind,
				Path: filepath.ToSlash(filepath.Join(RulesDir, "local", sub, e.Name())),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Detect derives Signals from the project on disk. Stack: project manifests
// (composer.json, go.mod, ...). Capabilities: the discovery documents under
// docs/architecture/ (test-environment.md, cross-cutting.md). Parsing here
// deliberately mirrors taskvisor's test-environment.md readers but stays
// independent: this package serves resolve-time pack selection, taskvisor
// serves dispatch-time command wrapping, and the two must not entangle.
func Detect(projectRoot string) Signals {
	sig := Signals{NAuthFlows: -1}
	sig.Lang, sig.Framework = detectStack(projectRoot)

	if body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "test-environment.md")); err == nil {
		s := string(body)
		sig.RunTarget = parseRunTarget(s)
		sig.HasDatabase = parseHasDatabase(s)
		sig.HasFrontend = parseHasFrontend(s)
		if sig.Framework == "" && strings.Contains(strings.ToLower(s), "symfony") {
			sig.Lang, sig.Framework = "php", "symfony"
		}
	}
	if body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "cross-cutting.md")); err == nil {
		sig.NAuthFlows = parseAuthFlowCount(string(body))
	}
	return sig
}

func detectStack(projectRoot string) (lang, framework string) {
	if data, err := os.ReadFile(filepath.Join(projectRoot, "composer.json")); err == nil {
		lang = "php"
		var manifest struct {
			Require map[string]string `json:"require"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			for pkg := range manifest.Require {
				if strings.HasPrefix(pkg, "symfony/") {
					framework = "symfony"
					break
				}
				if strings.HasPrefix(pkg, "laravel/") {
					framework = "laravel"
					break
				}
			}
		}
		return lang, framework
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
		return "go", ""
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "Cargo.toml")); err == nil {
		return "rust", ""
	}
	for _, f := range []string{"pyproject.toml", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(projectRoot, f)); err == nil {
			return "python", ""
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "package.json")); err == nil {
		return "node", ""
	}
	return "", ""
}

// parseRunTarget reads the "Run Target:" line; "" when the line is absent.
func parseRunTarget(body string) string {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "run target") {
			continue
		}
		if idx := strings.Index(low, ":"); idx >= 0 {
			if strings.Contains(low[idx:], "docker") {
				return "docker"
			}
			return "local"
		}
	}
	return ""
}

// parseHasDatabase reads the "Test Database:" line; a none/n-a value means no
// database, a missing line means unknown.
func parseHasDatabase(body string) Tri {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "test database") {
			continue
		}
		idx := strings.Index(low, ":")
		if idx < 0 {
			continue
		}
		val := strings.TrimSpace(strings.Trim(strings.TrimSpace(low[idx+1:]), "*_`"))
		if val == "" || val == "none" || val == "n/a" || val == "-" {
			return TriNo
		}
		return TriYes
	}
	return TriUnknown
}

// parseHasFrontend reads the "Playwright" status line, mirroring the
// generation template's HAS_FRONTEND / Playwright-availability gate.
func parseHasFrontend(body string) Tri {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "playwright") {
			continue
		}
		if strings.Contains(low, "not applicable") || strings.Contains(low, "not installed") || strings.Contains(low, "n/a") {
			return TriNo
		}
		if strings.Contains(low, "installed") || strings.Contains(low, "configured") || strings.Contains(low, "needs") {
			return TriYes
		}
	}
	return TriUnknown
}

// parseAuthFlowCount counts list items under the "## Auth Flows" heading of
// cross-cutting.md; -1 when the section is absent.
func parseAuthFlowCount(body string) int {
	lines := strings.Split(body, "\n")
	inSection := false
	count := 0
	found := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if inSection {
				break
			}
			if strings.Contains(strings.ToLower(trimmed), "auth flows") {
				inSection = true
				found = true
			}
			continue
		}
		if !inSection {
			continue
		}
		low := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") || startsWithDigitDot(trimmed) {
			if strings.Contains(low, "none") || strings.Contains(low, "no auth") {
				continue
			}
			count++
		}
	}
	if !found {
		return -1
	}
	return count
}

func startsWithDigitDot(s string) bool {
	if len(s) < 2 || s[0] < '0' || s[0] > '9' {
		return false
	}
	rest := strings.TrimLeft(s, "0123456789")
	return strings.HasPrefix(rest, ".")
}
