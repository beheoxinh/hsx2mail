package platform

import (
	"strings"
	"testing"
)

func TestGetPaths(t *testing.T) {
	paths, err := GetPaths()
	if err != nil {
		t.Fatalf("GetPaths() returned error: %v", err)
	}
	if paths == nil {
		t.Fatal("GetPaths() returned nil")
	}
	if paths.Config == "" {
		t.Error("GetPaths().Config is empty")
	}
	if paths.Data == "" {
		t.Error("GetPaths().Data is empty")
	}
	if paths.Cache == "" {
		t.Error("GetPaths().Cache is empty")
	}
}

func TestDatabasePath(t *testing.T) {
	paths, err := GetPaths()
	if err != nil {
		t.Fatalf("GetPaths() returned error: %v", err)
	}
	dbPath := paths.DatabasePath()
	if !strings.HasSuffix(dbPath, "hsx2mail.db") {
		t.Errorf("DatabasePath() = %q, want suffix 'hsx2mail.db'", dbPath)
	}
}

func TestContactsDatabasePath(t *testing.T) {
	paths, err := GetPaths()
	if err != nil {
		t.Fatalf("GetPaths() returned error: %v", err)
	}
	contactsPath := paths.ContactsDatabasePath()
	if !strings.HasSuffix(contactsPath, "contacts.db") {
		t.Errorf("ContactsDatabasePath() = %q, want suffix 'contacts.db'", contactsPath)
	}
}

func TestSearchIndexPath(t *testing.T) {
	paths, err := GetPaths()
	if err != nil {
		t.Fatalf("GetPaths() returned error: %v", err)
	}
	accountID := "test-account-123"
	indexPath := paths.SearchIndexPath(accountID)
	if !strings.Contains(indexPath, accountID) {
		t.Errorf("SearchIndexPath() = %q, want to contain %q", indexPath, accountID)
	}
}

func TestKeyringPath(t *testing.T) {
	paths, err := GetPaths()
	if err != nil {
		t.Fatalf("GetPaths() returned error: %v", err)
	}
	keyringPath := paths.KeyringPath()
	if !strings.HasSuffix(keyringPath, "keys") {
		t.Errorf("KeyringPath() = %q, want suffix 'keys'", keyringPath)
	}
}

func TestIsFlatpak(t *testing.T) {
	tests := []struct {
		name      string
		flatpakID string
		want      bool
	}{
		{
			name:      "not flatpak by default",
			flatpakID: "",
			want:      false,
		},
		{
			name:      "flatpak when FLATPAK_ID is set",
			flatpakID: "com.example.App",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FLATPAK_ID", tt.flatpakID)
			got := IsFlatpak()
			if got != tt.want {
				t.Errorf("IsFlatpak() = %v, want %v", got, tt.want)
			}
		})
	}
}
