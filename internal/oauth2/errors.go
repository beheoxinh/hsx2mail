package oauth2

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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
	desc := e.Description
	if desc != "" {
		if msg, ok := explainEntraError(desc); ok {
			return fmt.Sprintf("token error %s: %s", e.Code, msg)
		}
		return fmt.Sprintf("token error %s: %s", e.Code, desc)
	}
	return fmt.Sprintf("token error %s", e.Code)
}

// Entra ID (Azure AD) failures that a user can actually act on, usually hit
// when signing into a work/school mailbox whose tenant has not consented to
// the app. The raw AADSTS text is accurate but describes the server's view
// ("application not found in the directory"); these carry the code so support
// can still grep for it, plus the step the user or their admin must take.
var entraErrorHints = []struct {
	re   *regexp.Regexp
	hint string
}{
	{
		re:   regexp.MustCompile(`AADSTS700016:.*?not found in the directory '([^']*)'`),
		hint: "the app is not registered in tenant %s. Use your organization's own app registration (Settings → OAuth Credentials), or ask IT to grant admin consent to a multi-tenant app.",
	},
	{
		re:   regexp.MustCompile(`AADSTS50059`),
		hint: "no tenant could be identified for this request. Enterprise Microsoft 365 accounts must pick the right tenant (fpt.com, organizations) instead of leaving it unset.",
	},
	{
		re:   regexp.MustCompile(`AADSTS65001`),
		hint: "your organization must grant admin consent to this app before it can sign in. Ask your IT administrator to consent to the IMAP and SMTP permissions.",
	},
	{
		re:   regexp.MustCompile(`AADSTS50105`),
		hint: "your account is not assigned to this application. Ask your IT administrator to assign you to it.",
	},
	{
		re:   regexp.MustCompile(`AADSTS7000218`),
		hint: "the app registration expects a client secret. Enable public client flows (\"Allow public client flows\" = Yes) on the app registration, or supply the secret.",
	},
}

// explainEntraError turns a raw Entra error_description into an actionable
// sentence. Returns ok=false for descriptions this package has no advice for,
// so the caller can fall back to the original text.
func explainEntraError(desc string) (string, bool) {
	for _, h := range entraErrorHints {
		m := h.re.FindStringSubmatch(desc)
		if m == nil {
			continue
		}
		hint := h.hint
		if strings.Contains(hint, "%s") {
			tenant := ""
			if len(m) > 1 {
				tenant = m[1]
			}
			hint = fmt.Sprintf(hint, tenant)
		}
		code := desc
		if i := strings.Index(desc, ":"); i > 0 {
			code = desc[:i]
		}
		return fmt.Sprintf("%s — %s", code, hint), true
	}
	return "", false
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
