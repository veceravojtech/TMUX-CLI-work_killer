package sudo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogExecution_WritesJSONLine(t *testing.T) {
	tmpDir := t.TempDir()
	entry := LogEntry{
		Command:    "apt-get update",
		ExitCode:   0,
		DurationMs: 1500,
		StdoutLen:  2048,
		StderrLen:  0,
	}

	LogExecution(tmpDir, entry)

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var got LogEntry
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)

	assert.Equal(t, "apt-get update", got.Command)
	assert.Equal(t, 0, got.ExitCode)
	assert.Equal(t, int64(1500), got.DurationMs)
	assert.Equal(t, 2048, got.StdoutLen)
	assert.Equal(t, 0, got.StderrLen)
	assert.Empty(t, got.Error)

	_, parseErr := time.Parse(time.RFC3339, got.Timestamp)
	assert.NoError(t, parseErr)
}

func TestLogExecution_AppendsMultipleEntries(t *testing.T) {
	tmpDir := t.TempDir()

	for i := 0; i < 3; i++ {
		LogExecution(tmpDir, LogEntry{
			Command:    "cmd-" + string(rune('a'+i)),
			ExitCode:   i,
			DurationMs: int64(i * 100),
		})
	}

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 3)

	for _, line := range lines {
		var entry LogEntry
		assert.NoError(t, json.Unmarshal([]byte(line), &entry))
	}
}

func TestLogExecution_CreatesDirectoryIfMissing(t *testing.T) {
	tmpDir := t.TempDir()

	LogExecution(tmpDir, LogEntry{Command: "whoami"})

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	_, err := os.Stat(logPath)
	assert.NoError(t, err)
}

func TestLogExecution_ErrorFieldOmittedWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	LogExecution(tmpDir, LogEntry{Command: "ls", Error: ""})

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	assert.NotContains(t, string(data), `"err"`)
}

func TestLogExecution_ErrorFieldPresentWhenSet(t *testing.T) {
	tmpDir := t.TempDir()

	LogExecution(tmpDir, LogEntry{
		Command: "systemctl restart nginx",
		Error:   "context deadline exceeded",
	})

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var got LogEntry
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "context deadline exceeded", got.Error)
}

func TestLogExecution_SilentOnInvalidWorkingDir(t *testing.T) {
	assert.NotPanics(t, func() {
		LogExecution("/nonexistent/path/that/does/not/exist", LogEntry{Command: "test"})
	})
}

func TestLogExecution_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			LogExecution(tmpDir, LogEntry{
				Command:    "concurrent-cmd",
				ExitCode:   n,
				DurationMs: int64(n),
			})
		}(i)
	}
	wg.Wait()

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 10)

	for _, line := range lines {
		var entry LogEntry
		assert.NoError(t, json.Unmarshal([]byte(line), &entry))
	}
}

func TestLogExecution_SetsTimestamp(t *testing.T) {
	tmpDir := t.TempDir()

	before := time.Now()
	LogExecution(tmpDir, LogEntry{Command: "date"})
	after := time.Now()

	logPath := filepath.Join(tmpDir, ".tmux-cli", "logs", "sudo.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var got LogEntry
	require.NoError(t, json.Unmarshal(data, &got))

	ts, err := time.Parse(time.RFC3339, got.Timestamp)
	require.NoError(t, err)

	assert.False(t, ts.Before(before.Truncate(time.Second)))
	assert.False(t, ts.After(after.Add(time.Second)))
}
