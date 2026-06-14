package rules

// layout.go resolves a project's source-root globs and infra-layer directory
// name (the "Layout") and expands the {src}/{infra} tokens carried by the
// catalogue's applies_to globs against it. Expansion runs at LOAD time in the
// CLI layer (cmd/tmux-cli/rules.go), BEFORE Match/Check — so the routing engine
// (coderules.go / check.go) and its signature-stable, test-pinned glob handling
// stay untouched (design §6.4: Go owns ALL glob resolution; the resolver is
// pure read-only and never interactive — the discovery skill owns the ASK).
//
// Token contract:
//   - {src}   → one glob per resolved source root (de-duplicated, order-preserving).
//   - {infra} → the resolved infra-layer directory name.
// A glob carrying NEITHER token passes through byte-unchanged, so a flat-src
// greenfield project under DefaultLayout reproduces today's exact behavior.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Layout is a project's resolved source topology: the source-root globs code
// lives under, and the infra-layer directory name. SourceRoots entries are
// themselves globs (e.g. "contexts/*/src"); compileGlob's `*`→`[^/]*` keeps
// each `*` within a single path segment, so a monorepo root matches correctly.
type Layout struct {
	SourceRoots []string
	InfraLayer  string
}

// DefaultLayout is the greenfield skeleton the generator scaffolds: a flat
// top-level src/ with an Infrastructure layer. Expanding the tokenized
// catalogue against it reproduces the pre-tokenization behavior exactly.
func DefaultLayout() Layout {
	return Layout{SourceRoots: []string{"src"}, InfraLayer: "Infrastructure"}
}

// ResolveLayout derives a project's Layout, read-only, in precedence order:
//
//  1. docs/architecture/layout.md `## Layers` doc (authoritative — a monorepo's
//     root composer.json cannot enumerate per-context source roots).
//  2. composer.json autoload.psr-4 directory values (best-effort source roots;
//     infra-layer stays the default).
//  3. DefaultLayout() + an ASK warning (the resolver never silently assumes a
//     non-greenfield layout — discovery is responsible for asking).
//
// The second return is human-readable warnings: a parse note when a layout.md
// exists but carries no usable `## Layers` section, and the "discovery should
// ASK" note when nothing resolves. Never interactive.
func ResolveLayout(projectRoot string) (Layout, []string) {
	var warnings []string

	if body, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "layout.md")); err == nil {
		if layout, ok := parseLayersDoc(string(body)); ok {
			return layout, warnings
		}
		warnings = append(warnings,
			"docs/architecture/layout.md present but no parseable '## Layers' section — falling back to composer/default")
	}

	if roots, ok := composerSourceRoots(projectRoot); ok {
		return Layout{SourceRoots: roots, InfraLayer: DefaultLayout().InfraLayer}, warnings
	}

	warnings = append(warnings,
		"layout unresolved — assuming greenfield skeleton (src/Infrastructure); discovery should ASK")
	return DefaultLayout(), warnings
}

// parseLayersDoc reads the `## Layers` section of a layout.md body for two
// lines — `Source roots: <comma-list>` and `Infrastructure layer: <name>` —
// using the same heading/section/line-scan idioms as Detect (sectionBody +
// headingLevel + parseStackLine-style comma/Trim cleanup). ok is false when the
// section or either line is absent, so ResolveLayout falls through.
func parseLayersDoc(body string) (Layout, bool) {
	section, found := sectionBody(body, "layers")
	if !found {
		return Layout{}, false
	}

	var roots []string
	var infra string
	for _, raw := range strings.Split(section, "\n") {
		// Strip markdown list/heading decoration the same way parseStackLine does.
		line := strings.TrimLeft(raw, "*-+# \t")
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "source roots:"):
			roots = splitTrimList(line[len("source roots:"):])
		case strings.HasPrefix(low, "infrastructure layer:"):
			infra = strings.Trim(strings.TrimSpace(line[len("infrastructure layer:"):]), "*_` ")
		}
	}

	if len(roots) == 0 || infra == "" {
		return Layout{}, false
	}
	return Layout{SourceRoots: roots, InfraLayer: infra}, true
}

// splitTrimList comma-splits a value list and trims markdown/whitespace
// decoration off each entry (mirrors parseStackLine's cleanup), dropping empties.
func splitTrimList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		v := strings.Trim(strings.TrimSpace(part), "*_` ")
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// composerSourceRoots reads composer.json's autoload.psr-4 directory values as
// source roots (trailing `/` trimmed, de-duplicated, sorted for determinism).
// found is false when composer.json is absent, unparseable, or declares no
// psr-4 dirs — the caller then falls through to the default. Best-effort only:
// the `## Layers` doc is authoritative for monorepos (a path-package monorepo's
// root composer.json won't list per-context source roots).
func composerSourceRoots(projectRoot string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "composer.json"))
	if err != nil {
		return nil, false
	}
	var manifest struct {
		Autoload struct {
			PSR4 map[string]string `json:"psr-4"`
		} `json:"autoload"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil, false
	}

	seen := map[string]bool{}
	var roots []string
	for _, dir := range manifest.Autoload.PSR4 {
		d := strings.TrimRight(strings.TrimSpace(dir), "/")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		roots = append(roots, d)
	}
	if len(roots) == 0 {
		return nil, false
	}
	sort.Strings(roots)
	return roots, true
}

// ExpandGlob expands a single applies_to pattern against a Layout. It replaces
// {infra} with the infra-layer name, then — if {src} remains — emits one glob
// per source root (de-duplicated, order-preserving). A pattern with neither
// token returns a single, byte-unchanged glob.
func ExpandGlob(pattern string, layout Layout) []string {
	p := strings.ReplaceAll(pattern, "{infra}", layout.InfraLayer)
	if !strings.Contains(p, "{src}") {
		return []string{p}
	}

	seen := map[string]bool{}
	var out []string
	for _, root := range layout.SourceRoots {
		g := strings.ReplaceAll(p, "{src}", root)
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

// ExpandAppliesTo returns a copy of rules with each rule's AppliesTo replaced by
// the flat, de-duplicated expansion of its globs against layout. It operates on
// COPIES — the caller's slice and its rules' AppliesTo backing arrays are never
// mutated (provenance and every other field are carried verbatim).
func ExpandAppliesTo(rules []CodeRule, layout Layout) []CodeRule {
	out := make([]CodeRule, len(rules))
	for i, r := range rules {
		cp := r // struct copy; AppliesTo is replaced with a fresh slice below

		seen := map[string]bool{}
		var globs []string
		for _, pat := range r.AppliesTo {
			for _, g := range ExpandGlob(pat, layout) {
				if seen[g] {
					continue
				}
				seen[g] = true
				globs = append(globs, g)
			}
		}
		cp.AppliesTo = globs
		out[i] = cp
	}
	return out
}
