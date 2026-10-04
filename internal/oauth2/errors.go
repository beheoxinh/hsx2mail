package oauth2

import (
	"errors"
	"fmt"
)

// ErrReauthRequired marks a token failure that no retry can fix: the grant was
// revoked, expired past redemption, or the client credentials the token was
// issued to no longer match what this build is configured with. Callers must
// stop retrying and surface a re-authorization prompt instead of looping.
//
// Transient conditions (network down, 5xx, temporarily_unavailable) are
// deliberately NOT this error — those return a plain wrapped error so the
// existing backoff/retry logic keeps working.
var ErrReauthRequired = errors.New("re-authorization required")

// TokenError carries the provider's OAuth error code alongside the sentinel so
// logs and the UI can say *why* re-auth is needed (revoked grant vs. a client
// credential mismatch) without string-matching the wrapped message.
type TokenError struct {
	Code        string // e.g. "invalid_grant", "invalid_client"
	Description string
	Reauth      bool // true when Code is terminal and ErrReauthRequired applies
	Err         error
}

func (e *TokenError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("token error %s: %s", e.Code, e.Description)
	}
	return fmt.Sprintf("token error %s", e.Code)
}

func (e *TokenError) Unwrap() error { return e.Err }

// IsReauthRequired reports whether err is (or wraps) ErrReauthRequired. Use
// this instead of inspecting messages — the IDLE reconnect loop and the compose
// path both branch on it.
func IsReauthRequired(err error) bool { return errors.Is(err, ErrReauthRequired) }

// terminalTokenCodes are OAuth error codes that no retry can resolve.
//
//	invalid_grant     — refresh token expired, revoked, or already redeemed
//	invalid_client    — the client id/secret this build presents does not match
//	                    the one the token was issued to (config drift, wrong .env)
//	unauthorized_client — the client may not use this grant type
//
// Anything else (temporarily_unavailable, server_error, invalid_request,
// access_denied from a policy prompt the user can still answer) keeps the
// existing retry path.
var terminalTokenCodes = map[string]bool{
	"invalid_grant":       true,
	"invalid_client":      true,
	"unauthorized_client": true,
}

func newTokenError(code, description string) *TokenError {
	e := &TokenError{Code: code, Description: description}
	if terminalTokenCodes[code] {
		e.Reauth = true
		e.Err = ErrReauthRequired
	}
	return e
}
