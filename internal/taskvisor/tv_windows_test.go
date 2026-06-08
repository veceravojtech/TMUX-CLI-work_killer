package taskvisor

import (
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestKillWindowByName_Found(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("KillWindow", testSession, "@0").Return(nil)

	err := d.killWindowByName("supervisor")
	require.NoError(t, err)
	exec.AssertCalled(t, "KillWindow", testSession, "@0")
}

func TestKillWindowByName_NotFound(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.killWindowByName("foo")
	assert.NoError(t, err)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestKillWindowsByPrefix_MatchesMultiple(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@1", Name: "execute-1"},
		{TmuxWindowID: "@3", Name: "execute-3"},
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)
	exec.On("KillWindow", testSession, "@1").Return(nil)
	exec.On("KillWindow", testSession, "@3").Return(nil)

	err := d.killWindowsByPrefix("execute-")
	require.NoError(t, err)

	exec.AssertCalled(t, "KillWindow", testSession, "@1")
	exec.AssertCalled(t, "KillWindow", testSession, "@3")
	exec.AssertNotCalled(t, "KillWindow", testSession, "@0")
}

func TestKillWindowsByPrefix_NoMatches(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.killWindowsByPrefix("execute-")
	assert.NoError(t, err)
	exec.AssertNotCalled(t, "KillWindow", mock.Anything, mock.Anything)
}

func TestWaitWindowsGone_ImmediateSuccess(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, time.Second)
	assert.NoError(t, err)
}

func TestWaitWindowsGone_EventualSuccess(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, 2*time.Second)
	assert.NoError(t, err)
}

func TestWaitWindowsGone_Timeout(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor"},
	}, nil)

	err := d.waitWindowsGone([]string{"supervisor"}, 200*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestWaitClaudeBoot_ImmediateBoot(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitClaudeBoot_EventualBoot(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil).Once()
	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "claude"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitClaudeBoot_Timeout(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{
		{TmuxWindowID: "@0", Name: "supervisor", CurrentCommand: "zsh"},
	}, nil)

	err := d.waitClaudeBoot("supervisor", 200*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestWaitClaudeBoot_WindowNotFound(t *testing.T) {
	d, exec, _ := setupDaemon(t)
	d.session = testSession

	exec.On("ListWindows", testSession).Return([]tmux.WindowInfo{}, nil)

	err := d.waitClaudeBoot("supervisor", time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
