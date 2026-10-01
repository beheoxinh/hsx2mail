package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/draft"
	"github.com/beheoxinh/hsx2mail/internal/smtp"
)

// newStagingOps builds a draftOps with only the staging store wired, which is
// all resolveAttachmentContent / stageAttachments touch.
func newStagingOps(t *testing.T) *draftOps {
	t.Helper()
	return &draftOps{
		staging: draft.NewStagingStore(filepath.Join(t.TempDir(), "staging")),
	}
}

// ---------------------------------------------------------------------------
// 1. Staging a file then reading it back round-trips
// ---------------------------------------------------------------------------

func TestStagingFileRoundTrip(t *testing.T) {
	ops := newStagingOps(t)
	content := []byte("PDF-ish bytes \x00\x01\xfe\xff that are definitely not text")

	// The frontend picks a file: bytes go to the store, only an id comes back.
	picked, err := readFileAsAttachment(ops.staging, writeTempFile(t, content, "report.pdf"))
	if err != nil {
		t.Fatalf("readFileAsAttachment() returned error: %v", err)
	}
	if picked.Data != "" {
		t.Errorf("readFileAsAttachment() returned %d base64 chars, want none (staged)", len(picked.Data))
	}
	if picked.StagingID == "" {
		t.Fatal("readFileAsAttachment() returned no staging id")
	}
	if picked.Size != len(content) {
		t.Errorf("Size = %d, want %d", picked.Size, len(content))
	}

	// The autosave sends only the id; the send path resolves the bytes back.
	// A large file is the point of the exercise, so make it realistically big.
	big := bytes.Repeat([]byte("A"), 50*1024*1024)
	bigID, err := ops.staging.Put(big)
	if err != nil {
		t.Fatalf("Put() returned error: %v", err)
	}
	resolved, err := ops.resolveAttachmentContent([]smtp.Attachment{{
		Filename:  "big.bin",
		StagingID: bigID,
	}})
	if err != nil {
		t.Fatalf("resolveAttachmentContent() returned error: %v", err)
	}
	if !bytes.Equal(resolved[0].Content, big) {
		t.Errorf("resolved content is %d bytes, want %d", len(resolved[0].Content), len(big))
	}
}

// The autosave payload for a staged attachment must be metadata only.
func TestStagedAutosavePayloadCarriesNoBytes(t *testing.T) {
	ops := newStagingOps(t)
	content := bytes.Repeat([]byte("B"), 5*1024*1024)

	staged := ops.stageAttachments([]smtp.Attachment{{
		Filename:      "big.bin",
		ContentType:   "application/octet-stream",
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	}})
	if len(staged) != 1 {
		t.Fatalf("stageAttachments() returned %d attachments, want 1", len(staged))
	}
	if staged[0].StagingID == "" {
		t.Fatal("stageAttachments() produced no staging id")
	}
	if len(staged[0].Content) != 0 || staged[0].ContentBase64 != "" {
		t.Error("stageAttachments() left attachment bytes in the payload")
	}

	payload, err := json.Marshal(staged)
	if err != nil {
		t.Fatalf("json.Marshal() returned error: %v", err)
	}
	// 5 MB of content would be ~6.7 MB of base64; metadata is ~150 bytes.
	if len(payload) > 1024 {
		t.Errorf("autosave payload = %d bytes, want metadata only (<1 KB)", len(payload))
	}

	// And it must still resolve back to the original bytes.
	resolved, err := ops.resolveAttachmentContent(staged)
	if err != nil {
		t.Fatalf("resolveAttachmentContent() returned error: %v", err)
	}
	if !bytes.Equal(resolved[0].Content, content) {
		t.Error("staged attachment did not round-trip to the original bytes")
	}
}

// ---------------------------------------------------------------------------
// 2. The legacy base64 format still decodes
// ---------------------------------------------------------------------------

func TestLegacyBase64AttachmentDecodes(t *testing.T) {
	ops := newStagingOps(t)
	content := []byte("legacy draft attachment bytes")

	resolved, err := ops.resolveAttachmentContent([]smtp.Attachment{{
		Filename:      "legacy.txt",
		ContentType:   "text/plain",
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	}})
	if err != nil {
		t.Fatalf("resolveAttachmentContent() returned error: %v", err)
	}
	if !bytes.Equal(resolved[0].Content, content) {
		t.Errorf("legacy content = %q, want %q", resolved[0].Content, content)
	}
	if resolved[0].ContentBase64 != "" {
		t.Error("legacy base64 was not cleared after decoding")
	}
}

// An existing draft row written before staging stores attachments as a JSON
// array of smtp.Attachment with base64 `content`. It must unmarshal and
// resolve exactly as it did before.
func TestLegacyDraftRowStillLoads(t *testing.T) {
	ops := newStagingOps(t)
	content := []byte("bytes as stored in an old attachments_data blob")

	legacyRow, err := json.Marshal([]smtp.Attachment{{
		Filename:    "old.bin",
		ContentType: "application/octet-stream",
		Content:     content,
	}})
	if err != nil {
		t.Fatalf("json.Marshal() returned error: %v", err)
	}

	d := &draft.Draft{Subject: "old draft", AttachmentsData: legacyRow}
	msg := ops.toComposeMessage(d)

	if len(msg.Attachments) != 1 {
		t.Fatalf("toComposeMessage() returned %d attachments, want 1", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.Filename != "old.bin" {
		t.Errorf("Filename = %q, want %q", att.Filename, "old.bin")
	}
	if !bytes.Equal(att.Content, content) {
		t.Error("legacy draft attachment did not survive the load path")
	}
}

// ---------------------------------------------------------------------------
// 3. A draft saved with staging ids reloads with correct filenames/sizes
// ---------------------------------------------------------------------------

func TestStagedDraftReloadsWithMetadata(t *testing.T) {
	ops := newStagingOps(t)

	first := []byte("first attachment payload")
	second := []byte("second, differently sized attachment payload")

	msg := &smtp.ComposeMessage{
		Subject: "hello",
		Attachments: []smtp.Attachment{
			{Filename: "a.txt", ContentType: "text/plain", Content: first},
			{Filename: "b.pdf", ContentType: "application/pdf", Content: second},
		},
	}

	enc, err := ops.encryptDraftBody("acct-1", "me@example.com", *msg)
	if err != nil {
		t.Fatalf("encryptDraftBody() returned error: %v", err)
	}

	d := &draft.Draft{Subject: "hello", AttachmentsData: enc.attachmentsData}
	reloaded := ops.toComposeMessage(d)

	if len(reloaded.Attachments) != 2 {
		t.Fatalf("reloaded %d attachments, want 2", len(reloaded.Attachments))
	}

	want := []struct {
		filename string
		size     int
		content  []byte
	}{
		{"a.txt", len(first), first},
		{"b.pdf", len(second), second},
	}
	for i, w := range want {
		att := reloaded.Attachments[i]
		if att.Filename != w.filename {
			t.Errorf("attachment %d Filename = %q, want %q", i, att.Filename, w.filename)
		}
		if att.Size != w.size {
			t.Errorf("attachment %d Size = %d, want %d", i, att.Size, w.size)
		}
		if att.StagingID == "" {
			t.Errorf("attachment %d has no staging id after reload", i)
		}
		if len(att.Content) != 0 || att.ContentBase64 != "" {
			t.Errorf("attachment %d carried %d bytes across the bridge, want 0",
				i, len(att.Content)+len(att.ContentBase64))
		}

		// Sending it must produce the original bytes.
		resolved, err := ops.resolveAttachmentContent([]smtp.Attachment{att})
		if err != nil {
			t.Fatalf("resolveAttachmentContent() on reloaded attachment %d: %v", i, err)
		}
		if !bytes.Equal(resolved[0].Content, w.content) {
			t.Errorf("attachment %d did not resolve back to its original bytes", i)
		}
	}
}

// The stored row must be metadata-sized, not content-sized.
func TestStagedDraftRowIsSmall(t *testing.T) {
	ops := newStagingOps(t)
	content := bytes.Repeat([]byte("C"), 20*1024*1024)

	enc, err := ops.encryptDraftBody("acct-1", "me@example.com", smtp.ComposeMessage{
		Subject:     "big",
		Attachments: []smtp.Attachment{{Filename: "big.bin", Content: content}},
	})
	if err != nil {
		t.Fatalf("encryptDraftBody() returned error: %v", err)
	}
	if len(enc.attachmentsData) > 1024 {
		t.Errorf("attachments_data = %d bytes, want metadata only (<1 KB)", len(enc.attachmentsData))
	}
	if !bytes.Contains(enc.attachmentsData, []byte("staging_id")) {
		t.Error("attachments_data does not carry a staging_id")
	}
}

// A draft re-saved after a reload must not re-stage or re-send: the id is
// stable, so the row round-trips unchanged.
func TestStagedDraftResaveIsIdempotent(t *testing.T) {
	ops := newStagingOps(t)
	content := []byte("stable content")

	first, err := ops.encryptDraftBody("acct-1", "me@example.com", smtp.ComposeMessage{
		Attachments: []smtp.Attachment{{Filename: "a.txt", Content: content}},
	})
	if err != nil {
		t.Fatalf("first encryptDraftBody() returned error: %v", err)
	}
	firstID := stagingIDFromRow(t, first.attachmentsData)

	// Reload then re-save, as an autosave after reopening a draft does.
	reloaded := ops.toComposeMessage(&draft.Draft{AttachmentsData: first.attachmentsData})
	second, err := ops.encryptDraftBody("acct-1", "me@example.com", *reloaded)
	if err != nil {
		t.Fatalf("second encryptDraftBody() returned error: %v", err)
	}
	secondID := stagingIDFromRow(t, second.attachmentsData)

	if firstID != secondID {
		t.Errorf("re-save changed the staging id: %q then %q", firstID, secondID)
	}
	if !bytes.Equal(first.attachmentsData, second.attachmentsData) {
		t.Error("re-saving a staged draft changed the stored row")
	}
}

// Carried-over reply/forward attachments must arrive at the frontend as a
// reusable staging id, not base64, so re-saving the draft never re-sends bytes.
func TestCarriedOverAttachmentsAreStagedNotInlined(t *testing.T) {
	ops := newStagingOps(t)

	// Mirrors what buildReplyMessage hands over: one inline image plus one
	// regular attachment fetched from the original message.
	inlineB64 := base64.StdEncoding.EncodeToString([]byte("inline png bytes"))
	carried := ops.stageAttachments([]smtp.Attachment{
		{Filename: "cid:img", ContentType: "image/png", ContentBase64: inlineB64, ContentID: "img", Inline: true},
		{Filename: "report.pdf", ContentType: "application/pdf", Content: []byte("carried pdf bytes")},
	})

	// Inline keeps its bytes so the quoted body can render it.
	if carried[0].ContentBase64 != inlineB64 {
		t.Error("inline carry-over lost its base64, quoted body would break")
	}
	// Regular is staged and carries no bytes.
	if carried[1].StagingID == "" {
		t.Error("carried-over regular attachment has no staging id")
	}
	if len(carried[1].Content) != 0 {
		t.Error("carried-over regular attachment still carries bytes to the frontend")
	}

	// The id is the one the original attachment resolves to, so a draft
	// built from this reply reuses it instead of re-uploading.
	want := draft.StagingID([]byte("carried pdf bytes"))
	if carried[1].StagingID != want {
		t.Errorf("carry-over staging id = %q, want the content digest %q", carried[1].StagingID, want)
	}
}

// ---------------------------------------------------------------------------
// Mixed and degraded cases
// ---------------------------------------------------------------------------

// Inline images stay base64: the composer renders them as data URLs, so
// staging them would break the quoted body.
func TestInlineAttachmentsAreNotStaged(t *testing.T) {
	ops := newStagingOps(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("inline image bytes"))

	staged := ops.stageAttachments([]smtp.Attachment{{
		Filename:      "cid:image",
		ContentBase64: encoded,
		ContentID:     "image",
		Inline:        true,
	}})
	if staged[0].StagingID != "" {
		t.Error("inline attachment was staged, want inline base64 preserved")
	}
	if staged[0].ContentBase64 != encoded {
		t.Error("inline attachment lost its base64 content")
	}
}

// A missing staging file degrades to the inline form rather than failing the
// whole draft.
func TestMissingStagedFileDegradesGracefully(t *testing.T) {
	ops := newStagingOps(t)

	resolved, err := ops.resolveAttachmentContent([]smtp.Attachment{{
		Filename:      "gone.bin",
		StagingID:     draft.StagingID([]byte("deleted upstream")),
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("fallback")),
	}})
	if err != nil {
		t.Fatalf("resolveAttachmentContent() returned error: %v", err)
	}
	if string(resolved[0].Content) != "fallback" {
		t.Errorf("Content = %q, want the inline fallback", resolved[0].Content)
	}
}

// A nil staging store must not break composing: bytes stay inline.
func TestNilStagingStoreKeepsAttachmentsInline(t *testing.T) {
	ops := &draftOps{}
	encoded := base64.StdEncoding.EncodeToString([]byte("inline only"))

	staged := ops.stageAttachments([]smtp.Attachment{{
		Filename:      "a.txt",
		ContentBase64: encoded,
	}})
	if staged[0].StagingID != "" {
		t.Error("nil store produced a staging id")
	}
	if staged[0].ContentBase64 != encoded {
		t.Error("nil store dropped the inline content")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// stagingIDFromRow pulls the single staging id out of a stored attachments blob.
func stagingIDFromRow(t *testing.T, row []byte) string {
	t.Helper()
	var atts []smtp.Attachment
	if err := json.Unmarshal(row, &atts); err != nil {
		t.Fatalf("json.Unmarshal() returned error: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("row has %d attachments, want 1", len(atts))
	}
	if atts[0].StagingID == "" {
		t.Fatal("stored row has no staging id")
	}
	return atts[0].StagingID
}

// writeTempFile writes content to a temp file named name and returns its path.
func writeTempFile(t *testing.T, content []byte, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}
