package tmux

import (
	"testing"
)

func TestTmuxExecutor_Interface_Defined(t *testing.T) {
	// This test verifies that the TmuxExecutor interface is defined
	// and has the expected methods

	// We can't instantiate an interface directly, but we can verify
	// that a type implementing TmuxExecutor compiles
	var _ TmuxExecutor = (*mockExecutor)(nil)
}

// mockExecutor is a minimal implementation for interface verification
type mockExecutor struct{}

func (m *mockExecutor) CreateSession(id, path string) error {
	return nil
}

func (m *mockExecutor) KillSession(id string) error {
	return nil
}

func (m *mockExecutor) HasSession(id string) (bool, error) {
	return false, nil
}

func (m *mockExecutor) ListSessions() ([]string, error) {
	return nil, nil
}

func (m *mockExecutor) CreateWindow(sessionID, name, command string) (string, error) {
	return "@0", nil
}

func (m *mockExecutor) ListWindows(sessionID string) ([]WindowInfo, error) {
	return nil, nil
}

func (m *mockExecutor) SendMessage(sessionID, windowID, message string) error {
	return nil
}

func (m *mockExecutor) SendMessageWithDelay(sessionID, windowID, message string) error {
	return nil
}

func (m *mockExecutor) KillWindow(sessionID, windowID string) error {
	return nil
}

func (m *mockExecutor) SetWindowOption(sessionID, windowID, optionName, value string) error {
	return nil
}

func (m *mockExecutor) GetWindowOption(sessionID, windowID, optionName string) (string, error) {
	return "", nil
}

func (m *mockExecutor) CaptureWindowOutput(sessionID, windowID string) (string, error) {
	return "", nil
}

func (m *mockExecutor) SendMessageWithFeedback(sessionID, windowID, message string) (string, error) {
	return "", nil
}

func (m *mockExecutor) SetSessionEnvironment(sessionID, key, value string) error {
	return nil
}

func (m *mockExecutor) GetSessionEnvironment(sessionID, key string) (string, error) {
	return "", nil
}

func (m *mockExecutor) FindSessionByEnvironment(key, value string) (string, error) {
	return "", nil
}

func (m *mockExecutor) AttachSession(id string) error {
	return nil
}

func TestTmuxExecutor_Interface_HasSendMessage(t *testing.T) {
	// This test verifies that SendMessage is part of the TmuxExecutor interface
	// It will fail until SendMessage is added to the interface definition
	var executor TmuxExecutor = (*mockExecutor)(nil)

	// Verify the method exists by attempting to call it (won't actually execute)
	_ = executor.SendMessage
}
