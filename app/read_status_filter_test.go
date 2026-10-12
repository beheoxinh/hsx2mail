package app

import (
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/message"
)

// Unit test — pure slice filtering, no store, no IMAP, no socket.

func TestFilterNeedingReadChange(t *testing.T) {
	read := func(id string) *message.Message {
		return &message.Message{ID: id, IsRead: true}
	}
	unread := func(id string) *message.Message {
		return &message.Message{ID: id, IsRead: false}
	}

	tests := []struct {
		name     string
		messages []*message.Message
		isRead   bool
		want     []string
	}{
		{
			name:     "all already read — nothing to send",
			messages: []*message.Message{read("a"), read("b")},
			isRead:   true,
			want:     []string{},
		},
		{
			name:     "all already unread — nothing to send",
			messages: []*message.Message{unread("a"), unread("b")},
			isRead:   false,
			want:     []string{},
		},
		{
			name:     "mixed — only the unread ones need marking read",
			messages: []*message.Message{read("a"), unread("b"), unread("c")},
			isRead:   true,
			want:     []string{"b", "c"},
		},
		{
			name:     "mixed — only the read ones need marking unread",
			messages: []*message.Message{read("a"), unread("b"), read("c")},
			isRead:   false,
			want:     []string{"a", "c"},
		},
		{
			name:     "empty input",
			messages: []*message.Message{},
			isRead:   true,
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterNeedingReadChange(tt.messages, tt.isRead)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, m := range got {
				if m.ID != tt.want[i] {
					t.Errorf("got[%d].ID = %q, want %q", i, m.ID, tt.want[i])
				}
				if m.IsRead == tt.isRead {
					t.Errorf("got[%d] already in requested state (IsRead=%v)", i, m.IsRead)
				}
			}
		})
	}
}
