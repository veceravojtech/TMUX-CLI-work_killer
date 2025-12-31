package mcp

import "github.com/console/tmux-cli/internal/store"

// mockSessionStore is a test mock implementation of store.SessionStore.
// It allows testing Server components without requiring actual filesystem I/O.
type mockSessionStore struct {
	session   *store.Session
	loadError error
	saveError error
	sessions  []*store.Session
}

// Load returns the configured mock session or error
func (m *mockSessionStore) Load(projectPath string) (*store.Session, error) {
	if m.loadError != nil {
		return nil, m.loadError
	}
	return m.session, nil
}

// Save returns the configured mock error
func (m *mockSessionStore) Save(session *store.Session) error {
	return m.saveError
}
