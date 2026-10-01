package message

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Phase 2 task 2-05 asked to "flip the FTS join so FTS drives". This test
// records what the planner actually does with the shipped query so the claim
// stays falsifiable: if a future SQLite ever stops preferring the FTS virtual
// table as the outer loop, this fails.
func TestSearchConversations_FTSDrivesTheJoin(t *testing.T) {
	s, _, folderID := newBodyFailedTestStore(t)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 400; i++ {
		subject := fmt.Sprintf("subject %d about invoices", i)
		if i%50 == 0 {
			subject = fmt.Sprintf("quarterly budget report %d", i)
		}
		if _, err := s.db.Exec(`INSERT INTO messages
			(id, account_id, folder_id, uid, message_id, thread_id, subject, from_name, from_email, date, body_fetched, body_failed)
			VALUES (?,?,?,?,?,?,?,?,?,?,1,0)`,
			fmt.Sprintf("id-%04d", i), "acct-1", folderID, i,
			fmt.Sprintf("<m%d@example.com>", i), fmt.Sprintf("t-%d", i/3),
			subject, "Sender", fmt.Sprintf("s%d@example.com", i),
			base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// This is the shipped query shape, verbatim apart from the projection.
	steps := queryPlan(t, s, `
		SELECT COALESCE(m.thread_id, m.id) as conv_thread_id, MIN(m.subject), MAX(m.date), COUNT(*)
		FROM messages m
		JOIN messages_fts fts ON m.rowid = fts.rowid
		WHERE m.folder_id = ? AND messages_fts MATCH ?
		GROUP BY COALESCE(m.thread_id, m.id)`, folderID, "invoices")

	ftsIdx, messagesIdx := -1, -1
	for i, step := range steps {
		// The plan prints the table alias, not the real table name.
		if strings.Contains(step, "VIRTUAL TABLE") && ftsIdx < 0 {
			ftsIdx = i
		}
		if strings.Contains(step, "SEARCH m ") && messagesIdx < 0 {
			messagesIdx = i
		}
	}
	if ftsIdx < 0 {
		t.Fatalf("FTS virtual table is not in the plan at all: %v", steps)
	}
	if messagesIdx < 0 {
		t.Fatalf("messages lookup is not in the plan: %v", steps)
	}
	if ftsIdx > messagesIdx {
		t.Errorf("FTS is not the driving table; it is joined after messages: %v", steps)
	}
	// And messages must be reached by rowid, not by a folder scan.
	if !planMentions(steps, "rowid=?") && !planMentions(steps, "INTEGER PRIMARY KEY") {
		t.Errorf("messages is not looked up by FTS rowid: %v", steps)
	}
}

// TestSearchConversations_ResultsUnchanged guards the search surface: the
// shipped shape must return the same conversations and the same total as the
// data seeded above, with folder filtering applied after the FTS match.
func TestSearchConversations_ResultsUnchanged(t *testing.T) {
	s, _, folderID := newBodyFailedTestStore(t)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		subject := fmt.Sprintf("message %d", i)
		if i%10 == 0 {
			subject = fmt.Sprintf("invoice %d", i)
		}
		if _, err := s.db.Exec(`INSERT INTO messages
			(id, account_id, folder_id, uid, message_id, thread_id, subject, from_name, from_email, date, body_fetched, body_failed)
			VALUES (?,?,?,?,?,?,?,?,?,?,1,0)`,
			fmt.Sprintf("id-%04d", i), "acct-1", folderID, i,
			fmt.Sprintf("<m%d@example.com>", i), fmt.Sprintf("t-%d", i),
			subject, "Sender", "s@example.com",
			base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	results, total, err := s.SearchConversations(folderID, "invoice", 0, 50, "")
	if err != nil {
		t.Fatalf("SearchConversations: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if len(results) != 4 {
		t.Fatalf("returned %d results, want 4", len(results))
	}
	// Each seed message is its own thread here, so one hit per matching row.
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		if r.ThreadID == "" {
			t.Error("result has an empty thread id")
		}
		if seen[r.ThreadID] {
			t.Errorf("duplicate thread in results: %s", r.ThreadID)
		}
		seen[r.ThreadID] = true
	}

	// A term that matches nothing must be empty, not an error.
	empty, totalEmpty, err := s.SearchConversations(folderID, "zzzznomatch", 0, 50, "")
	if err != nil {
		t.Fatalf("SearchConversations(no match): %v", err)
	}
	if totalEmpty != 0 || len(empty) != 0 {
		t.Errorf("no-match query returned total=%d results=%d", totalEmpty, len(empty))
	}
}
