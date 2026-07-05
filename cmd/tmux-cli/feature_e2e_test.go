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

// TestFeatureEndToEnd is the golden/catalogue test for the /tmux:feature
// brownfield arc (F1-F6). It cannot drive the LLM command live — that would
// require Claude + tmux/claude sessions and the suite must pass with no tmux
// server (live-session unit tests crash-loop the daemon). The faithful,
// in-scope realization of "drive /tmux:feature on a fixture brownfield repo and
// assert the emitted goals" is therefore a STATIC golden assertion over the
// shipped command XML: the emitted-goal CONTRACT (BE tests-first->impl chain,
// FE Playwright-iff-runner-else-HTTP selection, literal-freedom) must be encoded
// in the shards, and feature.xml must document the command's boundary vs its
// neighbours. Modeled on rules_catalogue_test.go / consolidate_validate_xml_test.go.
func TestFeatureEndToEnd(t *testing.T) {
	stage4 := readEmbeddedCommand(t, "feature/stage-4-implementation.xml")
	stage3b := readEmbeddedCommand(t, "feature/stage-3b-test-strategy.xml")
	featureXML := readEmbeddedCommand(t, "feature.xml")

	// 1. BE path: the emitted goals form a tests-first -> impl depends_on chain
	//    with red->green validates.
	t.Run("BE_TDD_chain", func(t *testing.T) {
		for _, tok := range []string{"tests-first", "depends_on", "TDD", "red/green", "assertion failure"} {
			assert.Contains(t, stage4, tok,
				"stage-4 must encode the BE-TDD chain token %q", tok)
		}
		// tests-first must PRECEDE impl in the emitted chain (red before green).
		tfIdx := strings.Index(stage4, "tests-first")
		implIdx := strings.Index(stage4, "impl")
		require.GreaterOrEqual(t, tfIdx, 0, "tests-first token must be present")
		require.GreaterOrEqual(t, implIdx, 0, "impl token must be present")
		assert.Less(t, tfIdx, implIdx,
			"tests-first must precede impl — the emitted chain is tests-first -> impl")
	})

	// 2. FE path: Playwright E2E iff has_frontend + e2e_runner_available, else the
	//    HTTP-level fallback.
	t.Run("FE_playwright_iff_runner_else_http", func(t *testing.T) {
		for _, tok := range []string{"has_frontend", "e2e_runner_available", "Playwright", "HTTP-level fallback", "{{e2e_runner}}"} {
			assert.Contains(t, stage3b, tok,
				"stage-3b must encode the FE driver-selection token %q", tok)
		}
	})

	// 3. Emitted goals carry ZERO hardcoded project-specific literals. Asserted
	//    POSITIVELY (the prohibition + resolve-from-TOPOLOGY mandate are present),
	//    never by banning the src/ substring — the mandate text legitimately
	//    contains "src/" ("never a src/ skeleton literal").
	t.Run("zero_hardcoded_literals", func(t *testing.T) {
		for _, tok := range []string{"src/", "skeleton literal", "RESOLVED", "TOPOLOGY"} {
			assert.Contains(t, stage4, tok,
				"stage-4 must carry the no-hardcoded-literal / resolve-from-topology mandate token %q", tok)
		}
		assert.Contains(t, stage3b, "project-shaped value is hardcoded",
			"stage-3b must carry the hardcode prohibition for FE values")
	})

	// 3b. Solo-lane emission: a SINGLE-goal branch evaluates the canonical
	//     solo-lane gate (task-list.xml <lane-gate name="solo-lane">, G1-G5, by
	//     reference) at emission and passes lane=solo on a full pass, so a
	//     single-unit single-file /tmux:feature goal routes through supervisor
	//     step 3c inline execution with zero spawns. Mirrors the
	//     zero_hardcoded_literals token-assertion idiom.
	t.Run("solo_lane_single_unit", func(t *testing.T) {
		for _, tok := range []string{"solo-lane gate", "lane: solo", "by reference"} {
			assert.Contains(t, stage4, tok,
				"stage-4 must encode the solo-lane emission gate token %q", tok)
		}
		// The gate is referenced BY NAME, not restated: stage-4 must name the
		// canonical task-list.xml lane-gate block rather than copy G1-G5.
		assert.Contains(t, stage4, `lane-gate name="solo-lane"`,
			"stage-4 must reference the canonical task-list.xml lane-gate block by name")
	})

	// 4. feature.xml documents the command's BOUNDARY vs its neighbours. RED
	//    before the boundary edit (0 mentions) -> GREEN after. Lowercased to match
	//    the grep -qi semantics of the goal's validate rules.
	t.Run("boundary_docs", func(t *testing.T) {
		lower := strings.ToLower(featureXML)
		for _, tok := range []string{"boundary", "/worker", "bmad", "task-plan-generate"} {
			assert.Contains(t, lower, tok,
				"feature.xml must document the boundary-vs-neighbours token %q", tok)
		}
	})

	// 5. feature.xml + every feature/ shard parse as well-formed XML — guards the
	//    boundary edit. Defensive overlap with TestEmbeddedCommandsXML_WellFormed
	//    that localizes a boundary-edit malformation to this test.
	t.Run("feature_shards_well_formed", func(t *testing.T) {
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
		err := fs.WalkDir(embeddedCommands, "embedded/commands/tmux/feature", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".xml") {
				return err
			}
			walk(path)
			return nil
		})
		require.NoError(t, err)
		walk("embedded/commands/tmux/feature.xml")
		assert.Positive(t, checked, "at least one feature shard must be checked")
	})
}
