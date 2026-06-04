package main

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvestigateWorkerXml_OwnsClassificationTable asserts that the live
// per-check classifier (investigate-worker.xml) is now the single source of
// truth for the env/code/spec classification ruleset. The deterministic
// env-failure table and its sibling guards were moved here verbatim from the
// now-deprecated validate.xml; if they regress to stubs the live validation
// path silently loses its env-vs-code discrimination.
func TestInvestigateWorkerXml_OwnsClassificationTable(t *testing.T) {
	content := readEmbeddedCommand(t, "investigate-worker.xml")

	for _, marker := range []string{
		"DETERMINISTIC ENVIRONMENT-FAILURE CLASSIFICATION",
		"Connection refused",
		"env-config",
		"infra-flake",
		"GREEN OBJECTIVE CHECK",
		"SPEC-DEFECT REQUIRES A NAMED",
		"OPTION-PRESENTING CORRECTION",
		"validator-error",
	} {
		assert.Contains(t, content, marker,
			"investigate-worker.xml must own the moved classification marker %q", marker)
	}

	// The decision table must be carried in a <classification> block that sits
	// inside step 3, BEFORE the overall-VERDICT roll-up, so each finding is
	// classified before the verdict is determined.
	cls := strings.Index(content, "<classification")
	verdict := strings.Index(content, "Determine overall VERDICT")
	require.NotEqual(t, -1, cls, "investigate-worker.xml must carry a <classification> block")
	require.NotEqual(t, -1, verdict, "investigate-worker.xml must still roll up an overall VERDICT")
	assert.Less(t, cls, verdict,
		"the <classification> block must precede the overall-VERDICT roll-up")
}

// TestValidateXml_IsDeprecationStub asserts validate.xml has been collapsed to a
// thin deprecation alias: it carries a deprecation notice and no longer carries
// the duplicated classification body (the <decision-table> / "Connection
// refused" rows). The file is NOT deleted — a stray manual invocation of the
// deprecated validate skill must still parse.
func TestValidateXml_IsDeprecationStub(t *testing.T) {
	content := readEmbeddedCommand(t, "validate.xml")

	assert.Contains(t, content, "<deprecated>",
		"validate.xml must carry a <deprecated> notice")
	assert.Contains(t, content, "investigate-worker.xml",
		"the deprecation notice must point at investigate-worker.xml as the new home")
	assert.Contains(t, content, "retained one release",
		"the deprecation notice must state the one-release retention policy")

	// The classification body must be GONE — it now lives in investigate-worker.xml.
	assert.NotContains(t, content, "<decision-table>",
		"validate.xml must no longer carry the duplicated <decision-table>")
	assert.NotContains(t, content, "Connection refused",
		"validate.xml must no longer carry the duplicated env-failure table rows")

	// The stub must still be a well-formed validate task shell so a manual
	// invocation loads cleanly.
	assert.Contains(t, content, `<task id="validate"`,
		"validate.xml must keep its <task id=\"validate\"> root element")
}

// TestNoEmbeddedCommandPointsAtValidateStepTable asserts every embedded command
// (except validate.* itself) has had its dangling "validate.xml step 2d"
// cross-reference re-pointed away from the dead skill.
func TestNoEmbeddedCommandPointsAtValidateStepTable(t *testing.T) {
	const root = "embedded/commands/tmux"
	err := fs.WalkDir(embeddedCommands, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := path.Base(p)
		if strings.HasPrefix(base, "validate.") {
			return nil // the dead alias itself is exempt
		}
		b, readErr := embeddedCommands.ReadFile(p)
		require.NoError(t, readErr, "embedded command %s must be readable", p)
		assert.NotContains(t, string(b), "validate.xml step 2d",
			"%s still points at the dead validate.xml step 2d — re-point the cross-reference", p)
		return nil
	})
	require.NoError(t, err)
}
