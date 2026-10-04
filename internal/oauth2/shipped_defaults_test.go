package oauth2

import (
	"testing"
)

// TestApplyShippedFallbacksFillsOnlyWhenEmpty is the precedence guard: a
// configured value must survive untouched, and only a genuinely empty slot gets
// the shipped client id.
func TestApplyShippedFallbacksFillsOnlyWhenEmpty(t *testing.T) {
	savedGoogle, savedGoogleSecret := GoogleClientID, GoogleClientSecret
	savedMicrosoft := MicrosoftClientID
	t.Cleanup(func() {
		GoogleClientID, GoogleClientSecret, MicrosoftClientID =
			savedGoogle, savedGoogleSecret, savedMicrosoft
	})
	t.Run("empty slots get shipped ids", func(t *testing.T) {
		GoogleClientID, GoogleClientSecret, MicrosoftClientID = "", "", ""
		applyShippedFallbacks()
		if GoogleClientID != ShippedGoogleClientID {
			t.Errorf("GoogleClientID = %q, want shipped %q", GoogleClientID, ShippedGoogleClientID)
		}
		if MicrosoftClientID != ShippedMicrosoftClientID {
			t.Errorf("MicrosoftClientID = %q, want shipped %q", MicrosoftClientID, ShippedMicrosoftClientID)
		}
	})
	t.Run("configured values are never overwritten", func(t *testing.T) {
		GoogleClientID = "my-app.apps.googleusercontent.com"
		GoogleClientSecret = "my-secret"
		MicrosoftClientID = "11111111-2222-3333-4444-555555555555"
		applyShippedFallbacks()
		if GoogleClientID != "my-app.apps.googleusercontent.com" {
			t.Errorf("GoogleClientID overwritten with %q", GoogleClientID)
		}
		if GoogleClientSecret != "my-secret" {
			t.Errorf("GoogleClientSecret overwritten with %q", GoogleClientSecret)
		}
		if MicrosoftClientID != "11111111-2222-3333-4444-555555555555" {
			t.Errorf("MicrosoftClientID overwritten with %q", MicrosoftClientID)
		}
	})
}

// TestApplyShippedFallbacksNeverSetsASecret is the guard against repeating the
// invalid_client outage: the shipped ids are client ids only, so no code path
// may synthesize a secret for them.
func TestApplyShippedFallbacksNeverSetsASecret(t *testing.T) {
	savedGoogle, savedGoogleSecret := GoogleClientID, GoogleClientSecret
	t.Cleanup(func() { GoogleClientID, GoogleClientSecret = savedGoogle, savedGoogleSecret })
	GoogleClientID, GoogleClientSecret = "", ""
	applyShippedFallbacks()
	if GoogleClientSecret != "" {
		t.Fatalf("applyShippedFallbacks invented a client secret: %q", GoogleClientSecret)
	}
}

// TestShippedIDsResolveThroughTheProvider checks the shipped values actually
// reach consumers — a constant that is never wired up is a silent no-op.
func TestShippedIDsResolveThroughTheProvider(t *testing.T) {
	savedGoogle, savedGoogleSecret := GoogleClientID, GoogleClientSecret
	savedMicrosoft := MicrosoftClientID
	t.Cleanup(func() {
		GoogleClientID, GoogleClientSecret, MicrosoftClientID =
			savedGoogle, savedGoogleSecret, savedMicrosoft
	})
	GoogleClientID, GoogleClientSecret, MicrosoftClientID = "", "", ""
	applyShippedFallbacks()
	g, ok := ShippedClientConfigForID("google-mail")
	if !ok {
		t.Fatal("google-mail unresolved after fallback")
	}
	if g.ClientID != ShippedGoogleClientID {
		t.Errorf("google-mail id = %q, want %q", g.ClientID, ShippedGoogleClientID)
	}
	m, ok := ShippedClientConfigForID("microsoft-mail")
	if !ok {
		t.Fatal("microsoft-mail unresolved after fallback")
	}
	if m.ClientID != ShippedMicrosoftClientID {
		t.Errorf("microsoft-mail id = %q, want %q", m.ClientID, ShippedMicrosoftClientID)
	}
	// Microsoft runs as a public client under PKCE; Google with its own secret
	// from configuration only.
	if m.ClientSecret != "" {
		t.Errorf("microsoft-mail has a secret; PKCE exchanges must send none")
	}
}
