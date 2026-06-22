package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// bypassAcceptedKey is the top-level key in ~/.claude.json that records the
// user's one-time acceptance of Claude's "Bypass Permissions mode" warning.
// Seeding it to true ensures `claude --dangerously-skip-permissions` (the
// worker launch command) never opens the interactive prompt that would
// otherwise stall freshly spawned workers.
const bypassAcceptedKey = "bypassPermissionsModeAccepted"

// SeedClaudeBypass resolves the user's home directory and idempotently seeds
// bypassPermissionsModeAccepted=true into ~/.claude.json. A failure to resolve
// HOME is returned to the caller (which treats setup errors as best-effort).
func SeedClaudeBypass() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return seedClaudeBypassAt(filepath.Join(home, ".claude.json"))
}

// seedClaudeBypassAt seeds bypassPermissionsModeAccepted=true into the JSON file
// at path, preserving every existing top-level key byte-for-byte. It is
// idempotent: when the key is already boolean true the file is left untouched.
// An absent or empty file is treated as an empty object. Malformed JSON returns
// a wrapped error and leaves the file untouched.
func seedClaudeBypassAt(path string) error {
	obj := map[string]json.RawMessage{}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if cur, ok := obj[bypassAcceptedKey]; ok {
		var b bool
		if err := json.Unmarshal(cur, &b); err == nil && b {
			return nil
		}
	}

	obj[bypassAcceptedKey] = json.RawMessage("true")

	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}
