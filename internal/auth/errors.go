// Package auth implements the tmux-cli side of the device-code account login
// shared with tmux-web (design docs/architecture/session-log-streaming-design.md
// §3, P1). It provides a user-global token store (auth.json), a device-code flow
// client, transparent refresh with rotation, and a whoami lookup — all against the
// byte-exact contract in .tmux-cli/research/2026-07-22-16/p1-auth-contract.md.
//
// The package is a leaf over the standard library plus internal/identity for the
// machine fingerprint/hostname. It deliberately does NOT import internal/producer:
// the api.url resolution is mirrored (LoadAPIURL) rather than shared, so the two
// side channels evolve independently. Network failures are returned, never
// panicked; a missing or corrupt store degrades to "logged out".
package auth

import "errors"

var (
	// ErrReauthRequired is returned when the backend rejects a refresh token as
	// unknown/expired/revoked (401 invalid_grant) or an access token as invalid.
	// The stored auth.json has been (or should be) dropped and the user must run
	// `tmux-cli login` again.
	ErrReauthRequired = errors.New("auth: re-authentication required — run: tmux-cli login")

	// ErrAccessDenied is returned by Poll when the user denies the device
	// approval (400 access_denied). Polling stops.
	ErrAccessDenied = errors.New("auth: authorization denied")

	// ErrExpiredToken is returned by Poll when the device_code expires before
	// approval (400 expired_token). The login flow must be restarted.
	ErrExpiredToken = errors.New("auth: device code expired — restart login")

	// ErrNotLoggedIn is returned by store-aware helpers when no auth.json exists.
	ErrNotLoggedIn = errors.New("auth: not logged in")
)
