package tmux

// Session represents a tmux session
type Session struct {
	Name string
	ID   string
}

// SessionManager handles tmux session operations
type SessionManager interface {
	List() ([]Session, error)
	Create(name string) (*Session, error)
	Kill(sessionID string) error
}
