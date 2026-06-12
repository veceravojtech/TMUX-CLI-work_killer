package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WriteGoalMD(goalDir, description, phase, lane string, acceptance, validate []string, preconditions []Precondition, context, notInScope string, investigators []Investigator) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", description)

	if phase != "" {
		fmt.Fprintf(&b, "\n## Phase\n\n%s\n", phase)
	}

	// Lane (G5): surfaced for XML readers; the body is exactly the bare lane
	// string. An absent lane emits NO section — the zero-change contract for
	// lane-absent goals.
	if lane != "" {
		fmt.Fprintf(&b, "\n## Lane\n\n%s\n", lane)
	}

	b.WriteString("\n## Acceptance Criteria\n\n")
	for _, a := range acceptance {
		fmt.Fprintf(&b, "- %s\n", a)
	}

	b.WriteString("\n## Validation Rules\n\n")
	if len(validate) > 0 {
		for _, v := range validate {
			fmt.Fprintf(&b, "- %s\n", v)
		}
	} else {
		b.WriteString("(none)\n")
	}

	if len(preconditions) > 0 {
		b.WriteString("\n## Preconditions\n\n")
		for _, p := range preconditions {
			if p.Remedy != "" {
				fmt.Fprintf(&b, "- [%s] %s — %s\n", p.Kind, p.Spec, p.Remedy)
			} else {
				fmt.Fprintf(&b, "- [%s] %s\n", p.Kind, p.Spec)
			}
		}
	}

	if context != "" {
		fmt.Fprintf(&b, "\n## Context\n\n%s\n", context)
	}

	if notInScope != "" {
		fmt.Fprintf(&b, "\n## Not In Scope\n\n%s\n", notInScope)
	}

	// Investigation Config: read by investigate.xml (requires >=2 ### Investigator
	// subsections) and by parseGoalFindings. When no investigators are supplied,
	// derive 2-4 from the validate rules so the section is never missing.
	list := investigators
	if len(list) == 0 {
		// WriteGoalMD's signature is fixed (no fsRoot param) — recover the
		// project root from the canonical goalDir shape, same as the B2b gate.
		list = deriveInvestigators(ownSuiteFSRoot(goalDir), validate, nil)
		// Event goals get an extra, non-skippable emission investigator that
		// asserts the PRODUCER actually constructs/dispatches the event (catches
		// dead choreography). Only when the planner supplied no explicit
		// investigators — respect an explicit list. Re-cap so total stays <=4;
		// emission-check's -1 priority guarantees it survives truncation.
		if emi, ok := deriveEmissionInvestigator(phase, description, acceptance, validate); ok {
			list = capInvestigators(append(list, emi))
		}
	}

	// Mandatory own-suite-green gate (B2b): a code goal (declares src/|app/
	// deliverables, not a gate phase) ALWAYS gets an investigator that runs its
	// OWN integration+functional suite via phpunit — appended HERE, after the
	// list is built, so it applies to BOTH the explicit-config and the
	// deriveInvestigators-fallback paths and cannot be omitted by a planner. The
	// selector resolves the scope to existing test dirs under the project root;
	// when the goal owns no integration/functional suite (Empty), the gate SKIPS
	// rather than emit a paths-less phpunit that would run the whole suite. The
	// re-cap keeps the section at <=4 with both -1 pins (own-suite-green AND any
	// emission-check) surviving truncation — the B2b/B3 compose contract.
	if producesAppCode(phase, acceptance, validate, context) && !hasInvestigatorType(list, "own-suite-green") {
		deliverables := DeliverablesFromGoal(Goal{Acceptance: acceptance, Validate: validate})
		scope := SelectOwnSuiteScope(deliverables, ownSuiteFSRoot(goalDir))
		if !scope.Empty {
			list = capInvestigators(append(list, ownSuiteGateInvestigator(scope.Paths)))
		}
	}

	renderInvestigationConfig(&b, list, ResolveExecRuntime(ownSuiteFSRoot(goalDir)))

	// C10: incremental re-validation reuses a prior cycle's pass when its input
	// fingerprint (rule + in-scope changed files + preconditions) is unchanged.
	// --full forces a full re-validation — re-running every check regardless of
	// fingerprint; the final cycle before overall pass also re-runs all checks.
	// Appended last so the preceding section order is stable.
	b.WriteString("\n## Re-validation\n\n")
	b.WriteString("Incremental: only failed checks and checks whose inputs changed are re-run on retry. `--full` forces full re-validation.\n")

	return atomicWrite(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0o644)
}

// renderInvestigationConfig writes the `## Investigation Config` section — the
// heading followed by one `### Investigator N` block per entry — into b. It is
// the SINGLE rendering shared by WriteGoalMD (creation) and
// EnsureInvestigationConfig (repair-at-dispatch) so the two can never drift;
// TestRenderInvestigationConfig_MatchesWriteGoalMDOutput guards parity. The
// output is byte-identical to the inline loop it was extracted from.
func renderInvestigationConfig(b *strings.Builder, list []Investigator, er ExecRuntime) {
	b.WriteString("\n## Investigation Config\n\n")
	for i, inv := range list {
		fmt.Fprintf(b, "### Investigator %d: %s\n", i+1, inv.Name)
		fmt.Fprintf(b, "- type: %s\n", inv.Type)
		if len(inv.Paths) > 0 {
			fmt.Fprintf(b, "- paths: %s\n", strings.Join(inv.Paths, ", "))
		}
		for _, c := range inv.Commands {
			// wrapCommand routes the command into the project's runtime (docker
			// app/node container or host) so the daemon — not the generating LLM —
			// is the deterministic source of truth. Local mode is a no-op.
			fmt.Fprintf(b, "- command: %s\n", wrapCommand(c, er))
		}
		fmt.Fprintf(b, "- Pass: %s\n", inv.Pass)
		fmt.Fprintf(b, "- Fail: %s\n", inv.Fail)
		if inv.Condition != "" {
			fmt.Fprintf(b, "- condition: %s\n", inv.Condition)
		}
		b.WriteString("\n")
	}
}

// countInvestigators inspects rendered goal.md markdown for the Investigation
// Config section. hasSection is true when a line is `## Investigation Config`
// (a level-2 heading at line start — `### Investigator` lines never match the
// `## ` prefix); n counts `### Investigator ` headings from that point until the
// next `## ` heading (or EOF). It mirrors the section-scoped `### ` counting
// parseGoalFindings (cmd/tmux-cli/session.go) performs, so EnsureInvestigationConfig
// repairs exactly when that downstream parser — and investigate.xml — would see <2.
func countInvestigators(md string) (hasSection bool, n int) {
	inSection := false
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			if strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Investigation Config" {
				hasSection = true
				inSection = true
			} else if inSection {
				break // the next level-2 heading closes the section
			}
			continue
		}
		if inSection && strings.HasPrefix(line, "### Investigator ") {
			n++
		}
	}
	return hasSection, n
}

// EnsureInvestigationConfig is the B4 repair-at-dispatch guard. It reads
// goalDir/goal.md and guarantees a `## Investigation Config` section with >=2
// `### Investigator` entries — the floor investigate.xml hard-requires — WITHOUT
// rewriting any other byte of the file. WriteGoalMD always emits a valid section
// at CREATION, but a planner re-write of goal.md can strip it post-creation; this
// re-asserts it at dispatch so the validator never hard-fails for missing/<2.
//
// Behavior (never panics, never blocks dispatch):
//   - goal.md unreadable/absent -> (false, nil): creation should have written it,
//     and writeDispatchMd has its own fallback, so repair must not fail dispatch.
//   - section present with n>=2 -> (false, nil): a valid (planner-provided)
//     section is preserved byte-for-byte, never overwritten.
//   - section missing or n<2    -> derive the fallback from validate (the same
//     deriveInvestigators used at creation, padded to >=2 project-aware against
//     projectRoot), render it via the shared renderInvestigationConfig, splice it
//     in (replace a malformed section in place; else insert before
//     `## Re-validation`; else before `## Not In Scope`; else append),
//     atomicWrite, return (true, nil).
//
// SPLICE, never regenerate from the Goal struct: the planner adds prose the struct
// does not carry, so only the one section's byte range is ever touched, and
// exactly one `## Investigation Config` heading always remains.
func EnsureInvestigationConfig(projectRoot, goalDir string, validate []string) (repaired bool, err error) {
	mdPath := filepath.Join(goalDir, "goal.md")
	data, readErr := os.ReadFile(mdPath)
	if readErr != nil {
		// Unreadable/absent: log+continue at the caller, never block dispatch.
		return false, nil
	}
	md := string(data)

	if hasSection, n := countInvestigators(md); hasSection && n >= 2 {
		return false, nil // valid section: preserve verbatim
	} else {
		var sb strings.Builder
		renderInvestigationConfig(&sb, deriveInvestigators(projectRoot, validate, nil), ResolveExecRuntime(projectRoot))
		newMD := spliceInvestigationConfig(md, sb.String(), hasSection)
		if werr := atomicWrite(mdPath, []byte(newMD), 0o644); werr != nil {
			return false, werr
		}
		return true, nil
	}
}

// spliceInvestigationConfig returns md with the rendered Investigation Config
// section asserted exactly once. When malformedPresent is true it replaces the
// existing `## Investigation Config` section in place (heading -> next `## `
// heading or EOF); otherwise it inserts the section before `## Re-validation`,
// else before `## Not In Scope`, else appends it. Only the one section's byte
// range is touched — every other section is carried through verbatim.
func spliceInvestigationConfig(md, section string, malformedPresent bool) string {
	lines := strings.Split(md, "\n")

	if malformedPresent {
		if start := indexOfHeading(lines, "Investigation Config"); start >= 0 {
			end := len(lines)
			for j := start + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
					end = j
					break
				}
			}
			return joinSections(lines[:start], section, lines[end:])
		}
		// hasSection true but no exact heading match (shouldn't happen): fall
		// through to insertion so the section is still asserted exactly once.
	}

	for _, target := range []string{"Re-validation", "Not In Scope"} {
		if at := indexOfHeading(lines, target); at >= 0 {
			return joinSections(lines[:at], section, lines[at:])
		}
	}
	return joinSections(lines, section, nil)
}

// SetGoalMDLane splices the `## Lane` section of goalDir/goal.md so its body is
// exactly the bare lane string, following the spliceInvestigationConfig pattern:
// only the one section's byte range is touched, every other section is carried
// through verbatim. When the section is absent (e.g. a hand-edited goal.md) it
// is inserted before `## Acceptance Criteria` — the position WriteGoalMD emits
// it at — else appended. The read error propagates: a demotion caller must not
// silently leave goals.yaml and goal.md divergent.
func SetGoalMDLane(goalDir, lane string) error {
	mdPath := filepath.Join(goalDir, "goal.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return err
	}
	section := fmt.Sprintf("## Lane\n\n%s\n", lane)
	lines := strings.Split(string(data), "\n")

	var newMD string
	if start := indexOfHeading(lines, "Lane"); start >= 0 {
		end := len(lines)
		for j := start + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		newMD = joinSections(lines[:start], section, lines[end:])
	} else if at := indexOfHeading(lines, "Acceptance Criteria"); at >= 0 {
		newMD = joinSections(lines[:at], section, lines[at:])
	} else {
		newMD = joinSections(lines, section, nil)
	}
	return atomicWrite(mdPath, []byte(newMD), 0o644)
}

// indexOfHeading returns the index of the first line that is the level-2 heading
// `## <title>` (trimmed), or -1. Matches a level-2 heading only — a `### ` line
// never satisfies the `## ` prefix test.
func indexOfHeading(lines []string, title string) int {
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") && strings.TrimSpace(strings.TrimPrefix(t, "## ")) == title {
			return i
		}
	}
	return -1
}

// joinSections assembles before + section + after with exactly one blank line
// between each and a single trailing newline. It strips boundary blank lines
// (before's trailing, after's leading+trailing) so repeated repairs never
// accumulate blank lines — the spacing is idempotent. The interior bytes of
// before/after are preserved exactly (split on "\n", rejoin on "\n").
func joinSections(before []string, section string, after []string) string {
	for len(before) > 0 && strings.TrimSpace(before[len(before)-1]) == "" {
		before = before[:len(before)-1]
	}
	for len(after) > 0 && strings.TrimSpace(after[0]) == "" {
		after = after[1:]
	}
	for len(after) > 0 && strings.TrimSpace(after[len(after)-1]) == "" {
		after = after[:len(after)-1]
	}
	var parts []string
	if len(before) > 0 {
		parts = append(parts, strings.Join(before, "\n"))
	}
	parts = append(parts, strings.Trim(section, "\n"))
	if len(after) > 0 {
		parts = append(parts, strings.Join(after, "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
}
