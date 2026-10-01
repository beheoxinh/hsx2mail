package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/credentials"
	"github.com/beheoxinh/hsx2mail/internal/database"
	"github.com/beheoxinh/hsx2mail/internal/oauth2"
)

// TestGetValidOAuthTokenSerializesRefresh covers 5-05: N concurrent callers that
// all see an expiring token must produce exactly ONE network refresh. Providers
// that rotate the refresh token revoke the previous one, so a second POST of
// the same token leaves the caller holding a dead refresh token.
func TestGetValidOAuthTokenSerializesRefresh(t *testing.T) {
	const (
		accountID    = "acct-refresh-race"
		oldRefresh   = "refresh-token-OLD"
		newRefresh   = "refresh-token-NEW"
		newAccess    = "access-token-NEW"
		rounds       = 8
		wantRequests = 1
	)

	var tokenRequests atomic.Int64
	var revoked atomic.Bool

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests.Add(1)
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != oldRefresh {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid_grant"}`)
			return
		}
		// Rotating provider: the presented refresh token is burned.
		revoked.Store(true)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":3600,"token_type":"Bearer"}`, newAccess, newRefresh)
	}))
	defer tokenSrv.Close()

	credStore, db := newTestCredentialStore(t)
	insertAccount(t, db, accountID)

	// Custom provider → refreshOAuthToken takes the per-account config path,
	// which lets the test point TokenURL at the local server.
	if err := credStore.SetCustomOAuthProvider(accountID, credentials.CustomOAuthProvider{
		AuthURL:      tokenSrv.URL + "/authorize",
		TokenURL:     tokenSrv.URL + "/token",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
	}); err != nil {
		t.Fatalf("SetCustomOAuthProvider: %v", err)
	}
	if err := credStore.SetOAuthTokens(accountID, &credentials.OAuthTokens{
		Provider:     customOAuthProviderName,
		AccessToken:  "access-token-OLD",
		RefreshToken: oldRefresh,
		ExpiresAt:    time.Now().Add(time.Minute), // inside the 5m window
		Scopes:       []string{"https://example.test/mail"},
	}); err != nil {
		t.Fatalf("SetOAuthTokens: %v", err)
	}

	ops := &composeOps{credStore: credStore, oauth2Manager: oauth2.NewManager()}

	// Release all goroutines at once to maximize the overlap.
	var start sync.WaitGroup
	start.Add(1)

	var wg sync.WaitGroup
	errs := make([]error, rounds)
	tokens := make([]*credentials.OAuthTokens, rounds)
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			tokens[i], errs[i] = ops.getValidOAuthToken(context.Background(), accountID)
		}(i)
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed: %v", i, err)
		}
		if tokens[i] == nil || tokens[i].AccessToken != newAccess {
			t.Errorf("caller %d got access token %v, want %q", i, tokens[i], newAccess)
		}
		if tokens[i].RefreshToken != newRefresh {
			t.Errorf("caller %d got refresh token %q, want the rotated %q", i, tokens[i].RefreshToken, newRefresh)
		}
	}

	if got := tokenRequests.Load(); got != wantRequests {
		t.Fatalf("token endpoint was hit %d times, want %d — refresh is not serialized", got, wantRequests)
	}
	if !revoked.Load() {
		t.Fatal("token endpoint never received the original refresh token")
	}

	stored, err := credStore.GetOAuthTokens(accountID)
	if err != nil {
		t.Fatalf("GetOAuthTokens: %v", err)
	}
	if stored.RefreshToken != newRefresh {
		t.Fatalf("stored refresh token = %q, want the rotated %q", stored.RefreshToken, newRefresh)
	}
}

// TestGetValidOAuthTokenDifferentAccountsDoNotBlock makes sure the lock is
// per-account and not a global stall on the refresh path.
func TestGetValidOAuthTokenDifferentAccountsDoNotBlock(t *testing.T) {
	credStore, _ := newTestCredentialStore(t)

	ops := &composeOps{credStore: credStore}

	a := ops.tokenRefreshLock("acct-a")
	b := ops.tokenRefreshLock("acct-b")
	if a == b {
		t.Fatal("distinct accounts share one refresh lock")
	}
	if ops.tokenRefreshLock("acct-a") != a {
		t.Fatal("tokenRefreshLock returned a new lock for a known account")
	}
}

// insertAccount creates the accounts row the oauth_tokens FK points at.
func insertAccount(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO accounts (
			id, name, email, username,
			imap_host, imap_port, imap_security,
			smtp_host, smtp_port, smtp_security,
			auth_type, enabled, order_index, color,
			sync_period_days, sync_interval, sync_all_folders,
			secondary_sync_interval, read_receipt_request_policy,
			created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		id, "Test", id+"@example.test", id+"@example.test",
		"imap.example.test", 993, "ssl",
		"smtp.example.test", 465, "ssl",
		"oauth2", 1, 0, "#000000",
		30, 30, 0, 30, "never",
	); err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

func newTestCredentialStore(t *testing.T) (*credentials.Store, *sql.DB) {
	t.Helper()
	wrapped, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := wrapped.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { wrapped.Close() })

	store, err := credentials.NewStore(wrapped.DB, t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, wrapped.DB
}
