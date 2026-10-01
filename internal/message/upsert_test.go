package message

import (
	"testing"
	"time"
)

// A header-only re-sync (UID already known) must not destroy state that the
// header pass does not carry: the row's id, its downloaded body, and its
// body_fetched flag. These three regressions each caused silent data loss.

func TestUpsert_ReinsertKeepsIDAndAttachmentsFK(t *testing.T) {
	s, accountID, folderID := newBodyFailedTestStore(t)
	now := time.Now().UTC()

	first := &Message{
		AccountID: accountID,
		FolderID:  folderID,
		UID:       42,
		Subject:   "Original subject",
		Date:      now,
	}
	if err := s.Upsert(first); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	firstID := first.ID
	if firstID == "" {
		t.Fatal("first Upsert did not set m.ID from RETURNING")
	}

	// Give the message an attachment row so the FK is live.
	att := NewAttachmentStore(s.db)
	if err := att.Create(&Attachment{MessageID: firstID, Filename: "a.txt", Size: 3}); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	// Re-upsert the same (folder, uid) with a new random ID, as the header
	// sync does on every pass.
	second := &Message{
		AccountID: accountID,
		FolderID:  folderID,
		UID:       42,
		Subject:   "Corrected subject",
		Date:      now,
	}
	if err := s.Upsert(second); err != nil {
		t.Fatalf("re-Upsert must not fail on the attachments FK: %v", err)
	}

	if second.ID != firstID {
		t.Errorf("re-Upsert changed the row id: got %q, want %q", second.ID, firstID)
	}

	got, err := s.Get(firstID)
	if err != nil {
		t.Fatalf("GetByID after re-Upsert: %v", err)
	}
	if got.Subject != "Corrected subject" {
		t.Errorf("header not refreshed: got %q, want %q", got.Subject, "Corrected subject")
	}

	// The attachment must still point at a live message.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE message_id = ?`, firstID).Scan(&n); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 1 {
		t.Errorf("attachment lost its parent: got %d rows, want 1", n)
	}
}

func TestUpsert_HeaderOnlyPassPreservesBody(t *testing.T) {
	s, accountID, folderID := newBodyFailedTestStore(t)
	now := time.Now().UTC()

	withBody := &Message{
		AccountID:   accountID,
		FolderID:    folderID,
		UID:         7,
		Subject:     "Has a body",
		Date:        now,
		BodyText:    "the full text body",
		BodyHTML:    "<p>the full html body</p>",
		BodyFetched: true,
	}
	if err := s.Upsert(withBody); err != nil {
		t.Fatalf("Upsert with body: %v", err)
	}

	// A later header-only sync: no body, BodyFetched false.
	headerOnly := &Message{
		AccountID: accountID,
		FolderID:  folderID,
		UID:       7,
		Subject:   "Has a body (renamed)",
		Date:      now,
	}
	if err := s.Upsert(headerOnly); err != nil {
		t.Fatalf("Upsert header-only: %v", err)
	}

	got, err := s.Get(withBody.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BodyText != "the full text body" {
		t.Errorf("body_text was blanked by a header-only re-sync: got %q", got.BodyText)
	}
	if got.BodyHTML != "<p>the full html body</p>" {
		t.Errorf("body_html was blanked by a header-only re-sync: got %q", got.BodyHTML)
	}
	if !got.BodyFetched {
		t.Error("body_fetched was reset to false by a header-only re-sync")
	}
	if got.Subject != "Has a body (renamed)" {
		t.Errorf("subject not refreshed: got %q", got.Subject)
	}
}

func TestUpsert_ExplicitBodyFetchStillWins(t *testing.T) {
	s, accountID, folderID := newBodyFailedTestStore(t)
	now := time.Now().UTC()

	first := &Message{
		AccountID:   accountID,
		FolderID:    folderID,
		UID:       9,
		Date:        now,
		BodyText:    "stale",
		BodyFetched: true,
	}
	if err := s.Upsert(first); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	refreshed := &Message{
		AccountID:   accountID,
		FolderID:    folderID,
		UID:         9,
		Date:        now,
		BodyText:    "fresh",
		BodyFetched: true,
	}
	if err := s.Upsert(refreshed); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := s.Get(first.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BodyText != "fresh" {
		t.Errorf("explicit body re-fetch did not overwrite: got %q, want %q", got.BodyText, "fresh")
	}
}
