package oauth2

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refreshTestManager returns a Manager whose HTTP client is srv.Client().
func refreshTestManager(t *testing.T, srv *httptest.Server) *Manager {
	t.Helper()
	m := NewManager()
	m.httpClient = srv.Client()
	return m
}

func refreshTestProvider(name, tokenURL string, withSecret bool) ProviderConfig {
	p := ProviderConfig{Name: name, ClientID: "cid", TokenURL: tokenURL}
	if withSecret {
		p.ClientSecret = "secret"
	}
	return p
}

// TestRefreshTokenTerminalCodesBecomeReauthRequired pins the classification the
// IDLE loop and compose path branch on: only genuinely unfixable provider errors
// may surface as ErrReauthRequired.
func TestRefreshTokenTerminalCodesBecomeReauthRequired(t *testing.T) {
	terminal := []string{"invalid_grant", "invalid_client", "unauthorized_client"}
	for _, code := range terminal {
		t.Run(code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             code,
					"error_description": "simulated",
				})
			}))
			defer srv.Close()

			m := refreshTestManager(t, srv)
			_, err := m.RefreshTokenWithProvider(refreshTestProvider("google", srv.URL, true), "stale-refresh-token")
			if err == nil {
				t.Fatal("expected error")
			}
			if !IsReauthRequired(err) {
				t.Fatalf("code %q: IsReauthRequired = false, want true (err=%v)", code, err)
			}
			var te *TokenError
			if !errors.As(err, &te) {
				t.Fatalf("errors.As(*TokenError) failed for %q", code)
			}
			if te.Code != code {
				t.Fatalf("TokenError.Code = %q, want %q", te.Code, code)
			}
		})
	}
}

// TestRefreshTokenTransientErrorsStayRetryable is the guard against the
// over-eager reauth prompt: a server-side hiccup must NOT tell the user their
// grant is dead, because retrying will actually work.
func TestRefreshTokenTransientErrorsStayRetryable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"server_error", http.StatusInternalServerError, "server_error"},
		{"temporarily_unavailable", http.StatusBadRequest, "temporarily_unavailable"},
		{"access_denied", http.StatusBadRequest, "access_denied"},
		{"invalid_request", http.StatusBadRequest, "invalid_request"},
		{"non_json_body", http.StatusBadGateway, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.code == "" {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte("<html>gateway</html>"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             tc.code,
					"error_description": "simulated",
				})
			}))
			defer srv.Close()

			m := refreshTestManager(t, srv)
			_, err := m.RefreshTokenWithProvider(refreshTestProvider("google", srv.URL, true), "refresh-token")
			if err == nil {
				t.Fatal("expected error")
			}
			if IsReauthRequired(err) {
				t.Fatalf("%s: IsReauthRequired = true, want false (err=%v)", tc.name, err)
			}
		})
	}
}

// TestRefreshTokenSuccessPreservesOldRefreshToken covers the provider quirk
// where some omit refresh_token on refresh. Losing it would strand the account
// on the next expiry.
func TestRefreshTokenSuccessPreservesOldRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// no refresh_token in the response
		_, _ = w.Write([]byte(`{"access_token":"new","expires_in":3600}`))
	}))
	defer srv.Close()

	m := refreshTestManager(t, srv)
	tok, err := m.RefreshTokenWithProvider(refreshTestProvider("google", srv.URL, true), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshTokenWithProvider() error = %v", err)
	}
	if tok.AccessToken != "new" {
		t.Fatalf("AccessToken = %q, want %q", tok.AccessToken, "new")
	}
	if tok.RefreshToken != "old-refresh" {
		t.Fatalf("RefreshToken = %q, want the original to be carried forward", tok.RefreshToken)
	}
}

// TestRefreshTokenOmitsEmptyClientSecret pins the MS-PKCE path: sending
// client_secret="" makes Microsoft reject the grant, so the field must be
// absent entirely for public clients.
func TestRefreshTokenOmitsEmptyClientSecret(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","expires_in":3600}`))
	}))
	defer srv.Close()

	m := refreshTestManager(t, srv)
	// public client: no secret
	if _, err := m.RefreshTokenWithProvider(refreshTestProvider("microsoft", srv.URL, false), "rt"); err != nil {
		t.Fatalf("RefreshTokenWithProvider() error = %v", err)
	}
	if strings.Contains(gotBody, "client_secret") {
		t.Fatalf("request body leaked an empty client_secret: %s", gotBody)
	}
}
