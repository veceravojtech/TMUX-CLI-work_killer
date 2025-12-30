package testutil

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockTmuxExecutor_CreateSession_MocksCorrectly(t *testing.T) {
	// This test demonstrates the TDD pattern using the MockTmuxExecutor
	// Following AR5: Unit tests with mocks

	mockExec := new(MockTmuxExecutor)
	mockExec.On("CreateSession", "test-uuid", "/project").Return(nil)

	err := mockExec.CreateSession("test-uuid", "/project")

	assert.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestMockTmuxExecutor_CreateSession_ReturnsError(t *testing.T) {
	// Test error handling with mock

	mockExec := new(MockTmuxExecutor)
	expectedErr := errors.New("tmux not found")
	mockExec.On("CreateSession", "test-uuid", "/project").Return(expectedErr)

	err := mockExec.CreateSession("test-uuid", "/project")

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	mockExec.AssertExpectations(t)
}

func TestMockTmuxExecutor_HasSession_ReturnsTrue(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	mockExec.On("HasSession", "test-uuid").Return(true, nil)

	exists, err := mockExec.HasSession("test-uuid")

	assert.NoError(t, err)
	assert.True(t, exists)
	mockExec.AssertExpectations(t)
}

func TestMockTmuxExecutor_ListSessions_ReturnsSessionList(t *testing.T) {
	mockExec := new(MockTmuxExecutor)
	sessions := []string{"session1", "session2"}
	mockExec.On("ListSessions").Return(sessions, nil)

	result, err := mockExec.ListSessions()

	assert.NoError(t, err)
	assert.Equal(t, sessions, result)
	mockExec.AssertExpectations(t)
}
