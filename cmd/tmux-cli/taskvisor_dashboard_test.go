package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetDashboardWatchFlag returns the dashboard command to a pristine, unparsed
// state so flag-parsing assertions don't bleed across tests: pflag's Parse never
// clears Changed for an already-set flag, and the --watch value is backed by the
// package-global taskvisorDashboardWatch. Reset both before each re-parse.
func resetDashboardWatchFlag(t *testing.T) {
	t.Helper()
	taskvisorDashboardWatch = 0
	f := taskvisorDashboardCmd.Flags().Lookup("watch")
	require.NotNil(t, f, "--watch flag must be wired on the dashboard command")
	f.Changed = false
}

// --- Pure helper: resolveDashboardWatch ---

func TestResolveDashboardWatch_Absent(t *testing.T) {
	watch, interval := resolveDashboardWatch(false, 0)
	assert.False(t, watch, "absent --watch ⇒ single static snapshot")
	assert.Equal(t, time.Duration(0), interval)
}

func TestResolveDashboardWatch_BareDefault(t *testing.T) {
	watch, interval := resolveDashboardWatch(true, 0)
	assert.True(t, watch)
	assert.Equal(t, 5*time.Second, interval, "bare/non-positive ⇒ 5s defensive default")
}

func TestResolveDashboardWatch_Explicit(t *testing.T) {
	watch, interval := resolveDashboardWatch(true, 10*time.Second)
	assert.True(t, watch)
	assert.Equal(t, 10*time.Second, interval, "explicit value passes through")
}

// --- Command registration + flag wiring ---

func TestTaskvisorDashboardCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"taskvisor", "dashboard"})
	require.NoError(t, err)
	assert.Equal(t, "dashboard", cmd.Use, "Use literal also satisfies grep -rq '\"dashboard\"'")
}

func TestTaskvisorDashboardCmd_BareWatchNoOptDefVal(t *testing.T) {
	resetDashboardWatchFlag(t)
	require.NoError(t, taskvisorDashboardCmd.ParseFlags([]string{"--watch"}))
	assert.True(t, taskvisorDashboardCmd.Flags().Changed("watch"), "bare --watch marks Changed")
	assert.Equal(t, 5*time.Second, taskvisorDashboardWatch, "NoOptDefVal sets bare --watch to 5s")
}

func TestTaskvisorDashboardCmd_ExplicitInterval(t *testing.T) {
	resetDashboardWatchFlag(t)
	require.NoError(t, taskvisorDashboardCmd.ParseFlags([]string{"--watch=10s"}))
	assert.True(t, taskvisorDashboardCmd.Flags().Changed("watch"))
	assert.Equal(t, 10*time.Second, taskvisorDashboardWatch)
}

func TestTaskvisorDashboardCmd_AbsentNotChanged(t *testing.T) {
	resetDashboardWatchFlag(t)
	require.NoError(t, taskvisorDashboardCmd.ParseFlags([]string{}))
	assert.False(t, taskvisorDashboardCmd.Flags().Changed("watch"), "omitted --watch leaves Changed false")
}

func TestTaskvisorDashboardCmd_BadInterval(t *testing.T) {
	resetDashboardWatchFlag(t)
	err := taskvisorDashboardCmd.ParseFlags([]string{"--watch=10"})
	require.Error(t, err, "a unitless duration must be rejected by pflag")
}
