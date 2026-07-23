package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	storeDirName  = "tmux-cli"
	storeFileName = "auth.json"
)

// Store is the user-global auth.json token store. It is NOT per-project: one
// login serves every project on the machine (design §3).
type Store struct {
	path string
}

// DefaultStorePath resolves the store path per the contract: $XDG_CONFIG_HOME
// (else ~/.config) + tmux-cli/auth.json.
func DefaultStorePath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, storeDirName, storeFileName), nil
}

// NewStore builds a Store at the default XDG-resolved path.
func NewStore() (*Store, error) {
	p, err := DefaultStorePath()
	if err != nil {
		return nil, err
	}
	return &Store{path: p}, nil
}

// NewStoreAt builds a Store at an explicit path (test/override seam).
func NewStoreAt(path string) *Store { return &Store{path: path} }

// Path returns the resolved auth.json path.
func (s *Store) Path() string { return s.path }

// Load reads auth.json. A missing file, insecure permissions (any group/world
// bit), or corrupt JSON all degrade to (nil, nil) — "logged out" — with a stderr
// warning for the insecure/corrupt cases (never the file contents). Only a real
// I/O error (e.g. unreadable directory) is returned.
func (s *Store) Load() (*Auth, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		fmt.Fprintf(os.Stderr, "auth: ignoring %s: insecure permissions %#o (want 0600) — treating as logged out\n", s.path, perm)
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var a Auth
	if err := json.Unmarshal(data, &a); err != nil {
		fmt.Fprintf(os.Stderr, "auth: ignoring corrupt %s — treating as logged out\n", s.path)
		return nil, nil
	}
	return &a, nil
}

// Save writes auth.json atomically with 0600 enforced BEFORE the content lands:
// a temp file in the same directory is chmod'd to 0600, written, then renamed
// over the target. A crash mid-write leaves the previous store (or nothing)
// intact — never a partial file (contract: "no partial auth.json").
func (s *Store) Save(a *Auth) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, storeFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Enforce 0600 before any secret bytes are written.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Delete removes auth.json. It is idempotent: a missing file is not an error.
func (s *Store) Delete() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
