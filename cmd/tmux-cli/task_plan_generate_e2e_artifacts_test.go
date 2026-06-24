package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate_E2EArtifactConvPresent(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "E2E-ARTIFACT-CONV",
		"template must declare an E2E-ARTIFACT-CONV rule")
	assert.Contains(t, content, `id="E2E-ARTIFACT-CONV"`,
		"E2E-ARTIFACT-CONV must be a named rule with id attribute")
	assert.Contains(t, content, `critical="true"`,
		"E2E-ARTIFACT-CONV must be critical")
}

func TestTemplate_E2EArtifactConvCondition(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, `id="E2E-ARTIFACT-CONV" condition="HAS_FRONTEND"`,
		"E2E-ARTIFACT-CONV must have condition=\"HAS_FRONTEND\" attribute")
}

func TestTemplate_E2EArtifactConvTraceRetain(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "retain-on-failure",
		"bundle must contain retain-on-failure as the trace mode")
}

func TestTemplate_E2EArtifactConvScreenshot(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "only-on-failure",
		"bundle must contain only-on-failure as the screenshot setting")
}

func TestTemplate_E2EArtifactConvOutputDir(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "test-results",
		"bundle must contain test-results as the output directory name")
}

func TestTemplate_E2EArtifactConvReporter(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "open: 'never'",
		"bundle must contain open: 'never' for html reporter")
	assert.Contains(t, content, "'list'",
		"bundle must contain 'list' reporter")
}

func TestTemplate_E2EArtifactConvNoVideo(t *testing.T) {
	content := readGenerateBundle(t)
	for _, pair := range []struct {
		name string
		fsys fs.FS
	}{
		{"embeddedCommands", embeddedCommands},
	} {
		err := fs.WalkDir(pair.fsys, "embedded/commands/tmux", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, readErr := fs.ReadFile(pair.fsys, path)
			require.NoError(t, readErr, "reading %s/%s", pair.name, path)
			assert.NotContains(t, string(data), "video:",
				"video: mandate found in %s/%s — E2E-ARTIFACT-CONV must NOT mandate video", pair.name, path)
			return nil
		})
		require.NoError(t, err, "walking %s", pair.name)
	}
	_ = content
}

func TestTemplate_E2EArtifactConvRetries(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "retries: 0",
		"bundle must contain retries: 0 mandate")
}

func TestTemplate_E2EArtifactConvScaffoldSC20(t *testing.T) {
	content := readGenerateBundle(t)
	scaffold := sliceBetween(t, content, `n="2"`, `n="3.14"`)
	assert.Contains(t, scaffold, "SC-20",
		"scaffold section must contain SC-20 identifier")
	assert.Contains(t, scaffold, `condition="FRONTEND_MODE == vue"`,
		"SC-20 (Playwright config) must be gated on FRONTEND_MODE == vue — the P2 FrontendMode gate, not binary HAS_FRONTEND")
}

// Two-tier (director redesign §5): E2E investigator fail-criteria (the
// test-results/ artifact path) are authored at dispatch by /tmux:elaborate — the
// 3.18 action shard is a roadmap skeleton with no investigators. The E2E-ARTIFACT
// convention still ships in the resolved bundle (rules catalogue).
func TestTemplate_E2EArtifactConvInvestigatorFailCriteria(t *testing.T) {
	content := readGenerateBundle(t)
	assert.Contains(t, content, "test-results",
		"the E2E-ARTIFACT test-results convention must still ship in the resolved bundle")
	controllerActions := sliceBetween(t, content, `n="3.18"`, `n="3.19"`)
	assert.Contains(t, controllerActions, `<param name="status">roadmap`,
		"the action shard emits a roadmap skeleton")
	assert.NotContains(t, controllerActions, "test-results/",
		"the test-results/ investigator fail criteria are authored at dispatch by /tmux:elaborate")
}

// Two-tier: the final-gate E2E investigator fail criteria (test-results/) are
// authored at dispatch; the 3.29 final gates are roadmap skeletons.
func TestTemplate_E2EArtifactConvFinalGateFailCriteria(t *testing.T) {
	content := readGenerateBundle(t)
	finalGates := sliceBetween(t, content, `n="3.29"`, `</flow>`)
	assert.Contains(t, finalGates, `<param name="status">roadmap`,
		"the final gates emit roadmap skeletons")
	assert.NotContains(t, finalGates, "test-results/",
		"the final-gate test-results/ investigator criteria are authored at dispatch by /tmux:elaborate")
}

func TestTemplate_E2EArtifactConvCompanionMirror(t *testing.T) {
	data, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "E2E-ARTIFACT-CONV",
		"companion doc must mention E2E-ARTIFACT-CONV")
}

func TestTemplate_E2EArtifactConvTestStrategy(t *testing.T) {
	data, err := fs.ReadFile(embeddedTemplates, "embedded/templates/_base/test-strategy.md")
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "test-results/",
		"_base/test-strategy.md must mention test-results/ as the concrete E2E artifact directory")
}
