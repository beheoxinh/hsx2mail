package appstate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/database"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func TestGetUIStateDefault(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	state, err := store.GetUIState()
	if err != nil {
		t.Fatalf("GetUIState() error = %v", err)
	}
	if state.SidebarWidth != 240 {
		t.Errorf("SidebarWidth = %d, want 240", state.SidebarWidth)
	}
	if state.ListWidth != 420 {
		t.Errorf("ListWidth = %d, want 420", state.ListWidth)
	}
	if !state.UnifiedInboxExpanded {
		t.Error("UnifiedInboxExpanded = false, want true")
	}
	if state.ExpandedAccounts == nil {
		t.Error("ExpandedAccounts is nil, want initialized map")
	}
	if state.CollapsedFolders == nil {
		t.Error("CollapsedFolders is nil, want initialized map")
	}
}

func TestSaveGetUIState(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	saved := &UIState{
		SelectedAccountID:    "acct-1",
		SelectedFolderID:     "folder-1",
		SelectedFolderName:   "Inbox",
		SelectedFolderType:   "inbox",
		SidebarWidth:         300,
		ListWidth:            500,
		ExpandedAccounts:     map[string]bool{"acct-1": true, "acct-2": false},
		UnifiedInboxExpanded: false,
		CollapsedFolders:     map[string]bool{"folder-2": true},
	}

	if err := store.SaveUIState(saved); err != nil {
		t.Fatalf("SaveUIState() error = %v", err)
	}

	got, err := store.GetUIState()
	if err != nil {
		t.Fatalf("GetUIState() error = %v", err)
	}
	if got.SidebarWidth != 300 {
		t.Errorf("SidebarWidth = %d, want 300", got.SidebarWidth)
	}
	if got.ListWidth != 500 {
		t.Errorf("ListWidth = %d, want 500", got.ListWidth)
	}
	if got.SelectedAccountID != "acct-1" {
		t.Errorf("SelectedAccountID = %q, want %q", got.SelectedAccountID, "acct-1")
	}
	if got.SelectedFolderID != "folder-1" {
		t.Errorf("SelectedFolderID = %q, want %q", got.SelectedFolderID, "folder-1")
	}
	if got.UnifiedInboxExpanded {
		t.Error("UnifiedInboxExpanded = true, want false")
	}
	if !got.ExpandedAccounts["acct-1"] {
		t.Error("ExpandedAccounts[acct-1] = false, want true")
	}
	if got.ExpandedAccounts["acct-2"] {
		t.Error("ExpandedAccounts[acct-2] = true, want false")
	}
	if !got.CollapsedFolders["folder-2"] {
		t.Error("CollapsedFolders[folder-2] = false, want true")
	}
}

func TestSetGetDelete(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	// Set
	if err := store.Set("test_key", "test_value"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Get
	val, err := store.Get("test_key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if val != "test_value" {
		t.Errorf("Get() = %q, want %q", val, "test_value")
	}

	// Delete
	if err := store.Delete("test_key"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Get after delete returns empty string
	val, err = store.Get("test_key")
	if err != nil {
		t.Fatalf("Get() after Delete() error = %v", err)
	}
	if val != "" {
		t.Errorf("Get() after Delete() = %q, want empty string", val)
	}
}

func TestGetUIStateCorrupt(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	// Store invalid JSON under the ui_state key
	if err := store.Set(KeyUIState, "not valid json{{{"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// GetUIState should return defaults gracefully
	state, err := store.GetUIState()
	if err != nil {
		t.Fatalf("GetUIState() error = %v", err)
	}
	if state.SidebarWidth != 240 {
		t.Errorf("SidebarWidth = %d, want 240", state.SidebarWidth)
	}
	if state.ListWidth != 420 {
		t.Errorf("ListWidth = %d, want 420", state.ListWidth)
	}
	if !state.UnifiedInboxExpanded {
		t.Error("UnifiedInboxExpanded = false, want true")
	}
}

func TestSaveUIStateNilMaps(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	saved := &UIState{
		SidebarWidth:         280,
		ListWidth:            450,
		UnifiedInboxExpanded: true,
		ExpandedAccounts:     nil,
		CollapsedFolders:     nil,
	}

	if err := store.SaveUIState(saved); err != nil {
		t.Fatalf("SaveUIState() error = %v", err)
	}

	got, err := store.GetUIState()
	if err != nil {
		t.Fatalf("GetUIState() error = %v", err)
	}
	if got.SidebarWidth != 280 {
		t.Errorf("SidebarWidth = %d, want 280", got.SidebarWidth)
	}
	if got.ExpandedAccounts == nil {
		t.Error("ExpandedAccounts is nil, want initialized map")
	}
	if got.CollapsedFolders == nil {
		t.Error("CollapsedFolders is nil, want initialized map")
	}
}
