package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFeatureXml_MissingShardIsInstallError guards FE1: all seven stage shards
// ship with the install today, so a missing shard is a broken/stale install,
// not a "not-yet-authored" state. Every stage seam (0-5) must name the missing
// shard, print the install-error message ("install is incomplete or stale" +
// the `tmux-cli start` regeneration remedy), and treat it as a harness/infra
// defect — the graceful "Stage N pending (F2-F6)" language must be gone
// everywhere (seams, comment block, execution rule, critical-rules bullet).
func TestFeatureXml_MissingShardIsInstallError(t *testing.T) {
	content := readEmbeddedCommand(t, "feature.xml")

	// The pre-fix pending language must not survive anywhere in the file.
	assert.NotContains(t, content, "pending (F2",
		"feature.xml must no longer treat a missing shard as a pending future-feature state")
	assert.NotContains(t, content, "STOP gracefully",
		"feature.xml must no longer stop 'gracefully' on a missing shard — it is a named install error")

	shardByStage := map[int]string{
		0: "stage-0-capability.xml",
		1: "stage-1-context.xml",
		2: "stage-2-architecture.xml",
		3: "stage-3-docs.xml",
		4: "stage-4-implementation.xml",
		5: "stage-5-validation.xml",
	}
	for n := 0; n <= 5; n++ {
		start := strings.Index(content, fmt.Sprintf(`<stage n="%d"`, n))
		require.NotEqual(t, -1, start, "feature.xml must have stage %d", n)
		end := len(content)
		if n < 5 {
			end = strings.Index(content, fmt.Sprintf(`<stage n="%d"`, n+1))
			require.NotEqual(t, -1, end, "feature.xml must have stage %d after stage %d", n+1, n)
		} else if flowEnd := strings.Index(content, "</flow>"); flowEnd != -1 {
			end = flowEnd
		}
		body := content[start:end]

		assert.Contains(t, body, shardByStage[n],
			"stage %d seam must name its shard file", n)
		assert.Contains(t, body, "install is incomplete or stale",
			"stage %d seam must call a missing shard a broken/stale install", n)
		assert.Contains(t, body, `Run "tmux-cli start" to regenerate .claude/commands`,
			"stage %d seam must give the tmux-cli start regeneration remedy", n)
		assert.Contains(t, body, "harness/infra defect",
			"stage %d seam must route the missing shard to the shared error-reporting procedure", n)
	}

	// The execution rule + comment block must match: install defect, controlled
	// stop, never an improvised stage body.
	assert.Contains(t, content, "INSTALL DEFECT",
		"execution-rules must brand a missing shard an install defect")
	assert.Contains(t, content, "never improvise the stage body",
		"execution-rules must keep the never-improvise guard on the controlled stop")
}

// TestFeatureXml_StageHandoffsPersistToDisk guards FE2: stage handoffs are
// conversation-resident and die at compaction (Stage 4's daemon run spans
// hours), so the spine must mandate that every emitted context block is ALSO
// persisted to the research/dossier directory (Stage 0 -> feature-context.md,
// Stage 4 -> feature-goals.md) and that a stage missing its upstream
// in-conversation block re-reads the persisted file instead of re-deriving,
// stopping with its named error only when BOTH are absent.
func TestFeatureXml_StageHandoffsPersistToDisk(t *testing.T) {
	content := readEmbeddedCommand(t, "feature.xml")

	llmStart := strings.Index(content, `<llm critical="true">`)
	require.NotEqual(t, -1, llmStart, "feature.xml must have the critical llm block")
	llmEnd := strings.Index(content[llmStart:], "</llm>")
	require.NotEqual(t, -1, llmEnd, "the critical llm block must close")
	llmBody := content[llmStart : llmStart+llmEnd]

	for _, marker := range []string{
		"feature-context.md",
		"feature-goals.md",
		"re-read the persisted file",
		"BOTH",
	} {
		assert.Contains(t, llmBody, marker,
			"the critical llm block must carry the handoff-persistence mandate marker %q", marker)
	}

	// Reviewer-blocker guard: the NEVER ESCAPE THE FLOW write allowlist must
	// itself permit the persisted context blocks, or an executor obeying the
	// allowlist literally would refuse the persistence writes FE2 mandates.
	escStart := strings.Index(llmBody, "NEVER ESCAPE THE FLOW")
	require.NotEqual(t, -1, escStart, "the critical llm block must carry the NEVER ESCAPE THE FLOW mandate")
	escEnd := strings.Index(llmBody[escStart:], "</mandate>")
	require.NotEqual(t, -1, escEnd, "the NEVER ESCAPE THE FLOW mandate must close")
	escBody := llmBody[escStart : escStart+escEnd]
	for _, marker := range []string{"feature-context.md", "feature-goals.md"} {
		assert.Contains(t, escBody, marker,
			"the NEVER ESCAPE THE FLOW write allowlist must include the persisted context block %q", marker)
	}

	rulesStart := strings.Index(content, "<execution-rules>")
	require.NotEqual(t, -1, rulesStart, "feature.xml must have execution-rules")
	rulesEnd := strings.Index(content[rulesStart:], "</execution-rules>")
	require.NotEqual(t, -1, rulesEnd, "execution-rules must close")
	rulesBody := content[rulesStart : rulesStart+rulesEnd]

	for _, marker := range []string{
		"HANDOFF PERSISTENCE",
		"feature-context.md",
		"feature-goals.md",
	} {
		assert.Contains(t, rulesBody, marker,
			"execution-rules must carry the handoff-persistence entry marker %q", marker)
	}
}
