// Package database provides SQLite database functionality
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/logging"
	_ "modernc.org/sqlite"

	"github.com/rs/zerolog"
)

// Connection pool constants
const (
	// MaxOpenConns limits concurrent database connections.
	// SQLite with WAL mode only supports one writer at a time, so having many
	// connections just increases lock contention. Keep this modest.
	MaxOpenConns = 12

	// BaseIdleConns is the minimum number of idle connections to keep.
	BaseIdleConns = 3

	// MaxIdleConns is the maximum number of idle connections to keep.
	// This is capped to prevent excessive memory usage from warm connections.
	MaxIdleConns = 6

	// IdleConnsPerAccount is how many additional idle connections to keep per account.
	IdleConnsPerAccount = 1

	// CheckpointInterval is how often to run automatic WAL checkpoints.
	// This prevents the WAL file from growing too large.
	CheckpointInterval = 5 * time.Minute
)

// DB wraps the SQL database connection
type DB struct {
	*sql.DB
	path string
	log  zerolog.Logger
}

// Open opens or creates a SQLite database at the given path
func Open(path string) (*DB, error) {
	// Ensure directory exists with secure permissions (owner only)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database with PRAGMAs embedded in the DSN.
	// SQLite PRAGMAs are per-connection, and Go's database/sql creates connections
	// lazily in a pool. Using _pragma in the DSN ensures every new connection gets
	// the same configuration (busy_timeout, WAL, etc.), preventing SQLITE_BUSY
	// errors when a pooled connection lacks busy_timeout.
	// _txlock=immediate makes every BEGIN acquire the write lock up front.
	// Without it modernc/sqlite issues a plain (DEFERRED) BEGIN, so a
	// transaction that starts as a read and later upgrades to a write can fail
	// with SQLITE_BUSY_SNAPSHOT — busy_timeout does not help there, because
	// SQLite refuses to block a lock upgrade that would invalidate a snapshot
	// another connection is holding. That surfaced as "database is locked"
	// whenever sync (writer) and the UI's read queries overlapped.
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-64000)&_pragma=wal_autocheckpoint(1000)&_pragma=journal_size_limit(67108864)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for SQLite
	// MaxOpenConns: Modest ceiling - SQLite WAL only allows one writer at a time
	// MaxIdleConns: Start low, will be scaled dynamically based on account count
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(BaseIdleConns)

	// Test connection - this actually creates the file if it doesn't exist
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Ensure database file has secure permissions (owner read/write only)
	// This prevents other users on the system from reading email data
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set database permissions: %w", err)
	}

	return &DB{
		DB:   db,
		path: path,
		log:  logging.WithComponent("database"),
	}, nil
}

// UpdateIdleConns adjusts the number of idle connections based on account count.
// This should be called when accounts are added or removed.
// Formula: BaseIdleConns + (numAccounts * IdleConnsPerAccount), capped at MaxIdleConns
func (db *DB) UpdateIdleConns(numAccounts int) {
	idleConns := BaseIdleConns + (numAccounts * IdleConnsPerAccount)

	// Apply bounds
	if idleConns < BaseIdleConns {
		idleConns = BaseIdleConns
	}
	if idleConns > MaxIdleConns {
		idleConns = MaxIdleConns
	}

	db.SetMaxIdleConns(idleConns)

	db.log.Debug().
		Int("accounts", numAccounts).
		Int("idleConns", idleConns).
		Msg("Updated database connection pool")
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// Checkpoint runs a WAL checkpoint to merge the write-ahead log back into
// the main database file. This prevents the WAL file from growing too large.
//
// Uses TRUNCATE, not PASSIVE. PASSIVE copies whatever pages it can but leaves
// the -wal file at its high-water size, so the file grew monotonically for the
// life of the process. TRUNCATE additionally resets the file to zero bytes.
// It is safe here because this only ever runs from the idle maintenance
// ticker: a busy reader just makes the checkpoint a no-op for that pass rather
// than blocking a sync writer.
func (db *DB) Checkpoint() error {
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("failed to checkpoint WAL: %w", err)
	}
	return nil
}

// Optimize refreshes SQLite's query-planner statistics. Cheap, and it keeps
// ANALYZE data fresh enough that the v42 partial/expression indexes keep being
// chosen after the data distribution changes.
func (db *DB) Optimize() error {
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("failed to optimize database: %w", err)
	}
	return nil
}

// StartCheckpointRoutine starts a background goroutine that periodically
// checkpoints the WAL file. This should be called once at application startup.
// The routine will stop when the context is cancelled.
func (db *DB) StartCheckpointRoutine(ctx context.Context) {
	ticker := time.NewTicker(CheckpointInterval)
	defer ticker.Stop()

	db.log.Debug().Dur("interval", CheckpointInterval).Msg("WAL checkpoint routine started")

	for {
		select {
		case <-ticker.C:
			if err := db.Checkpoint(); err != nil {
				db.log.Error().Err(err).Msg("Periodic WAL checkpoint failed")
				continue
			}
			if err := db.Optimize(); err != nil {
				db.log.Debug().Err(err).Msg("Periodic PRAGMA optimize failed")
			}
			db.log.Debug().Msg("Periodic WAL checkpoint completed")
		case <-ctx.Done():
			db.log.Debug().Msg("WAL checkpoint routine stopped")
			return
		}
	}
}

// Path returns the database file path
func (db *DB) Path() string {
	return db.path
}

// ErrSchemaTooNew is returned by Migrate when the database's recorded migration
// version is HIGHER than the highest migration this build knows about. This
// happens when a user downgrades Hsx2Mail after a newer version applied a
// forward-only migration. Callers (App.Startup) surface a friendly dialog
// pointing the user at docs/SQL_ROLLBACK.md.
type ErrSchemaTooNew struct {
	DBVersion    int
	BuildVersion int
}

func (e *ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("database schema version %d is newer than this Hsx2Mail build (max known: %d). See https://github.com/beheoxinh/hsx2mail/blob/main/docs/SQL_ROLLBACK.md", e.DBVersion, e.BuildVersion)
}

// Migrate runs all pending migrations
func (db *DB) Migrate() error {
	// Create migrations table if not exists
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Schema-version gate: refuse if the DB was written by a newer Hsx2Mail. The
	// DB has migrations this build doesn't know how to interpret — opening it
	// would query columns/tables in an unexpected shape and likely corrupt
	// autocomplete or crash. Surface a typed error the app can catch.
	maxKnown := 0
	if len(migrations) > 0 {
		maxKnown = migrations[len(migrations)-1].Version
	}
	if currentVersion > maxKnown {
		return &ErrSchemaTooNew{DBVersion: currentVersion, BuildVersion: maxKnown}
	}

	// Apply migrations
	for _, m := range migrations {
		if m.Version > currentVersion {
			if err := db.applyMigration(m); err != nil {
				return fmt.Errorf("failed to apply migration %d: %w", m.Version, err)
			}
		}
	}

	return nil
}

func (db *DB) applyMigration(m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Re-check the marker INSIDE the transaction. Migrate() read the current
	// version before opening this tx, so a second process (a detached composer
	// window opening the same database) can slip in and apply the same
	// migration first. With _txlock=immediate the two transactions serialize,
	// but the loser would then re-run the ALTER TABLE and die with
	// "duplicate column name" — taking the composer window down at startup.
	var alreadyApplied int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM migrations WHERE version = ?`, m.Version,
	).Scan(&alreadyApplied); err != nil {
		return fmt.Errorf("failed to re-check migration %d: %w", m.Version, err)
	}
	if alreadyApplied > 0 {
		return tx.Rollback()
	}

	// Execute migration
	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("migration SQL failed: %w", err)
	}

	// Record migration
	if _, err := tx.Exec("INSERT INTO migrations (version) VALUES (?)", m.Version); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}
