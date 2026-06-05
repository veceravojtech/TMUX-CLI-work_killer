package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBinaryStale_Untouched_NotStale(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tmux-cli")
	require.NoError(t, os.WriteFile(bin, []byte("binary-content"), 0o755))

	resetBuildStamp()
	executablePath = func() (string, error) { return bin, nil }
	defer func() { executablePath = resolvedExecutable }()

	initBuildStamp()

	stale, detail := BinaryStale()
	assert.False(t, stale)
	assert.Empty(t, detail)
}

func TestBinaryStale_TouchedMtime_Stale(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tmux-cli")
	require.NoError(t, os.WriteFile(bin, []byte("binary-content"), 0o755))

	resetBuildStamp()
	executablePath = func() (string, error) { return bin, nil }
	defer func() { executablePath = resolvedExecutable }()

	initBuildStamp()

	future := time.Now().Add(10 * time.Second)
	require.NoError(t, os.Chtimes(bin, future, future))

	stale, detail := BinaryStale()
	assert.True(t, stale)
	assert.Contains(t, detail, "binary replaced")
}

func TestBinaryStale_Replaced_Stale(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tmux-cli")
	require.NoError(t, os.WriteFile(bin, []byte("old-content"), 0o755))

	resetBuildStamp()
	executablePath = func() (string, error) { return bin, nil }
	defer func() { executablePath = resolvedExecutable }()

	initBuildStamp()

	require.NoError(t, os.WriteFile(bin, []byte("new-content-that-is-different-size"), 0o755))

	stale, detail := BinaryStale()
	assert.True(t, stale)
	assert.Contains(t, detail, "binary replaced")
}

func TestBinaryStale_Deleted_Stale(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tmux-cli")
	require.NoError(t, os.WriteFile(bin, []byte("binary-content"), 0o755))

	resetBuildStamp()
	executablePath = func() (string, error) { return bin, nil }
	defer func() { executablePath = resolvedExecutable }()

	initBuildStamp()

	require.NoError(t, os.Remove(bin))

	stale, detail := BinaryStale()
	assert.True(t, stale)
	assert.Contains(t, detail, "binary stat failed")
}

func TestBinaryStale_InitFailed_NotStale(t *testing.T) {
	resetBuildStamp()
	executablePath = func() (string, error) { return "", os.ErrNotExist }
	defer func() { executablePath = resolvedExecutable }()

	initBuildStamp()

	stale, detail := BinaryStale()
	assert.False(t, stale)
	assert.Empty(t, detail)
}
