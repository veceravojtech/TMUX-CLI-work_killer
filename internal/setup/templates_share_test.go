package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- 6.30: Share namespace cross-BC types in embedded templates ---

func TestTemplates_BoundedContextsShareNamespace(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "bounded-contexts.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "bounded-contexts.md must exist")
	content := string(data)

	assert.Contains(t, content, "## Share Namespace")
	assert.Contains(t, content, "#### DataTypes")
	assert.Contains(t, content, "#### Events")
	assert.Contains(t, content, "#### Exceptions")
	assert.Contains(t, content, "Share namespace has zero dependencies on any BC")
}

func TestTemplates_ScaffoldShareDirectory(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "scaffold.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "scaffold.md must exist")
	content := string(data)

	assert.Contains(t, content, "src/Share/")
	assert.Contains(t, content, "DataType/")
	assert.Contains(t, content, "Event/")
	assert.Contains(t, content, "Exception/")
	assert.Contains(t, content, "Messaging/")
}

func TestTemplates_DomainModelShareReference(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "domain-model.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "domain-model.md must exist")
	content := string(data)

	assert.Contains(t, content, "## Share Namespace")
	assert.Contains(t, content, "### Shared DataTypes")
	assert.Contains(t, content, "### Shared Events")
}

func TestTemplates_ScaffoldDeptracShareRule(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "templates", "_base", "scaffold.md")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "scaffold.md must exist")
	content := string(data)

	assert.Contains(t, content, "`src/Share/`")
	assert.Contains(t, content, "Share imports nothing")
}
