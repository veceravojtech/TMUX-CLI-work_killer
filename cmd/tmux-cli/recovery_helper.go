package main

import (
	"fmt"

	"github.com/console/tmux-cli/internal/recovery"
	"github.com/console/tmux-cli/internal/store"
)

// MaybeRecoverSession checks if session needs recovery and performs it transparently
// This is called by ALL session access commands before executing their logic
// Returns error if recovery fails, nil if no recovery needed or recovery succeeded
// Updated to accept session object directly to fix API mismatch bug
// Recovery messages are sent to supervisor window instead of stderr
func MaybeRecoverSession(
	session *store.Session,
	recoveryManager recovery.RecoveryManager,
) error {
	if session == nil {
		return fmt.Errorf("session is required")
	}

	// 1. Check if recovery is needed
	recoveryNeeded, err := recoveryManager.IsRecoveryNeeded(session)
	if err != nil {
		return fmt.Errorf("check recovery needed: %w", err)
	}

	// 2. If no recovery needed, return immediately
	if !recoveryNeeded {
		return nil
	}

	// 3. Perform recovery (recreate session + windows)
	// Session object already loaded by caller
	err = recoveryManager.RecoverSession(session)
	if err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	// 4. Verify recovery succeeded (FR14, NFR10)
	err = recoveryManager.VerifyRecovery(session)
	if err != nil {
		return fmt.Errorf("recovery verification failed: %w", err)
	}

	return nil
}
