package app

import (
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/oauth2"
)

// TestOAuthBuildStatusAcceptsSecretlessClients is the regression guard for a bug
// that silently disabled the shipped PKCE-only OAuth clients.
//
// GetOAuthBuildStatus drove the launch-time "OAuth credentials missing" dialog.
// It used to require client_id AND client_secret for Google, so a build that
// relies on the shipped Thunderbird client id (no secret, PKCE instead) reported
// Google as unconfigured and popped the dialog on an app that could authorize
// fine. The user's only visible symptom was a warning about missing credentials
// they had no way to supply.
func TestOAuthBuildStatusAcceptsSecretlessClients(t *testing.T) {
	savedID, savedSecret := oauth2.GoogleClientID, oauth2.GoogleClientSecret
	savedMS := oauth2.MicrosoftClientID
	t.Cleanup(func() {
		oauth2.GoogleClientID, oauth2.GoogleClientSecret = savedID, savedSecret
		oauth2.MicrosoftClientID = savedMS
	})

	t.Run("a client id without a secret counts as configured", func(t *testing.T) {
		// Microsoft's public client runs PKCE, so an id alone is enough.
		oauth2.GoogleClientID = ""
		oauth2.GoogleClientSecret = ""
		oauth2.MicrosoftClientID = oauth2.ShippedMicrosoftClientID

		a := &App{}
		if got := a.GetOAuthBuildStatus(); !got.Microsoft {
			t.Error("Microsoft reported unconfigured despite a client id; " +
				"PKCE flows do not need a secret")
		}
	})

	t.Run("a configured id with a secret is still configured", func(t *testing.T) {
		oauth2.GoogleClientID = "my-app.apps.googleusercontent.com"
		oauth2.GoogleClientSecret = "my-secret"

		a := &App{}
		if got := a.GetOAuthBuildStatus(); !got.Google {
			t.Error("Google reported unconfigured with both id and secret set")
		}
	})

	t.Run("a truly empty slot stays unconfigured", func(t *testing.T) {
		oauth2.GoogleClientID = ""
		oauth2.GoogleClientSecret = ""
		oauth2.MicrosoftClientID = ""

		a := &App{}
		if got := a.GetOAuthBuildStatus(); got.Google || got.Microsoft {
			t.Errorf("empty slots must report unconfigured, got %+v", got)
		}
	})
}
