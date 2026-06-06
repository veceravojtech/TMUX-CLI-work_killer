package taskvisor

import (
	"fmt"
	"testing"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestKillWindowByName_ClosesPipePaneBeforeKill(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@3", Name: "supervisor-001"},
	}, nil)
	exec.On("ClosePipePane", testSession, "@3").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)

	err := d.killWindowByName("supervisor-001")
	require.NoError(t, err)

	exec.AssertCalled(t, "ClosePipePane", testSession, "@3")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")

	// Verify ordering: ClosePipePane must be called before KillWindow.
	var closePipeIdx, killIdx int
	for i, call := range exec.Calls {
		if call.Method == "ClosePipePane" {
			closePipeIdx = i
		}
		if call.Method == "KillWindow" {
			killIdx = i
		}
	}
	assert.Less(t, closePipeIdx, killIdx, "ClosePipePane must be called before KillWindow")
}

func TestKillWindowByName_ClosePipePaneError_StillKills(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@5", Name: "validator-001"},
	}, nil)
	exec.On("ClosePipePane", testSession, "@5").Return(fmt.Errorf("pipe-pane not active"))
	exec.On("KillWindow", testSession, "@5").Return(nil)

	err := d.killWindowByName("validator-001")
	require.NoError(t, err, "ClosePipePane error must be swallowed; kill must proceed")

	exec.AssertCalled(t, "KillWindow", testSession, "@5")
}

func TestKillWindowByName_NoMatchingWindow_NoPipePaneCall(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "other-window"},
	}, nil)

	err := d.killWindowByName("supervisor-001")
	require.NoError(t, err)

	exec.AssertNotCalled(t, "ClosePipePane", mock.Anything, mock.Anything)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestKillWindowsByPrefix_ClosesPipePaneBeforeEachKill(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@10", Name: "execute-001-1"},
		{TmuxWindowID: "@11", Name: "execute-001-2"},
		{TmuxWindowID: "@12", Name: "execute-001-3"},
	}, nil)
	exec.On("ClosePipePane", testSession, mock.Anything).Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)

	err := d.killWindowsByPrefix("execute-001-")
	require.NoError(t, err)

	// Each window gets ClosePipePane then KillWindow.
	for _, wid := range []string{"@10", "@11", "@12"} {
		exec.AssertCalled(t, "ClosePipePane", testSession, wid)
		exec.AssertCalled(t, "KillWindow", testSession, wid)
	}

	// Verify ordering: for each window, ClosePipePane comes before KillWindow.
	callMethods := make([]string, 0, len(exec.Calls))
	for _, call := range exec.Calls {
		if call.Method == "ClosePipePane" || call.Method == "KillWindow" {
			callMethods = append(callMethods, call.Method)
		}
	}
	// With 3 windows: [ClosePipePane, KillWindow, ClosePipePane, KillWindow, ClosePipePane, KillWindow]
	require.Len(t, callMethods, 6)
	for i := 0; i < 6; i += 2 {
		assert.Equal(t, "ClosePipePane", callMethods[i], "even indices must be ClosePipePane")
		assert.Equal(t, "KillWindow", callMethods[i+1], "odd indices must be KillWindow")
	}
}

func TestKillWindowsByPrefix_ClosePipePaneError_ContinuesKilling(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@20", Name: "execute-002-1"},
		{TmuxWindowID: "@21", Name: "execute-002-2"},
	}, nil)
	// First ClosePipePane fails, second succeeds.
	exec.On("ClosePipePane", testSession, "@20").Return(fmt.Errorf("already closed"))
	exec.On("ClosePipePane", testSession, "@21").Return(nil)
	exec.On("KillWindow", testSession, mock.Anything).Return(nil)

	err := d.killWindowsByPrefix("execute-002-")
	require.NoError(t, err)

	// Both windows must still be killed.
	exec.AssertCalled(t, "KillWindow", testSession, "@20")
	exec.AssertCalled(t, "KillWindow", testSession, "@21")
}
