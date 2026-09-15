package settings

import (
	"testing"
)

func TestAddAndList(t *testing.T) {
	store := NewImageAllowlistStore(openTestDB(t))

	if err := store.Add("sender", "test@example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error on list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Type != "sender" {
		t.Errorf("type: got %q, want %q", entries[0].Type, "sender")
	}
	if entries[0].Value != "test@example.com" {
		t.Errorf("value: got %q, want %q", entries[0].Value, "test@example.com")
	}
}

func TestAddValidation(t *testing.T) {
	tests := []struct {
		name      string
		entryType string
		value     string
		wantErr   bool
	}{
		{name: "valid_sender", entryType: "sender", value: "test@example.com"},
		{name: "valid_domain", entryType: "domain", value: "example.com"},
		{name: "empty_value", entryType: "sender", value: "", wantErr: true},
		{name: "whitespace_value", entryType: "sender", value: "   ", wantErr: true},
		{name: "invalid_type", entryType: "invalid", value: "test@example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewImageAllowlistStore(openTestDB(t))

			err := store.Add(tt.entryType, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestIsAllowedBySender(t *testing.T) {
	store := NewImageAllowlistStore(openTestDB(t))

	if err := store.Add("sender", "test@example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	allowed, err := store.IsAllowed("test@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed, got not allowed")
	}
}

func TestIsAllowedByDomain(t *testing.T) {
	store := NewImageAllowlistStore(openTestDB(t))

	if err := store.Add("domain", "example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	allowed, err := store.IsAllowed("anyone@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed, got not allowed")
	}
}

func TestIsAllowedNotInList(t *testing.T) {
	store := NewImageAllowlistStore(openTestDB(t))

	allowed, err := store.IsAllowed("unknown@unknown.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected not allowed, got allowed")
	}
}

func TestRemove(t *testing.T) {
	store := NewImageAllowlistStore(openTestDB(t))

	if err := store.Add("sender", "test@example.com"); err != nil {
		t.Fatalf("unexpected error on add: %v", err)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error on list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	if err := store.Remove(entries[0].ID); err != nil {
		t.Fatalf("unexpected error on remove: %v", err)
	}

	entries, err = store.List()
	if err != nil {
		t.Fatalf("unexpected error on list after remove: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries after remove, want 0", len(entries))
	}
}
