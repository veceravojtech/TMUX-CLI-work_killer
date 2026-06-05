package taskvisor

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goals_atomicwrite_test.go — P5 fix #3: atomicWrite's os.Remove(tmp) cleanup
// after a failed rename must no longer swallow its error. The rename error stays
// the authoritative return; a failed unlink is surfaced via log.Printf so a
// stale .tmp file can never go invisible.

func TestAtomicWrite_RenameFails_ReturnsRenameError_LogsUnlinkError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only-dir permission enforcement does not apply to root")
	}

	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	path := filepath.Join(sub, "goals.yaml")
	tmp := path + ".tmp"
	// Pre-create tmp so the in-function os.WriteFile(tmp) succeeds even once the
	// parent dir is read-only (truncating an existing file needs file perm, not
	// dir perm), while rename(tmp,path) AND remove(tmp) both need dir-write and
	// therefore both fail with EACCES.
	require.NoError(t, os.WriteFile(tmp, []byte("old"), 0o644))
	require.NoError(t, os.Chmod(sub, 0o555))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) }) // let t.TempDir cleanup remove it

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	err := atomicWrite(path, []byte("new"), 0o644)

	require.Error(t, err, "a failed rename must surface as the returned error")
	assert.Contains(t, err.Error(), "rename",
		"the rename error is authoritative — not the unlink error")
	assert.Contains(t, buf.String(), "failed to remove stale tmp",
		"a failed tmp unlink must be logged, never silently dropped")
}

func TestAtomicWrite_Success_NoTmpLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goals.yaml")

	require.NoError(t, atomicWrite(path, []byte("hello"), 0o644))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data), "happy path writes the exact bytes")

	_, statErr := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "no .tmp file may linger after a successful write")
}
