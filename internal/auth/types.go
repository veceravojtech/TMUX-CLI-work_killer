package auth

import "time"

// staleThreshold is how close to expiry a stored access token may be before a
// pre-emptive refresh is triggered. Mirrors the contract's "refresh when
// expires_at − now < 120s" policy.
const staleThreshold = 120 * time.Second

// ClientMeta is the device/code registration payload. JSON keys are byte-exact
// with the contract (§1 request body).
type ClientMeta struct {
	Client      string `json:"client"`
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
	Version     string `json:"version"`
}

// DeviceCode is the POST /api/v1/auth/device/code response (§1).
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Token is the success shape of both the device token poll (§3) and refresh (§4).
type Token struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scopes       []string `json:"scopes"`
	Account      string   `json:"account"`
}

// Whoami is the GET /api/v1/auth/whoami response (§5).
type Whoami struct {
	Account     string   `json:"account"`
	Scopes      []string `json:"scopes"`
	DeviceLabel string   `json:"device_label"`
	CreatedAt   string   `json:"created_at"`
}

// Auth is the on-disk auth.json shape (contract "CLI-side auth store"). ExpiresAt
// is a time.Time so encoding/json emits/parses RFC3339 verbatim, matching the
// contract's expires_at field.
type Auth struct {
	APIURL       string    `json:"api_url"`
	Account      string    `json:"account"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes"`
}

// Stale reports whether the access token is within staleThreshold of expiry (or
// already expired) as of now, per the contract's refresh policy.
func (a *Auth) Stale(now time.Time) bool {
	return a.ExpiresAt.Sub(now) < staleThreshold
}

// AuthFromToken builds an Auth record from a freshly issued/rotated Token,
// stamping expires_at = now + expires_in. apiURL is the base the token was issued
// against (persisted so a later api.url change is detectable).
func AuthFromToken(apiURL string, t *Token, now time.Time) *Auth {
	return &Auth{
		APIURL:       apiURL,
		Account:      t.Account,
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    now.Add(time.Duration(t.ExpiresIn) * time.Second),
		Scopes:       t.Scopes,
	}
}
