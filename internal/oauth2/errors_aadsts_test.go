package oauth2

import (
	"strings"
	"testing"
)

// TestTokenErrorEntraHints is the regression guard for the enterprise-login
// advice: an unregistered single-tenant app is the failure every corporate
// mailbox hits, and the raw AADSTS blob told users nothing they could act on.
func TestTokenErrorEntraHints(t *testing.T) {
	const fpt = "AADSTS700016: Application with identifier '0816227c-ae3d-4470-9e1e-73c183e16b94' was not found in the directory 'FPT Information System'. This can happen if the application has not been installed by the administrator of the tenant or consented to by any user in the tenant."

	t.Run("unregistered single-tenant app names the tenant", func(t *testing.T) {
		got := newTokenError("invalid_grant", fpt).Error()
		if !strings.Contains(got, "FPT Information System") {
			t.Errorf("hint lost the tenant name: %q", got)
		}
		if !strings.Contains(got, "OAuth Credentials") {
			t.Errorf("hint does not say where to supply a client id: %q", got)
		}
	})

	t.Run("unspecified tenant", func(t *testing.T) {
		got := newTokenError("invalid_grant", "AADSTS50059: No tenant-identifying information found").Error()
		if !strings.Contains(got, "tenant") {
			t.Errorf("expected tenant advice, got %q", got)
		}
	})

	t.Run("admin consent", func(t *testing.T) {
		got := newTokenError("invalid_grant", "AADSTS65001: The user or administrator has not consented").Error()
		if !strings.Contains(got, "consent") {
			t.Errorf("expected consent advice, got %q", got)
		}
	})

	t.Run("not assigned to app", func(t *testing.T) {
		got := newTokenError("invalid_grant", "AADSTS50105: The user is not assigned to the application").Error()
		if !strings.Contains(got, "assigned") {
			t.Errorf("expected assignment advice, got %q", got)
		}
	})

	t.Run("secret required", func(t *testing.T) {
		got := newTokenError("invalid_grant", "AADSTS7000218: Request body must contain a parameter named 'client_secret'").Error()
		if !strings.Contains(got, "client secret") && !strings.Contains(got, "client_secret") {
			t.Errorf("expected client secret advice, got %q", got)
		}
	})

	t.Run("unrecognised description is passed through", func(t *testing.T) {
		got := newTokenError("invalid_grant", "AADSTS50126: Invalid username or password").Error()
		if !strings.Contains(got, "AADSTS50126") {
			t.Errorf("expected the raw code preserved, got %q", got)
		}
		if strings.Contains(got, "OAuth Credentials") {
			t.Errorf("must not apply unrelated advice: %q", got)
		}
	})

	t.Run("empty description", func(t *testing.T) {
		if got := newTokenError("invalid_scope", "").Error(); got != "token error invalid_scope" {
			t.Errorf("got %q", got)
		}
	})
}
