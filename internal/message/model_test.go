package message

import (
	"testing"
	"time"
)

func TestDefaultFetchOptions(t *testing.T) {
	opts := DefaultFetchOptions()

	if !opts.Envelope {
		t.Error("DefaultFetchOptions().Envelope should be true")
	}
	if !opts.Flags {
		t.Error("DefaultFetchOptions().Flags should be true")
	}
	if opts.BodyText {
		t.Error("DefaultFetchOptions().BodyText should be false")
	}
	if opts.BodyHTML {
		t.Error("DefaultFetchOptions().BodyHTML should be false")
	}
	if opts.Attachments {
		t.Error("DefaultFetchOptions().Attachments should be false")
	}
}

func TestFullFetchOptions(t *testing.T) {
	opts := FullFetchOptions()

	if !opts.Envelope {
		t.Error("FullFetchOptions().Envelope should be true")
	}
	if !opts.Flags {
		t.Error("FullFetchOptions().Flags should be true")
	}
	if !opts.BodyText {
		t.Error("FullFetchOptions().BodyText should be true")
	}
	if !opts.BodyHTML {
		t.Error("FullFetchOptions().BodyHTML should be true")
	}
	if !opts.Attachments {
		t.Error("FullFetchOptions().Attachments should be true")
	}
}

func TestToHeader(t *testing.T) {
	now := time.Now()
	msg := &Message{
		ID:             "msg-1",
		AccountID:      "acc-1",
		FolderID:       "folder-inbox",
		UID:            100,
		Subject:        "Test Subject",
		FromName:       "Alice",
		FromEmail:      "alice@example.com",
		Date:           now,
		Snippet:        "Hello world...",
		IsRead:         true,
		IsStarred:      true,
		HasAttachments: true,
	}

	header := msg.ToHeader()

	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"ID", header.ID, msg.ID},
		{"AccountID", header.AccountID, msg.AccountID},
		{"FolderID", header.FolderID, msg.FolderID},
		{"UID", header.UID, msg.UID},
		{"Subject", header.Subject, msg.Subject},
		{"FromName", header.FromName, msg.FromName},
		{"FromEmail", header.FromEmail, msg.FromEmail},
		{"Date", header.Date, msg.Date},
		{"Snippet", header.Snippet, msg.Snippet},
		{"IsRead", header.IsRead, msg.IsRead},
		{"IsStarred", header.IsStarred, msg.IsStarred},
		{"HasAttachments", header.HasAttachments, msg.HasAttachments},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("ToHeader().%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestAddressStruct(t *testing.T) {
	addr := Address{
		Name:  "Bob Smith",
		Email: "bob@example.com",
	}

	if addr.Name != "Bob Smith" {
		t.Errorf("Address.Name = %q, want %q", addr.Name, "Bob Smith")
	}
	if addr.Email != "bob@example.com" {
		t.Errorf("Address.Email = %q, want %q", addr.Email, "bob@example.com")
	}
}
