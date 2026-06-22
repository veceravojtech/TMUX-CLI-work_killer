package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runConcurrencyArgs invokes runTaskvisorConcurrency through a FRESH cobra command
// re-bound to the package flag vars, so each call has clean flag/Changed state
// (the real init()-bound command would leak Changed("set") between subtests).
// Faithful to the real binding in init() — same flags, same RunE.
func runConcurrencyArgs(t *testing.T, args []string) error {
	t.Helper()
	concSet, concInc, concDec = 0, false, false
	c := &cobra.Command{Use: "concurrency", Args: cobra.NoArgs, RunE: runTaskvisorConcurrency}
	c.Flags().IntVar(&concSet, "set", 0, "")
	c.Flags().BoolVar(&concInc, "inc", false, "")
	c.Flags().BoolVar(&concDec, "dec", false, "")
	c.MarkFlagsMutuallyExclusive("set", "inc", "dec")
	c.SetArgs(args)
	c.SetOut(new(bytes.Buffer))
	c.SetErr(new(bytes.Buffer))
	return c.Execute()
}

// readOverride reads the override integer as written under the resolved root for
// the current cwd (mirrors what the daemon would read).
func readOverride(t *testing.T) (string, bool) {
	t.Helper()
	root, err := taskvisorProjectRoot()
	require.NoError(t, err)
	b, err := os.ReadFile(taskvisor.ConcurrencyOverridePath(root))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func TestRunTaskvisorConcurrency_Set(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	withCwd(t, dir, func() {
		require.NoError(t, runConcurrencyArgs(t, []string{"--set", "3"}))
		content, ok := readOverride(t)
		require.True(t, ok, "override file must exist after --set")
		assert.Equal(t, "3\n", content)
	})
}

func TestRunTaskvisorConcurrency_SetBelowOne_Errors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	withCwd(t, dir, func() {
		err := runConcurrencyArgs(t, []string{"--set", "0"})
		require.Error(t, err, "--set 0 must error")
		// File must NOT be written.
		_, ok := readOverride(t)
		assert.False(t, ok, "no override file written on --set 0")
	})
}

func TestRunTaskvisorConcurrency_IncDecFloor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	withCwd(t, dir, func() {
		// Seed override at 2, then --inc → 3.
		require.NoError(t, runConcurrencyArgs(t, []string{"--set", "2"}))
		require.NoError(t, runConcurrencyArgs(t, []string{"--inc"}))
		content, ok := readOverride(t)
		require.True(t, ok)
		assert.Equal(t, "3\n", content)

		// Down to 1, then --dec floors at 1 (no error).
		require.NoError(t, runConcurrencyArgs(t, []string{"--set", "1"}))
		require.NoError(t, runConcurrencyArgs(t, []string{"--dec"}))
		content, ok = readOverride(t)
		require.True(t, ok)
		assert.Equal(t, "1\n", content)
	})
}

func TestRunTaskvisorConcurrency_BarePrintsCurrent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	withCwd(t, dir, func() {
		require.NoError(t, runConcurrencyArgs(t, []string{"--set", "4"}))
		// Bare invocation writes nothing and prints the current cap. It must not
		// error and must not alter the override file.
		require.NoError(t, runConcurrencyArgs(t, []string{}))
		content, ok := readOverride(t)
		require.True(t, ok)
		assert.Equal(t, "4\n", content, "bare concurrency must not change the override")
	})
}

func TestRunTaskvisorConcurrency_MutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	withCwd(t, dir, func() {
		err := runConcurrencyArgs(t, []string{"--inc", "--dec"})
		require.Error(t, err, "combining --inc and --dec must error")
	})
}

// TestTaskvisorHelpListsConcurrency mirrors the validate
// (`taskvisor --help | grep -qi concurrency`) as a pure unit assertion: the
// registered command tree exposes the `concurrency` subcommand in help output.
func TestTaskvisorHelpListsConcurrency(t *testing.T) {
	buf := new(bytes.Buffer)
	// Execute() on a child re-roots to rootCmd, so drive the real root with
	// `taskvisor --help` to render the taskvisor subcommand listing.
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"taskvisor", "--help"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	require.NoError(t, rootCmd.Execute())
	assert.True(t, strings.Contains(strings.ToLower(buf.String()), "concurrency"),
		"taskvisor --help must list the concurrency subcommand")
}
