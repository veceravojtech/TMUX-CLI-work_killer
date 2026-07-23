package auth

import (
	"context"
	"errors"
)

// EnsureFresh returns a usable Auth, refreshing pre-emptively when the stored
// access token is stale (within staleThreshold of expiry). A fresh token is
// returned as-is with no network call. On invalid_grant the store is deleted and
// ErrReauthRequired is returned. A nil auth yields ErrNotLoggedIn.
func EnsureFresh(ctx context.Context, c *Client, s *Store, a *Auth) (*Auth, error) {
	if a == nil {
		return nil, ErrNotLoggedIn
	}
	if !a.Stale(c.now()) {
		return a, nil
	}
	return RefreshStore(ctx, c, s, a)
}

// RefreshStore refreshes a's tokens and, on success, persists the ROTATED pair
// atomically (old refresh token is now invalid server-side). On invalid_grant it
// deletes the store and returns ErrReauthRequired; any other error is returned
// with the store left untouched.
func RefreshStore(ctx context.Context, c *Client, s *Store, a *Auth) (*Auth, error) {
	tok, err := c.Refresh(ctx, a.RefreshToken)
	if errors.Is(err, ErrReauthRequired) {
		_ = s.Delete()
		return nil, ErrReauthRequired
	}
	if err != nil {
		return nil, err
	}
	na := AuthFromToken(a.APIURL, tok, c.now())
	if err := s.Save(na); err != nil {
		return nil, err
	}
	return na, nil
}
