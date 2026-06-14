package main

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Shared, untagged test helpers for the cmd/tmux-cli package. They live here
// (not in taskvisor_test.go) because that file carries a //go:build integration
// tag, and these helpers are used by untagged test files
// (goal_add_persistence_test.go, goal_priority_test.go, project_test.go,
// taskvisor_project_root_test.go) that must compile and run under the default
// `go test`. Keeping the helpers in an untagged file makes them available to
// both the default and the integration build.

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	return string(out)
}

func withTempCwd(t *testing.T, fn func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(orig)) }()
	fn(dir)
}
