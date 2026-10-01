# Database Rollback Guide

This guide walks you through rolling back Email Hub's database schema after an upgrade. Schema migrations in Email Hub are **forward-only by design** — the running application doesn't downgrade automatically. If you upgrade Email Hub to a new version that ran a migration and then want to go back to an older version, you need to manually reshape the database so the older version can read it.

Each section below covers a single released-to-released schema transition with a documented rollback path. Intermediate development schemas (e.g., the v31 that existed mid-cycle but never shipped) don't get their own section — there's no real-world DB at that state to roll back from. Find the section that matches your release-to-release transition.

## When you might need this

- You upgraded to a newer Email Hub (e.g., 0.3.0) and want to go back to 0.2.5 for any reason.
- Email Hub shows an error like *"This database was written by a newer Email Hub. Either upgrade Email Hub or follow the rollback guide..."* — the schema-version gate is refusing to open a DB that's newer than your running build.

## Contents

1. [Why migrations are forward-only](#why-migrations-are-forward-only-read-this-first) — the three structural reasons, stated plainly.
2. [Backup and recovery runbook](#backup-and-recovery-runbook) — WAL-safe backup, restore, credential caveats, corruption repair. **This is the supported path back.**
3. [v40 / v41 / v42 — current migrations, no shipped rollback](#v40--v41--v42--current-migrations-no-shipped-rollback).
4. [What you need](#what-you-need), [What you'll lose](#what-youll-lose), [Procedure](#procedure).
5. Per-transition rollback sections (`v39 → v30`, and any older ones) further down.

## What you'll lose

Each rollback section lists the **data lost on rollback** for that migration. This is inherent: if the newer schema added columns that the older schema doesn't have, those values are dropped when you go back. Anything else round-trips losslessly.

## What you need

- The Email Hub DB file. Default path:
  - Linux: `~/.local/share/hsx2mail/hsx2mail.db`
  - macOS: `~/Library/Application Support/Hsx2Mail/hsx2mail.db`
  - Windows: `%LOCALAPPDATA%\hsx2mail\hsx2mail.db`
- `sqlite3` command-line tool installed (most Linux/macOS systems have it; Windows users may need to install from sqlite.org).
- The matching rollback script for your migration, downloaded from the repo's `tools/db/` directory on the branch/tag corresponding to the version that introduced the migration.

---

## Why migrations are forward-only (read this first)

SQLite has no down-migration. Hsx2Mail's `Migrate()` walks `migrations` upward, applies
every version above the recorded one, and **never** un-applies. Three separate reasons,
all of them structural rather than stylistic:

1. **The ledger cannot express a down path.** `migrations` is
   `(version INTEGER PRIMARY KEY, applied_at DATETIME)`. It records *that* a version ran,
   not *how* to reverse it. There is no `down` column, no inverse-SQL column, and no
   checksum — so a later build has no way to know what a given version did, let alone
   undo it.
2. **`ADD COLUMN` is not cleanly reversible.** Of 42 migrations, the large majority are
   one or more `ALTER TABLE … ADD COLUMN`. SQLite (3.51.2, via `modernc.org/sqlite`
   v1.42.2) *does* support `ALTER TABLE … DROP COLUMN`, but it refuses whenever the
   column is indexed, part of a `UNIQUE`/primary key, or referenced by a constraint —
   and it leaves the index behind if you drop a column an index uses. Reproduced:
   ```
   sqlite> create table t(a text);
   sqlite> alter table t add column c text default 'x';
   sqlite> alter table t drop column c;     -- succeeds, data in `a` survives
   ```
   but with `create index ix on t(c)` first:
   ```
   sqlite> alter table t drop column c;
   Error: stepping, error in index ix after drop column: no such column: c
   ```
   The index survives, now dangling. Any column that a *later* migration indexed — and
   v42 indexes eight — cannot be dropped at all until that index is dropped first. And a
   dropped column takes its data with it; there is no undo.
3. **Reconstructing a schema is lossy and can be wrong.** When a rollback is genuinely
   needed, the only safe technique is the SQLite "swap table" dance: create a table with
   the *old* shape, `INSERT … SELECT` the surviving columns, `DROP` the new table,
   `ALTER TABLE … RENAME`. This preserves the columns you keep and **silently discards
   the ones the migration added** — including any data the user put in them. It also
   has to be written by hand, per release transition, and can be wrong.

**What this means in practice.** A rollback script is a *reconstruction*, not an undo.
`tools/db/rollback-v39-to-v30.sql` rebuilds the v30 schema (the pre-unified `contacts` +
`carddav_contacts` tables, dropping `extension_secrets`, the per-account SMTP credential
columns and `messages.body_failed`). That is a hand-written, per-transition artifact; it
is not generated from `migrations.go` and it is not re-runnable. The app will never run
it for you.

**The supported path back is a backup**, not a rollback script. Restoring a pre-upgrade
snapshot is lossless; running a rollback script is not. The runbook below is the primary
procedure; the per-transition sections later in this file are the fallback.

---

## Backup and recovery runbook

### Where the data lives

One database file plus WAL sidecars. See `DATABASE.md` §1 for the per-platform paths.

```
hsx2mail.db        main database
hsx2mail.db-wal    write-ahead log    (present whenever WAL mode is active)
hsx2mail.db-shm    shared-memory index (present whenever WAL mode is active)
```

Also worth backing up, if you want a complete restore:

- `<data>/attachments/staging/` — staged composer attachment blobs (7-day retention).
- `<data>/extensions/*/data.db` — per-extension databases.
- The OS keyring / `secret_service` entries and the AES fallback ciphertexts in
  `accounts.encrypted_password`, `contact_sources.encrypted_password`, and the OAuth
  token columns. **A database restore does not restore keyring entries** — see
  "Restoring credentials" below.

### Safe backup — the app is running

Never `cp` the `.db` alone while the app is running. In WAL mode recent commits live in
`-wal` and are *not* in the main file; a copy without the `-wal` is missing data. Use
SQLite's online backup API, which takes a consistent snapshot without blocking writers:

```bash
DB=~/.local/share/hsx2mail/hsx2mail.db
sqlite3 "$DB" ".backup '/tmp/hsx2mail-backup.db'"
```

`.backup` is the right tool for a hot backup. Verify it, then keep it:

```bash
sqlite3 /tmp/hsx2mail-backup.db 'PRAGMA integrity_check;'   # must print "ok"
sqlite3 /tmp/hsx2mail-backup.db 'SELECT MAX(version) FROM migrations;'
```

### Safe backup — the app is closed

With the app fully quit (menu Quit, or `pkill -f hsx2mail`), a plain file copy is fine
because the WAL is checkpointed on the last clean close. To be certain, checkpoint first
while it is still open (or from another shell just before quitting):

```bash
sqlite3 "$DB" 'PRAGMA wal_checkpoint(TRUNCATE);'
cp "$DB"        /backup/hsx2mail.db
```

If you have already quit and the `-wal` still exists, copy all three files **together**
into the same directory — that is still a consistent set:

```bash
cp "$DB" "$DB-wal" "$DB-shm" /backup/    # only when the app is not running
```

If you want a fresh `.db` with no sidecars, open the copy and checkpoint it:

```bash
sqlite3 /backup/hsx2mail.db 'PRAGMA wal_checkpoint(TRUNCATE);'
```

### Restoring from a backup

1. **Quit the app completely.**
2. Move the current database aside (do not delete it until the restore is verified):
   ```bash
   mv "$DB" "$DB.broken"
   rm -f "$DB-wal" "$DB-shm"          # stale sidecars must not pair with the restored file
   ```
3. Copy the backup in as `hsx2mail.db`:
   ```bash
   cp /tmp/hsx2mail-backup.db "$DB"
   chmod 600 "$DB"
   ```
4. Start the app. `Migrate()` will apply any migrations newer than the restored
   `MAX(version)` — a backup from v38 opened by a v42 build simply migrates forward.
5. Verify mail is present, then delete `$DB.broken`.

### Restoring credentials

The keyring is a separate, OS-owned store. A database restore brings back the *references*
to keyring entries and the AES fallback ciphertexts, but:

- If credentials were in the **keyring** and the keyring still has them, the restored DB
  resolves them normally.
- If the keyring was reset (new machine, `gnome-keyring` data cleared), the restored DB's
  keyring lookups fail with `ErrKeyringUnavailable` and you must re-enter passwords /
  re-authorise OAuth. The AES fallback ciphertexts in the DB *do* survive a DB restore
  (they are inside it), provided the same key material is derived — see
  `internal/credentials`. If the key is itself stored in the keyring, a keyring reset
  means the fallback cannot be decrypted either and every credential must be re-entered.

There is no supported way to move a keyring between machines. Plan credential re-entry
into any restore-from-backup procedure.

### Corruption and repair

If the app will not open the database, or `PRAGMA integrity_check` reports errors:

1. **Preserve the damaged file** (copy it aside) before doing anything.
2. **Try a checkpoint** — a crash can leave a hot `-wal` that looks like corruption:
   ```bash
   sqlite3 "$DB" 'PRAGMA wal_checkpoint(TRUNCATE);'
   ```
3. **Re-run the check** on the main file alone (move `-wal`/`-shm` aside first):
   ```bash
   sqlite3 "$DB" 'PRAGMA integrity_check;'
   ```
4. **Recover what you can** into a fresh database and rebuild the schema by re-running
   migrations on the empty file:
   ```bash
   # Extract rows table-by-table from the damaged file into a clean schema.
   # The clean schema's version is created by starting the app once on an empty DB
   # (or by applying migrations), then copying rows in.
   ```
5. If the damaged file will not even open, fall back to `.recover`:
   ```bash
   sqlite3 "$DB.broken" ".recover" | sqlite3 /tmp/recovered.db
   sqlite3 /tmp/recovered.db 'PRAGMA integrity_check;'
   ```
6. **Last resort: restore a backup** (§ Restoring from a backup). This is why § Backup
   above exists.

A single malformed row in `messages_fts` (the FTS5 index) is recoverable — drop and
rebuild the search index rather than the whole database. See `DATABASE.md` §7.

---

## v40 / v41 / v42 — current migrations, no shipped rollback

Migrations **v40**, **v41** and **v42** are the current schema (v42 as of this writing).
They have **no release-to-release rollback section** below because no shipped release has
rolled *back* out of them yet. When the first one does, add a section here. What each one
does, and what a rollback would cost, so you can decide before a real incident:

| v | Change | Rolling back would… |
|---|---|---|
| 40 | `accounts.oauth_account_id` — a stable `"<tid>:<oid>"` identity captured from the mail ID token, so incremental consent is validated against an immutable pair instead of the mutable `email` claim (Microsoft). | Drop one `TEXT` column. Nullable, no data loss for rows that never set it. |
| 41 | `accounts.secondary_sync_interval` — per-account reconciliation cadence for secondary folders (e.g. Archive), `0` meaning "derive from `sync_interval`, clamped to a 10-minute floor". `NOT NULL DEFAULT 0`. | Drop the column; every row falls back to deriving from `sync_interval`. No data loss. |
| 42 | **Index-only.** Eight new indexes on `messages` (`idx_messages_needs_body`, `idx_messages_account_date`, `idx_messages_folder_thread_date`, `idx_messages_folder_conv`, `idx_messages_thread_norm`, `idx_messages_message_id_norm`, `idx_messages_in_reply_to_norm`, `idx_messages_account_message_id`). No column or table added. | This is the one clean case: `DROP INDEX` for all eight, in any order, no data touched. See `DATABASE.md` §11. |

Because v42 is index-only, it is the *only* one of the three with a trivially correct
rollback. v40 and v41 are single nullable/defaulted columns, so `ALTER TABLE … DROP
COLUMN` works for them provided no index references them (none does today) — but do it
with the app **closed** and verify with `PRAGMA integrity_check` afterwards.

### Rollback recipe for v40 / v41 / v42 (closed app)

```bash
# 1. Quit the app.
pkill -f hsx2mail

# 2. Back up first. Always.
cp ~/.local/share/hsx2mail/hsx2mail.db /tmp/before-v42-rollback.db

# 3. v42 (indexes only) — safe, idempotent.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db <<'SQL'
DROP INDEX IF EXISTS idx_messages_needs_body;
DROP INDEX IF EXISTS idx_messages_account_date;
DROP INDEX IF EXISTS idx_messages_folder_thread_date;
DROP INDEX IF EXISTS idx_messages_folder_conv;
DROP INDEX IF EXISTS idx_messages_thread_norm;
DROP INDEX IF EXISTS idx_messages_message_id_norm;
DROP INDEX IF EXISTS idx_messages_in_reply_to_norm;
DROP INDEX IF EXISTS idx_messages_account_message_id;
SQL

# 4. v41 / v40 (columns) — only if you actually need the old shape.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db <<'SQL'
PRAGMA legacy_alter_table = OFF;   -- keep foreign_keys enforced during the rename
ALTER TABLE accounts DROP COLUMN secondary_sync_interval;   -- v41
ALTER TABLE accounts DROP COLUMN oauth_account_id;           -- v40
SQL

# 5. Update the ledger so Migrate() does not re-apply them on next start.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db \
  "DELETE FROM migrations WHERE version IN (40, 41, 42);"

# 6. Verify.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db 'PRAGMA integrity_check;'
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db 'SELECT MAX(version) FROM migrations;'
```

Note step 5. The ledger is authoritative; dropping the columns without deleting the
`migrations` rows would make the next `Migrate()` try to `ALTER TABLE … ADD COLUMN` them
back and fail with "duplicate column name". Conversely, deleting the ledger rows without
dropping the objects leaves the schema ahead of the ledger, which `Migrate()` handles
idempotently (its `ADD COLUMN` would fail — so you do need both steps, in that order).

**Caveat, stated plainly:** step 4 is destructive to any data in those columns, and
`ALTER TABLE … DROP COLUMN` is not a supported operation for a shipped application to
rely on. If a rollback is not a genuine emergency, restore a backup instead (§ Restoring
from a backup). These steps exist so an operator is not stuck, not because they are the
recommended path.

### Procedure

1. **Quit Email Hub completely** (use the menu Quit, or kill the process — make sure nothing is using `hsx2mail.db`).

2. **Back up your DB file** as a precaution. This script makes changes that are difficult to reverse cleanly if anything goes wrong, so a real file copy is your safety net:

   ```bash
   cp ~/.local/share/hsx2mail/hsx2mail.db ~/.local/share/hsx2mail/hsx2mail.db.before-rollback
   ```

   (Adjust the path for your OS — see "What you need" above.)

3. **Download the rollback script** from the repo. On the branch where the 0.3.0 schema lives (0.3.0 or later):

   ```bash
   curl -O https://raw.githubusercontent.com/beheoxinh/hsx2mail/main/tools/db/rollback-v39-to-v30.sql
   ```

   (Or download via your browser from `https://github.com/beheoxinh/hsx2mail/blob/main/tools/db/rollback-v39-to-v30.sql`.)

4. **Run the script against your DB**:

   ```bash
   sqlite3 ~/.local/share/hsx2mail/hsx2mail.db < rollback-v39-to-v30.sql
   ```

   The script runs in a single transaction. If anything fails, no changes are committed and your DB is unchanged.

5. **Verify the rollback** worked:

   ```bash
   sqlite3 ~/.local/share/hsx2mail/hsx2mail.db \
     "SELECT COUNT(*) FROM contacts; SELECT COUNT(*) FROM carddav_contacts; SELECT MAX(version) FROM migrations;"
   ```

   You should see your contacts counts and `30` as the max migration version.

6. **Launch the older Email Hub** (0.2.5 or earlier). It should start normally and your contacts autocomplete should work as before.

### If something goes wrong

Restore the backup you made in step 2:

```bash
cp ~/.local/share/hsx2mail/hsx2mail.db.before-rollback ~/.local/share/hsx2mail/hsx2mail.db
```

You're back to the v39 state and can run Email Hub 0.3.0 again.

If the issue persists, open a GitHub issue with the SQL error output and the version you were rolling back from / to.

---

## Rollback: v39 → v30 (Email Hub 0.3.0 → 0.2.5)

**Introduced in**: Email Hub 0.3.0 (cumulative effect of migrations 31 + 32 + 33 + 34 + 35 + 36 + 37 + 38 + 39 — see notes below).

**What 0.3.0 changed since 0.2.5**:

- **Migration 31** (Phase 2b.2.a): Replaced the legacy denormalized `contacts` (autocomplete-by-email) and `carddav_contacts` (per-email fan-out) tables with a unified `contact_records` schema covering both local and CardDAV contacts. Added multi-field support (phones, addresses, URLs, IMPPs, organization, title, notes, birthday, nickname, categories) and the `vcard_raw` round-trip column.
- **Migration 32**: Switched local-record IDs from the synthetic `"local-<email>"` shape (a leftover from the v30 email-as-PK schema) to UUIDs. Brings local records in line with CardDAV's vCard-UID identity semantics — emails become fully editable sub-rows in `contact_emails` rather than encoded into the record id.
- **Migration 33** (Phase 2b.2.b.1 follow-up): Added a missing `FOREIGN KEY ... ON DELETE CASCADE` from `carddav_record_state.addressbook_id` to `contact_source_addressbooks(id)`. Closes the privacy gap where deleting a contacts provider left record + state zombies in the local DB. Pre-step cleans any pre-existing orphans so the FK rebuild succeeds.
- **Migration 34** (Phase 2b.2.b.2): First-class PHOTO field support — adds `photo_data`, `photo_media_type`, `photo_url` columns to `contact_records` so the vCard parser/builder can extract and emit PHOTO natively. Before v34, photos round-tripped opaquely via `vcard_raw` but were never displayed.
- **Migration 35** (Calendar extension Phase 1B): Adds the `extension_secrets` table — shared keyring + AES fallback storage for the new `coreapi.Storage.Secrets` surface. First consumer is the Calendar extension's CalDAV password storage. The table tracks all extension secret keys regardless of where the value lives (empty `encrypted_value` = "in OS keyring", non-empty = "AES ciphertext right here").
- **Migration 36** (Extension OAuth keyring-fallback): Adds `encrypted_access_token` and `encrypted_refresh_token` columns to `oauth_tokens` so non-mail OAuth slots (`google-contacts`, `google-calendar`, `microsoft-contacts`, `microsoft-calendar`) can persist tokens on systems where the OS keyring isn't available — previously only the mail slots had an encrypted-DB fallback (via the `accounts` table from v9).
- **Migration 37** (Per-account "No outgoing server" + separate SMTP credentials): Adds `no_outgoing_server` (INTEGER, default 0), `smtp_username` (TEXT, default `''`), and `encrypted_smtp_password` (TEXT, nullable) columns to the `accounts` table. Lets users mark an account as receive-only (hidden from the composer's From dropdown, send attempts blocked), and lets Generic-provider accounts authenticate SMTP with credentials separate from IMAP. The keyring carries the separate SMTP password under `<accountID>:smtp` when set; `encrypted_smtp_password` is the AES-fallback companion for systems without an OS keyring.
- **Migration 38** (Per-account "Reply/Forward with" identity preference): Adds `reply_forward_identity_id` (TEXT, default `''`) to the `accounts` table. For receive-only accounts (the v37 feature), lets the user pick a specific identity from another sendable account to pre-select in the composer when replying or forwarding messages received here. Empty value falls back to the user's default sending account, then to the first available identity. Sendable accounts ignore the column entirely.
- **Migration 39** (Persistent body-parse-failed flag): Adds `body_failed` (INTEGER, default 0) to the `messages` table. Set to 1 when a body fetch+parse produced no usable content (and the message isn't encrypted, which legitimately has empty plaintext until view-time decryption). Replaces an in-memory cap on parse retries that reset every sync session — so an unparseable message used to be re-fetched from IMAP on every cycle, forever (issue #240). The body-fetch queue now excludes any row with `body_failed = 1`. A future parser improvement can re-queue previously-skipped messages via a one-line `UPDATE messages SET body_failed = 0 WHERE …` migration.

Migrations 31 through 39 ship together in 0.3.0 — no real-world DB will ever stop between them. The rollback script below handles the cumulative v39 state, which is what your DB will be in after upgrading from 0.2.5.

**Data lost on rollback to v30**:

- All multi-field contact data — phone numbers, addresses, URLs, instant-messaging handles, organization, job title, notes, birthday, nickname, categories. The v30 schema has no columns for these, so they're dropped.
- The `vcard_raw` round-trip preservation column. Means that the next time CardDAV sync runs under the older Email Hub, it will re-fetch and re-parse vCards from the server — only the fields the older parser knows about (email + display name) survive.
- CardDAV contacts' synthetic local IDs are reshaped. Older Email Hub identifies CardDAV contacts during sync by `href` (server-side URL path), not by local ID, so this doesn't affect sync correctness — only the row IDs change.
- Local-record UUIDs are dropped — v30 keys local contacts by email, which is the natural identity for the legacy schema.
- **Extension secrets stored in the AES-fallback path** (i.e., entries in the `extension_secrets` table). Keyring-stored entries are NOT touched by the rollback SQL — they remain in the OS keyring but become orphaned (no DB row pointing at them). To clean them up, use your OS keyring manager (Seahorse / Keychain / Credential Manager) and remove entries starting with `ext:`. In practice the Calendar extension is the only Phase-1 consumer, so the impact is: any saved CalDAV passwords will need to be re-entered after rollback + upgrade.
- **Per-extension OAuth grants** (the `google-contacts`, `google-calendar`, `microsoft-contacts`, `microsoft-calendar` slot tokens). The rollback deletes those `oauth_tokens` rows and drops the new fallback columns. v0.2.5 doesn't have the contacts/calendar extensions, so this only matters when you upgrade back to 0.3.0 — you'll need to re-grant calendar / contacts access from inside the relevant extension setting. Per-slot keyring entries (keys of the form `<accountID>:<configID>:access_token` / `refresh_token`) are NOT cleared by the rollback SQL; remove them from the OS keyring manager if you want a clean state. The mail OAuth grant (the `google-mail` / `microsoft-mail` row) is untouched.
- The local-contact `kind` (`manual` vs. `collected`) and the `name_overridden` flag. The v30 / pre-v0.3.0 `contacts` table has no columns for these (older `ensureTable` never created them). Re-upgrading after rollback reruns migration 31, which backfills both from literal defaults (`'collected'` / `0`) regardless of what the rollback would have stored, so preservation through the round-trip isn't possible. Practically: contacts you marked as manually-added in v0.3.0 will look auto-collected after a full round-trip; user-edited names auto-collected from sent mail won't be protected from being overwritten on the next auto-collection until you re-mark them.
- **Per-account "No outgoing server" + separate SMTP credentials state** (v37). The `no_outgoing_server`, `smtp_username`, and `encrypted_smtp_password` columns are dropped. Any account you marked receive-only in v0.3.0 will become sendable again under v0.2.5 if it still has a valid `smtp_host`; if SMTP host was left blank, v0.2.5 will surface an SMTP error at send time instead. Accounts that used separate SMTP credentials revert to using the IMAP username + IMAP password for SMTP AUTH, which is what v0.2.5 has always done. Separate-SMTP keyring entries (keys of the form `<accountID>:smtp`) are NOT cleared by the rollback SQL; remove them from the OS keyring manager if you want a clean state — v0.2.5 ignores them entirely. After re-upgrading to v0.3.0, you'll need to re-enter any separate SMTP passwords (the `<accountID>:smtp` keyring entry survives if you didn't manually clear it, in which case the toggle still works without re-entry).
- **Per-account "Reply/Forward with" identity preference** (v38). The `reply_forward_identity_id` column is dropped. v0.2.5 has no concept of receive-only accounts (that's the v37 feature this preference depends on), so the dropped value wouldn't have applied under v0.2.5 anyway — there's no behavior change. On re-upgrade to v0.3.0, accounts that had this preference set will revert to the empty default (i.e., the composer will use the user's default sending account when replying/forwarding on a receive-only account); the user re-picks the identity in the account's Server tab.
- **Persistent body-parse-failed flag** (v39). The `body_failed` column is dropped. Effect under v0.2.5: every message that v0.3.0 had marked unparseable will be subject to v0.2.5's old (unbounded) re-fetch behavior — the same state the user was in before #240 was fixed. Bounded only by message count, not by sync cycles. On re-upgrade to v0.3.0, those messages will be re-attempted up to one time and re-flagged via the new persistence path; transient, self-correcting.

**OAuth Credentials picker leftover state (inert, not cleaned by the rollback)**:

The v0.3.0 OAuth Credentials picker (Settings → Accounts → OAuth Credentials, plus the equivalent extension settings sections) accumulates state in three places that the rollback SQL doesn't touch. All three are inert under v0.2.5 — the older code doesn't read them — so leaving them in place is safe. They're listed here in case you want a fully clean state, or are doing a re-upgrade and want to start fresh:

- The `user_oauth_clients` table (on-demand, created when the user first saves a Custom client_id+secret). The table itself stays; rows stay. v0.2.5 doesn't read it. On re-upgrade to v0.3.0, the saved values become active again.
- The `user_oauth_slot_aliases` table (on-demand, created when the user first picks the "Email Hub - <provider>" alias option). Same treatment.
- Per-slot rows in the existing `settings` table with key `oauth_active_choice:<slot_id>` (introduced post-v0.3.0-build1). These encode the user's explicit picker selection independent of which credentials/alias rows happen to exist. v0.2.5 doesn't read them. On re-upgrade to v0.3.0, the marker takes effect again and the picker reflects what was last selected.

Optional cleanup (run only if you want zero leftover OAuth picker state in the DB):

```sql
DROP TABLE IF EXISTS user_oauth_clients;
DROP TABLE IF EXISTS user_oauth_slot_aliases;
DELETE FROM settings WHERE key LIKE 'oauth_active_choice:%';
```

Per-slot keyring entries (keyed as `oauth_user_client:<configID>`) are NOT cleared by SQL — remove them via the OS keyring manager (Seahorse / Keychain / Credential Manager) if you want a full external cleanup.

**What round-trips losslessly**:

- All emails and display names.
- Send-count and last-used autocomplete metadata (per-email).
- CardDAV addressbook membership, href, and ETag (for re-sync identity).

