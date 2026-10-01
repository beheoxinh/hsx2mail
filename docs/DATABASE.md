# DATABASE — canonical schema reference

**Status: canonical.** This file is the single source of truth for the Hsx2Mail SQLite
schema. Any other document that describes tables, indexes, PRAGMAs or migrations is
derived from this one; if they disagree, this file wins.

Every claim below is traceable to a file:line in the repository or to a migration
version. Statements that could not be verified are marked **[unverified]** and should
not be relied on.

| Fact | Source |
|---|---|
| Migration ledger, v1..v42 | `internal/database/migrations.go` |
| DSN + PRAGMAs, pool sizing, checkpoint | `internal/database/database.go` |
| Runtime-added column `attachments.content` | `internal/message/attachment_store.go:26-48` |
| Per-extension `ext_kv` | `internal/extensions/store.go:98` |
| DB / staging / extension paths | `internal/platform/paths.go:139-180` |

The table, column and index inventories in this file are **generated** by
`tools/db/schemadump` + `tools/db/gen-database-doc.py`, which run the real `Migrate()`
against an empty database and read `sqlite_master`, `PRAGMA table_info` and
`PRAGMA foreign_key_list`. They cannot drift from the code unless someone edits a
migration without re-running the scripts. Regenerate after any migration change — see
[§10 Regenerating and verifying this document](#10-regenerating-and-verifying-this-document).

---

## 1. File locations

The main database is one file plus its WAL sidecars. `platform.GetPaths` resolves the
directories; `Paths.DatabasePath()` appends the filename (`internal/platform/paths.go:139-142`).

| Platform | Data directory | Main DB | Staging attachments | Extension DBs |
|---|---|---|---|---|
| Linux/BSD | `$XDG_DATA_HOME/hsx2mail` (default `~/.local/share/hsx2mail`) | `hsx2mail.db` | `attachments/staging/` | `extensions/<name>/data.db` |
| macOS | `~/Library/Application Support/Hsx2Mail` | `hsx2mail.db` | `attachments/staging/` | `extensions/<name>/data.db` |
| Windows | `%AppData%\Hsx2Mail` (Roaming) | `hsx2mail.db` | `attachments/staging/` | `extensions/<name>/data.db` |

Sources: `internal/platform/paths.go:10` (`appName = "hsx2mail"`), `:42-51` (XDG),
`:71-72` (macOS), `:104-106` (Windows), `:139-142` (`DatabasePath`),
`:174-176` (`AttachmentStagingPath`), `:178-180` (`ExtensionsDir`).

There is a second, unrelated file `contacts.db` (`internal/platform/paths.go:144-147`).
It is **not** part of the schema documented here; unified contacts live in the main DB
since v31.

Three files make up one consistent database:

```
hsx2mail.db        main database file
hsx2mail.db-wal    write-ahead log   (exists while WAL mode is active)
hsx2mail.db-shm    shared-memory index (exists while WAL mode is active)
```

`Open` sets the DB file to `0600` and creates the parent directory with `0700`
(`internal/database/database.go:49-53`, `:85-90`).

---

## 2. Connection and PRAGMA configuration

All PRAGMAs are attached to the DSN, not issued as `PRAGMA` statements. This is
deliberate: `database/sql` opens connections lazily from a pool, and PRAGMAs are
per-connection, so a statement issued once at startup would leave later pooled
connections unconfigured (`internal/database/database.go:55-59`).

DSN, verbatim from `internal/database/database.go:67`:

```go
dsn := fmt.Sprintf("file:%s?_txlock=immediate"+
    "&_pragma=busy_timeout(30000)"+
    "&_pragma=journal_mode(WAL)"+
    "&_pragma=synchronous(NORMAL)"+
    "&_pragma=foreign_keys(ON)"+
    "&_pragma=cache_size(-64000)"+
    "&_pragma=wal_autocheckpoint(1000)"+
    "&_pragma=journal_size_limit(67108864)", path)
```

(Shown wrapped; the source is one line.)

| Setting | Value | Why |
|---|---|---|
| `journal_mode` | `WAL` | Readers do not block the single writer. Required for IDLE-driven background sync to coexist with UI reads. |
| `busy_timeout` | `30000` (30 s) | A writer that arrives while a read txn is open waits instead of returning `SQLITE_BUSY`. |
| `_txlock=immediate` | `immediate` | Makes every `BEGIN` take the write lock up front. Without it modernc/sqlite issues a plain `DEFERRED` `BEGIN`, so a txn that starts as a read and later upgrades to a write can fail with `SQLITE_BUSY_SNAPSHOT`; `busy_timeout` does **not** help there because SQLite refuses to block a lock upgrade that would invalidate a snapshot another connection holds. This surfaced as "database is locked" whenever sync (writer) and UI reads overlapped. (`internal/database/database.go:60-66`) |
| `synchronous` | `NORMAL` | With WAL, `NORMAL` is durable across process crashes; only an OS/power crash can lose the last commits. Chosen over `FULL` for sync throughput. |
| `foreign_keys` | `ON` | Off by default in SQLite. Required — 24 foreign keys are declared and relied on (§5). |
| `cache_size` | `-64000` (64 MB) | Negative = KiB rather than pages. Bounds per-connection page cache. |
| `wal_autocheckpoint` | `1000` (pages) | SQLite's own automatic checkpoint. |
| `journal_size_limit` | `67108864` (64 MB) | Caps how large the `-wal` file may grow after a checkpoint. |

### Connection pool

`internal/database/database.go:19-37`, `:73-77`:

| Constant | Value | Notes |
|---|---|---|
| `MaxOpenConns` | `12` | Modest ceiling. WAL permits only one writer; more connections just add lock contention. |
| `BaseIdleConns` | `3` | Initial `SetMaxIdleConns` at `Open`. |
| `MaxIdleConns` | `6` | Ceiling after scaling. |
| `IdleConnsPerAccount` | `1` | Added per configured account. |
| `CheckpointInterval` | `5 * time.Minute` | Ticker period for `StartCheckpointRoutine`. |

`UpdateIdleConns(numAccounts)` (`:99-102`) sets
`min(BaseIdleConns + numAccounts*IdleConnsPerAccount, MaxIdleConns)`. It is called when
accounts are added or removed.

### WAL maintenance

`StartCheckpointRoutine(ctx)` (`internal/database/database.go:155-176`) runs on a
5-minute ticker and, each tick:

1. `Checkpoint()` → `PRAGMA wal_checkpoint(TRUNCATE)` (`:135-140`). `TRUNCATE`, not
   `PASSIVE`: `PASSIVE` copies what it can but leaves `-wal` at its high-water size, so
   the file grew monotonically for the life of the process; `TRUNCATE` additionally
   resets it to zero bytes. Safe from the idle ticker because a busy reader only turns
   the checkpoint into a no-op for that pass (`:129-134`).
2. `Optimize()` → `PRAGMA optimize` (`:145-152`), which refreshes the query planner's
   sqlite_stat1 data.

No `VACUUM` runs on a schedule; the app never calls one automatically.

### Multi-process access

The detached composer is a **second OS process** that opens the same file
(`app/detached_composer.go:150-158`), so the DSN pragmas and `_txlock=immediate` apply to
two independent connection pools against one database. See §6.

---

## 3. Migration inventory (v1..v42)

Source: `internal/database/migrations.go`. "Line" is the `Version: N,` line in that
file. Every migration is applied inside its own transaction, with the version recorded
in the same transaction (§6).

| v | Line | What it does | Objects |
|---|---|---|---|
<!--GEN:MIGRATIONS-->
| 1 | 12 | Baseline: accounts, identities, folders, messages, attachments, drafts, and their indexes | new table `accounts`, `identities`, `folders`, `messages` (+2 more); index `idx_identities_account`, `idx_folders_account`, `idx_folders_parent`, `idx_messages_account` (+7 more) |
| 2 | 187 | Add encrypted password column for fallback credential storage | `ALTER TABLE accounts` |
| 3 | 195 | Add references column for threading (stores References header as JSON array) | index `idx_messages_in_reply_to`; `ALTER TABLE messages` |
| 4 | 205 | Add sync-related fields to drafts table for local-first draft saving | index `idx_drafts_sync_status`; `ALTER TABLE drafts` |
| 5 | 232 | Global settings table for application preferences | new table `IF NOT EXISTS settings`; `INSERT INTO settings`, `ALTER TABLE accounts`, `ALTER TABLE messages` |
| 6 | 257 | Contact sources (CardDAV servers/accounts) + per-source address books | new table `contact_sources`, `contact_source_addressbooks`, `carddav_contacts`; index `idx_contact_source_addressbooks_source`, `idx_carddav_contacts_addressbook`, `idx_carddav_contacts_email` |
| 7 | 305 | Add encrypted password column to contact_sources for fallback credential storage | `ALTER TABLE contact_sources` |
| 8 | 313 | Add folder mapping columns to accounts table | `ALTER TABLE accounts` |
| 9 | 328 | OAuth token metadata table | new table `oauth_tokens`; `ALTER TABLE accounts` |
| 10 | 349 | Incremental sync support: fetch headers first, bodies later | index `idx_messages_body_fetched`; `ALTER TABLE messages` |
| 11 | 362 | Add sync_interval column to accounts for automatic email polling | `ALTER TABLE accounts` |
| 12 | 372 | Add color column to accounts for visual identification in unified inbox | `ALTER TABLE accounts` |
| 13 | 380 | App state table for persisting UI state across sessions | new table `IF NOT EXISTS app_state` |
| 14 | 392 | Create FTS5 virtual table for full-text search | new table `fts_index_status`; `INSERT INTO messages_fts` |
| 15 | 442 | Add signature settings to identities table | `ALTER TABLE identities` |
| 16 | 466 | Image allowlist table for "Always Load" remote images feature | new table `IF NOT EXISTS image_allowlist`; index `idx_image_allowlist_type_value` |
| 17 | 483 | Add account_id to contact_sources for linking OAuth contact sources to email accounts | new table `contact_source_oauth`; index `idx_contact_sources_account`; `ALTER TABLE contact_sources` |
| 18 | 509 | Trusted certificates table for certificate trust-on-first-use (TOFU) | new table `IF NOT EXISTS trusted_certificates` |
| 19 | 526 | S/MIME user certificates (imported PKCS#12 with private key in keyring/encrypted fallback) | new table `IF NOT EXISTS smime_certificates`, `IF NOT EXISTS smime_sender_certs`; index `idx_smime_certificates_account`, `idx_smime_certificates_email`, `idx_smime_sender_certs_email`, `idx_smime_sender_certs_fingerprint`; `ALTER TABLE messages`, `ALTER TABLE accounts` |
| 20 | 577 | Raw S/MIME body for on-view verification/decryption | `ALTER TABLE messages`, `ALTER TABLE accounts` |
| 21 | 590 | Whether the draft body is encrypted (encrypt-to-self) | `ALTER TABLE drafts` |
| 22 | 600 | Per-message S/MIME sign preference (preserved across draft save/load) | `ALTER TABLE drafts` |
| 23 | 607 | Store attachment data alongside draft body (inline images + regular attachments) | `ALTER TABLE drafts` |
| 24 | 616 | PGP keypairs and per-sender key associations | new table `IF NOT EXISTS pgp_keys`, `IF NOT EXISTS pgp_sender_keys`; index `idx_pgp_keys_account`, `idx_pgp_keys_email`, `idx_pgp_keys_fingerprint`, `idx_pgp_sender_keys_email` (+1 more); `ALTER TABLE messages`, `ALTER TABLE accounts`, `ALTER TABLE drafts` |
| 25 | 677 | PGP keyserver (HKP) configuration | new table `IF NOT EXISTS pgp_keyservers` |
| 26 | 694 | accounts.sync_all_folders, folders.subscribed | `ALTER TABLE accounts`, `ALTER TABLE folders` |
| 27 | 701 | accounts.sync_folders_enabled | — |
| 28 | 705 | accounts.shared_mailbox_parent_id | — |
| 29 | 709 | Extension system Phase 1: multi-config OAuth support. | new table `oauth_tokens_new`; `ALTER TABLE oauth_tokens`, `UPDATE oauth_tokens`, `INSERT INTO oauth_tokens_new`, `DROP TABLE oauth_tokens;` (+3 more) |
| 30 | 753 | Phase 2b: write capability flag for contact sources. | `ALTER TABLE contact_sources` |
| 31 | 771 | Phase 2b.2.a: Unified contact-record schema. | new table `IF NOT EXISTS contacts`, `contact_records`, `contact_emails`, `contact_phones` (+5 more); index `idx_contact_records_source`, `idx_contact_records_source_kind`, `idx_contact_records_source_ref`, `idx_contact_emails_email` (+4 more); `INSERT INTO contact_records`, `INSERT INTO carddav_record_state`, `DROP TABLE contacts;`, `DROP TABLE carddav_contacts;` |
| 32 | 1023 | Phase 2b.2 follow-up: rewrite local contact_records IDs from the | `INSERT INTO _migration_32_idmap`, `UPDATE contact_records`, `UPDATE contact_emails`, `UPDATE contact_phones` (+5 more) |
| 33 | 1105 | Phase 2b.2.b.1 follow-up: add the FK that should have been on | new table `carddav_record_state_new`; index `idx_carddav_record_state_addressbook`; `INSERT INTO carddav_record_state_new`, `DROP TABLE carddav_record_state;`, `ALTER TABLE carddav_record_state_new` |
| 34 | 1176 | Phase 2b.2.b.2: first-class PHOTO field support on contact_records. | `ALTER TABLE contact_records` |
| 35 | 1199 | Phase 1B of the Calendar extension introduces the shared | new table `IF NOT EXISTS extension_secrets`; index `idx_extension_secrets_ext` |
| 36 | 1229 | Per-(account, client_config) encrypted fallback for OAuth tokens. | `ALTER TABLE oauth_tokens` |
| 37 | 1252 | v0.3.0: "No outgoing server" + separate SMTP credentials. | `ALTER TABLE accounts` |
| 38 | 1276 | v0.3.0: "Reply/Forward with" identity preference for receive-only | `ALTER TABLE accounts` |
| 39 | 1290 | Persistent flag set when a body fetch+parse produced no usable content | `ALTER TABLE messages` |
| 40 | 1305 | Stable OAuth account identity ("<tid>:<oid>" for Microsoft) captured | `ALTER TABLE accounts` |
| 41 | 1319 | Per-account background polling interval for secondary folders | `ALTER TABLE accounts` |
| 42 | 1331 | Index-only migrations for the Phase 2 sync/data-layer work. No | index `idx_messages_needs_body`, `idx_messages_account_date`, `idx_messages_folder_thread_date`, `idx_messages_folder_conv` (+4 more) |
<!--/GEN:MIGRATIONS-->
| **42** | 1331 | Current schema version | — |

**No migration exists above v42.** `ErrSchemaTooNew` (§7) fires if the database reports a
higher version.

### Migrations with no comment block

v26, v27 and v28 in `migrations.go` carry no SQL comment; their effect is visible in the
statements themselves:

| v | Statements |
|---|---|
| 26 | `ALTER TABLE accounts ADD COLUMN sync_all_folders …`; `ALTER TABLE folders ADD COLUMN subscribed …` |
| 27 | `ALTER TABLE accounts ADD COLUMN sync_folders_enabled …` |
| 28 | `ALTER TABLE accounts ADD COLUMN shared_mailbox_parent_id …` |

---

## 4. Table inventory

30 application tables in the main database, plus the FTS5 virtual table
`messages_fts` with its four shadow tables, plus `sqlite_sequence` (created implicitly
by `AUTOINCREMENT` on `image_allowlist` and `pgp_keyservers`).

"Created" is the migration that issues the `CREATE TABLE`.

| Table | Created | Purpose |
|---|---|---|
| `accounts` | v1 | IMAP/SMTP endpoint, auth type, sync policy, special-folder path overrides, colour, shared-mailbox parent. |
| `identities` | v1 | Per-account sender identities / aliases. |
| `folders` | v1 | IMAP folder tree: path, type, UIDVALIDITY, cached counts, per-folder sync state. `parent_id` is self-referential. |
| `messages` | v1 | Message headers and bodies, threading keys, body-fetch bookkeeping. Largest table. |
| `attachments` | v1 | Attachment **metadata** only (filename, MIME type, size, `local_path`). Bytes live on disk or in the runtime-added `content` column — see §8. |
| `drafts` | v1 | Local draft records, IMAP draft UID, per-folder sync status, encryption/signing flags. |
| `settings` | v5 | Flat key/value application settings. |
| `oauth_tokens` | v9 | OAuth token sets per account. Primary key was widened from `(account_id)` to `(account_id, client_config_id)` in v29 using the SQLite swap-table dance. |
| `contact_sources` | v6 | CardDAV / Google / Microsoft contact source configuration. |
| `contact_source_addressbooks` | v6 | Address-book paths belonging to a contact source. |
| `contact_source_oauth` | v17 | OAuth tokens for a non-mail contact source. |
| `app_state` | v13 | UI state persistence (pane widths, selected folder, …). |
| `messages_fts` | v14 | **Virtual** FTS5 index over `messages` (external-content). See §7. |
| `fts_index_status` | v14 | Per-folder FTS indexing watermark / progress state. |
| `image_allowlist` | v16 | Per-sender allowlist for remote images. Has an `INTEGER PRIMARY KEY AUTOINCREMENT` `id`. |
| `trusted_certificates` | v18 | TOFU certificate fingerprints. |
| `smime_certificates` | v19 | S/MIME certificates held per account. |
| `smime_sender_certs` | v19 | Sender → S/MIME certificate association. |
| `pgp_keys` | v24 | PGP keypairs held per account. |
| `pgp_sender_keys` | v24 | Sender → PGP key association. |
| `pgp_keyservers` | v25 | HKP keyserver configuration. Has an `INTEGER PRIMARY KEY AUTOINCREMENT` `id`. |
| `contact_records` | v31 | Unified, vCard-shaped contact record. Replaced the legacy autocomplete `contacts` table for durable storage. |
| `contact_emails` | v31 | vCard `EMAIL` properties, one row per address, with primary flag and usage counters. |
| `contact_addresses` | v31 | vCard `ADR` properties. |
| `contact_phones` | v31 | vCard `TEL` properties. |
| `contact_impps` | v31 | vCard `IMPP` properties (instant-messaging handles). |
| `contact_urls` | v31 | vCard `URL` properties. |
| `contact_categories` | v31 | vCard `CATEGORIES` values. |
| `carddav_record_state` | v31 | Per-record CardDAV sync bookkeeping (href, etag, last-sync). |
| `extension_secrets` | v35 | Per-extension secrets, stored encrypted. Created by core, not by the extension. |
| `migrations` | — | Schema version ledger. Not created by a migration: `Migrate()` issues `CREATE TABLE IF NOT EXISTS migrations (version INTEGER PRIMARY KEY, applied_at DATETIME)` itself (`internal/database/database.go:201-208`). |
| `sqlite_sequence` | — | SQLite bookkeeping for `AUTOINCREMENT`. Not application-owned; do not touch. |

### FTS5 shadow tables

`messages_fts` is an external-content FTS5 table (`content='messages'`,
`content_rowid='rowid'`). SQLite materialises four shadow tables that are **not**
application tables and must never be written to directly:

`messages_fts_data`, `messages_fts_idx`, `messages_fts_docsize`, `messages_fts_config`.

### Full column list

<!--GEN:COLUMNS-->
- **`accounts`** — id:TEXT, name:TEXT, email:TEXT, imap_host:TEXT, imap_port:INTEGER, imap_security:TEXT, smtp_host:TEXT, smtp_port:INTEGER, smtp_security:TEXT, auth_type:TEXT, username:TEXT, enabled:INTEGER, order_index:INTEGER, sync_period_days:INTEGER, created_at:DATETIME, updated_at:DATETIME, encrypted_password:TEXT, read_receipt_request_policy:TEXT, sent_folder_path:TEXT, drafts_folder_path:TEXT, trash_folder_path:TEXT, spam_folder_path:TEXT, archive_folder_path:TEXT, all_mail_folder_path:TEXT, starred_folder_path:TEXT, encrypted_access_token:TEXT, encrypted_refresh_token:TEXT, sync_interval:INTEGER, color:TEXT, smime_sign_policy:TEXT, smime_default_cert_id:TEXT, smime_encrypt_policy:TEXT, pgp_sign_policy:TEXT, pgp_encrypt_policy:TEXT, pgp_default_key_id:TEXT, sync_all_folders:INTEGER, sync_folders_enabled:INTEGER, shared_mailbox_parent_id:TEXT, no_outgoing_server:INTEGER, smtp_username:TEXT, encrypted_smtp_password:TEXT, reply_forward_identity_id:TEXT, oauth_stable_id:TEXT, secondary_sync_interval:INTEGER
- **`app_state`** — key:TEXT, value:TEXT, updated_at:DATETIME
- **`attachments`** — id:TEXT, message_id:TEXT, filename:TEXT, content_type:TEXT, size:INTEGER, content_id:TEXT, is_inline:INTEGER, local_path:TEXT
- **`carddav_record_state`** — record_id:TEXT, addressbook_id:TEXT, href:TEXT, etag:TEXT, synced_at:DATETIME
- **`contact_addresses`** — record_id:TEXT, addr_type:TEXT, street:TEXT, city:TEXT, region:TEXT, postcode:TEXT, country:TEXT, idx:INTEGER
- **`contact_categories`** — record_id:TEXT, category:TEXT
- **`contact_emails`** — record_id:TEXT, email:TEXT, email_type:TEXT, is_primary:INTEGER, send_count:INTEGER, last_used:DATETIME, name_overridden:INTEGER
- **`contact_impps`** — record_id:TEXT, handle:TEXT, impp_type:TEXT
- **`contact_phones`** — record_id:TEXT, number:TEXT, phone_type:TEXT, is_primary:INTEGER
- **`contact_records`** — id:TEXT, source:TEXT, kind:TEXT, source_ref:TEXT, fn:TEXT, n_given:TEXT, n_family:TEXT, org:TEXT, title:TEXT, note:TEXT, bday:TEXT, nickname:TEXT, vcard_raw:TEXT, created_at:DATETIME, updated_at:DATETIME, photo_data:TEXT, photo_media_type:TEXT, photo_url:TEXT
- **`contact_source_addressbooks`** — id:TEXT, source_id:TEXT, path:TEXT, name:TEXT, enabled:INTEGER, sync_token:TEXT, last_synced_at:DATETIME
- **`contact_source_oauth`** — source_id:TEXT, provider:TEXT, expires_at:DATETIME, scopes:TEXT, created_at:DATETIME, updated_at:DATETIME, client_config_id:TEXT
- **`contact_sources`** — id:TEXT, name:TEXT, type:TEXT, url:TEXT, username:TEXT, enabled:INTEGER, sync_interval:INTEGER, last_synced_at:DATETIME, last_error:TEXT, last_error_at:DATETIME, created_at:DATETIME, encrypted_password:TEXT, account_id:TEXT, encrypted_access_token:TEXT, encrypted_refresh_token:TEXT, writable:INTEGER
- **`contact_urls`** — record_id:TEXT, url:TEXT, url_type:TEXT
- **`drafts`** — id:TEXT, account_id:TEXT, to_list:TEXT, cc_list:TEXT, bcc_list:TEXT, subject:TEXT, body_html:TEXT, body_text:TEXT, in_reply_to_id:TEXT, reply_type:TEXT, identity_id:TEXT, created_at:DATETIME, updated_at:DATETIME, sync_status:TEXT, imap_uid:INTEGER, folder_id:TEXT, references_list:TEXT, last_sync_attempt:DATETIME, sync_error:TEXT, encrypted:INTEGER, encrypted_body:BLOB, sign_message:INTEGER, attachments_data:BLOB, pgp_sign_message:INTEGER, pgp_encrypted:INTEGER, pgp_encrypted_body:BLOB
- **`extension_secrets`** — extension:TEXT, key:TEXT, encrypted_value:TEXT, created_at:INTEGER
- **`folders`** — id:TEXT, account_id:TEXT, name:TEXT, path:TEXT, folder_type:TEXT, parent_id:TEXT, uid_validity:INTEGER, uid_next:INTEGER, highest_mod_seq:INTEGER, total_count:INTEGER, unread_count:INTEGER, last_sync:DATETIME, subscribed:INTEGER
- **`fts_index_status`** — folder_id:TEXT, indexed_count:INTEGER, total_count:INTEGER, is_complete:INTEGER, last_indexed_at:DATETIME
- **`identities`** — id:TEXT, account_id:TEXT, email:TEXT, name:TEXT, is_default:INTEGER, signature_html:TEXT, signature_text:TEXT, order_index:INTEGER, created_at:DATETIME, signature_enabled:INTEGER, signature_for_new:INTEGER, signature_for_reply:INTEGER, signature_for_forward:INTEGER, signature_placement:TEXT, signature_separator:INTEGER, updated_at:DATETIME
- **`image_allowlist`** — id:INTEGER, type:TEXT, value:TEXT, created_at:DATETIME
- **`messages`** — id:TEXT, account_id:TEXT, folder_id:TEXT, uid:INTEGER, message_id:TEXT, in_reply_to:TEXT, thread_id:TEXT, subject:TEXT, from_name:TEXT, from_email:TEXT, to_list:TEXT, cc_list:TEXT, bcc_list:TEXT, reply_to:TEXT, date:DATETIME, snippet:TEXT, is_read:INTEGER, is_starred:INTEGER, is_answered:INTEGER, is_forwarded:INTEGER, is_draft:INTEGER, is_deleted:INTEGER, size:INTEGER, has_attachments:INTEGER, body_text:TEXT, body_html:TEXT, received_at:DATETIME, references_list:TEXT, read_receipt_to:TEXT, read_receipt_handled:INTEGER, body_fetched:INTEGER, smime_status:TEXT, smime_signer_email:TEXT, smime_signer_subject:TEXT, smime_raw_body:BLOB, smime_encrypted:INTEGER, pgp_status:TEXT, pgp_signer_email:TEXT, pgp_signer_key_id:TEXT, pgp_raw_body:BLOB, pgp_encrypted:INTEGER, body_failed:INTEGER
- **`migrations`** — version:INTEGER, applied_at:DATETIME
- **`oauth_tokens`** — account_id:TEXT, client_config_id:TEXT, provider:TEXT, expires_at:DATETIME, scopes:TEXT, created_at:DATETIME, updated_at:DATETIME, encrypted_access_token:TEXT, encrypted_refresh_token:TEXT
- **`pgp_keys`** — id:TEXT, account_id:TEXT, email:TEXT, key_id:TEXT, fingerprint:TEXT, user_id:TEXT, algorithm:TEXT, key_size:INTEGER, created_at_key:DATETIME, expires_at_key:DATETIME, public_key_armored:TEXT, encrypted_private_key:TEXT, is_default:INTEGER, created_at:DATETIME
- **`pgp_keyservers`** — id:INTEGER, url:TEXT, order_index:INTEGER, created_at:DATETIME
- **`pgp_sender_keys`** — id:TEXT, email:TEXT, key_id:TEXT, fingerprint:TEXT, user_id:TEXT, algorithm:TEXT, key_size:INTEGER, created_at_key:DATETIME, expires_at_key:DATETIME, public_key_armored:TEXT, source:TEXT, collected_at:DATETIME, last_seen_at:DATETIME
- **`settings`** — key:TEXT, value:TEXT
- **`smime_certificates`** — id:TEXT, account_id:TEXT, email:TEXT, subject:TEXT, issuer:TEXT, serial_number:TEXT, fingerprint:TEXT, not_before:DATETIME, not_after:DATETIME, cert_chain_pem:TEXT, encrypted_private_key:TEXT, is_default:INTEGER, created_at:DATETIME
- **`smime_sender_certs`** — id:TEXT, email:TEXT, subject:TEXT, issuer:TEXT, serial_number:TEXT, fingerprint:TEXT, not_before:DATETIME, not_after:DATETIME, cert_pem:TEXT, collected_at:DATETIME, last_seen_at:DATETIME
- **`trusted_certificates`** — id:TEXT, fingerprint:TEXT, host:TEXT, subject:TEXT, issuer:TEXT, not_before:DATETIME, not_after:DATETIME, accepted_at:DATETIME
<!--/GEN:COLUMNS-->

---

## 5. Foreign keys

24 foreign keys are declared. Enforcement depends on `foreign_keys(ON)` being set per
connection — see §2.

| Child | Parent | On delete |
|---|---|---|
| `attachments.message_id` | `messages(id)` | CASCADE |
| `carddav_record_state.record_id` | `contact_records(id)` | CASCADE |
| `carddav_record_state.addressbook_id` | `contact_source_addressbooks(id)` | CASCADE |
| `contact_addresses.record_id` | `contact_records(id)` | CASCADE |
| `contact_categories.record_id` | `contact_records(id)` | CASCADE |
| `contact_emails.record_id` | `contact_records(id)` | CASCADE |
| `contact_impps.record_id` | `contact_records(id)` | CASCADE |
| `contact_phones.record_id` | `contact_records(id)` | CASCADE |
| `contact_source_addressbooks.source_id` | `contact_sources(id)` | CASCADE |
| `contact_source_oauth.source_id` | `contact_sources(id)` | CASCADE |
| `contact_sources.account_id` | `accounts(id)` | CASCADE |
| `contact_urls.record_id` | `contact_records(id)` | CASCADE |
| `drafts.account_id` | `accounts(id)` | CASCADE |
| `drafts.folder_id` | `folders(id)` | **SET NULL** |
| `drafts.identity_id` | `identities(id)` | **NO ACTION** |
| `folders.account_id` | `accounts(id)` | CASCADE |
| `folders.parent_id` | `folders(id)` | CASCADE |
| `fts_index_status.folder_id` | `folders(id)` | CASCADE |
| `identities.account_id` | `accounts(id)` | CASCADE |
| `messages.account_id` | `accounts(id)` | CASCADE |
| `messages.folder_id` | `folders(id)` | CASCADE |
| `oauth_tokens.account_id` | `accounts(id)` | CASCADE |
| `pgp_keys.account_id` | `accounts(id)` | CASCADE |
| `smime_certificates.account_id` | `accounts(id)` | CASCADE |

Two behaviours matter when reasoning about deletes:

- `folders.parent_id` is `ON DELETE CASCADE`, so removing a parent folder removes its
  whole subtree in one statement — and via `messages.folder_id`, the messages in it.
- `drafts.folder_id` is `SET NULL`, so deleting a folder leaves the draft row and
  unlinks it. `drafts.identity_id` is `NO ACTION`, so deleting an identity that a draft
  still references **fails**; the caller must clear the draft's identity first.

---

## 6. Transaction boundaries

| Scope | Boundary | Source |
|---|---|---|
| Each migration | Own transaction. Version marker is re-checked *inside* the transaction, the SQL runs, and the version row is inserted in the same transaction, then `tx.Commit()`. | `internal/database/database.go:241-286` |
| Multi-statement store writes | Explicit `db.Begin()` in `internal/account/store.go:511,736`, `internal/message/store.go`, `internal/message/attachment_store.go`, `internal/message/fts.go:178` (`BeginTx`), `internal/pgp/store.go`, `internal/smime/store.go`, `internal/contact/store.go`, `internal/carddav/store.go`, `internal/extensions/store.go`. | those files |
| Everything else | Implicit single-statement transactions. | — |

The re-check inside the migration transaction exists because the detached composer is a
second process on the same file: `Migrate()` reads the current version before opening the
transaction, so a concurrent composer can apply the same migration first. With
`_txlock=immediate` the two transactions serialise, and without the re-check the loser
would re-run the `ALTER TABLE` and die with "duplicate column name", taking the composer
window down with it (`internal/database/database.go:248-256`).

`PRAGMA foreign_keys`, `journal_mode` and several others are **not** transactional-safe
inside a transaction; they are set once per connection via the DSN instead.

---

## 7. FTS and the schema-version guard

### Full-text search

`messages_fts` is an external-content FTS5 table over `messages` with the columns
`subject, from_name, from_email, to_list, cc_list, snippet, body_text` and
`content='messages'`, `content_rowid='rowid'` (created in v14,
`internal/database/migrations.go:397`).

Three triggers keep it in sync with `messages` (same migration):

| Trigger | Fires on |
|---|---|
| `messages_fts_insert` | AFTER INSERT ON `messages` |
| `messages_fts_delete` | AFTER DELETE ON `messages` |
| `messages_fts_update` | AFTER UPDATE ON `messages` |

Because it is external-content, the FTS table stores no copy of the text — it stores
only the index. `DELETE FROM messages_fts(messages_fts, rowid, …)` form in the triggers
is the documented FTS5 idiom for an external-content delete.

`fts_index_status` (v14) tracks per-folder indexing progress so a restart resumes rather
than re-indexing.

### Schema-version guard

`Migrate()` reads `COALESCE(MAX(version), 0)` from `migrations`. If that value is
greater than the highest version this build knows, it refuses to open and returns
`*ErrSchemaTooNew` (`internal/database/database.go:189-196`, `:210-240`). The guard
exists because the DB may carry migrations the running build cannot interpret; querying
those columns would likely corrupt autocomplete state or crash.

`ErrSchemaTooNew.Error()` points the user at `docs/SQL_ROLLBACK.md`.

---

## 8. Runtime-added columns (not migrations)

One column in the shipped schema is **not** created by any migration:

| Column | Added by | Notes |
|---|---|---|
| `attachments.content BLOB` | `AttachmentStore.ensureContentColumn()` — `internal/message/attachment_store.go:26-48`, called from `NewAttachmentStore` (`:17-22`) | Checked with `SELECT COUNT(*) FROM pragma_table_info('attachments') WHERE name = 'content'`; if absent, `ALTER TABLE attachments ADD COLUMN content BLOB`. |

Consequences to keep in mind:

- A database migrated by `Migrate()` alone does **not** have this column. Any tool that
  diffs the schema after only running migrations will report `attachments` without
  `content`.
- It is added outside the migration ledger, so it has no `migrations` row and no
  rollback entry.
- Attachment bytes therefore live in one of two places: the `content` blob (inline
  attachments, for offline access) or a file referenced by `local_path`.

---

## 9. Per-extension databases

Extensions do **not** get tables in the main database. `extensions.OpenStore` opens
`<dataDir>/extensions/<name>/data.db`, applies the extension's own migration list, and
ensures the canonical `ext_kv` table exists **before** user migrations run, so KV is
available even for an extension that declares no tables
(`internal/extensions/store.go:39-52`, `:92-110`).

Each extension database has its own `migrations(version INTEGER PRIMARY KEY, applied_at
DATETIME)` ledger. The only extension-related table in the **main** database is
`extension_secrets` (v35), which core owns and extensions access through the keyring
abstraction.

---

## 10. Regenerating and verifying this document

The table inventory (§4), the column list, the index inventory (§11) and the migration
inventory (§3) are **generated**, not hand-written. Two scripts do it:

| Script | Role |
|---|---|
| `tools/db/schemadump/main.go` | Runs `database.Migrate()` against a throwaway database and prints `sqlite_master`, `PRAGMA table_info` and `PRAGMA foreign_key_list`. |
| `tools/db/gen-database-doc.py` | Parses `migrations.go` plus that dump and splices the results between the `<!--GEN:NAME-->` markers. |

Regenerate after any schema change, from the repository root:

```bash
go run ./tools/db/schemadump > /tmp/schema_raw.txt
python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt
git diff docs/DATABASE.md          # the diff IS the schema change, in doc form
```

Because the dump comes from executing the real `Migrate()`, the generated parts cannot
drift from the code. If `git diff` is empty, the schema did not change.

### Manual spot checks against a live database

```bash
DB=~/.local/share/hsx2mail/hsx2mail.db
sqlite3 "$DB" 'SELECT MAX(version) FROM migrations;'            # must be 42
sqlite3 "$DB" 'SELECT type,name FROM sqlite_master ORDER BY type,name;'
sqlite3 "$DB" "PRAGMA table_info('attachments');"               # shows the runtime-added content column
sqlite3 "$DB" "PRAGMA foreign_key_list('messages');"
sqlite3 "$DB" 'PRAGMA journal_mode; PRAGMA foreign_keys; PRAGMA busy_timeout;'
```

`PRAGMA journal_mode` must report `wal` and `PRAGMA foreign_keys` must report `1`. If
`foreign_keys` is `0` in a live session, that session did not receive the DSN pragmas —
see §2.

### Manual steps for each new migration

1. Add the migration to `internal/database/migrations.go`. Never edit an existing
   migration: the `migrations` ledger records only the version number, so an edited v30 is
   invisible to a database that already applied v30 and invisible to the ledger on the
   next run.
2. Add a `DESC_OVERRIDE` entry in `tools/db/gen-database-doc.py` if the migration's
   leading SQL comment is empty or a bare `X table` header.
3. Re-run the two commands above.
4. Update the **hand-written** sections a script cannot infer: §2 if a PRAGMA changed,
   §5 if the FK count changed (24 today), §6 if transaction boundaries changed, §8 if a
   new runtime-added column appeared, §12 if a retention rule changed.
5. Add rollback notes to `SQL_ROLLBACK.md` unless the migration is a bare `DROP INDEX`.

`make test` exercises the migration path against a fresh database; `make check` runs
build, vet, tests and linters.
---

## 11. Index inventory

42 explicitly created indexes, plus 37 `sqlite_autoindex_*` entries that SQLite creates
implicitly for `UNIQUE` and non-`INTEGER PRIMARY KEY` primary-key constraints — those are
not listed individually.
<!--GEN:INDEXES-->
| `idx_attachments_message` | `ON attachments(message_id)` |
| `idx_carddav_record_state_addressbook` | `ON carddav_record_state(addressbook_id)` |
| `idx_contact_addresses_record` | `ON contact_addresses(record_id)` |
| `idx_contact_emails_email` | `ON contact_emails(email)` |
| `idx_contact_emails_rank` | `ON contact_emails(send_count DESC, last_used DESC)` |
| `idx_contact_records_source` | `ON contact_records(source)` |
| `idx_contact_records_source_kind` | `ON contact_records(source, kind)` |
| `idx_contact_records_source_ref` | `ON contact_records(source_ref)` |
| `idx_contact_source_addressbooks_source` | `ON contact_source_addressbooks(source_id)` |
| `idx_contact_sources_account` | `ON contact_sources(account_id)` |
| `idx_drafts_account` | `ON drafts(account_id)` |
| `idx_drafts_sync_status` | `ON drafts(sync_status)` |
| `idx_extension_secrets_ext` | `ON extension_secrets(extension)` |
| `idx_folders_account` | `ON folders(account_id)` |
| `idx_folders_parent` | `ON folders(parent_id)` |
| `idx_identities_account` | `ON identities(account_id)` |
| `idx_image_allowlist_type_value` | `ON image_allowlist(type, value)` |
| `idx_messages_account` | `ON messages(account_id)` |
| `idx_messages_account_date` | `ON messages(account_id, date)` |
| `idx_messages_account_message_id` | `ON messages(account_id, message_id)` |
| `idx_messages_body_fetched` | `ON messages(folder_id, body_fetched)` |
| `idx_messages_date` | `ON messages(date DESC)` |
| `idx_messages_folder` | `ON messages(folder_id)` |
| `idx_messages_folder_conv` | `ON messages(folder_id, COALESCE(thread_id, id))` |
| `idx_messages_folder_thread_date` | `ON messages(folder_id, thread_id, date DESC)` |
| `idx_messages_in_reply_to` | `ON messages(in_reply_to)` |
| `idx_messages_in_reply_to_norm` | `ON messages(account_id, REPLACE(REPLACE(in_reply_to, '<', ''), '>', ''))` |
| `idx_messages_message_id` | `ON messages(message_id)` |
| `idx_messages_message_id_norm` | `ON messages(account_id, REPLACE(REPLACE(message_id, '<', ''), '>', ''))` |
| `idx_messages_needs_body` | `ON messages(folder_id, date DESC) WHERE body_fetched = 0 AND body_failed = 0` |
| `idx_messages_thread` | `ON messages(thread_id)` |
| `idx_messages_thread_norm` | `ON messages(account_id, REPLACE(REPLACE(COALESCE(thread_id, id), '<', ''), '>', ''))` |
| `idx_messages_unread` | `ON messages(folder_id, is_read) WHERE is_read = 0` |
| `idx_pgp_keys_account` | `ON pgp_keys(account_id)` |
| `idx_pgp_keys_email` | `ON pgp_keys(email)` |
| `idx_pgp_keys_fingerprint` | `ON pgp_keys(fingerprint)` |
| `idx_pgp_sender_keys_email` | `ON pgp_sender_keys(email)` |
| `idx_pgp_sender_keys_fingerprint` | `ON pgp_sender_keys(fingerprint)` |
| `idx_smime_certificates_account` | `ON smime_certificates(account_id)` |
| `idx_smime_certificates_email` | `ON smime_certificates(email)` |
| `idx_smime_sender_certs_email` | `ON smime_sender_certs(email)` |
| `idx_smime_sender_certs_fingerprint` | `ON smime_sender_certs(fingerprint)` |
<!--/GEN:INDEXES-->

### v42 — the current index-only migration

v42 adds **no** columns and **no** tables. Every object it creates is a pure accelerator
for a query that already existed, which is what makes it the one recent migration that
can be rolled back with a bare `DROP INDEX` (see `SQL_ROLLBACK.md`).

| Index | Definition | Serves |
|---|---|---|
| `idx_messages_needs_body` | `messages(folder_id, date DESC) WHERE body_fetched = 0 AND body_failed = 0` | Body-fetch candidate queue (`GetMessagesWithoutBody*`). The partial predicate matches the "never fetched and not permanently failed" branch exactly, so that branch is an index range scan already in date order instead of a folder scan plus a temp sort. The separate "fetched but empty" self-heal branch reads `body_text` and cannot be served by any index. |
| `idx_messages_account_date` | `messages(account_id, date)` | Account-wide date-range walks; retention prune. |
| `idx_messages_folder_thread_date` | `messages(folder_id, thread_id, date DESC)` | Conversation open and per-thread flag sync: folder, then thread, then chronological order. |
| `idx_messages_folder_conv` | `messages(folder_id, COALESCE(thread_id, id))` | Conversation list. List and count queries both `GROUP BY COALESCE(thread_id, id)`; an expression index on the same expression lets SQLite feed the `GROUP BY` from the index instead of building a temp B-tree per page. |
| `idx_messages_thread_norm` | `messages(account_id, REPLACE(REPLACE(thread_id, '<', ''), '>', ''))` | Thread lookup. Stored values keep their angle brackets (IMAP delivers them that way), so queries must strip them with `REPLACE()`; indexing the stripped form turns those comparisons into index seeks. |
| `idx_messages_message_id_norm` | `messages(account_id, REPLACE(REPLACE(message_id, '<', ''), '>', ''))` | Same normalisation for `Message-ID`. |
| `idx_messages_in_reply_to_norm` | `messages(account_id, REPLACE(REPLACE(in_reply_to, '<', ''), '>', ''))` | Batched `FindThreadID`: one lookup per reference in the batch. |
| `idx_messages_account_message_id` | `messages(account_id, message_id)` | Batched `FindThreadID`, keyed on `(account_id, message_id)`. |

---

## 12. Retention and cleanup rules

Three independent reclaim paths. None of them is time-based on message age alone; each
is driven by an explicit signal.

### 12.1 Message retention (folder-scoped, sync-period driven)

`MessageStore.DeleteOlderThanInFolder(accountID, folderID, cutoff)`
(`internal/message/store.go:1486`) deletes messages in **one folder of one account**
older than `cutoff`, and returns the count. It is deliberately folder-scoped: retention
is derived from the folder's sync period, and an account-wide delete would also wipe
Sent / Trash / Archive.

The cutoff is computed once per sync cycle in
`internal/sync/messages.go:108-120`:

```go
if syncPeriodDays > 0 {
    sinceDate = time.Now().AddDate(0, 0, -syncPeriodDays)
}
```

`syncPeriodDays` comes from the folder's sync configuration. `syncPeriodDays == 0`
disables the cutoff for that folder. Attachments go with their message through the
`attachments.message_id` CASCADE (§5), not through an explicit delete.

### 12.2 Staged attachment blobs

`draft.StagingStore` (`internal/draft/staging.go`) holds attachment bytes on disk so that
only ids cross the Wails IPC bridge. Two rules govern it:

- **Retention:** `draft.StagingRetention = 7 * 24 * time.Hour` (`internal/draft/staging.go:38`).
  Long enough that reopening a draft from the last few days always finds its bytes; short
  enough that the directory cannot grow without bound.
- **Reference check, not eager delete:** staging ids are content-addressed, so two drafts
  holding identical bytes share one file. An eager delete on draft removal would strip the
  other draft's attachment. The startup sweep therefore keeps anything a live draft still
  references (`app/app.go:625-640`):

  ```go
  removed, err := a.draftOps.staging.SweepOlderThan(draft.StagingRetention, ...)
  ```

### 12.3 Body-fetch failure marker

`messages.body_failed` (v39) marks a message whose body could not be parsed, so an
unparseable message is not re-fetched from IMAP on every sync cycle forever. A future
parser improvement is expected to clear the flag via its own migration so previously
skipped messages get retried under the new parser
(`internal/database/migrations.go:1296-1301`).

### 12.4 What is *not* cleaned up automatically

- No `VACUUM` is ever scheduled (§2). Freed pages are reused by SQLite but the file does
  not shrink. Use `VACUUM` manually (see `SQL_ROLLBACK.md` §Backup), and only with the
  app closed.
- Message bodies are never dropped to save space beyond the §12.1 window.
- `oauth_tokens`, `pgp_keys`, `smime_certificates`, `trusted_certificates` and
  `extension_secrets` live until the owning account is deleted (§5 CASCADE), or until
  the user removes the key/certificate explicitly.
- Contacts (`contact_records` and its six sub-tables) are removed by CardDAV sync or by
  deleting the source; there is no age-based prune.

---

## 13. Backup and recovery

Covered in full, including WAL-safe copy, `.backup`, corruption repair and the
forward-only nature of migrations, in **`SQL_ROLLBACK.md`**.

The one-line version: to snapshot safely, either stop the app and copy
`hsx2mail.db` + `-wal` + `-shm` together, or use `sqlite3 … ".backup out.db"` while the
app is running.

---

## 14. Related documents

| Topic | Owner |
|---|---|
| Schema, migrations, indexes, PRAGMAs, retention | this file |
| Backup, restore, corruption repair, v40/v41/v42 rollback | `SQL_ROLLBACK.md` |
| Build, test, release, troubleshooting | `OPERATIONS.md` |
| Full system structure | `architecture.md` |
| Evidence behind the schema decisions | `analysis/60-database.md` |
