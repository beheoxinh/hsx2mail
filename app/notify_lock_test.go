package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/database"
	"github.com/beheoxinh/hsx2mail/internal/notification"
	"github.com/beheoxinh/hsx2mail/internal/settings"
	hsync "github.com/beheoxinh/hsx2mail/internal/sync"
)

// stubNotifier records what would have been shown on screen.
type stubNotifier struct {
	mu   sync.Mutex
	seen []notification.Notification
}

func (s *stubNotifier) Show(n notification.Notification) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, n)
	return uint32(len(s.seen)), nil
}

func (s *stubNotifier) Start(context.Context) error { return nil }

func (s *stubNotifier) Stop() {}

func (s *stubNotifier) SetClickHandler(notification.ClickHandler) {}

func (s *stubNotifier) last() notification.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		return notification.Notification{}
	}
	return s.seen[len(s.seen)-1]
}

// stubSessionLock is a platform.SessionLockMonitor with a settable state.
type stubSessionLock struct{ locked bool }

func (s *stubSessionLock) Start(context.Context) error { return nil }
func (s *stubSessionLock) Locked() bool                { return s.locked }
func (s *stubSessionLock) Stop() error                 { return nil }

// TestNotificationRedactedWhileLocked is the runnable check for Phase 3 task
// 3-14: with the session locked, neither the sender nor the subject may reach
// the notification rendered on the lock screen.
func TestNotificationRedactedWhileLocked(t *testing.T) {
	const (
		subject = "Invoice 4711 attached"
		sender  = "ACME Payroll"
		email   = "payroll@acme.example"
	)

	for _, tc := range []struct {
		name      string
		locked    bool
		count     int
		wantTitle string
		wantBody  string
		mustHide  []string
	}{
		{
			name:  "unlocked single message keeps content",
			count: 1, wantTitle: "New email from " + sender, wantBody: subject,
		},
		{
			name:  "unlocked burst names the account",
			count: 3, wantTitle: "New emails", wantBody: "Work",
		},
		{
			name:   "locked single message is redacted",
			locked: true, count: 1, wantTitle: "New mail", wantBody: "1 new message",
			mustHide: []string{subject, sender, email},
		},
		{
			name:   "locked burst is redacted",
			locked: true, count: 7, wantTitle: "New mail", wantBody: "7 new messages",
			mustHide: []string{subject, sender, email},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &stubNotifier{}
			a := &App{notifier: n}
			if tc.locked {
				a.sessionLock = &stubSessionLock{locked: true}
			}

			a.sendSystemNotification(
				hsync.NewMailInfo{AccountID: "a1", AccountName: "Work", Count: tc.count},
				subject, sender, email, "t1",
			)

			got := n.last()
			if got.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tc.wantTitle)
			}
			if got.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", got.Body, tc.wantBody)
			}
			for _, secret := range tc.mustHide {
				if strings.Contains(got.Title, secret) || strings.Contains(got.Body, secret) {
					t.Errorf("notification leaks %q: title=%q body=%q", secret, got.Title, got.Body)
				}
			}
		})
	}
}

// TestStartHiddenOverrideWinsOverSettings is the runnable check for Phase 3
// task 3-05: the --start-hidden flag on the autostart entry must force a
// window-less boot even when the stored start_hidden toggle is off. Without
// the override, enabling autostart then turning the toggle off would start a
// window at every session login.
func TestStartHiddenOverrideWinsOverSettings(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "starthidden.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := &App{settingsStore: settings.NewStore(db)}

	// Default: nothing enabled, no override -> visible window.
	if a.GetStartHiddenActive() {
		t.Error("GetStartHiddenActive() = true with no settings and no override, want false")
	}

	// The normal settings path still works.
	if err := a.settingsStore.SetRunBackground(true); err != nil {
		t.Fatal(err)
	}
	if err := a.settingsStore.SetStartHidden(true); err != nil {
		t.Fatal(err)
	}
	if !a.GetStartHiddenActive() {
		t.Error("GetStartHiddenActive() = false with start_hidden+run_background on, want true")
	}

	// start_hidden off, but the autostart entry passed --start-hidden.
	if err := a.settingsStore.SetStartHidden(false); err != nil {
		t.Fatal(err)
	}
	if a.GetStartHiddenActive() {
		t.Error("GetStartHiddenActive() = true after start_hidden was turned off, want false")
	}
	a.SetStartHiddenOverride(true)
	if !a.GetStartHiddenActive() {
		t.Error("GetStartHiddenActive() = false despite --start-hidden override, want true")
	}
	a.SetStartHiddenOverride(false)
	if a.GetStartHiddenActive() {
		t.Error("GetStartHiddenActive() = true after the override was cleared, want false")
	}
}
