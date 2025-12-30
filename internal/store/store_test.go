package store

import (
	"testing"
)

func TestSessionStore_Interface_Defined(t *testing.T) {
	// This test verifies that the SessionStore interface is defined
	// and has the expected methods

	// We can't instantiate an interface directly, but we can verify
	// that a type implementing SessionStore compiles
	var _ SessionStore = (*mockStore)(nil)
}

// mockStore is a minimal implementation for interface verification
type mockStore struct{}

func (m *mockStore) Save(session *Session) error {
	return nil
}

func (m *mockStore) Load(id string) (*Session, error) {
	return nil, nil
}

func (m *mockStore) Delete(id string) error {
	return nil
}

func (m *mockStore) List() ([]*Session, error) {
	return nil, nil
}

func (m *mockStore) Move(id string, destination string) error {
	return nil
}
