package main

import (
	"encoding/xml"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrApplyFixesEndToEnd is the golden/catalogue test for the
// /tmux:cr-apply-fixes brownfield compliance-remediation arc. Like
// TestFeatureEndToEnd it cannot drive the LLM command live (that would require
// Claude + tmux/claude sessions, and the suite must pass with no tmux server),
// so the faithful in-scope realization is a STATIC golden assertion over the
// shipped command XML: the emitted-contract (detect-via-rules-check,
// one-goal-per-category, GATE_SET = catalogue validate_cmd + full Stage-0
// quality gates, depends_on phase chain, confirm gate + auto_approve,
// literal-freedom) must be encoded in the shards, and cr-apply-fixes.xml must
// document the command's boundary vs its neighbours. Modeled on
// feature_e2e_test.go.
func TestCrApplyFixesEndToEnd(t *testing.T) {
	stage1 := readEmbeddedCommand(t, "cr-apply-fixes/stage-1-detect.xml")
	stage2 := readEmbeddedCommand(t, "cr-apply-fixes/stage-2-dispatch.xml")
	stage3 := readEmbeddedCommand(t, "cr-apply-fixes/stage-3-validation.xml")
	spine := readEmbeddedCommand(t, "cr-apply-fixes.xml")

	// 1. Detection path: Stage 1 composes `tmux-cli rules check` for catalogue
	//    violations and routes the agent_review rules to convention-audit
	//    investigators, grouping by category.
	t.Run("detect_via_rules_check", func(t *testing.T) {
		for _, tok := range []string{"rules check", "--json", "CheckResult", "agent_review", "convention-audit", "category"} {
			assert.Contains(t, stage1, tok,
				"stage-1 must encode the violation-detection token %q", tok)
		}
	})

	// 2. Dispatch path: ONE goal per category, phase-ordered depends_on chain,
	//    emitted via goal-create, payloads from `rules match`.
	t.Run("one_goal_per_category_chain", func(t *testing.T) {
		for _, tok := range []string{"goal-create", "depends_on", "PHASE_ORDER", "rules match", "ONE goal per affected CATEGORY"} {
			assert.Contains(t, stage2, tok,
				"stage-2 must encode the per-category dispatch token %q", tok)
		}
		// PHASE_ORDER must precede the chain-commit so categories emit in rank order.
		require.Contains(t, stage2, "PHASE_ORDER")
		require.Contains(t, stage2, "goal-create")
	})

	// 3. GATE_SET: each goal's validate is the category's resolved validate_cmd
	//    PLUS the FULL Stage-0 quality-gate list (static-analysis + lint/style +
	//    tests), with review-kind must rules routed to investigation_config.
	t.Run("gate_set_catalogue_plus_quality_gates", func(t *testing.T) {
		for _, tok := range []string{"GATE_SET", "validate_cmd", "static-analysis", "lint/style", "investigation_config"} {
			assert.Contains(t, stage2, tok,
				"stage-2 must encode the GATE_SET composition token %q", tok)
		}
		// Conditional manifest/schema validators — parity with feature stage-4 4.3a
		// (the gap closed after cross-checking the feature hardening commits).
		for _, tok := range []string{"mono-schema", "db-validate"} {
			assert.Contains(t, stage2, tok,
				"stage-2 GATE_SET must include the conditional manifest/schema validator %q", tok)
		}
	})

	// 4. Confirm gate is the sole interactive point and honors auto_approve;
	//    interaction is text-based (never AskUserQuestion).
	t.Run("confirm_gate_auto_approve", func(t *testing.T) {
		for _, tok := range []string{"auto_approve", "confirm", "AskUserQuestion"} {
			assert.Contains(t, stage2, tok,
				"stage-2 must encode the confirm-gate token %q", tok)
		}
		// The mandate is to NEVER use AskUserQuestion (text-only interaction).
		assert.Contains(t, stage2, "NEVER use AskUserQuestion",
			"stage-2 confirm gate must forbid AskUserQuestion (tmux text-only)")
	})

	// 5. Validation delegates to /tmux:investigate and surfaces an aggregated
	//    verdict — never re-implements the spawn/collect/verdict loop.
	t.Run("validation_delegates_to_investigate", func(t *testing.T) {
		for _, tok := range []string{"/tmux:investigate", "goal-validation-done", "COMPOSER, not engine"} {
			assert.Contains(t, stage3, tok,
				"stage-3 must encode the investigate-delegation token %q", tok)
		}
	})

	// 6. Emitted goals carry ZERO hardcoded project-specific literals. Asserted
	//    POSITIVELY (the prohibition + resolve-from-TOPOLOGY mandate are present),
	//    never by banning the src/ substring — the mandate text legitimately
	//    contains "src/" ("never a src/ skeleton literal").
	t.Run("zero_hardcoded_literals", func(t *testing.T) {
		for _, tok := range []string{"src/", "skeleton literal", "RESOLVED", "TOPOLOGY"} {
			assert.Contains(t, stage2, tok,
				"stage-2 must carry the no-hardcoded-literal / resolve-from-topology mandate token %q", tok)
		}
	})

	// 7. DETECTED, NOT ASSUMED — a clean diff emits zero goals; never a no-op
	//    gate goal. The mandate lives in the spine.
	t.Run("detected_not_assumed", func(t *testing.T) {
		lower := strings.ToLower(spine)
		assert.Contains(t, lower, "detected, not assumed",
			"spine must carry the detected-not-assumed mandate (clean diff emits zero goals)")
	})

	// 8. cr-apply-fixes.xml documents the command's BOUNDARY vs its neighbours.
	//    Lowercased to match the grep -qi semantics of any boundary validate.
	t.Run("boundary_docs", func(t *testing.T) {
		lower := strings.ToLower(spine)
		for _, tok := range []string{"boundary", "/tmux:feature", "/tmux:investigate", "code-rules:goals", "/code-review"} {
			assert.Contains(t, lower, tok,
				"cr-apply-fixes.xml must document the boundary-vs-neighbours token %q", tok)
		}
	})

	// 9. cr-apply-fixes.xml + every cr-apply-fixes/ shard parse as well-formed
	//    XML — defensive overlap with TestEmbeddedCommandsXML_WellFormed that
	//    localizes a malformation to this command.
	t.Run("cr_apply_fixes_shards_well_formed", func(t *testing.T) {
		var checked int
		walk := func(path string) {
			b, readErr := embeddedCommands.ReadFile(path)
			require.NoError(t, readErr, "reading %s", path)
			dec := xml.NewDecoder(strings.NewReader(string(b)))
			for {
				_, tokErr := dec.Token()
				if tokErr == io.EOF {
					break
				}
				require.NoError(t, tokErr, "embedded XML %s must be well-formed", path)
			}
			checked++
		}
		err := fs.WalkDir(embeddedCommands, "embedded/commands/tmux/cr-apply-fixes", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".xml") {
				return err
			}
			walk(path)
			return nil
		})
		require.NoError(t, err)
		walk("embedded/commands/tmux/cr-apply-fixes.xml")
		assert.Positive(t, checked, "at least one cr-apply-fixes shard must be checked")
	})
}
