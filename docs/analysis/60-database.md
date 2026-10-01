# 60 — Database & Persistence Layer Analysis

**Scope:** `internal/database/**`, `internal/{message,draft,folder,account,contact,undo,appstate}/**`, `tools/db/**`, `docs/SQL_ROLLBACK.md`
**Method:** codegraph exploration + mechanical extraction of the real migration set into a scratch SQLite DB (`/tmp/opencode/scratch.db`, 41/41 migrations apply cleanly), seeded with 3 accounts / 36 folders / 40 000 messages, then `EXPLAIN QUERY PLAN` on the 5 hottest queries + measured FTS trigger overhead + measured `SQLITE_BUSY_SNAPSHOT` behaviour.
**No source files were modified.**

---

## 1. Current schema state

| Property | Value |
|---|---|
| Migrations defined | **41** (v1..v41) |
| Highest version | **v41** (`accounts.secondary_sync_interval`) |
| Version tracking | `migrations` table, `SELECT COALESCE(MAX(version),0)` — `internal/database/database.go:187` |
| `PRAGMA user_version` | **0** — never used. The app ignores SQLite's native version mechanism. |
| Tables (excl. FTS shadow/shm) | 30 |
| Explicit `CREATE INDEX` | 34 (+37 implicit from `UNIQUE`/`PRIMARY KEY`) |
| Foreign keys | 19 tables have FKs, **11 have none** |
| `PRAGMA foreign_keys` | `ON`, applied per-connection via DSN — `database.go:60` |
| Destructive migrations | v29, v31, v33 (DROP TABLE) |
| Rollback scripts | 1 (`tools/db/rollback-v39-to-v30.sql`), covers v31..v39 only |
| `go test ./internal/database/...` | **FAILING** — 2 tests red (see §7) |

### 1.1 Migration inventory (v1..v41)

| v | Purpose | Destructive | Rollback |
|---|---|---|---|
| 1 | Core schema: `accounts`, `identities`, `folders`, `messages`, `attachments`, `drafts` + first indexes | no | — |
| 2 | `accounts.encrypted_password` (keyring fallback) | no | — |
| 3 | `messages.references_list` (References header as JSON array, for threading) | no | — |
| 4 | Draft sync fields: `sync_status`, `imap_uid`, `folder_id` FK, `references_list`, `last_sync_attempt`, `sync_error` | no | — |
| 5 | `settings` key-value table | no | — |
| 6 | `contact_sources` (CardDAV accounts) | no | — |
| 7 | `contact_sources.encrypted_password` | no | — |
| 8 | `account_sender_certs` / PGP sender-key wiring | no | — |
| 9 | `accounts.encrypted_refresh_token` | no | — |
| 10 | `messages.body_fetched` (two-phase header→body sync flag) | no | — |
| 11 | `accounts.sync_interval` (polling period) | no | — |
| 12 | `accounts.color` (unified-inbox tinting) | no | — |
| 13 | `app_state` (UI state persistence) | no | — |
| 14 | **FTS5 virtual table `messages_fts`** + 3 external-content triggers | no | — |
| 15 | Identity signature settings (`signature_enabled`, `signature_for_new`, `signature_for_reply`, …) | no | — |
| 16 | `image_allowlist` | no | — |
| 17 | `contact_sources.account_id` FK, `contact_source_oauth`, per-source OAuth | no | — |
| 18 | `trusted_certificates` (TOFU) | no | — |
| 19 | `smime_certificates`, `smime_sender_certs` | no | — |
| 20 | `messages.smime_raw_body` **BLOB**, `smime_encrypted` | no | — |
| 21 | `drafts.encrypted`, `drafts.encrypted_body` BLOB (encrypt-to-self) | no | — |
| 22 | `drafts.sign_message` | no | — |
| 23 | `drafts.attachments_data` (inline + regular attachments on drafts) | no | — |
| 24 | `pgp_keys`, `pgp_sender_keys`, per-message `pgp_status`/`pgp_encrypted` | no | — |
| 25 | `pgp_keyservers` | no | — |
| 26 | `accounts.sync_all_folders`, `folders.subscribed` | no | — |
| 27 | `accounts.sync_folders_enabled` | no | — |
| 28 | `accounts.shared_mailbox_parent_id` | no | — |
| 29 | **OAuth multi-config**: PK `(account_id)` → `(account_id, client_config_id)` | **YES** — `DROP TABLE oauth_tokens` at `migrations.go:742` (swap-table dance; data copied first, so recoverable-on-failure but not reversible) | **NO** |
| 30 | `contact_sources.writable` | no | — |
| 31 | **Unified contact schema**: `contact_records` + `contact_emails/phones/addresses/urls/impps/categories` replacing `contacts` + `carddav_contacts` | **YES** — `DROP TABLE contacts; DROP TABLE carddav_contacts;` at `migrations.go:1018-1019`. Rows not matched by the backfill JOIN are silently lost. | yes (in `rollback-v39-to-v30.sql`) |
| 32 | Rewrite legacy `local-*` record IDs → UUIDs (staging table `_migration_32_idmap`) | no (drops its own temp table, `migrations.go` v32) | yes |
| 33 | `carddav_record_state` → add `addressbook_id` FK | **YES** — `DROP TABLE carddav_record_state` (swap dance, `migrations.go` v33) | yes |
| 34 | Contact photo columns (`contact_records.photo_*`, `data_type`) | no | yes |
| 35 | `extension_secrets` | no | yes |
| 36 | `oauth_tokens.encrypted_access_token` / `encrypted_refresh_token` fallback | no | yes |
| 37 | Per-account SMTP-receive-only: `no_outgoing_server`, `smtp_username`, `encrypted_smtp_password` | no | yes |
| 38 | `accounts.reply_forward_identity_id` | no | yes |
| 39 | `messages.body_failed` (persistent body-parse-failed flag) | no | yes |
| 40 | `accounts.oauth_stable_id` (immutable `<tid>:<oid>` for Microsoft incremental consent) | no | **NO** |
| 41 | `accounts.secondary_sync_interval` | no | **NO** |

**Rollback coverage gap:** the only script is `tools/db/rollback-v39-to-v30.sql` (v31→v30). **v29, v40, v41 have no rollback.** Worse, running the v39→v30 script against a current v41 DB leaves `oauth_stable_id` (v40) and `secondary_sync_interval` (v41) in place. The script's own header (lines 225-228) correctly argues that leaving `reply_forward_identity_id` behind "will cause v38's ADD COLUMN to fail on the next upgrade" — the identical argument applies to v40/v41, so the script is stale.

---

## 2. Concurrency / PRAGMA configuration

| Setting | Value | Source | Assessment |
|---|---|---|---|
| DSN | `file:%s?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-64000)` | `internal/database/database.go:60` | Pragmas are per-connection; embedding in DSN is the correct choice. |
| `journal_mode` | `WAL` | `database.go:60` | Correct — enables concurrent readers alongside one writer. |
| `synchronous` | `NORMAL` | `database.go:60` | Acceptable under WAL. Loses only the last transactions on OS crash / power loss, not on app crash. |
| `busy_timeout` | `30000` ms | `database.go:60` | Present. **Does not cover `SQLITE_BUSY_SNAPSHOT`** — see F-1. |
| `cache_size` | `-64000` (64 MB) per connection | `database.go:60` | × `MaxOpenConns=12` ⇒ up to **768 MB** of page cache worst case. Fine on this hardware, oversized for a mail client. |
| `mmap_size` | **not set** (SQLite default 0) | — | Missing. `PRAGMA mmap_size=268435456` would cut read syscalls on large body reads. |
| `wal_autocheckpoint` | **not set** (SQLite default 1000 pages ≈ 4 MB) | — | The app also runs its own `wal_checkpoint(PASSIVE)` every 5 min (`database.go:37`, `:122-123`). Two checkpointers; PASSIVE never blocks writers and never truncates the WAL. |
| `journal_size_limit` | **not set** | — | Missing. Without it the `-wal` file is never truncated back after checkpoint → persistent bloat across restarts. |
| `MaxOpenConns` | `12` — **fixed, never scaled** | `database.go:23` | WAL allows 1 writer. 12 open conns means up to 11 goroutines can contend. |
| `BaseIdleConns` | `3` | `database.go:26` | |
| `MaxIdleConns` | `6` | `database.go:30` | |
| `IdleConnsPerAccount` | `1` | `database.go:33` | |
| Pool scaling | `UpdateIdleConns(n)` = `clamp(3 + n*1, 3, 6)` | `database.go:95-112` | **Only idle conns scale. `MaxOpenConns` stays 12 regardless of account count.** Called from `app/app.go:1058,1061`. |
| Checkpoint | `PRAGMA wal_checkpoint(PASSIVE)` every 5 min | `database.go:37`, `:122-123`, started at `app/app.go:620` | PASSIVE only. Should be `TRUNCATE` to reclaim `-wal` space. |
| DB file perms | `os.MkdirAll(dir, 0700)` + `os.Chmod(path, 0600)` | `database.go:51`, `:80` | Correct. Note `os.Chmod` runs *after* `db.Ping()` (`:73-76` then `:80`) — brief window where the file exists with umask-derived perms. |
| `ANALYZE` | **never run, anywhere** | grep across `internal/`, `app/`, `extensions/` → 0 hits | See F-8. |
| `VACUUM` | **never run, anywhere** | 0 hits | See F-9. |
| `integrity_check` | **never run** | 0 hits | No corruption self-heal/detection. |
| `SQLITE_BUSY` retry | **no retry logic anywhere** | grep → only `withIMAPRetry` (IMAP, not SQL) | Combined with F-1 this is a hard failure mode. |
| Write mutex in store layer | **none** (`internal/database`, `internal/message`) | only `internal/message/fts.go:17` has one (indexer-only) | All write serialization is delegated to SQLite. |

---

## 3. Transaction-boundary analysis

`BEGIN`/`Commit` appears in only 4 non-test files:

| Site | Scope | Verdict |
|---|---|---|
| `internal/database/database.go:217-233` (`applyMigration`) | one migration per tx | ✅ correct — per-migration atomicity + `defer tx.Rollback()` |
| `internal/message/store.go:739-762` (`UpdateFlagsByUIDBatch`) | prepared-stmt batch | ✅ correct — but read-then-write safe (no read) |
| `internal/message/store.go:1145-1157` (`UpdateBodiesBatch`) | prepared-stmt batch | ✅ correct |
| `internal/message/attachment_store.go:199` | `Create` | ✅ correct |
| `internal/account/store.go:511-524` (`Reorder`) | account order | ✅ correct |
| `internal/account/store.go:736-754` | identity reorder | ✅ correct |
| `internal/contact/store.go:928-937` (`UpsertRecord`) | record + all sub-tables | ✅ correct; write precedes read (`:974` INSERT → `:1013` SELECT), so no lock upgrade |
| `internal/carddav/store.go:235, 542, 722, 799` | CardDAV batches | ⚠️ `:742` SELECT precedes `:766` INSERT → **read-then-write upgrade**, see F-1 |
| `internal/extensions/store.go:138` | one extension migration per tx | ✅ correct |
| **`internal/message/store.go` — everything else** | 21 `Exec` calls, 0 transactions | ❌ see F-2, F-3 |
| **`internal/draft/store.go` (391 lines)** | 0 transactions | ⚠️ single-statement writes only — acceptable |
| **`internal/folder/store.go` (438 lines)** | 0 transactions | ⚠️ see F-4 |
| `internal/undo/undo.go` | 0 (in-memory stack, `undo.go:41`) | N/A |

**Headline:** `internal/message/store.go` is 2 539 lines with **21 `Exec` calls and only 2 transactions** — the single largest, hottest, and most failure-prone store in the codebase has essentially no transactional discipline.

---

## 4. Index inventory

All 34 explicit indexes, with the query each actually serves (verified against `EXPLAIN QUERY PLAN` on the seeded DB):

| Index | Columns | Serves | Verdict |
|---|---|---|---|
| `idx_messages_account` | `messages(account_id)` | `GetConversation` thread scan (`store.go:1557`), `CountConversationsUnifiedInbox` | **Chosen by planner for the thread query — but that's the wrong index, see F-6** |
| `idx_messages_folder` | `messages(folder_id)` | folder listing | **Never chosen** — planner prefers `idx_messages_body_fetched` (same leading col, strictly better) |
| `idx_messages_body_fetched` | `messages(folder_id, body_fetched)` | body fetch batching (`store.go:888-994`), and **`ListConversationsByFolder`** | ⚠️ Doubles as the driver index for the conversation query — accidental, not designed |
| `idx_messages_date` | `messages(date DESC)` | **`DeleteOlderThan` (cleanup by date)** | ⚠️ Used, but the wrong index for a *per-account* delete, see F-7 |
| `idx_messages_thread` | `messages(thread_id)` | threading | **Dead** — every thread query wraps the column in `REPLACE()` so it can never be used, F-6 |
| `idx_messages_in_reply_to` | `messages(in_reply_to)` | threading | **Dead** — same `REPLACE()` problem |
| `idx_messages_message_id` | `messages(message_id)` | dedup / `ExistsInFolder` | **Dead** in the thread query (F-6); still used by non-wrapped lookups |
| `idx_messages_unread` | `messages(folder_id, is_read) WHERE is_read=0` (partial) | `CountUnreadByFolder` (`store.go:278`) | ✅ good, but planner instead picks the `UNIQUE(folder_id,uid)` covering index — see EXPLAIN below |
| `idx_folders_account` / `idx_folders_parent` | `folders(account_id)` / `(parent_id)` | folder tree, inbox stats | ✅ |
| `idx_identities_account` | `identities(account_id)` | identity list | ✅ |
| `idx_drafts_account` | `drafts(account_id)` | **`ListPendingSync`** (`draft/store.go:267-268`) | ⚠️ Filters `account_id=? AND sync_status IN('pending','failed')`; planner uses this then filters. Composite `(account_id, sync_status)` would be covering — F-7 |
| `idx_drafts_sync_status` | `drafts(sync_status)` | pending-draft sweep | ⚠️ Same |
| `idx_attachments_message` | `attachments(message_id)` | attachment load + FK-cascade delete | ✅ |
| `idx_contact_emails_email` | `contact_emails(email)` | contact lookup by email | ✅ |
| `idx_contact_emails_rank` | `contact_emails(send_count DESC, last_used DESC)` | autocomplete ranking | ✅ |
| `idx_contact_records_source` / `_source_kind` / `_source_ref` | `contact_records(source)` / `(source,kind)` / `(source_ref)` | contact search / sync scoping | ✅ |
| `idx_contact_sources_account`, `idx_contact_source_addressbooks_source`, `idx_carddav_record_state_addressbook` | source scoping | CardDAV sync | ✅ |
| `idx_pgp_keys_account/_email/_fingerprint`, `idx_pgp_sender_keys_email/_fingerprint` | PGP key lookup | ✅ |
| `idx_smime_certificates_account/_email`, `idx_smime_sender_certs_email/_fingerprint` | S/MIME lookup | ✅ |
| `idx_image_allowlist_type_value` | `image_allowlist(type, value)` | ✅ |
| `idx_extension_secrets_ext` | `extension_secrets(extension)` | ✅ |

**Missing indexes (not in the schema):**

| Missing index | Query it would serve | Location |
|---|---|---|
| `messages(account_id, date)` | `DELETE FROM messages WHERE account_id=? AND date<?` | `store.go:1038` |
| `messages(folder_id, is_read, id)` covering | `SELECT id FROM messages WHERE folder_id=? AND is_read=0` (bulk mark-read/unread) | `store.go:286, 305` |
| `drafts(account_id, sync_status)` | `ListPendingSync` | `draft/store.go:268` |
| `messages_fts` tokenizer index | CJK search — no index can fix this; needs `tokenize='trigram'` | `migrations.go` v14 |
| expression index on `COALESCE(thread_id,id)` | `ListConversationsByFolder` GROUP BY | `store.go:1300-1315` |

---

## 5. EXPLAIN QUERY PLAN — the 5 hottest queries

Seeded DB: 3 accounts, 36 folders, 40 000 messages, 2 KB `body_text` on 20 000 rows. Plans are **identical with and without `ANALYZE`** — the app never runs it, so the no-stats plan is the shipped plan.

### 5.1 List conversations — `store.go:1300-1319` (fires on every folder selection)
```sql
SELECT COALESCE(thread_id,id) AS conv_thread_id, MIN(subject), MAX(snippet),
       COUNT(*), SUM(CASE WHEN is_read=0 THEN 1 ELSE 0 END), MAX(date),
       GROUP_CONCAT(id), json_group_array(DISTINCT json_object('name',from_name,'email',from_email))
FROM messages WHERE folder_id = ? GROUP BY COALESCE(thread_id,id)
ORDER BY <max-date expr> DESC LIMIT ? OFFSET ?
```
```
SEARCH messages USING INDEX idx_messages_body_fetched (folder_id=?)
USE TEMP B-TREE FOR GROUP BY
USE TEMP B-TREE FOR ORDER BY
```
**No index can serve `GROUP BY COALESCE(thread_id, id)`** — it is a non-indexable expression, so every conversation list materialises *all* rows in the folder, builds a temp B-tree for grouping, then another for the sort, then applies `LIMIT`. For a 5 000-message folder that is a full scan + 2 temp B-trees per page turn. `idx_messages_thread` and `idx_messages_folder` are both bypassed.

### 5.2 List messages in thread — `store.go:1544-1564` (fires on every conversation open — the worst query in the app)
```sql
SELECT ... FROM messages m INNER JOIN folders f ON m.folder_id = f.id
WHERE m.account_id = ? AND (
  REPLACE(REPLACE(COALESCE(m.thread_id, m.id),'<',''),'>','') = ?
  OR REPLACE(REPLACE(m.message_id,'<',''),'>','') = ?
  OR REPLACE(REPLACE(m.in_reply_to,'<',''),'>','') = ?)
ORDER BY m.date ASC
```
```
SEARCH m USING INDEX idx_messages_account (account_id=?)
SEARCH f USING COVERING INDEX sqlite_autoindex_folders_1 (id=?)
USE TEMP B-TREE FOR ORDER BY
```
The three `REPLACE(REPLACE(...))` wrappers make `idx_messages_thread`, `idx_messages_in_reply_to` and `idx_messages_message_id` structurally unusable. The only sargable term is `account_id`, so opening a single conversation scans **every message in the account** and applies 3 string rewrites per row. On a 100 k-message account that is 300 k function calls on the hottest UI interaction. **P0 performance.**

### 5.3 Unread counts
```sql
-- store.go:278  per-folder
SELECT COUNT(*) FROM messages WHERE folder_id = ? AND is_read = 0;
```
```
SEARCH messages USING COVERING INDEX sqlite_autoindex_messages_2 (folder_id=? AND is_read=?)
```
`sqlite_autoindex_messages_2` is the implicit index from `UNIQUE(folder_id, uid)` — it happens to be covering here and is *smaller* than `idx_messages_unread`, so the dedicated partial index is never chosen. Not a bug (the plan is good), but it means the partial index is dead weight on every write.

```sql
-- store.go:232  unified inbox — denormalised counter
SELECT COALESCE(SUM(f.unread_count),0) FROM folders f
INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1 WHERE f.folder_type = 'inbox';
```
```
SCAN a
SEARCH f USING INDEX idx_folders_account (account_id=?)
```
Cheap, but driven by the denormalised `folders.unread_count` column — it can disagree with the live `COUNT(*)` in 5.3a. `messages.is_read` and `folders.unread_count` are maintained by separate code paths with no reconciliation job.

### 5.4 FTS search — `store.go:2232-2236`
```sql
SELECT COUNT(DISTINCT COALESCE(m.thread_id, m.id)) FROM messages m
JOIN messages_fts fts ON m.rowid = fts.rowid
WHERE m.folder_id = ? AND messages_fts MATCH ?
```
```
USE TEMP B-TREE FOR count(DISTINCT)
SCAN fts VIRTUAL TABLE INDEX 0:M7
SEARCH m USING INTEGER PRIMARY KEY (rowid=?)
```
Plan is fine. **The tokenizer is not** — see F-10.

### 5.5 Cleanup by date — `store.go:1038`
```sql
DELETE FROM messages WHERE account_id = ? AND date < ?
```
```
SEARCH messages USING INDEX idx_messages_date (date<?)
SEARCH attachments USING COVERING INDEX idx_attachments_message (message_id=?)
```
The planner prefers the global `date` index over `idx_messages_account`, so a per-account retention purge drives an index-ordered delete across *all* accounts' date ranges, and cascades to `attachments` one row at a time. `messages(account_id, date)` would make it a single range scan. Because `messages` is a 42-column table with `body_html`/`body_text`/`smime_raw_body`/`pgp_raw_body`, each deleted row is a full record rewrite in the WAL.

---

## 6. Findings

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| **F-1** | **P0** | `internal/database/database.go:60` | No `_txlock=immediate` in the DSN. `modernc.org/sqlite` `tx.go:23-24` emits a bare `BEGIN` (= `DEFERRED`) whenever `beginMode == ""`. Any transaction that reads before it writes (e.g. `internal/carddav/store.go:742` SELECT → `:766` INSERT) hits `SQLITE_BUSY_SNAPSHOT`, which **`busy_timeout` provably cannot rescue**. Measured: fails in **0 ms** with `database is locked` despite `busy_timeout=30000`. | Intermittent hard sync failure with no retry path. Worst case: the CardDAV sync goroutine and the UI thread collide → 30 s stall or immediate error. | Add `&_txlock=immediate` to the DSN at `database.go:60` (driver supports it: `sqlite.go:187-192`). Makes every `Begin()` take the write lock up front, converting the fatal snapshot conflict into an ordinary busy_timeout wait. |
| **F-2** | **P0** | `internal/sync/messages.go:97-105` + `internal/message/store.go:799` | On `UIDValidity` change the sync engine calls `DeleteByFolder(folderID)` — an un-transacted `DELETE FROM messages WHERE folder_id = ?` that destroys the entire local copy — and only *then* re-fetches from IMAP. No staging table, no transaction, no backup. | If the process crashes, the network drops, or the IMAP FETCH returns partial results mid-resync, the folder is **permanently empty** and messages that were only on the server are unrecoverable. Worst data-loss path in the app. | Stage into a temp table inside one transaction, or at minimum set the folder's `subscribed=0`/sentinel first and only `DELETE` after the replacement set is durably inserted. |
| **F-3** | **P0** | `internal/sync/fetch.go:88`, `:95`, `:102` | Three separate non-transacted writes: `messageStore.Delete(messageID)` → `messageStore.UpdateBody(...)` → loop of `attachmentStore.Create(att)`. | `UpdateBody` (`store.go:877`) does **not** check `RowsAffected`, so if the row was deleted at `:88` the subsequent UPDATE silently matches 0 rows and returns `nil` — **the body is lost with no error anywhere**. Attachment `Create` failures are only logged at Debug (`:103`), so attachments vanish silently. | Wrap the three writes in one transaction, and make `UpdateBody` (and the batch equivalent) return an error when `RowsAffected == 0`. |
| **F-4** | **P1** | `internal/message/store.go:1300-1319` | `ListConversationsByFolder` groups on `COALESCE(thread_id, id)`, a non-indexable expression. Plan = full folder scan + 2 temp B-trees per page. | Every folder selection and every scroll-page is O(messages in folder). Degrades linearly; a 50 k-row folder is unusable. | Materialise a `conversation_key` column at insert time (strip angle brackets there) with a `UNIQUE(folder_id, conversation_key)` + `INDEX(folder_id, conversation_key, latest_date)`, or add an expression index `ON messages(folder_id, COALESCE(thread_id,id))`. |
| **F-5** | **P0** | `internal/message/store.go:1558-1560` | `REPLACE(REPLACE(COALESCE(m.thread_id,m.id),'<',''),'>','') = ?` OR the same on `message_id` and `in_reply_to`. The `REPLACE()` wrapper is unindexable, so `idx_messages_thread`, `idx_messages_in_reply_to` and `idx_messages_message_id` are all dead code. | Opening a conversation scans every message in the account and runs 3 string rewrites per row. This is the app's hottest user action. | Store the bracket-stripped value at write time in a dedicated column, or drop the runtime `REPLACE()` and normalise on insert so the raw `thread_id` is directly comparable. |
| **F-6** | **P1** | `internal/database/migrations.go` v14 (`messages_fts`) | `CREATE VIRTUAL TABLE messages_fts USING fts5(...)` with **no `tokenize=` clause** ⇒ `unicode61`, which treats an unbroken CJK run as a single token. Measured on the seeded DB: searching `项目` or `讨论` inside `我们需要尽快讨论这个项目进度` returns **0 rows**; only the full-sentence match or a `prefix*` match works. Latin control (`project`) returns 1. | **Full-text search is functionally broken for Chinese/Japanese/Korean.** The app ships `zh-CN`, `zh-TW`, `zh-HK` locales. | Rebuild with `tokenize='trigram'`. Verified: trigram matches `项目进度` but still needs ≥3 characters (`讨论` → 0 hits), so the search UI must enforce/pad a 3-char minimum. Requires a new migration v42 + FTS reindex. |
| **F-7** | **P1** | `internal/message/store.go:1557`, `:1038`; `internal/draft/store.go:268` | Missing composite indexes: `messages(account_id, date)` (per-account retention purge picks the global `idx_messages_date` instead), `messages(folder_id, is_read, id)` (bulk mark-read ID fetches), `drafts(account_id, sync_status)`. | Retention purge does an index-ordered delete across all accounts with per-row attachment cascade. Bulk mark-read on a large folder scans a non-covering index. | Add the three indexes in a v42 migration. |
| **F-8** | **P1** | `internal/database/database.go:60` | FTS `messages_fts_update` is a plain `AFTER UPDATE ON messages` (delete-then-insert) that fires on **every** UPDATE, including flag-only ones. | **Measured:** 5 000 `UPDATE messages SET is_read=1` took **113 ms with the FTS triggers vs 21 ms without — 5.4× overhead** with 2 KB bodies, and the gap widens with real 10-15 KB bodies. Mark-as-read is one of the most frequent actions in the app, and it runs inside `UpdateFlagsByUIDBatch`'s transaction, inflating the WAL. | Restrict the trigger to `AFTER UPDATE OF subject, from_name, from_email, to_list, cc_list, snippet, body_text ON messages`, so flag updates never touch the FTS index. |
| **F-9** | **P1** | whole repo (0 hits) | **`VACUUM` is never run.** `journal_size_limit` and `wal_autocheckpoint` are never set; the only checkpoint is `wal_checkpoint(PASSIVE)` (`database.go:123`), which never truncates. | SQLite never returns free pages to the filesystem, and the `-wal` file is never truncated. A long-lived install accumulates bloat monotonically — worse given `messages` carries 4 potentially large columns. | Set `&_pragma=journal_size_limit(67108864)` in the DSN, switch the 5-minute routine to `wal_checkpoint(TRUNCATE)`, and schedule `PRAGMA optimize` (or `VACUUM` on idle) at shutdown. |
| **F-10** | **P1** | whole repo (0 hits) | **`ANALYZE` is never run.** All 5 query plans were verified to be identical with and without stats, so the absence is currently benign — but the plan quality in §5 depends on SQLite's default heuristics. | A future data distribution (very skewed folder sizes) will make the planner choose badly with no way to correct it, and `PRAGMA optimize` is unavailable to fix it. | Run `PRAGMA optimize` after sync completes (cheap, SQLite decides whether stats are stale). |
| **F-11** | **P1** | `internal/database/database_test.go:190-201` and `:367-379` | **The `internal/database` test suite is red on `main`.** `TestMigrationV32_LocalRecordIDsRewrittenToUUIDs` and `TestMigrationV33_CleansExistingOrphans` both `t.Fatalf("re-migrate: ...")` with `duplicate column name: secondary_sync_interval`. The hand-rolled downgrade simulation drops columns for v36/v37/v38/v39 but **not v41**, so `db.Migrate()` re-runs v41's `ALTER TABLE accounts ADD COLUMN secondary_sync_interval` against a column that still exists. | The only re-migration/idempotency regression tests in the persistence layer are not running green, so migration-safety regressions are invisible. | Add `secondary_sync_interval` (v41) and `oauth_stable_id` (v40) to the drop list in both tests, and derive that list from the `migrations` slice instead of hard-coding it so it can never go stale again. |
| **F-12** | **P1** | `app/detached_composer.go:150` | The composer process calls `database.Open(paths.DatabasePath())` but **never calls `Migrate()`** (only `app/app.go:456` does). | The composer therefore never receives the `ErrSchemaTooNew` guard (`database.go:200-201`). A composer built against an older schema will silently query a newer DB and misbehave instead of failing closed. | After `Open`, read `MAX(version)` and return the same `ErrSchemaTooNew` if it exceeds the build's max known. The composer is only ever spawned by an already-migrated main, so this is a cheap assertion, not a migration. |
| **F-13** | **P2** | `internal/database/database.go:23`, `:95-112` | `UpdateIdleConns` scales only `MaxIdleConns`. `MaxOpenConns` is pinned at 12 no matter how many accounts exist, while the comment at `:20-23` says the ceiling exists to limit writer contention. | With many accounts the sync engine has more in-flight writers than the ceiling was meant to allow; contention shows up as `busy_timeout` waits rather than as backpressure. | Either scale `MaxOpenConns` deliberately (and document the writer-serialisation consequence), or drop `MaxOpenConns` to ~4-6 and let `busy_timeout` absorb the queueing — 12 gives WAL no benefit since only one can write. |
| **F-14** | **P2** | `internal/database/database.go:60` | `cache_size(-64000)` is per-connection; with `MaxOpenConns=12` the theoretical page-cache ceiling is **768 MB**. | Over-reserved memory on small installs; on this 64 GB host it is harmless, but it is not a considered budget. | Set `_pragma=cache_size(-16000)` (16 MB) and lower `MaxOpenConns`, or set a global `PRAGMA cache_size` on one connection. |
| **F-15** | **P2** | `internal/database/database.go:80` | `os.Chmod(path, 0600)` runs **after** `db.Ping()` (`:73-76`), which is what actually creates the file. | Small window in which the DB exists with umask-derived permissions before being tightened. | `os.OpenFile(path, os.O_RDWR\|os.O_CREATE, 0600)` first, or rely on a `0700` parent dir (already set at `:51`) and chmod immediately after open. |
| **F-16** | **P2** | schema: 11 tables with no FK | `accounts`, `app_state`, `contact_records`, `extension_secrets`, `image_allowlist`, `migrations`, `pgp_keyservers`, `pgp_sender_keys`, `settings`, `smime_sender_certs`, `trusted_certificates` have **no** foreign keys. Most are key-value tables where that is correct, but `contact_records` and `pgp_sender_keys` / `smime_sender_certs` can orphan. | Orphan contact records and orphan sender keys accumulate silently; no cascade cleanup. | Add `account_id REFERENCES accounts(id) ON DELETE CASCADE` to `contact_records`, `pgp_sender_keys`, `smime_sender_certs`, `pgp_keyservers`. |
| **F-17** | **P2** | schema: `identities`, `drafts`, `messages.message_id` | `identities` has **no** `UNIQUE(account_id, email)` — duplicate identities per account are possible. `drafts` has no `UNIQUE(account_id, imap_uid)`. `messages.message_id` is indexed but **not** unique, so duplicate RFC Message-IDs can be inserted. (`messages` *does* correctly have `UNIQUE(folder_id, uid)`, and `folders` has `UNIQUE(account_id, path)`.) | Duplicate sender identities (ambiguous reply-from pick), duplicate drafts after a sync retry, and duplicate `message_id` rows breaking the `ExistsInFolder` dedup at `sync/messages.go:226-227`. | Add the three `UNIQUE` constraints in a v42 migration, de-duplicating existing rows first. |
| **F-18** | **P2** | schema-wide | **No `CHECK` constraints anywhere.** `messages.is_read`/`is_starred`/`has_attachments`/`body_fetched`, `drafts.sync_status`, `accounts.sync_interval`, `contact_sources.*` are all unconstrained. | Nothing stops a bad write from storing `is_read = 7` or `sync_status = 'pendin'`. The code writes these as Go bools/int so it is currently safe, but the DB provides no backstop. | Add `CHECK (x IN (0,1))` on the boolean columns and `CHECK (sync_status IN ('pending','synced','failed'))` on `drafts.sync_status`. |
| **F-19** | **P2** | `internal/undo/undo.go:41` | The undo stack is **in-memory only**. There is no `undo_commands` table — `AGENTS.md` §4 claims v20 added one, but v20 is actually `messages.smime_raw_body` and no such table exists in the 30-table schema. | Undo history is lost on every restart; the documented capability does not exist. | Either persist the stack (bounded, e.g. 50 entries) or correct `AGENTS.md` §4. Note `maxSize` is properly bounded (`undo.go:70-71`), so there is no unbounded-memory risk. |
| **F-20** | **P2** | `tools/db/rollback-v39-to-v30.sql` (whole file); `docs/SQL_ROLLBACK.md:80` | The only rollback script stops at v39. The current schema is v41. v40 (`oauth_stable_id`) and v41 (`secondary_sync_interval`) have no documented reverse path, and the script leaves both columns in place if run against a v41 DB. Line 225-228 correctly argues that a leftover v38 column breaks the next upgrade — the identical argument applies to v40/v41. Stale project name `aerion.db` in the verify command (`:247`) and the usage comment (`:100`). | Downgrading from a current build leaves the schema at v30 with migrations ≥31 deleted but v40/v41 columns present → the next upgrade's `ADD COLUMN` fails. Also `migrations.go:200-201` surfaces `ErrSchemaTooNew` pointing users at this guide, which no longer covers their version. | Add a v41→v30 section + script (or generalise the existing one to drop all columns added after v30), and fix the `aerion.db` references. |
| **F-21** | **P3** | `internal/settings/image_allowlist.go` | The image-allowlist store has **zero test files**. | Untested persistence path. | Add store tests. |
| **F-22** | **P3** | `internal/database/migrations.go:580`, `:596`; `internal/message/store.go:351,353` | `messages.smime_raw_body` and `pgp_raw_body` are BLOBs, but read paths only ever touch them as `(smime_raw_body IS NOT NULL) AS has_smime` — never selected. So there is **no blob-in-list-query abuse**. | ✅ None. Recorded as a verified non-issue; worth keeping in mind if a future "export raw message" feature selects these in a list path. | None. |
| **F-23** | **P3** | `internal/message/store.go:130,2267`; `:194,1369,2334` | `json_group_array`/`GROUP_CONCAT` are computed **in SQL** and parsed in Go (`parseParticipantsJSON`, `store.go:1381`). | ✅ Correct design, not JSON-column abuse — aggregation stays server-side, parsing happens once per row. No action. | None. |

**Attachment storage:** `attachments` stores `local_path TEXT` (filesystem-backed), **not** the blob — `internal/database/migrations.go:145-154`. No DB bloat from attachments, and the FK `ON DELETE CASCADE` plus `idx_attachments_message` are both present. This is a good decision; it is misdocumented in `AGENTS.md` §4 ("metadata + inline content").

---

## 7. Test coverage

### 7.1 `internal/database` — 1 file, 13 tests, **2 currently failing**

| Test | Covers |
|---|---|
| `TestOpen` | open + ping |
| `TestMigrate` | full chain applies |
| `TestMigrateIdempotent` | re-run is a no-op |
| `TestUpdateIdleConns` | pool scaling formula |
| `TestCheckpoint` | `wal_checkpoint(PASSIVE)` |
| `TestPath` | path accessor |
| `TestMigrationV29_OAuthCompositeKey` | v29 swap-table + PK change |
| `TestMigrationV31_DuplicateCardDAVEmailDoesNotFailMigration` | v31 duplicate-email tolerance |
| `TestMigrationV32_LocalRecordIDsRewrittenToUUIDs` | v32 UUID rewrite — **FAILS (F-11)** |
| `TestMigrationV33_AddsAddressbookFK` | v33 addressbook FK |
| `TestMigrationV33_CleansExistingOrphans` | v33 orphan cleanup — **FAILS (F-11)** |
| `TestMigrationV34_AddsPhotoColumns` | v34 photo columns |

**Migrations with dedicated tests: 5 of 41** (v29, v31, v32, v33, v34). The 36 that follow — including both destructive migrations v31's successors and every v35-v41 change — have no targeted test. The only coverage for them is `TestMigrate` asserting the chain applies without error, which cannot detect a column added in the wrong place, a lost `DEFAULT`, or a destructive backfill that silently drops rows.

### 7.2 Store-level coverage

| Package | Test files | Tests |
|---|---|---|
| `internal/message` | 2 | 5 |
| `internal/draft` | 2 | 7 |
| `internal/folder` | 2 | 12 |
| `internal/account` | 1 | 8 |
| `internal/contact` | 3 | 21 |
| `internal/carddav` | 3 | 30 |
| `internal/settings` | 2 | 18 |
| `internal/certificate` | 1 | 9 |
| `internal/undo` | 1 | 10 |
| `internal/appstate` | 1 | 5 |
| `internal/settings` (image_allowlist) | **0** | **0** (F-21) |

**Gaps:** no test anywhere asserts the FTS triggers keep `messages_fts` in sync with `messages` (the trigger contract that F-8 depends on); no test exercises concurrent access from two connections, which is the entire F-1 failure mode; no test asserts `UpdateBody` errors on 0 rows affected (F-3); no test covers `ListConversationsByFolder` / `GetConversation` result correctness, only the store's other paths.

---

## 8. Rollback & upgrade safety

**What is correct:**
- Each migration runs in its own transaction with `defer tx.Rollback()` (`database.go:216-233`), so a failed migration leaves the schema at the previous version and `migrations` un-updated. Multi-statement migration SQL works on `modernc.org/sqlite` — confirmed by the 2 green re-migration tests and by the 41/41 clean application in the scratch DB.
- `ErrSchemaTooNew` (`database.go:159-171, 200-201`) is a typed error with a real user-facing message that points at `docs/SQL_ROLLBACK.md`. Downgrade protection exists and works.
- The v39→v30 rollback script is well-built: it wraps everything in `BEGIN TRANSACTION … COMMIT` (lines 106, 247), reconstructs the legacy `contacts`/`carddav_contacts` shape by JOIN projection rather than requiring an external dump, and — critically — ends with `DELETE FROM migrations WHERE version >= 31` (line 242) so the version marker matches the new schema.
- `os.MkdirAll(dir, 0700)` + `os.Chmod(path, 0600)` keep the mail store owner-only.

**What is not:**
- **Coverage stops at v39.** Current version is v41. See F-20.
- **v29 has no rollback at all.** It drops `oauth_tokens`; reversing it means re-deriving the old single-config PK, which no script does.
- **v31 is destructive and only partially guarded.** `DROP TABLE contacts; DROP TABLE carddav_contacts;` (`migrations.go:1018-1019`) discards any row the backfill JOIN did not match. It runs inside the migration transaction so a crash rolls back, but a *successful* run with a lossy JOIN is unrecoverable without a pre-migration backup. `docs/SQL_ROLLBACK.md:12-14` acknowledges loss is inherent but does not call out v31 specifically as a hard-drop migration.
- **The rollback script is not re-runnable against the current schema** for the reason stated in F-20.
- **The downgrade simulation in the test suite has already gone stale** (F-11), which is the same class of bug as F-20 — hand-maintained lists of what a migration touched.

---

## 9. Summary of P0/P1

**P0 (4)**
1. `internal/database/database.go:60` — missing `_txlock=immediate`; deferred-tx read→write upgrade fails with `SQLITE_BUSY_SNAPSHOT` in 0 ms regardless of `busy_timeout`. Live hazard at `internal/carddav/store.go:742`→`:766`.
2. `internal/sync/messages.go:97` — `DeleteByFolder` destroys the folder before the resync is durable. Unrecoverable message loss on any interruption.
3. `internal/sync/fetch.go:88`/`:95`/`:102` — three untransacted writes, and `UpdateBody` (`internal/message/store.go:877`) does not check `RowsAffected`, so body loss is silent.
4. `internal/message/store.go:1558-1560` — `REPLACE(REPLACE(...))` on three columns makes the conversation-open query scan the entire account.

**P1 (6)**
5. `internal/database/migrations.go` v14 — FTS `unicode61` tokenizer; CJK search returns zero results (measured).
6. `internal/database/database.go:60` (v14 triggers) — flag-only UPDATEs churn the FTS index; measured 5.4× write cost.
7. `internal/message/store.go:1300` — `GROUP BY COALESCE(thread_id,id)` cannot use an index; full folder scan + 2 temp B-trees per page.
8. `internal/message/store.go:1038`, `:286`, `internal/draft/store.go:268` — missing composite indexes `(account_id,date)`, `(folder_id,is_read,id)`, `(account_id,sync_status)`.
9. `internal/database/database.go` — no `VACUUM`, no `journal_size_limit`, no `wal_autocheckpoint`; `PASSIVE` checkpoint never truncates.
10. `internal/database/database_test.go:190`, `:367` — persistence test suite red on `main`; the downgrade column list is stale (missing v40/v41).
