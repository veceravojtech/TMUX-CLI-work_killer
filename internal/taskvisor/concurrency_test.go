package taskvisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ReadConcurrencyOverride ------------------------------------------------

func TestReadConcurrencyOverride_AbsentFile_NotOk(t *testing.T) {
	dir := t.TempDir()
	n, ok := ReadConcurrencyOverride(dir)
	assert.False(t, ok)
	assert.Equal(t, 0, n)
}

func TestReadConcurrencyOverride_ValidInt_Ok(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
	require.NoError(t, os.WriteFile(ConcurrencyOverridePath(dir), []byte("4\n"), 0o644))
	n, ok := ReadConcurrencyOverride(dir)
	assert.True(t, ok)
	assert.Equal(t, 4, n)
}

func TestReadConcurrencyOverride_NonIntOrZero_NotOk(t *testing.T) {
	cases := []string{"abc", "0", "-1", "", "  ", "1.5"}
	for _, content := range cases {
		t.Run("content="+content, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tmux-cli"), 0o755))
			require.NoError(t, os.WriteFile(ConcurrencyOverridePath(dir), []byte(content), 0o644))
			n, ok := ReadConcurrencyOverride(dir)
			assert.False(t, ok, "content %q must fall back (not ok)", content)
			assert.Equal(t, 0, n)
		})
	}
}

// --- WriteConcurrencyOverride -----------------------------------------------

func TestWriteConcurrencyOverride_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteConcurrencyOverride(dir, 3))

	n, ok := ReadConcurrencyOverride(dir)
	assert.True(t, ok)
	assert.Equal(t, 3, n)

	// Exact on-disk format is "<n>\n".
	b, err := os.ReadFile(ConcurrencyOverridePath(dir))
	require.NoError(t, err)
	assert.Equal(t, "3\n", string(b))
}

func TestWriteConcurrencyOverride_FloorsAtOne(t *testing.T) {
	for _, in := range []int{0, -5} {
		dir := t.TempDir()
		require.NoError(t, WriteConcurrencyOverride(dir, in))
		n, ok := ReadConcurrencyOverride(dir)
		assert.True(t, ok)
		assert.Equal(t, 1, n, "write(%d) must floor to 1", in)
	}
}

// --- maxGoals() precedence --------------------------------------------------

func TestMaxGoals_OverrideBeatsSettings(t *testing.T) {
	dir := t.TempDir()
	writeSettingsMaxGoals(t, dir, 1)
	require.NoError(t, WriteConcurrencyOverride(dir, 4))

	d := &Daemon{workDir: dir}
	assert.Equal(t, 4, d.maxGoals())
}

func TestMaxGoals_NoOverride_UsesSettings(t *testing.T) {
	dir := t.TempDir()
	writeSettingsMaxGoals(t, dir, 2)

	d := &Daemon{workDir: dir}
	assert.Equal(t, 2, d.maxGoals())
}

func TestMaxGoals_InvalidOverride_FallsBackToSettings(t *testing.T) {
	dir := t.TempDir()
	writeSettingsMaxGoals(t, dir, 2)
	// An unparsable override must NOT collapse the cap — fall back to settings.
	require.NoError(t, os.WriteFile(ConcurrencyOverridePath(dir), []byte("oops"), 0o644))

	d := &Daemon{workDir: dir}
	assert.Equal(t, 2, d.maxGoals())
}

// --- signal-tick cap-change / drain -----------------------------------------

// TestConcurrencySignalRaisesDispatch: writing the override to 4 while
// setting.yaml says max_goals=1 raises the effective cap on the next tick, so
// the budget computed from d.maxGoals() admits 4 disjoint goals — no restart.
func TestConcurrencySignalRaisesDispatch(t *testing.T) {
	dir := t.TempDir()
	writeSettingsMaxGoals(t, dir, 1)
	require.NoError(t, WriteConcurrencyOverride(dir, 4))

	d := &Daemon{workDir: dir}
	cap := d.maxGoals()
	require.Equal(t, 4, cap)

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalPending, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalPending, Scope: []string{"internal/b/**"}},
		{ID: "goal-003", Status: GoalPending, Scope: []string{"internal/c/**"}},
		{ID: "goal-004", Status: GoalPending, Scope: []string{"internal/d/**"}},
	}}
	got := gf.DisjointReadySet(cap)
	require.Len(t, got, 4)
}

// TestConcurrencyLowerDrainsNeverKills: with the override lowered to 2 while 3
// goals are already running, the budget (2-3 < 0) yields no new dispatch and the
// running goals stay GoalRunning — drain, never killed.
func TestConcurrencyLowerDrainsNeverKills(t *testing.T) {
	dir := t.TempDir()
	writeSettingsMaxGoals(t, dir, 1)
	require.NoError(t, WriteConcurrencyOverride(dir, 2))

	d := &Daemon{workDir: dir}
	require.Equal(t, 2, d.maxGoals())

	gf := &GoalsFile{Goals: []Goal{
		{ID: "goal-001", Status: GoalRunning, Scope: []string{"internal/a/**"}},
		{ID: "goal-002", Status: GoalRunning, Scope: []string{"internal/b/**"}},
		{ID: "goal-003", Status: GoalRunning, Scope: []string{"internal/c/**"}},
		{ID: "goal-004", Status: GoalPending, Scope: []string{"internal/d/**"}},
	}}
	assert.Nil(t, gf.DisjointReadySet(d.maxGoals()), "budget 2-3<0 ⇒ no new dispatch")
	// The 3 running goals are untouched — drain, not killed.
	for _, g := range gf.Goals[:3] {
		assert.Equal(t, GoalRunning, g.Status)
	}
}
