package main

import (
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readEmbeddedCommand returns the text of an embedded command template under
// embedded/commands/tmux/.
func readEmbeddedCommand(t *testing.T, rel string) string {
	t.Helper()
	b, err := embeddedCommands.ReadFile("embedded/commands/tmux/" + rel)
	require.NoError(t, err, "embedded command %s must be readable", rel)
	return string(b)
}

// readTemplateSource returns the on-disk text of a template under
// embedded/templates/ (the package dir is the test CWD).
//
// We read the canonical source files directly rather than via the embed.FS:
// the `//go:embed embedded/templates` directive in session.go silently EXCLUDES
// the `_base/` directory because go:embed drops names beginning with '_'. That
// is a pre-existing defect (RISK in the report) — _base templates are not
// shipped today. Guarding the source files keeps these content tests valid
// regardless of that embed gap.
func readTemplateSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("embedded/templates/" + rel)
	require.NoError(t, err, "template source %s must be readable", rel)
	return string(b)
}

// gate0Block extracts the substring between the GATE0:BEGIN and GATE0:END
// sentinels (exclusive of the sentinels themselves).
func gate0Block(t *testing.T, content string) string {
	t.Helper()
	// Match the full sentinel markers, not the bare "GATE0:BEGIN"/"GATE0:END"
	// substrings — the explanatory header comment also mentions those words
	// (e.g. "...append their own sections AFTER GATE0:END.").
	const beginMarker = "<!-- GATE0:BEGIN -->"
	const endMarker = "<!-- GATE0:END -->"
	begin := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)
	require.NotEqual(t, -1, begin, "GATE0:BEGIN sentinel must be present")
	require.NotEqual(t, -1, end, "GATE0:END sentinel must be present")
	require.Less(t, begin, end, "GATE0:BEGIN must precede GATE0:END")
	return content[begin : end+len(endMarker)]
}

// TestDiscoverXML_RunTargetDecisionAsksPublishedPorts verifies the run-target
// decision elicits the published host:container port mappings.
func TestDiscoverXML_RunTargetDecisionAsksPublishedPorts(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")
	assert.Contains(t, content, "PUBLISHED_PORTS",
		"run-target decision must capture a discrete PUBLISHED_PORTS list")
	assert.Contains(t, content, "host:container",
		"run-target decision must ask the published host:container port mappings")
}

// TestDiscoverXML_PublishedPortsInTestEnvOutput verifies the Step-7 summary
// block carries a Published Ports heading guarded by an is_docker conditional.
func TestDiscoverXML_PublishedPortsInTestEnvOutput(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	// Isolate the Step-7 pre-save summary template.
	start := strings.Index(content, "Here's the test environment configuration")
	require.NotEqual(t, -1, start, "Step-7 summary template must be present")
	end := strings.Index(content[start:], "Should I save this to")
	require.NotEqual(t, -1, end, "Step-7 summary template must end with the save prompt")
	summary := content[start : start+end]

	assert.Contains(t, summary, "Published Ports",
		"summary block must contain a Published Ports heading")

	// The Published Ports heading must sit inside an is_docker conditional:
	// an {{#if is_docker}} must open before it within the summary.
	docker := strings.Index(summary, "{{#if is_docker}}")
	ports := strings.Index(summary, "Published Ports")
	require.NotEqual(t, -1, docker, "summary must open an is_docker conditional")
	assert.Less(t, docker, ports,
		"Published Ports heading must appear after the is_docker conditional opens")
}

// TestDiscoverXML_D14GatesPublishedPorts verifies the D-14 gate rule requires
// Published Ports alongside the existing Run Target requirement.
func TestDiscoverXML_D14GatesPublishedPorts(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	var d14 string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "D-14 gate") {
			d14 = line
			break
		}
	}
	require.NotEmpty(t, d14, "D-14 gate rule line must be present")
	assert.Contains(t, d14, "Run Target",
		"D-14 gate must still require Run Target")
	assert.Contains(t, d14, "Published Ports",
		"D-14 gate must require Published Ports when docker+compose")
}

// TestDiscoverXML_PublishedPortsNotRecordedAsADR verifies the ADR-exclusion rule
// keeps published ports out of ADRs (test-environment.md is the sole sink) and
// that no new ADR topic was introduced for them.
func TestDiscoverXML_PublishedPortsNotRecordedAsADR(t *testing.T) {
	content := readEmbeddedCommand(t, "project-discovery.xml")

	// Isolate the run-target ADR-exclusion rule.
	start := strings.Index(content, "EXCEPTION: the run-target decision")
	require.NotEqual(t, -1, start, "ADR-exclusion rule must be present")
	end := strings.Index(content[start:], "</rule>")
	require.NotEqual(t, -1, end, "ADR-exclusion rule must close")
	rule := content[start : start+end]

	assert.Contains(t, rule, "PUBLISHED_PORTS",
		"ADR-exclusion rule must name published ports")
	assert.Contains(t, rule, "test-environment.md",
		"ADR-exclusion rule must keep test-environment.md as the sole sink")
	assert.Contains(t, strings.ToUpper(rule), "NEVER AS AN ADR",
		"ADR-exclusion rule must state published ports are never an ADR")

	// No new ADR topic may be introduced for published ports.
	assert.NotContains(t, content, `topic="published`,
		"published ports must not be a separate ADR decision topic")
}

// TestAgentsTemplates_PublishedPortsBlockBothTiers verifies both agents.md tiers
// carry a Published Ports block inside the GATE0 sentinels.
func TestAgentsTemplates_PublishedPortsBlockBothTiers(t *testing.T) {
	for _, rel := range []string{"_base/agents.md", "php-symfony/agents.md"} {
		content := readTemplateSource(t, rel)
		block := gate0Block(t, content)
		assert.Contains(t, block, "## Published Ports",
			"%s must contain a ## Published Ports block inside the GATE0 sentinels", rel)
	}
}

// TestAgentsTemplates_Gate0SentinelsStillAligned verifies both agents.md tiers
// keep the GATE0 contract headings intact and aligned.
func TestAgentsTemplates_Gate0SentinelsStillAligned(t *testing.T) {
	for _, rel := range []string{"_base/agents.md", "php-symfony/agents.md"} {
		content := readTemplateSource(t, rel)
		block := gate0Block(t, content)
		assert.Contains(t, block, "## Run Target",
			"%s GATE0 block must retain the ## Run Target heading", rel)
		assert.Contains(t, block, "## Gate 0 Status",
			"%s GATE0 block must retain the ## Gate 0 Status heading", rel)
	}

	// The Published Ports block must be mirrored verbatim across both tiers.
	base := gate0Block(t, readTemplateSource(t, "_base/agents.md"))
	overlay := gate0Block(t, readTemplateSource(t, "php-symfony/agents.md"))
	extractPorts := func(s string) string {
		i := strings.Index(s, "## Published Ports")
		j := strings.Index(s, "## Services")
		require.NotEqual(t, -1, i)
		require.NotEqual(t, -1, j)
		return s[i:j]
	}
	assert.Equal(t, extractPorts(base), extractPorts(overlay),
		"the Published Ports block must be identical (verbatim) across both agents.md tiers")
}

// TestDiscoveryTemplate_Section14NotesPublishedPorts verifies _base/discovery.md
// Section 14 notes that published ports are captured in Step 4 for docker.
func TestDiscoveryTemplate_Section14NotesPublishedPorts(t *testing.T) {
	content := readTemplateSource(t, "_base/discovery.md")

	start := strings.Index(content, "## 14. Test Environment")
	require.NotEqual(t, -1, start, "Section 14 must be present")
	section := content[start:]

	assert.Contains(t, section, "PUBLISHED_PORTS",
		"Section 14 must note the published ports capture")
	assert.Contains(t, section, "Step 4",
		"Section 14 must note published ports are captured in Step 4")
	assert.Contains(t, section, "docker",
		"Section 14 note must scope published ports to the docker run target")
}

// TestEmbeddedCommandsXML_WellFormed verifies every embedded command XML parses
// without error — no malformed tags were introduced by this change.
func TestEmbeddedCommandsXML_WellFormed(t *testing.T) {
	var checked int
	var sawDiscover bool
	err := fs.WalkDir(embeddedCommands, "embedded/commands/tmux", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".xml") {
			return err
		}
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
		if strings.HasSuffix(path, "project-discovery.xml") {
			sawDiscover = true
		}
		checked++
		return nil
	})
	require.NoError(t, err)
	assert.Positive(t, checked, "at least one embedded command XML must be checked")
	assert.True(t, sawDiscover, "the edited project-discovery.xml must be among the verified files")
}
