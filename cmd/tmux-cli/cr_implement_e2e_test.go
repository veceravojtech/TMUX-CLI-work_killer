package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrImplementEndToEnd is the golden/catalogue test for the
// /tmux:cr-implement comment-driven MR remediation composer. Like
// TestCrApplyFixesEndToEnd / TestFeatureEndToEnd it cannot drive the LLM command
// live (that would require Claude + tmux/claude sessions, and the suite must pass
// with no tmux server), so the faithful in-scope realization is a STATIC golden
// assertion over the shipped embedded command files: the .md frontmatter must
// delegate to the .xml spine, the five-stage sequence must be present and in
// order, and the Stage 2 confirm gate, the thread-as-data security invariant, the
// empty-$ARGUMENTS hard stop, and the two no-op SUCCESS paths must be encoded.
// cr-implement is a SINGLE .xml (unlike cr-apply-fixes' sharded dir), so the
// well-formed parse runs on the one spine. Modeled on cr_apply_fixes_e2e_test.go.
func TestCrImplementEndToEnd(t *testing.T) {
	md := readEmbeddedCommand(t, "cr-implement.md")
	spine := readEmbeddedCommand(t, "cr-implement.xml")

	// 1. Both embedded files ship, the .md opens with frontmatter, and the .xml
	//    parses as well-formed XML (single <task> root). The token loop mirrors
	//    cr_apply_fixes_e2e_test.go:127-135.
	t.Run("files_present_and_well_formed", func(t *testing.T) {
		require.NotEmpty(t, md, "cr-implement.md must be present and non-empty")
		require.NotEmpty(t, spine, "cr-implement.xml must be present and non-empty")
		assert.True(t, strings.HasPrefix(md, "---"),
			"cr-implement.md must open with the `---` frontmatter delimiter")
		assert.Contains(t, md, "description:",
			"cr-implement.md frontmatter must carry a description: key")

		dec := xml.NewDecoder(strings.NewReader(spine))
		for {
			_, tokErr := dec.Token()
			if tokErr == io.EOF {
				break
			}
			require.NoError(t, tokErr, "cr-implement.xml must be well-formed XML")
		}
	})

	// 2. The .md frontmatter shard delegates execution to the .xml spine verbatim.
	t.Run("md_delegates_to_xml", func(t *testing.T) {
		assert.Contains(t, md, "cr-implement.xml",
			"cr-implement.md must cite the .xml spine it delegates to")
		assert.Contains(t, md, "EXACTLY as written",
			"cr-implement.md must instruct following the .xml EXACTLY as written")
	})

	// 3. The five <stage n="0..4"> markers all exist, appear in strictly
	//    increasing position, and number exactly five. The literal-slice form
	//    avoids the fmt/strconv imports.
	t.Run("five_stages_present_in_order", func(t *testing.T) {
		stageTags := []string{
			"<stage n=\"0\"",
			"<stage n=\"1\"",
			"<stage n=\"2\"",
			"<stage n=\"3\"",
			"<stage n=\"4\"",
		}
		prev := -1
		for _, tag := range stageTags {
			idx := strings.Index(spine, tag)
			require.GreaterOrEqual(t, idx, 0, "spine must contain stage marker %q", tag)
			require.Greater(t, idx, prev, "stage marker %q must follow the previous stage in order", tag)
			prev = idx
		}
		assert.Equal(t, 5, strings.Count(spine, "<stage n="),
			"spine must declare exactly five stages")
	})

	// 4. Stage 2 is the confirm gate, and the thread-as-data security invariant is
	//    encoded. `confirm gate` is matched case-insensitively (source is
	//    title-case "Confirm gate"); the upper-case security tokens match the
	//    un-lowered spine.
	t.Run("stage2_confirm_gate_and_thread_as_data", func(t *testing.T) {
		lowered := strings.ToLower(spine)
		assert.Contains(t, lowered, "confirm gate",
			"spine must name the Stage 2 confirm gate")
		for _, tok := range []string{"UNTRUSTED DATA", "DATA, NOT INSTRUCTIONS", "THREAD-AS-DATA"} {
			assert.Contains(t, spine, tok,
				"spine must encode the thread-as-data security invariant token %q", tok)
		}
	})

	// 5. Empty $ARGUMENTS is a documented HARD STOP with a usage message.
	t.Run("empty_arguments_hard_stop", func(t *testing.T) {
		assert.Contains(t, spine, "$ARGUMENTS",
			"spine must reference the $ARGUMENTS MR iid input")
		assert.Contains(t, spine, "HARD STOP",
			"spine must document the empty-$ARGUMENTS hard stop")
		assert.Contains(t, spine, "usage: /tmux:cr-implement",
			"spine must print a usage message on empty $ARGUMENTS")
	})

	// 6. Both no-op SUCCESS paths are documented: zero unresolved threads and
	//    all-non-actionable — each emits no goal and is a success, not an error.
	t.Run("no_op_success_paths", func(t *testing.T) {
		for _, tok := range []string{
			"no unresolved threads on MR",
			"clean no-op",
			"EVERY unresolved thread is NON-ACTIONABLE",
			"success result, not an error",
		} {
			assert.Contains(t, spine, tok,
				"spine must document the no-op success-path token %q", tok)
		}
	})
}
