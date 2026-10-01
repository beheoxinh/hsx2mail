package sync

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/beheoxinh/hsx2mail/internal/message"
	gomessage "github.com/emersion/go-message"
)

// computeThreadID determines the thread ID for a message
func (e *Engine) computeThreadID(accountID string, m *message.Message) string {
	// Parse references from JSON
	refs := messageReferences(m)

	// Try to find existing thread
	threadID, err := e.messageStore.FindThreadID(accountID, m.MessageID, m.InReplyTo, refs)
	if err != nil {
		e.log.Debug().Err(err).Msg("Error finding thread ID, using message ID")
		return m.MessageID
	}

	return threadID
}

// computeThreadIDsBatch is computeThreadID for a whole header batch, with the
// per-reference lookups hoisted into one indexed query.
//
// ComputeThreadID queried once per reference, so a 500-message batch with 8
// references each issued ~4,000 queries while the IMAP connection was still
// open. Resolution order is preserved exactly: within one message the refs are
// probed in order (In-Reply-To first, then References), and the pre-built map
// is queried in that same order.
func (e *Engine) computeThreadIDsBatch(accountID string, msgs []*message.Message) map[string]string {
	// Collect every reference in the batch, keeping the first occurrence of
	// each so the map is built from the same candidate set FindThreadID
	// would have walked.
	seen := make(map[string]bool)
	candidates := make([]string, 0, len(msgs))
	for _, m := range msgs {
		for _, ref := range threadCandidates(m) {
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			candidates = append(candidates, ref)
		}
	}

	resolved := make(map[string]string, len(candidates))
	if len(candidates) > 0 {
		var err error
		resolved, err = e.messageStore.FindThreadIDsBatch(accountID, candidates)
		if err != nil {
			e.log.Warn().Err(err).Int("refs", len(candidates)).
				Msg("Batched thread lookup failed, falling back to per-message lookup")
			resolved = nil
		}
	}

	out := make(map[string]string, len(msgs))
	for _, m := range msgs {
		refs := messageReferences(m)
		threadID := ""
		if resolved != nil {
			threadID, _ = lookupThreadKey(resolved, m.MessageID, m.InReplyTo, refs)
		} else {
			var err error
			threadID, err = e.messageStore.FindThreadID(accountID, m.MessageID, m.InReplyTo, refs)
			if err != nil {
				e.log.Debug().Err(err).Msg("Error finding thread ID, using message ID")
			}
		}
		if threadID == "" {
			threadID = m.MessageID
		}
		out[m.ID] = threadID
	}
	return out
}

// lookupThreadKey mirrors FindThreadID's resolution order against an
// already-populated map: try the message's own id, then In-Reply-To, then each
// reference in order, and fall back to the first reference (or the message id)
// when nothing matched an existing row.
func lookupThreadKey(resolved map[string]string, messageID, inReplyTo string, references []string) (string, bool) {
	normInReplyTo := normalizeRef(inReplyTo)

	for _, ref := range threadCandidateOrder(inReplyTo, references) {
		if key, ok := resolved[ref]; ok {
			return normalizeRef(key), true
		}
	}

	// No existing thread found - use the first reference as thread ID (root
	// message). This is the original message that started the thread.
	// No existing thread found - use the first reference as thread ID (root
	// message). Mirrors FindThreadID: references are already normalized.
	for _, r := range references {
		if r != "" {
			return r, false
		}
	}
	if normInReplyTo != "" {
		return normInReplyTo, false
	}
	return normalizeRef(messageID), false
}

// threadCandidates returns the normalized lookup keys a single message would
// have probed, in FindThreadID's order.
//
// The message's own Message-ID is deliberately absent: FindThreadID builds its
// candidate list from In-Reply-To plus References only, so a message carrying
// no threading headers starts its own thread. Probing its own id here would
// make a just-inserted row with a NULL thread_id resolve to its own row UUID
// instead.
func threadCandidates(m *message.Message) []string {
	return threadCandidateOrder(m.InReplyTo, messageReferences(m))
}

func threadCandidateOrder(inReplyTo string, references []string) []string {
	out := make([]string, 0, len(references)+1)
	if normInReplyTo := normalizeRef(inReplyTo); normInReplyTo != "" {
		out = append(out, normInReplyTo)
	}
	out = append(out, references...)
	return out
}

// messageReferences decodes the JSON References array stored on a message,
// normalized the same way FindThreadID normalizes it.
func messageReferences(m *message.Message) []string {
	if m.References == "" {
		return nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(m.References), &raw); err != nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, ref := range raw {
		if norm := normalizeRef(ref); norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

// normalizeRef strips angle brackets, matching message.normalizeMessageID.
func normalizeRef(id string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(id), "<"), ">")
}

// extractReferences extracts the References header from raw message bytes
func (e *Engine) extractReferences(raw []byte) []string {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return nil
	}

	refsHeader := entity.Header.Get("References")
	if refsHeader == "" {
		return nil
	}

	// References header contains space or newline-separated Message-IDs
	// Format: <msgid1> <msgid2> <msgid3>
	var refs []string
	// Split by whitespace and filter for valid message-ids
	parts := strings.Fields(refsHeader)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			refs = append(refs, part)
		}
	}

	return refs
}

// extractDispositionNotificationTo extracts the Disposition-Notification-To header
// This header indicates the sender is requesting a read receipt
func (e *Engine) extractDispositionNotificationTo(raw []byte) string {
	reader := bytes.NewReader(raw)

	entity, err := gomessage.Read(reader)
	if err != nil {
		return ""
	}

	dntHeader := entity.Header.Get("Disposition-Notification-To")
	if dntHeader == "" {
		return ""
	}

	// The header value is typically an email address, possibly with a name
	// e.g., "John Doe <john@example.com>" or just "john@example.com"
	return strings.TrimSpace(dntHeader)
}
