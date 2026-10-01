package sync

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/account"
	"github.com/beheoxinh/hsx2mail/internal/folder"
	"github.com/beheoxinh/hsx2mail/internal/logging"
	"github.com/rs/zerolog"
)

// NewMailInfo contains information about newly arrived mail
type NewMailInfo struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	FolderID    string `json:"folderId"`
	Subject     string `json:"subject"`
	FromName    string `json:"fromName"`
	FromEmail   string `json:"fromEmail"`
	Count       int    `json:"count"` // Number of new messages
}

// Boot-storm control constants (Phase 3 task 3-07).
//
// Without these, every account is "due" on the first tick (LastSync == nil)
// and every failing account retries every tick, so a boot with N accounts
// opens N connections at once and a network outage turns into a N-connection
// retry storm.
const (
	// MaxConcurrentAccountSyncs caps simultaneous scheduled account syncs.
	MaxConcurrentAccountSyncs = 3

	// InitialJitterWindow is the upper bound of the per-account random delay
	// applied before the very first attempt, spreading the boot fan-out.
	InitialJitterWindow = 30 * time.Second

	// BackoffBase is the first failure backoff; each further consecutive
	// failure doubles it.
	BackoffBase = 2 * time.Minute

	// BackoffMax caps the exponential backoff.
	BackoffMax = 60 * time.Minute
)

// NewMailCallback is called when new mail arrives
type NewMailCallback func(info NewMailInfo)

// SyncCompletedCallback is called when a sync operation completes (success or error)
type SyncCompletedCallback func(accountID, folderID string, err error)

// Scheduler handles periodic background sync of email accounts
type Scheduler struct {
	engine       *Engine
	accountStore *account.Store
	folderStore  *folder.Store
	log          zerolog.Logger

	// Callbacks
	newMailCallback       NewMailCallback
	syncCompletedCallback SyncCompletedCallback
	isConnected           func() bool // optional: skip sync when offline

	// Control
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	running       bool
	runningMu     sync.Mutex
	checkInterval time.Duration

	// Track syncing accounts to prevent concurrent syncs
	syncing   map[string]bool
	syncingMu sync.Mutex

	// Per-account cancellation for running syncs
	syncCancels  map[string]context.CancelFunc
	syncCancelMu sync.Mutex

	// Boot-storm control (Phase 3 task 3-07).
	//
	// failCount/nextAttempt hold the per-account failure backoff so N accounts
	// that all fail at boot do not retry in lockstep every tick. slots caps how
	// many accounts may sync at once, so a boot with 20 accounts opens 2
	// connections instead of 20. seeded marks the accounts whose initial jitter
	// has been drawn already.
	failCount   map[string]int
	nextAttempt map[string]time.Time
	seeded      map[string]bool
	stormMu     sync.Mutex
	slots       chan struct{}
}

// NewScheduler creates a new sync scheduler
func NewScheduler(engine *Engine, accountStore *account.Store, folderStore *folder.Store) *Scheduler {
	return &Scheduler{
		engine:        engine,
		accountStore:  accountStore,
		folderStore:   folderStore,
		log:           logging.WithComponent("sync-scheduler"),
		checkInterval: 1 * time.Minute, // Check every minute if any account is due
		syncing:       make(map[string]bool),
		syncCancels:   make(map[string]context.CancelFunc),
		failCount:     make(map[string]int),
		nextAttempt:   make(map[string]time.Time),
		seeded:        make(map[string]bool),
		slots:         make(chan struct{}, MaxConcurrentAccountSyncs),
	}
}

// SetNewMailCallback sets the callback for new mail notifications
func (s *Scheduler) SetNewMailCallback(callback NewMailCallback) {
	s.newMailCallback = callback
}

// SetSyncCompletedCallback sets the callback for sync completion notifications
func (s *Scheduler) SetSyncCompletedCallback(callback SyncCompletedCallback) {
	s.syncCompletedCallback = callback
}

// SetConnectivityCheck sets a function to check network connectivity.
// When set, the scheduler skips sync ticks when offline to avoid wasted
// connection attempts and unnecessary error logging.
func (s *Scheduler) SetConnectivityCheck(check func() bool) {
	s.isConnected = check
}

// Start starts the background sync scheduler
func (s *Scheduler) Start(ctx context.Context) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		s.log.Warn().Msg("Scheduler already running")
		return
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	s.wg.Add(1)
	go s.run()

	s.log.Info().Msg("Email sync scheduler started")
}

// Stop stops the background sync scheduler
func (s *Scheduler) Stop() {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return
	}

	s.cancel()
	s.wg.Wait()
	s.running = false

	s.log.Info().Msg("Email sync scheduler stopped")
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	// Initial sync on startup (after a short delay to let the app initialize)
	select {
	case <-time.After(10 * time.Second):
		s.syncDueAccounts()
	case <-s.ctx.Done():
		return
	}

	// Periodic check
	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.syncDueAccounts()
		case <-s.ctx.Done():
			return
		}
	}
}

// syncDueAccounts checks all accounts and syncs those that are due
func (s *Scheduler) syncDueAccounts() {
	// Skip sync tick if we know we're offline
	if s.isConnected != nil && !s.isConnected() {
		s.log.Debug().Msg("Skipping sync tick — offline")
		return
	}

	accounts, err := s.accountStore.List()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to list accounts for sync check")
		return
	}

	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}

		// Skip manual-only accounts
		if acc.SyncInterval <= 0 {
			continue
		}
		// Check if sync is due for INBOX
		if !s.isSyncDue(acc) {
			continue
		}

		// Draw the per-account boot jitter once, before the first attempt, so
		// N accounts starting together do not all hit the server in the same
		// second. A failure backoff already in force wins over the jitter.
		s.seedJitter(acc.ID)

		// Phase 3 task 3-07: honour the failure backoff. This is what stops a
		// network outage from turning into one sync attempt per account per tick.
		if wait, blocked := s.backoffRemaining(acc.ID); blocked {
			s.log.Debug().
				Str("account", acc.Name).
				Dur("retryIn", wait).
				Msg("Account in failure backoff, skipping")
			continue
		}

		s.log.Debug().Str("account", acc.Name).Msg("Account is due for sync")

		// Cap concurrent account syncs. The slot is taken here, not inside the
		// goroutine, so a full cap defers the account to the next tick instead
		// of parking a goroutine that would do nothing.
		select {
		case s.slots <- struct{}{}:
		default:
			s.log.Debug().
				Str("account", acc.Name).
				Int("inFlight", len(s.slots)).
				Msg("Concurrent account sync cap reached, deferring to next tick")
			continue
		}

		// Sync in background (don't block the scheduler)
		go func(a *account.Account) {
			defer func() { <-s.slots }()
			s.syncAccountInbox(a)
		}(acc)
	}
}

// seedJitter draws the one-off per-account boot delay (Phase 3 task 3-07).
// Drawn from math/rand so N accounts waking at the same instant spread their
// first connection over InitialJitterWindow instead of stampeding.
func (s *Scheduler) seedJitter(accountID string) {
	s.stormMu.Lock()
	defer s.stormMu.Unlock()

	if s.seeded[accountID] {
		return
	}
	s.seeded[accountID] = true
	s.nextAttempt[accountID] = time.Now().Add(
		time.Duration(rand.Int63n(int64(InitialJitterWindow))))
}

// backoffRemaining reports how long an account must still wait before it may
// be attempted, and whether it is blocked at all.
func (s *Scheduler) backoffRemaining(accountID string) (time.Duration, bool) {
	s.stormMu.Lock()
	defer s.stormMu.Unlock()

	wait, ok := s.nextAttempt[accountID]
	if !ok {
		return 0, false
	}
	remaining := time.Until(wait)
	if remaining <= 0 {
		delete(s.nextAttempt, accountID)
		return 0, false
	}
	return remaining, true
}

// recordFailure arms the exponential backoff for an account. Only real sync
// errors count — a cancellation (shutdown, or the 30-minute timeout firing)
// leaves the schedule untouched so a restart is not punished for a user
// action.
func (s *Scheduler) recordFailure(accountID string) {
	s.stormMu.Lock()
	defer s.stormMu.Unlock()

	s.failCount[accountID]++
	backoff := BackoffBase << min(s.failCount[accountID]-1, 10)
	if backoff > BackoffMax || backoff <= 0 {
		backoff = BackoffMax
	}
	// Jitter the retry too: without it, accounts that failed together
	// retry together and the backoff only shifts the stampede.
	jitter := time.Duration(rand.Int63n(int64(backoff) / 4))
	s.nextAttempt[accountID] = time.Now().Add(backoff + jitter)

	s.log.Warn().
		Str("accountID", accountID).
		Int("consecutiveFailures", s.failCount[accountID]).
		Dur("backoff", backoff+jitter).
		Msg("Account sync failed, backing off")
}

// recordSuccess clears the failure backoff so the next due sync runs on the
// account's normal schedule.
func (s *Scheduler) recordSuccess(accountID string) {
	s.stormMu.Lock()
	defer s.stormMu.Unlock()

	if s.failCount[accountID] == 0 {
		return
	}
	s.log.Info().
		Str("accountID", accountID).
		Int("previousFailures", s.failCount[accountID]).
		Msg("Account sync recovered, clearing backoff")
	delete(s.failCount, accountID)
	delete(s.nextAttempt, accountID)
}

// isSyncDue returns true if an account's INBOX is due for sync
func (s *Scheduler) isSyncDue(acc *account.Account) bool {
	// Get the INBOX folder for this account
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		s.log.Warn().Err(err).Str("account", acc.ID).Msg("Failed to get INBOX folder")
		return true // Sync anyway to create the folder
	}
	if inbox == nil {
		return true // No INBOX yet, needs sync
	}

	// Never synced - definitely due
	if inbox.LastSync == nil {
		return true
	}

	// Calculate time since last sync
	elapsed := time.Since(*inbox.LastSync)
	interval := time.Duration(acc.SyncInterval) * time.Minute

	return elapsed >= interval
}

// syncAccountInbox syncs the INBOX for an account
func (s *Scheduler) syncAccountInbox(acc *account.Account) {
	// Prevent concurrent syncs for the same account
	s.syncingMu.Lock()
	if s.syncing[acc.ID] {
		s.syncingMu.Unlock()
		s.log.Debug().Str("account", acc.Name).Msg("Sync already in progress, skipping")
		return
	}
	s.syncing[acc.ID] = true
	s.syncingMu.Unlock()

	// Create a cancellable context with timeout for this sync operation
	// 30 minute timeout prevents syncs from running forever if connection hangs
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	s.syncCancelMu.Lock()
	s.syncCancels[acc.ID] = cancel
	s.syncCancelMu.Unlock()

	defer func() {
		// Clean up cancel function
		cancel() // Always call cancel to release timeout resources
		s.syncCancelMu.Lock()
		delete(s.syncCancels, acc.ID)
		s.syncCancelMu.Unlock()

		s.syncingMu.Lock()
		delete(s.syncing, acc.ID)
		s.syncingMu.Unlock()
	}()

	s.log.Info().Str("account", acc.Name).Msg("Starting scheduled sync for INBOX")

	// First, ensure folders are synced
	if err := s.engine.SyncFolders(ctx, acc.ID); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during folder sync")
			return
		}
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to sync folders")
		s.recordFailure(acc.ID)
		return
	}

	// Get the INBOX folder
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to get INBOX folder")
		s.recordFailure(acc.ID)
		return
	}
	if inbox == nil {
		s.log.Warn().Str("account", acc.Name).Msg("INBOX folder not found")
		s.recordFailure(acc.ID)
		return
	}

	// Get current message count before sync
	previousCount := inbox.TotalCount

	// Sync messages (use account's sync period setting)
	if err := s.engine.SyncMessages(ctx, acc.ID, inbox.ID, acc.SyncPeriodDays, false); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during message sync")
			// Notify completion even on cancel so frontend clears progress
			if s.syncCompletedCallback != nil {
				s.syncCompletedCallback(acc.ID, inbox.ID, nil)
			}
			return
		}
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to sync messages")
		s.recordFailure(acc.ID)
		// Notify completion with error so frontend clears progress
		if s.syncCompletedCallback != nil {
			s.syncCompletedCallback(acc.ID, inbox.ID, err)
		}
		return
	}

	// Get updated folder info
	updatedInbox, err := s.folderStore.Get(inbox.ID)
	if err != nil {
		s.log.Error().Err(err).Str("account", acc.Name).Msg("Failed to get updated INBOX folder")
		s.recordFailure(acc.ID)
		// Notify completion with error
		if s.syncCompletedCallback != nil {
			s.syncCompletedCallback(acc.ID, inbox.ID, err)
		}
		return
	}

	// Check if there are new messages
	if updatedInbox != nil && updatedInbox.TotalCount > previousCount {
		newCount := updatedInbox.TotalCount - previousCount
		s.log.Info().
			Str("account", acc.Name).
			Int("newMessages", newCount).
			Msg("New messages arrived")

		// Notify about new mail
		if s.newMailCallback != nil {
			s.newMailCallback(NewMailInfo{
				AccountID:   acc.ID,
				AccountName: acc.Name,
				FolderID:    inbox.ID,
				Count:       newCount,
			})
		}
	}

	// Notify that sync completed for Inbox
	if s.syncCompletedCallback != nil && inbox != nil {
		s.syncCompletedCallback(acc.ID, inbox.ID, nil)
	}

	// Sync additional subscribed/all folders (beyond Inbox)
	s.syncAdditionalFolders(ctx, acc, inbox)

	s.recordSuccess(acc.ID)
	s.log.Debug().Str("account", acc.Name).Msg("Scheduled sync completed")
}

// secondarySyncIntervalFor returns the polling interval for secondary folders
// (Trash/Spam/Archive/All Mail/Starred/Important) from the account's setting.
// The user-configurable SecondarySyncInterval wins when set; otherwise we
// derive one from SyncInterval with a 10-minute floor so secondary folders
// still refresh promptly without polling as hard as INBOX.
func secondarySyncIntervalFor(acc *account.Account) time.Duration {
	if acc != nil && acc.SecondarySyncInterval > 0 {
		return time.Duration(acc.SecondarySyncInterval) * time.Minute
	}
	// Default: same cadence as INBOX but never faster than 10 minutes.
	if acc != nil && acc.SyncInterval > 0 {
		if acc.SyncInterval < 10 {
			return 10 * time.Minute
		}
		return time.Duration(acc.SyncInterval) * time.Minute
	}
	return 10 * time.Minute
}

// syncAdditionalFolders syncs subscribed or all folders beyond Inbox,
// based on the account's SyncAllFolders setting.
func (s *Scheduler) syncAdditionalFolders(ctx context.Context, acc *account.Account, inbox *folder.Folder) {
	folders, err := s.getAccountSyncFolders(acc)
	if err != nil {
		s.log.Warn().Err(err).Str("account", acc.Name).Msg("Failed to get sync folders")
		return
	}

	secondaryInterval := secondarySyncIntervalFor(acc)

	// Filter out Inbox (already synced) and limit to 2 concurrent syncs
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup

	for _, f := range folders {
		// Skip Inbox — already synced above with notification handling
		if inbox != nil && f.ID == inbox.ID {
			continue
		}

		// Throttle secondary folders: skip when synced recently. Core folders
		// (Drafts, Sent) sync every tick since they can change on our own actions.
		if folder.IsSecondaryFolder(f.Type) && f.LastSync != nil &&
			time.Since(*f.LastSync) < secondaryInterval {
			continue
		}

		// A folder synced in a previous tick may be stale (mode switch, full sync
		// default flip). Re-read so the LastSync throttle above sees fresh state.
		if fresh, gErr := s.folderStore.Get(f.ID); gErr == nil && fresh != nil {
			f = fresh
		}

		// Check context
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(f *folder.Folder) {
			defer wg.Done()
			defer func() { <-sem }()

			// Secondary folders on first-ever sync: pull the full window. Gmail's
			// All Mail / Important may hold old messages the 30-day default would
			// miss on first sync. Subsequent syncs are incremental (modseq) so the
			// period only bounds the initial fetch.
			syncDays := acc.SyncPeriodDays
			if folder.IsSecondaryFolder(f.Type) && f.LastSync == nil {
				syncDays = 0
			}
			if syncErr := s.engine.SyncMessages(ctx, acc.ID, f.ID, syncDays, false); syncErr != nil {
				if ctx.Err() == nil {
					s.log.Warn().Err(syncErr).Str("folder", f.Path).Msg("Failed to sync additional folder")
				}
			}
			// Notify completion for this folder
			if s.syncCompletedCallback != nil {
				s.syncCompletedCallback(acc.ID, f.ID, nil)
			}
		}(f)
	}
	wg.Wait()
}

// getAccountSyncFolders returns the folders to sync for an account.
func (s *Scheduler) getAccountSyncFolders(acc *account.Account) ([]*folder.Folder, error) {
	if acc.SyncAllFolders {
		return s.folderStore.List(acc.ID)
	}
	if acc.SyncFoldersEnabled {
		return s.folderStore.ListSubscribed(acc.ID)
	}
	// Default: core folders + secondary folders (Trash/Spam/Archive/All/Starred/Important)
	// so unread badges and message content stay accurate for every folder.
	coreTypes := []folder.Type{folder.TypeInbox, folder.TypeDrafts, folder.TypeSent}
	var folders []*folder.Folder
	byID := make(map[string]bool)
	for _, ft := range coreTypes {
		f, err := s.folderStore.GetByType(acc.ID, ft)
		if err != nil {
			continue
		}
		if f != nil {
			folders = append(folders, f)
			byID[f.ID] = true
		}
	}
	// Append secondary folders (dedupe)
	allFolders, listErr := s.folderStore.List(acc.ID)
	if listErr == nil {
		for _, f := range allFolders {
			if byID[f.ID] {
				continue
			}
			if folder.IsSecondaryFolder(f.Type) {
				folders = append(folders, f)
			}
		}
	}
	return folders, nil
}

// TriggerSync manually triggers a sync for a specific account (non-blocking)
func (s *Scheduler) TriggerSync(accountID string) {
	acc, err := s.accountStore.Get(accountID)
	if err != nil {
		s.log.Error().Err(err).Str("accountID", accountID).Msg("Failed to get account for manual sync")
		return
	}

	go s.syncAccountInbox(acc)
}

// CancelSync cancels any running sync for the specified account
func (s *Scheduler) CancelSync(accountID string) {
	s.syncCancelMu.Lock()
	if cancel, ok := s.syncCancels[accountID]; ok {
		s.log.Info().Str("accountID", accountID).Msg("Cancelling running sync")
		cancel()
	}
	s.syncCancelMu.Unlock()
}

// TriggerSyncAll manually triggers a sync for all enabled accounts (non-blocking)
func (s *Scheduler) TriggerSyncAll() {
	accounts, err := s.accountStore.List()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to list accounts for manual sync")
		return
	}

	for _, acc := range accounts {
		if acc.Enabled {
			go s.syncAccountInbox(acc)
		}
	}
}

// SyncAccountInboxBlocking syncs INBOX and returns new mail info (blocking)
// This is useful for IDLE-triggered syncs where we want to wait for completion
func (s *Scheduler) SyncAccountInboxBlocking(accountID string) (*NewMailInfo, error) {
	acc, err := s.accountStore.Get(accountID)
	if err != nil {
		return nil, err
	}

	// Prevent concurrent syncs for the same account
	s.syncingMu.Lock()
	if s.syncing[acc.ID] {
		s.syncingMu.Unlock()
		s.log.Debug().Str("account", acc.Name).Msg("Sync already in progress, skipping")
		return nil, nil
	}
	s.syncing[acc.ID] = true
	s.syncingMu.Unlock()

	// Create a cancellable context with timeout for this sync operation
	// 30 minute timeout prevents syncs from running forever if connection hangs
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	s.syncCancelMu.Lock()
	s.syncCancels[acc.ID] = cancel
	s.syncCancelMu.Unlock()

	defer func() {
		// Clean up cancel function
		cancel() // Always call cancel to release timeout resources
		s.syncCancelMu.Lock()
		delete(s.syncCancels, acc.ID)
		s.syncCancelMu.Unlock()

		s.syncingMu.Lock()
		delete(s.syncing, acc.ID)
		s.syncingMu.Unlock()
	}()

	// Get the INBOX folder
	inbox, err := s.folderStore.GetByType(acc.ID, folder.TypeInbox)
	if err != nil {
		return nil, err
	}
	if inbox == nil {
		// Try syncing folders first
		if err := s.engine.SyncFolders(ctx, acc.ID); err != nil {
			if ctx.Err() != nil {
				s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during folder sync")
				return nil, ctx.Err()
			}
			return nil, err
		}
		inbox, err = s.folderStore.GetByType(acc.ID, folder.TypeInbox)
		if err != nil || inbox == nil {
			return nil, err
		}
	}

	// Get current message count before sync
	previousCount := inbox.TotalCount

	// Sync messages (use account's sync period setting)
	// IDLE-triggered inbox sync (new mail + cross-client deletions) — use the
	// lightweight incremental flag path; the scheduled sync above stays the
	// authoritative full reconciliation.
	if err := s.engine.SyncMessages(ctx, acc.ID, inbox.ID, acc.SyncPeriodDays, true); err != nil {
		if ctx.Err() != nil {
			s.log.Info().Str("account", acc.Name).Msg("Sync cancelled during message sync")
			return nil, ctx.Err()
		}
		return nil, err
	}

	// Get updated folder info
	updatedInbox, err := s.folderStore.Get(inbox.ID)
	if err != nil {
		return nil, err
	}

	// Check if there are new messages
	if updatedInbox != nil && updatedInbox.TotalCount > previousCount {
		newCount := updatedInbox.TotalCount - previousCount
		return &NewMailInfo{
			AccountID:   acc.ID,
			AccountName: acc.Name,
			FolderID:    inbox.ID,
			Count:       newCount,
		}, nil
	}

	return nil, nil
}
