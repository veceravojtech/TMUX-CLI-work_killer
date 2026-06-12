package main

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const shardDir = "embedded/commands/tmux/task-plan-generate"

var loadDirectiveRe = regexp.MustCompile(`<load file="([^"]+)">`)

func readGenerateBundle(t *testing.T) string {
	t.Helper()

	spineBytes, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err, "spine must be readable")
	result := string(spineBytes)

	stubRe := regexp.MustCompile(`(?s)<step n="[^"]+" title="[^"]+">\s*<load file="([^"]+)">[^<]*</load>\s*</step>`)
	result = stubRe.ReplaceAllStringFunc(result, func(stub string) string {
		m := stubRe.FindStringSubmatch(stub)
		require.Len(t, m, 2, "stub must capture file path")
		filePath := m[1]
		embedPath := strings.Replace(filePath, ".claude/commands/tmux/", "embedded/commands/tmux/", 1)
		shardBytes, readErr := embeddedCommands.ReadFile(embedPath)
		require.NoError(t, readErr, "shard %s must exist in embed FS", embedPath)
		return string(shardBytes)
	})

	// The <conventions> block loads the rule catalogue at runtime
	// (`tmux-cli rules resolve`); the bundle mirrors that by appending every
	// embedded rules file — the bundle is everything the planning agent loads.
	var ruleFiles []string
	err = fs.WalkDir(embeddedRules, "embedded/rules", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ruleFiles = append(ruleFiles, p)
		return nil
	})
	require.NoError(t, err, "embedded rules must be walkable")
	sort.Strings(ruleFiles)
	for _, p := range ruleFiles {
		data, readErr := embeddedRules.ReadFile(p)
		require.NoError(t, readErr, "rules file %s must be readable", p)
		result += "\n" + string(data)
	}

	return result
}

func TestShards_StubTargetsExist(t *testing.T) {
	spineBytes, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err)
	spine := string(spineBytes)

	matches := loadDirectiveRe.FindAllStringSubmatch(spine, -1)
	require.NotEmpty(t, matches, "spine must contain at least one <load> directive")

	for _, m := range matches {
		filePath := m[1]
		embedPath := strings.Replace(filePath, ".claude/commands/tmux/", "embedded/commands/tmux/", 1)
		_, err := embeddedCommands.ReadFile(embedPath)
		assert.NoError(t, err, "stub references %s but the file does not exist in embed FS", filePath)
	}
}

func TestShards_OneToOneReferences(t *testing.T) {
	spineBytes, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err)
	spine := string(spineBytes)

	matches := loadDirectiveRe.FindAllStringSubmatch(spine, -1)
	stubFiles := make(map[string]int)
	for _, m := range matches {
		stubFiles[m[1]]++
	}

	var shardFiles []string
	err = fs.WalkDir(embeddedCommands, shardDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		shardFiles = append(shardFiles, p)
		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, 21, len(shardFiles), "exactly 21 shard files must exist")
	assert.Equal(t, 21, len(stubFiles), "exactly 21 stubs must exist in the spine")

	for _, sf := range shardFiles {
		installedPath := strings.Replace(sf, "embedded/commands/tmux/", ".claude/commands/tmux/", 1)
		assert.Equal(t, 1, stubFiles[installedPath],
			"shard %s must be referenced exactly once in the spine", sf)
	}
}

func TestShards_FilenameMatchesStepN(t *testing.T) {
	stepNRe := regexp.MustCompile(`<step\s+n="([^"]+)"`)
	filenameStepRe := regexp.MustCompile(`step-([0-9][0-9a-z.]*)-`)

	err := fs.WalkDir(embeddedCommands, shardDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := path.Base(p)
		fnMatch := filenameStepRe.FindStringSubmatch(base)
		require.NotNil(t, fnMatch, "shard filename %s must match step-N-slug.xml pattern", base)

		data, readErr := embeddedCommands.ReadFile(p)
		require.NoError(t, readErr)
		xmlMatch := stepNRe.FindStringSubmatch(string(data))
		require.NotNil(t, xmlMatch, "shard %s must have a <step n=\"...\"> root element", base)

		assert.Equal(t, fnMatch[1], xmlMatch[1],
			"shard %s: filename step number %q must match XML n=%q", base, fnMatch[1], xmlMatch[1])
		return nil
	})
	require.NoError(t, err)
}

func TestShards_NoLeftoverStepBodies(t *testing.T) {
	spineBytes, err := embeddedCommands.ReadFile("embedded/commands/tmux/task-plan-generate.xml")
	require.NoError(t, err)
	spine := string(spineBytes)

	substepRe := regexp.MustCompile(`<substep\s+n="([^"]+)"`)
	matches := substepRe.FindAllStringSubmatch(spine, -1)
	for _, m := range matches {
		n := m[1]
		prefix := strings.Split(n, ".")[0]
		allowed := prefix == "0" || prefix == "0b" || prefix == "4" || prefix == "5" || prefix == "6"
		if !allowed {
			stepNum := n
			if idx := strings.Index(n, "."); idx > 0 {
				stepNum = n[:idx]
			}
			allowed = stepNum == "0" || stepNum == "0b" || stepNum == "4" || stepNum == "5" || stepNum == "6"
		}
		assert.True(t, allowed,
			"spine contains <substep n=%q> which belongs to a sharded step — "+
				"leftover body detected (only steps 0/0b/0c/4/5/6 may have substeps inline)", n)
	}
}

func TestShards_NoMdInShardDir(t *testing.T) {
	var mdFiles []string
	err := fs.WalkDir(embeddedCommands, shardDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, ".md") {
			mdFiles = append(mdFiles, p)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, mdFiles, "shard directory must contain zero .md files (would register as spurious slash commands)")
}
