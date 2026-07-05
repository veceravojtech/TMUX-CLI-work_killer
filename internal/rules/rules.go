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

// MarshalJSON renders a Tri as a legible string ("unknown"/"yes"/"no") so the
// `rules resolve --signals` dump is readable by the planner XML rather than
// leaking the 0/1/2 integers. Unknown stays distinct from No.
func (t Tri) MarshalJSON() ([]byte, error) {
	switch t {
	case TriYes:
		return []byte(`"yes"`), nil
	case TriNo:
		return []byte(`"no"`), nil
	default:
		return []byte(`"unknown"`), nil
	}
}

// Signals are the per-project facts pack conditions are evaluated against.
// Zero values mean unknown: empty Lang/Framework/RunTarget, Tri fields
// TriUnknown, NAuthFlows < 0, NBoundedContexts < 0. The json tags are the
// authority for the `rules resolve --signals` dump.
type Signals struct {
	Lang             string `json:"lang"`
	Framework        string `json:"framework"`
	Architecture     string `json:"architecture"` // "ddd" | "basic" | "" (unknown); see matches for the ddd default
	RunTarget        string `json:"run_target"`   // "docker" | "local" | "" (unknown)
	HasDatabase      Tri    `json:"has_database"`
	HasFrontend      Tri    `json:"has_frontend"`
	FrontendMode     string `json:"frontend_mode"` // "vue" | "twig" | "none" | "" (unknown); a stack-style signal
	NAuthFlows       int    `json:"n_auth_flows"`  // -1 = unknown
	UsesJWT          Tri    `json:"uses_jwt"`
	HasMailer        Tri    `json:"has_mailer"`
	HasMessenger     Tri    `json:"has_messenger"`
	HasHTTPClient    Tri    `json:"has_http_client"`
	NBoundedContexts int    `json:"n_bounded_contexts"` // -1 = unknown
}

// Condition is a pack's structured `when` clause. All set fields must hold
// (AND). A nil Condition always holds.
type Condition struct {
	RunTarget    string  `yaml:"run_target"`
	HasDatabase  *bool   `yaml:"has_database"`
	HasFrontend  *bool   `yaml:"has_frontend"`
	MinAuthFlows *int    `yaml:"min_auth_flows"`
	Lang         string  `yaml:"lang"`
	Framework    string  `yaml:"framework"`
	FrontendMode *string `yaml:"frontend_mode"` // stack-style: must be KNOWN to match
	Architecture string  `yaml:"architecture"`  // "ddd" | "basic"; unknown signal matches only "ddd" (the generator default), with a warning
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
	// FrontendMode is a stack signal: an unknown mode must NOT match (no
	// conservative include-with-warning) — a wrong frontend rule misdirects.
	if c.FrontendMode != nil {
		if sig.FrontendMode == "" || !strings.EqualFold(sig.FrontendMode, *c.FrontendMode) {
			return false, ""
		}
	}
	// Architecture is stack-style with ONE deliberate asymmetry: an unknown
	// signal still matches an `architecture: ddd` condition (with a warning),
	// because ddd is the topology this generator scaffolds by default and the
	// pre-architecture-signal packs loaded that way. "basic" (and any future
	// value) must be KNOWN — declared by discovery — to match.
	if c.Architecture != "" {
		switch {
		case strings.EqualFold(sig.Architecture, c.Architecture):
		case sig.Architecture == "" && strings.EqualFold(c.Architecture, "ddd"):
			conservative = append(conservative, "architecture unknown (assuming ddd)")
		default:
			return false, ""
		}
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
	sig := Signals{NAuthFlows: -1, NBoundedContexts: -1}

	var testEnv string
	if body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "test-environment.md")); err == nil {
		testEnv = string(body)
	}

	// Stack resolution tiers, in priority order:
	//   1. an explicit **Stack:** line in test-environment.md (authoritative);
	//   2. project-manifest detection (detectStack);
	//   3. a symfony mention in test-environment.md (last-resort greenfield).
	stackFound := false
	if lang, framework, ok := parseStackLine(testEnv); ok {
		sig.Lang, sig.Framework, stackFound = lang, framework, true
	} else {
		sig.Lang, sig.Framework = detectStack(projectRoot)
	}

	if testEnv != "" {
		sig.RunTarget = parseRunTarget(testEnv)
		sig.HasDatabase = parseHasDatabase(testEnv)
		sig.HasFrontend = parseHasFrontend(testEnv)
		sig.FrontendMode = parseFrontendMode(testEnv)
		if !stackFound && sig.Framework == "" && strings.Contains(strings.ToLower(testEnv), "symfony") {
			sig.Lang, sig.Framework = "php", "symfony"
		}
	}

	// Architecture: an explicit "Architecture:" line in test-environment.md is
	// authoritative; otherwise DDD markers on disk (a bounded-context inventory
	// doc, or contexts/*/composer.json path-packages) derive "ddd". Anything
	// else stays unknown — matches() then still admits `architecture: ddd`
	// packs conservatively, so pre-signal projects keep resolving as before.
	sig.Architecture = parseArchitecture(testEnv)
	if sig.Architecture == "" && hasDDDMarkers(projectRoot) {
		sig.Architecture = "ddd"
	}

	var crossCutting string
	if body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "cross-cutting.md")); err == nil {
		crossCutting = string(body)
		sig.NAuthFlows = parseAuthFlowCount(crossCutting)
	}

	// Capability signals: composer require keys are authoritative; UsesJWT also
	// consults the cross-cutting security section.
	sig.UsesJWT = parseUsesJWT(projectRoot, crossCutting)
	sig.HasMailer = parseHasCapability(projectRoot, "mailer")
	sig.HasMessenger = parseHasCapability(projectRoot, "messenger")
	sig.HasHTTPClient = parseHasCapability(projectRoot, "http-client")
	sig.NBoundedContexts = parseBoundedContextCount(projectRoot)

	return sig
}

// parseStackLine extracts lang/framework from an explicit "**Stack:** <lang>-<framework>"
// line (split on the FIRST hyphen; no hyphen → framework empty). ok is false
// when no usable Stack line exists, so Detect falls through to manifest
// detection. An empty lang is treated as no line.
func parseStackLine(body string) (lang, framework string, ok bool) {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimLeft(raw, "*# \t")
		if !strings.HasPrefix(strings.ToLower(line), "stack:") {
			continue
		}
		val := strings.TrimSpace(line[len("stack:"):])
		val = stripQualifier(strings.ToLower(strings.TrimSpace(strings.Trim(val, "*_` "))))
		if val == "" {
			continue
		}
		if dash := strings.Index(val, "-"); dash >= 0 {
			lang = strings.TrimSpace(val[:dash])
			framework = strings.TrimSpace(val[dash+1:])
		} else {
			lang = val
		}
		if lang == "" {
			continue
		}
		return lang, framework, true
	}
	return "", "", false
}

// parseArchitecture extracts the declared architecture ("ddd" | "basic") from
// an "**Architecture:** <value>" line (parseStackLine idiom: strip markdown
// decoration, drop a trailing parenthetical qualifier). Any other value is
// ignored — an unrecognized architecture must stay unknown, not misroute packs.
func parseArchitecture(body string) string {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimLeft(raw, "*# \t")
		if !strings.HasPrefix(strings.ToLower(line), "architecture:") {
			continue
		}
		val := stripQualifier(strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(line[len("architecture:"):]), "*_` "))))
		switch val {
		case "ddd", "basic":
			return val
		}
	}
	return ""
}

// hasDDDMarkers reports on-disk evidence of the DDD monorepo topology: a
// bounded-context inventory doc, or any contexts/<bc>/composer.json
// path-package. A flat basic app has neither.
func hasDDDMarkers(projectRoot string) bool {
	if _, err := os.Stat(filepath.Join(projectRoot, "docs", "architecture", "bounded-contexts.md")); err == nil {
		return true
	}
	matches, err := filepath.Glob(filepath.Join(projectRoot, "contexts", "*", "composer.json"))
	return err == nil && len(matches) > 0
}

// composerRequires reads composer.json's `require` map. found is false when
// composer.json is absent or unparseable — the caller distinguishes "no
// composer" (unknown) from "composer present without the key" (no).
func composerRequires(projectRoot string) (require map[string]string, found bool) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "composer.json"))
	if err != nil {
		return nil, false
	}
	var manifest struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil, false
	}
	return manifest.Require, true
}

// parseHasCapability returns TriYes when any composer require key contains
// needle, TriNo when composer.json is present without it, TriUnknown when no
// composer.json exists. Capability presence is conservative: composer is the
// authority for a Symfony component being installed.
func parseHasCapability(projectRoot, needle string) Tri {
	req, ok := composerRequires(projectRoot)
	if !ok {
		return TriUnknown
	}
	for pkg := range req {
		if strings.Contains(strings.ToLower(pkg), needle) {
			return TriYes
		}
	}
	return TriNo
}

// parseUsesJWT resolves JWT usage from two sources: a JWT library in composer
// (lexik/..., firebase/php-jwt) is an authoritative yes; otherwise the
// cross-cutting security section decides (JWT mention → yes, section present
// without it → no). Unknown only when neither composer nor a security section
// speaks to it.
func parseUsesJWT(projectRoot, crossCutting string) Tri {
	composerSeen := false
	if req, ok := composerRequires(projectRoot); ok {
		composerSeen = true
		for pkg := range req {
			if strings.Contains(strings.ToLower(pkg), "jwt") {
				return TriYes
			}
		}
	}
	if sec, found := sectionBody(crossCutting, "security"); found {
		if strings.Contains(strings.ToLower(sec), "jwt") {
			return TriYes
		}
		return TriNo
	}
	if composerSeen {
		return TriNo
	}
	return TriUnknown
}

// parseBoundedContextCount counts the level-3 (BC) headings under the
// "## Bounded Context Inventory" H2 of bounded-contexts.md (the format
// project-discovery writes and task-plan-generate reads). When that H2 is
// absent it falls back to counting all `### ` headings. -1 when the file is
// absent (unknown).
func parseBoundedContextCount(projectRoot string) int {
	body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "bounded-contexts.md"))
	if err != nil {
		return -1
	}
	lines := strings.Split(string(body), "\n")
	inInventory, sawInventory, count := false, false, 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") { // an H2 boundary
			low := strings.ToLower(trimmed)
			inInventory = strings.Contains(low, "inventory") || strings.Contains(low, "bounded context")
			if inInventory {
				sawInventory = true
			}
			continue
		}
		if inInventory && strings.HasPrefix(trimmed, "### ") {
			count++
		}
	}
	if !sawInventory {
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "### ") {
				count++
			}
		}
	}
	return count
}

// sectionBody returns the lines under the first markdown heading whose text
// contains needle (case-insensitive), up to the next heading of the same or
// shallower level (deeper sub-headings are kept). found reports whether such a
// section exists.
func sectionBody(body, needle string) (string, bool) {
	var out []string
	inSection, found, level := false, false, 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if lvl := headingLevel(trimmed); lvl > 0 {
			if inSection && lvl <= level {
				break
			}
			if !inSection && strings.Contains(strings.ToLower(trimmed), needle) {
				inSection, found, level = true, true, lvl
				continue
			}
			if inSection {
				out = append(out, line)
			}
			continue
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), found
}

// headingLevel returns the markdown heading level (number of leading '#'
// followed by a space), or 0 when the line is not a heading.
func headingLevel(trimmed string) int {
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n > 0 && n < len(trimmed) && trimmed[n] == ' ' {
		return n
	}
	return 0
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

// stripQualifier drops a trailing parenthetical reason that discovery docs
// append to a field value, e.g. "none (no persistence)" -> "none" or
// "php-symfony (base web app)" -> "php-symfony", so callers can match the
// leading token rather than the whole annotated line.
func stripQualifier(val string) string {
	if i := strings.IndexByte(val, '('); i >= 0 {
		val = strings.TrimSpace(val[:i])
	}
	return val
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
		val := stripQualifier(strings.TrimSpace(strings.Trim(strings.TrimSpace(low[idx+1:]), "*_`")))
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

// parseFrontendMode resolves the discrete frontend mode (vue|twig|none, or ""
// for unknown). An explicit "**Frontend:** <mode>" line (mirroring the
// parseStackLine idiom) is authoritative when it names one of the accepted
// modes; any other value is ignored and the mode is DERIVED from frontend
// presence via parseHasFrontend (so the two signals never disagree): a present
// frontend defaults to the generic "vue" unless the body names "twig", an
// absent frontend is "none", and an undetectable one stays "".
func parseFrontendMode(body string) string {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimLeft(raw, "*# \t")
		if !strings.HasPrefix(strings.ToLower(line), "frontend:") {
			continue
		}
		val := stripQualifier(strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(line[len("frontend:"):]), "*_` "))))
		switch val {
		case "vue", "twig", "none":
			return val
		}
		// Unrecognized value: ignore this line and keep scanning, then fall
		// through to derivation (mirrors parseStackLine skipping unusable lines).
	}
	switch parseHasFrontend(body) {
	case TriYes:
		if strings.Contains(strings.ToLower(body), "twig") {
			return "twig"
		}
		return "vue"
	case TriNo:
		return "none"
	default:
		return ""
	}
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
