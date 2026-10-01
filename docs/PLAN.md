# Hsx2Mail — Remediation Plan

> **STATUS (this file is now a historical record, not a to-do list).**
> All six phases below have been implemented. The authoritative statement of
> current behaviour is the reference docs in this directory; this plan is kept
> because it records *why* each change exists and what to check if one is
> reverted. See "Verification status" at the end for the quality gates that must
> stay green, and "Known remaining debt" for what was deliberately not done.

Derived from `docs/analysis/10`–`docs/analysis/80` and governed by
`docs/analysis/90-verification.md` (authoritative: only findings that doc marks
**CONFIRMED** or **PARTIALLY CONFIRMED** appear here; its corrected line numbers and
corrected impact wording are used everywhere, and REFUTED impacts are excluded).

Companion index: `docs/README.md`.

---

## 1. Executive summary

Hsx2Mail v0.3.2 is a feature-complete email client whose correctness and performance are
undermined by a small number of concrete defects in its sync/data layer, and whose Linux
distribution story is currently broken at the build level. `make test` is red in
`internal/database` (two migration tests), and the Flatpak manifest requests `-mod=vendor`
with no `vendor/` directory, so the primary distribution channel does not build. The sync
engine has three confirmed paths that destroy or orphan server-side/local state (header
upsert clobbers bodies and violates the attachment FK, draft autosave rewrites `imap_uid`
from stale memory, body-fetch writes are not transactional). Background mode has no tray,
autostart writes a host-side `Exec=hsx2mail` inside Flatpak, and the single-instance lock can
unlink a live instance's socket. Sync is also structurally allocation- and query-heavy at
scale (per-message autocommit upserts, full UID materialization, non-sargable thread
predicates). The good news: the mechanisms are understood, each has an exact `file:line`,
and most are small, testable changes.

Blunt verdict on the four product goals:

1. **Run great on Linux — NO (correctable).** Crashes are not the problem; packaging and
   session integration are. Flatpak cannot build (`80-F-01`), the runtime version is not on
   Flathub (`80-F-06`), notifications lose their preferred path (`80-F-02`), and the desktop
   entry lacks D-Bus activation (`80-F-09/F-10`). All are fixable in Phase 0/3.
2. **Keep a background process that checks mail frequently — PARTIAL.** The scheduler and
   IDLE exist and work, but a boot storm fires a sync for every account at once (`20-F-08`),
   IDLE dies permanently after 10 error cycles (`10-15`, `20-F-10`), and there is no tray to
   manage or restore a hidden window (`20-F-01`).
3. **Auto-start into the desktop session on boot — NO.** The Flatpak path writes a command
   that does not resolve for Flatpak-only users (`20-F-02`), the toggle is decoupled from
   `run_background`/`start_hidden` (`20-F-06`), and the enable can silently fail (`20-F-07`).
4. **Sync mail continuously and efficiently — PARTIAL.** Push/poll plumbing is present, but
   per-message autocommit header upserts (`10-11`), full local+remote UID maps per sync
   (`10-8`), and un-batched deleted-UID lookups (`10-3`) make sync cost scale with total mail
   rather than with change volume.

**Single most important action:** Phase 0 task `0-01` — restore a green `go test ./...` so
every later fix can be gated. Without it, none of the data-loss fixes can be trusted to stay
fixed.

---

## 2. Severity model

| Level | Definition |
|---|---|
| **P0** | Data loss, private-data/credential exposure, security hole with a path to exfiltration, or a build/CI break that blocks shipping. |
| **P1** | Correctness bug (wrong result, silent state loss, race) or a major performance regression that makes a core action unusable at scale. |
| **P2** | Bounded performance or robustness issue; degrades under load or leaves resources pinned but does not corrupt. |
| **P3** | Technical debt, dead code, or latent hazard with no current caller. |

Severity for `20-F-01` (no tray) is set to P1 in this plan: the analysis called it P0, but
`90-verification.md` classifies it as usability, not data loss/security. It is kept as P1
because without a tray, "background process" is not a usable mode.

---

## 3. Roadmap

Phase order is mandatory: **0 → 1 → 2 → 3 → 4 → 5 → 6**. Within a phase, tasks are ordered
by dependency.

### Phase 0 — Unblock: green tests, buildable Flatpak, CI/lint gate

**Objective:** Make the tree build and test green on every channel, and install a gate that
fails a PR when it regresses. Nothing in Phases 1–6 can be safely landed without this.

**Exit criteria:**
- `go test ./...` exits 0 (currently fails in `internal/database`).
- `go build ./...` exits 0.
- `make flatpak` produces an artifact on a clean host.
- CI runs build/test/vet/lint + `svelte-check` + `desktop-file-validate` + a Flatpak build and
  blocks merge on failure.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 0-01 | Repair red `internal/database` tests | P0 | `internal/database/database_test.go:190-201,367-379`; drop list `:169-178`; `migrations.go:1327` | Re-migrate helper drops columns for v36–v39 but not v40 (`oauth_stable_id`) / v41 (`secondary_sync_interval`), so `Migrate()` re-runs `ALTER TABLE` and fails `duplicate column name`. | Add both columns to the drop list and derive the list from the `migrations` slice so it cannot go stale. | `go test ./internal/database/... -run 'MigrationV32|MigrationV33'` exits 0. | S | — |
| 0-02 | Flatpak manifest: remove `-mod=vendor` with no vendor dir | P0 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:47` | Manifest builds `-mod=vendor`, repo has no `vendor/`; all Flatpak builds fail with "inconsistent vendoring". | Drop `-mod=vendor` and use the existing `build/flatpak/flathub/go.mod.yml` (60 module archives) as sources, or commit `go mod vendor`. | `flatpak-builder --force-clean` on a clean checkout succeeds. | S | — |
| 0-03 | Pin GNOME runtime to a Flathub-available version | P0 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:3-4`; `build/flatpak/build-flatpak.sh:33`; `build-local.sh:33` | `runtime-version: '50'` is not on Flathub; `--install-deps-from=flathub` fails on a clean machine. | Pin to the newest runtime actually on Flathub (48 or 49); add an upgrade task for 50. | `flatpak install --install-deps-from=flathub` resolves the runtime. | S | — |
| 0-04 | Bump Flatpak Dockerfile toolchain | P0 | `build/flatpak/Dockerfile:17,22,37-39` vs `go.mod:3` | Dockerfile pins Go 1.23.0, Node 20.11.0, GNOME 47 against a Go 1.25 / Node 24 / GNOME 50 module; container builds cannot compile. | Set Go 1.25.x, Node 24.x, GNOME 50; pin `wails@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)` at `Dockerfile:26` instead of `@latest`. | `build/flatpak/build-flatpak-docker.sh` completes. | S | 0-03 |
| 0-05 | Restore sandbox notification/permission talk-names | P0 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:10-21`; dev manifest `:7-18` | `--talk-name=org.freedesktop.portal.Desktop` absent; the preferred notification path fails and silently falls back to direct D-Bus. | Add `--talk-name=org.freedesktop.portal.Desktop`, `org.freedesktop.Notifications`, `org.freedesktop.appearance`, `org.freedesktop.secrets`. | New-mail notification appears from the Flatpak build. | S | 0-02 |
| 0-06 | Add CI quality gate | P0 | `Makefile:120-134`; `.golangci.yml` | Nothing runs tests/lint/build automatically; `80-F-01` and `80-F-04` would have been caught by a 3-minute run. | Add a workflow running `go build ./...`, `go test ./...`, `go vet ./...`, `golangci-lint run`, `cd frontend && npm run check && npm run lint`, `desktop-file-validate build/linux/hsx2mail.desktop`, and a Flatpak build job. | Break a test intentionally; CI fails; revert; CI passes. | M | 0-01 |
| 0-07 | Wire `svelte-check` + `knip` into `make lint` | P1 | `Makefile:125-134`; `frontend/package.json:12,15` | `lint` runs `golangci-lint` + `eslint` only; `svelte-check` and `knip` are configured but unreachable, so Svelte type errors and dead deps ship. | Add `lint-types: cd frontend && npm run check` and make `lint` depend on it; add a `knip` gate. | `make lint` reports a deliberate type error. | S | — |
| 0-08 | Rename Flathub release tooling to the current identity | P0 | `build/flatpak/flathub/release.sh:39-41`; `calculate-hashes.sh:14,25,90,93`; `flathub/README.md:15,31` | Release scripts reference the pre-rename `io.github.hkdb.Aerion.yml`, `github.com/hkdb/aerion`, `aerion-v0.1.13-…`; `release.sh` copies a file that does not exist. | Replace every `hkdb`/`Aerion`/`aerion` with `beheoxinh`/`Hsx2Mail`; add the missing version locations. | `rg -i 'hkdb|aerion' build/flatpak/flathub` returns no hits; `release.sh` dry run finds its inputs. | S | — |

---

### Phase 1 — Data loss and integrity

**Objective:** Eliminate the confirmed paths that destroy bodies, orphans attachments and
drafts, permanently flag messages as failed, or lose local history.

**Exit criteria:**
- A header re-upsert on a row that has a body and attachments preserves both.
- Draft autosave never overwrites `imap_uid`/`folder_id`/`sync_status`.
- UIDValidity resync and body-fetch writes are transactional (or staged).
- `DeleteOlderThan` only prunes the folder being synced.
- New regression tests named in §5 pass under `-race`.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 1-01 | Stop header re-upsert from violating the attachment FK | P0 | `internal/message/store.go:620-621`; `internal/sync/messages.go:787-816`; `migrations.go:147` | `Upsert` assigns a fresh UUID and writes `id=excluded.id`; `attachments.message_id` references `messages(id)` with no `ON UPDATE`, so a re-upsert of a message that has attachments raises `FOREIGN KEY constraint failed (19)`, is logged as a warning at `messages.go:816-818`, and the refreshed header/flags are dropped. (New finding NF-1, `90-verification.md`.) | On conflict, keep the existing `id`; update only the non-key columns. Never include `id = excluded.id` in the `DO UPDATE` set. | `internal/message/upsert_test.go`: upsert twice with attachments; assert 1 attachment row and updated flags. | S | 0-01 |
| 1-02 | Stop header upsert from blanking downloaded bodies | P0 | `internal/message/store.go:620,631-632` (ON CONFLICT DO UPDATE); callers `internal/sync/messages.go:815`, `header_recovery.go:137` | Header sync upserts `BodyText=""`, `BodyHTML=""`, `BodyFetched=false`; `DO UPDATE` unconditionally overwrites, destroying any downloaded body and re-queuing a full re-download. | Exclude `body_text`, `body_html`, `body_fetched` from the `DO UPDATE` set, or guard each with `CASE WHEN excluded.body_fetched = 1`. | `upsert_test.go`: upsert body, then upsert headers; assert body preserved and `body_fetched` stays 1. | S | 1-01 |
| 1-03 | Make attachment batch insert idempotent | P0 | `internal/message/attachment_store.go:194,206`; folder-scoped clear `:237` | `CreateBatch` is a bare `INSERT` with no conflict handling and no delete-first in the sync body path, so re-processing a body duplicates attachments. | Use `INSERT OR IGNORE` against a unique `(message_id, filename, …)` constraint, or delete-then-insert inside the same transaction as the body write. | `attachment_store_test.go`: call `CreateBatch` twice with the same input; assert row count stable. | S | 0-01 |
| 1-04 | Stop draft autosave from overwriting sync columns | P0 | `app/draft.go:201-203,575-581`; `internal/draft/store.go:84` | `saveDraftToDB` calls `draftStore.Update(localDraft)` with an object loaded before the last `syncToIMAP`; `Update` writes `sync_status, imap_uid, folder_id` from memory, so a fresh UID (e.g. 100) is rewritten to 0 and the server copy is orphaned forever. | Add a targeted `UpdateContent(id, …)` that touches only body/flag columns; re-read the row inside the lock. Exclude sync-state columns from the generic `Update`. | `internal/draft/store_test.go`: set `imap_uid=100`, call autosave path with stale object, assert `imap_uid` still 100. | M | 0-01 |
| 1-05 | Append replacement draft before expunging the old UID | P1 | `app/draft.go:306-314` (delete) → `:390` (append); `:397-419` post-append guard; `internal/imap/client.go:781-812` | The server-side draft is expunged before its replacement is APPENDed; a crash/network drop between the two destroys the only server copy. | APPEND first, then expunge the old UID; on APPEND failure or UID collision, leave the old copy in place and log. | `internal/smtp`/`app` draft test with a forced APPEND error; assert old draft still exists. | M | 1-04 |
| 1-06 | Make UIDValidity resync non-destructive | P0 | `internal/sync/messages.go:97`; `internal/message/store.go:798-799` | On UIDValidity change the engine runs an un-transacted `DeleteByFolder` before re-fetch; a crash mid-refetch leaves the folder locally empty until the next self-healing sync. (Impact corrected by `90-verification.md`: transient, not permanent.) | Stage the re-fetch into a temp/replacement set inside one transaction, or mark the folder with a sentinel and delete only after durable insert. | `internal/sync` test: abort between delete and insert; assert no message loss on reopen. | M | 0-01 |
| 1-07 | Transact body-fetch writes and check `RowsAffected` | P0 | `internal/sync/fetch.go:88,95,102`; `internal/message/store.go:877` | `Delete` → `UpdateBody` → N× `attachmentStore.Create` are three separate autocommit writes; `UpdateBody` ignores `RowsAffected`, so a deleted row silently no-ops and the body is lost with no error; attachment failures log at Debug only. | Wrap the three writes in one transaction; make `UpdateBody` (and batch equivalent) return an error when `RowsAffected == 0`; log attachment failures at Warn. | `internal/message/store_test.go`: delete row then `UpdateBody`; assert error returned. | M | 0-01 |
| 1-08 | Stop permanently flagging requested-but-unreturned IDs | P0 | `internal/sync/fetch.go:361,367-368,393`; `shouldChargeFailure:327-328` | `markUnresolvedAsFailed` charges IDs that were requested but not returned; a server that defers a message marks it permanently failed. | Only charge IDs known to have been returned-and-unparseable; keep unreturned IDs pending with the attempt counter and a bounded retry. | `internal/sync/fetch_test.go`: partial response leaves missing IDs pending, not `body_failed`. | M | 1-09 |
| 1-09 | Bound the body-fetch retry loop | P0 | `internal/sync/fetch.go:554-557,588,600-603` | On `UpdateBodiesBatch` error the loop re-selects the identical candidate set with no iteration cap, spinning at full IMAP-fetch rate until the process dies (transient `SQLITE_BUSY` becomes a busy-loop). | On batch-write failure, abort the body fetch or mark IDs with the attempt counter from 1-08; add a bounded total-iteration guard. | `fetch_test.go`: inject a persistent write error; assert the loop terminates with an error, not a hang. | S | 1-08 |
| 1-10 | Force write-lock at transaction start | P0 | `internal/database/database.go:60` | DSN has no `_txlock=immediate`; modernc emits bare `BEGIN` (DEFERRED), so read-then-write transactions (e.g. `internal/carddav/store.go:742` SELECT → `:766` INSERT) hit `SQLITE_BUSY_SNAPSHOT`, which `busy_timeout` cannot rescue. | Append `&_txlock=immediate` to the DSN (driver supports it). | `internal/database` concurrency test with two read-then-write goroutines; assert no `SQLITE_BUSY_SNAPSHOT`. | S | 0-01 |
| 1-11 | Fix undo convergence (batch, peek-then-pop, no re-push) | P1 | `app/undo.go:20-25`; `app/actions.go:533-554`; `internal/undo/undo.go:73-84,117` | A bulk move spanning N folders pushes N `MoveCommand`s but `Undo` pops one; `Pop()` removes before `Undo()` so a failure loses the entry; `MoveCommand.Undo` re-pushes via `MoveToFolder` (toggle/grow). | Add a composite `BatchCommand`; use existing `Peek()` → `Undo()` → `Pop()`; add `moveToFolderInternal(ids, dest, recordUndo bool)` and have undo pass `false`. | `app/undo_test.go`: bulk move across 2 folders, undo restores both; forced failure keeps the command. | M | 0-01 |
| 1-12 | Sanitize MDN headers before writing | P1(sec) | `internal/smtp/mdn.go:81,83-85`; protection bypassed vs `internal/smtp/message.go:188` | `Original-Message-ID`/`Final-Recipient` are written with raw `fmt.Sprintf`, bypassing `writeHeader`'s CRLF stripping (currently masked upstream, but the sink is unguarded). | Route every MDN header through `writeHeader` or a shared `sanitizeField`. | Unit test: CRLF in `ReadReceiptTo` does not reach the output. | S | 0-01 |
| 1-13 | Migrate the detached composer database | P1 | `app/detached_composer.go:150`; main call `app/app.go:456` | Composer process opens the DB without `Migrate()`, so it can run against an older schema while the main process migrates. | Call `db.Migrate()` after `database.Open` in the composer, or reuse the preflight path. | Launch a detached composer against a pre-migration DB; assert it migrates before use. | S | 0-01 |
| 1-14 | Scope retention pruning to the synced folder | P0 | `internal/message/store.go:1036-1038`; call `internal/sync/messages.go:117` | `DeleteOlderThan(accountID, before)` is account-wide but runs on every `SyncMessages`, silently dropping local history in folders that were not being trimmed. (Impact corrected by `90-verification.md`: no re-download spiral, but local history is still lost.) | Change to `WHERE folder_id = ? AND date < ?` and call it only for the folder being synced, or move it to an explicit user action. | `store_test.go`: two folders, prune one; assert the other's messages remain. | S | 0-01 |
| 1-15 | Serialize cross-process draft writes | P1 | `app/draft.go:562`; `app/detached_composer.go:686-696`; `Composer.svelte:1182-1188`; `app/compose.go:542-544` | Two processes (inline + detached) edit the same `drafts` row with no shared lock; last `Update` wins and both can APPEND, orphaning a server copy. `ComposerApp.cancelDraftSync` also returns without waiting, so a delete can race an in-flight APPEND. | Take a per-draft advisory lock (`flock` on a lockfile or atomic `UPDATE drafts SET owner=? WHERE id=? AND owner=''`); make `ComposerApp.cancelDraftSync` wait on `done` like `App`'s. | Test: two writers contend; assert one APPEND and a consistent row. | L | 1-04, 1-05 |

---

### Phase 2 — Sync and data-layer performance

**Objective:** Make sync cost proportional to change volume, and make folder/conversation/search
queries index-driven instead of full-scan.

**Exit criteria:**
- Header sync writes in batches (one transaction per batch, not per message).
- No per-sync materialization of all local+remote UIDs.
- `EXPLAIN QUERY PLAN` shows index use for list, conversation, and FTS queries; the new v42
  indexes exist.
- Deleted-UID reconciliation is a constant number of queries per batch.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 2-01 | Batch header upserts | P0 | `internal/sync/messages.go:815`; `internal/message/store.go:637-651` | `Upsert` is one autocommit `Exec` per message; 500 messages = 500 WAL commits contending with FTS triggers and the 12-connection pool. | Add `UpsertBatch([]*Message)` doing one `BeginTx` + prepared statement + loop + `Commit`; call it from `fetchMessageHeaders`. | `EXPLAIN`/count WAL commits; `store_test.go` inserts 500 in one txn. | M | 0-01 |
| 2-02 | Replace full UID maps with delta search | P0 | `internal/sync/messages.go:126-132`; `internal/message/store.go:838` (`GetAllUIDs`) | Every sync loads all local and all remote UIDs into maps/slices for a comparison that is almost always "N new, 0 deleted" — memory and GC scale with total mail. | Use the persisted `folders.uid_next` (`messages.go:368`) and `UID SEARCH UID <lastSeen+1>:*` for new mail; reconcile deletions with a single count/`UID SEARCH DELETED`. | Sync a 100k-row folder; assert bounded allocations (benchmark). | M | 2-01 |
| 2-03 | Batch deleted-UID lookups | P0 | `internal/sync/messages.go:223-227` | For Gmail, each deleted UID triggers `GetByUID` + 2× `ExistsInFolder` (3N queries, no transaction, IMAP held). | One `SELECT message_id … WHERE folder_id=? AND uid IN (…)` plus one `SELECT DISTINCT message_id, folder_type` for trash/spam, in one transaction. | `store_test.go`: N UIDs produce a constant query count (wrap driver). | S | 0-01 |
| 2-04 | Make the body-candidate query sargable | P0 | `internal/message/store.go:944-970,994-1015`; index `migrations.go:358` | The `body_fetched` disjunction plus unindexed columns defeats `idx_messages_body_fetched`; each body batch scans the folder and temp-sorts. | Split the predicate into indexed branches with `UNION`, or add `idx_messages_needs_body ON messages(folder_id, date DESC) WHERE body_fetched=0 AND body_failed=0`; move the self-heal branch to a separate rate-limited pass. | `EXPLAIN QUERY PLAN` shows index seek; body fetch timing improves. | M | 2-10 |
| 2-05 | Flip the FTS join so FTS drives | P0 | `internal/message/store.go:2234` (count), `:2271` region (main) | `messages`→`messages_fts` join re-scans FTS per outer row instead of driving from the FTS match set. | Rewrite as `messages_fts` (MATCH) → join `messages` on `rowid`; apply folder/account filters after the match. | `EXPLAIN QUERY PLAN`; search timing on a large DB. | M | 0-01 |
| 2-06 | Make `GetConversation` sargable | P0 | `internal/message/store.go:1558-1560` | Wrapping `thread_id`/`message_id`/`in_reply_to` in `REPLACE()` defeats `idx_messages_thread`, `idx_messages_in_reply_to`, `idx_messages_message_id`. | Store thread keys without angle brackets at insert time (or add a `thread_key` column) and query the bare column. | `EXPLAIN QUERY PLAN` shows three index seeks; conversation-open timing. | M | 2-10 |
| 2-07 | Replace the conversation-list double aggregation | P0 | `internal/message/store.go:1300-1319,1439` | `ListConversationsByFolder` + `CountConversationsByFolder` each group on `COALESCE(thread_id,id)` (non-indexable), producing two full folder scans + temp B-trees per page. | Materialize a `conversation_key` column at insert with `INDEX(folder_id, conversation_key, latest_date)`, or add the expression index `ON messages(folder_id, COALESCE(thread_id,id))`. | `EXPLAIN QUERY PLAN`; folder-open timing at 50k rows. | L | 2-10 |
| 2-08 | Stop dragging bodies through `GetByIDs` | P0 | `internal/message/store.go:2108-2117` (`:2111` in SELECT) | `GetByIDs` selects `body_text, body_html` even for callers that only need uid/folder, pulling full HTML through the driver on bulk-flag paths. | Add a projection parameter; default to header/flag columns and only include bodies when the caller asks. | `store_test.go`: assertion on returned struct fields; memory/allocation benchmark. | S | 0-01 |
| 2-09 | Batch `FindThreadID` lookups | P0 | `internal/message/store.go:1721-1743`; call `internal/sync/messages.go:861` | `FindThreadID` issues one query per reference (N+1); a 500-message batch with 8 refs each = ~4,000 queries. | One `SELECT thread_key, id, message_id FROM messages WHERE account_id=? AND message_id IN (?,…)` for all refs in the batch, resolved in memory. | Query-count test; batch sync timing. | M | 2-06 |
| 2-10 | Add the v42 index set | P0 | `internal/database/migrations.go:137,139,358` | Missing: `(account_id,date)` for retention prune, `(folder_id,thread_id,date DESC)`, `(folder_id, COALESCE(thread_id,id))`, partial needs-body index. | Add a v42 migration creating the indexes from `analysis/40` §3.4 (`idx_messages_account_date`, `idx_messages_folder_thread_date`, `idx_messages_folder_conv`, `idx_messages_needs_body`). | `internal/database` migration test; `EXPLAIN QUERY PLAN` per hot query. | S | 0-01 |
| 2-11 | Cache prepared statements / stop rebuilding SQL | P1 | `internal/message/store.go:1293-1296,1300,1319` | `ListConversationsByFolder` rebuilds SQL with `fmt.Sprintf` per call; no long-lived `sql.Stmt` exists for hot reads (only 4 `.Prepare(` sites, all in loops). | Cache the two SQL variants as prepared statements; select `participantsExpr`/`orderClause` from a map instead of string building. | Benchmark `ListConversationsByFolder`; assert one prepare. | M | 2-07 |
| 2-12 | Add a deterministic sort tiebreaker | P1 | `internal/message/store.go:114-116,1317,2275` | `ORDER BY MAX(date) DESC` has no tiebreaker; hundreds of conversations tie on the key and paging can reorder rows. | Append `, id DESC` (or the conversation key) to every such `ORDER BY`. | Test: paginated fetch with all-equal dates returns stable, non-overlapping pages. | S | 2-07 |
| 2-13 | Skip `GetAllUIDs` on the flags fast path | P1 | `internal/sync/messages.go:443-461`; `internal/sync/condstore.go:132-134,151-153` | The IDLE flag path loads all local UIDs to pass `existingUIDs`, but the incremental branch never reads it. | Check `shouldUseCondStore(...)` first; only call `GetAllUIDs` when the full-reconcile branch runs. | Test: incremental flag sync issues no full UID scan. | S | 2-02 |
| 2-14 | Stop re-running `SyncFolders` on EXPUNGE bursts | P1 | `app/background.go` (`handleIdleExpunge`) | Each EXPUNGE event runs a full reconcile; a 500-message bulk delete produces 500 EXPUNGEs and repeated `SyncFolders` (N_mailboxes STATUS round-trips each). | Add `SyncFolderExpunge(accountID, folderID)` doing only the UID diff (use `UID SEARCH DELETED` when QRESYNC is available); never call `SyncFolders` from the expunge path. | Test: burst of EXPUNGEs issues one diff, not N reconciles. | M | 2-02 |
| 2-15 | Pass attachments by staging id, not base64 | P1 | `Composer.svelte:534-540,355`; `app/draft.go:83`; `internal/draft/store.go:82`; `app/compose.go:795-816` | Every autosave marshals all attachment bytes as `content_base64` JSON across the Wails IPC bridge (100 MB/file cap), causing UI jank and re-encryption on every pause. | Stage bytes in a Go-side table keyed by `staging_id`; send only id + metadata; reuse the id for carried-over reply/forward attachments. | Test: 50 MB attachment autosave round-trip is small and does not re-send bytes. | L | 1-04 |
| 2-16 | Enable WAL housekeeping | P1 | `internal/database/database.go:60,123` | `VACUUM` never runs, `journal_size_limit`/`wal_autocheckpoint` unset, and the 5-minute `wal_checkpoint(PASSIVE)` never truncates, so the DB and `-wal` grow monotonically. | Add `&_pragma=journal_size_limit(67108864)`; switch the routine to `wal_checkpoint(TRUNCATE)`; run `PRAGMA optimize` after sync/shutdown. | Assert `-wal` size stays bounded after heavy sync. | S | 0-01 |
| 2-17 | Fix the body-fetch counter data race | P1 | `internal/sync/fetch.go:516-531,537,558,561` | The 30 s heartbeat goroutine reads `fetched`/`failed`/`totalWithoutBody` while the main loop writes them with no mutex/atomics; a real race detectable by `-race`. | Use `sync/atomic` (`atomic.Int64`) for `fetched`/`failed` or a dedicated mutex. | `go test -race ./internal/sync/...` clean. | S | 0-01 |

---

### Phase 3 — Background operation, Linux autostart, tray, single-instance, boot storm

**Objective:** Deliver the product goal: a supervised background process that starts with the
session, survives, is manageable without a visible window, and does not stampede on boot.

**Exit criteria:**
- A hidden background instance is recoverable (tray or documented equivalent).
- Autostart installs a working entry on both native and Flatpak channels.
- Booting with N accounts does not fire N simultaneous syncs.
- Two instances can never both hold the lock and run schedulers.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 3-01 | Add a tray icon for background mode | P1 | `app/app.go:858,892-902,912`; `go.mod` (no systray) | Background mode hides the window with no tray, no global shortcut, no in-app restore; only recovery is re-running the binary or clicking a notification. | Add `getlantern/systray` (or `fyne.io/systray`) with Open / Sync now / Preferences / Quit, on its own goroutine (Wails v2 has no tray API). | Launch hidden; tray menu opens the window and quits. | L | — |
| 3-02 | Fix Flatpak autostart command | P0 | `internal/platform/autostart_linux.go:118` (`:110-113` comment) | Flatpak autostart passes `"commandline": ["hsx2mail"]`, producing a host-side `Exec=hsx2mail` that does not resolve for Flatpak-only users. | Remove `commandline` and let the portal synthesize `flatpak run …`; if the escaping bug persists, set `["flatpak","run","io.github.beheoxinh.Hsx2Mail"]`. | Fresh profile, toggle autostart, log out/in; app starts. | S | 0-05 |
| 3-03 | Fix single-instance stale-socket race | P0 | `internal/platform/singleinstance_linux.go:54-71` | `EADDRINUSE` is treated as proof of a stale socket; a live-but-slow instance's socket is unlinked and a second process binds a fresh one, yielding two schedulers/IDLE sets and two SQLite writers. (Impact corrected: duplicate instances, not corruption.) | Take `flock(LOCK_EX\|LOCK_NB)` on a pid file, or verify liveness with `Dial` + `SO_PEERCRED`; never `Remove` unconditionally after a failed `Dial`. | Test: launch two instances under load; second exits without unlinking. | M | — |
| 3-04 | Quote and re-verify the autostart Exec path | P1 | `internal/platform/autostart_linux.go:20,232-238` | `Exec` is `os.Executable()`, unquoted, pinning a build-specific path (Nix hash, AppImage mount) that breaks on upgrade and on paths with spaces. | Emit `Exec="<quoted-abs-path>" --start-hidden`; re-verify the entry on startup when `os.Executable()` changes. | Autostart entry survives a moved binary; space-containing path works. | S | 3-02 |
| 3-05 | Couple autostart with background + hidden start | P1 | `app/settings.go:222-237`; `frontend/src/lib/components/settings/GeneralTab.svelte:466-475` | Enabling autostart alone writes a `.desktop` that boots a visible window, defeating `run_background`/`start_hidden`. | `SetAutostart(true)` also sets `run_background=true` and `start_hidden=true`. | Toggle autostart; assert both companion flags set. | S | 3-06 |
| 3-06 | Write the autostart flag only after OS success | P1 | `app/settings.go:225-232`; `internal/platform/autostart_linux.go:157-158` | The DB flag is written before the OS call and `current == enabled` short-circuits retries, so a failed enable shows "on" with nothing installed. | Write the flag after `Enable()`/`Disable()` succeeds; roll back on error; surface a toast. | Force `Enable()` failure; assert flag stays false and an error shows. | S | — |
| 3-07 | Add boot-storm backoff and jitter | P1 | `internal/sync/scheduler.go:67,138,200-202,245-252` | A 1-minute ticker plus `LastSync == nil ⇒ due` and no error backoff makes every account sync immediately at boot (and retry every tick). | Add per-account exponential backoff on failure, randomized jitter on first sync, and a cap on concurrent account syncs. | Simulate 5 offline accounts at boot; assert staggered, backed-off attempts. | M | — |
| 3-08 | Classify auth vs transient token failures | P1 | `app/compose.go:69-93` | Every token-refresh failure emits `oauth:reauth-required`, so an offline boot spams a re-auth modal per account every 60 s. | Emit reauth only for auth-class failures; suppress/rate-limit transient network failures per account. | Offline boot; assert no reauth modal for network errors. | M | 3-07 |
| 3-09 | Reset IDLE attempts on success and re-arm after give-up | P1 | `internal/imap/idle.go:184-212`; `app/background.go:877` | `attempts` resets only after connect, not after a healthy IDLE cycle, so a long session hits `MaxReconnectAttempts` and IDLE dies permanently; nothing re-arms it except wake/network. | Reset `attempts` after a cycle that lasted ≥ `IdleTimeout`; add a slow re-arm timer (e.g. 15 min) after give-up, or restart on a successful poll sync. | Long-lived connection with forced IDLE errors re-arms automatically. | M | — |
| 3-10 | Reap composer child processes | P1 | `app/ipc.go:243` | `cmd.Start()` with no `cmd.Wait()` leaves one zombie per closed detached composer until the main process exits. | `go func() { _ = cmd.Wait() }()` after `Start()`. | Open/close a detached composer; assert no zombie in `ps`. | S | — |
| 3-11 | Emit `app:ready` after D-Bus inits | P1 | `app/app.go:778-788` | `app:ready` is emitted before `initNotifications`/`initSleepWakeMonitor`/`initThemeMonitor`, so a hidden start boots the frontend with no notification listener and a new-mail event can be lost. | Emit `app:ready` after the D-Bus initializers, or run them fire-and-forget with their own completion events. | Hidden start; assert notification listener is registered before `app:ready`. | S | — |
| 3-12 | Ship a `systemd --user` unit + `sd_notify` | P2 | `internal/platform/startup_linux.go:24` | `NotifyStartupComplete` is `gdk_notify_startup_complete()`, not `sd_notify`; there is no `Type=notify`, no `Restart=on-failure`, no supervision, so a crash at 3 a.m. is never restarted. | Ship `hsx2mail.service` with `Type=notify`, `ExecStart=… --background`, `Restart=on-failure`, `WantedBy=default.target`; call `sd_notify(READY=1)` when `$NOTIFY_SOCKET` is set. | `systemctl --user start hsx2mail` reports active; kill process and it restarts. | M | 3-01 |
| 3-13 | Move sockets to `$XDG_RUNTIME_DIR` | P2 | `internal/platform/singleinstance_linux.go:150-152`; `internal/ipc/server_unix.go:71-73` | Sockets live in `os.TempDir()` (`/tmp`), which outlives the session and is subject to uid recycling and `$TMPDIR` divergence. | Place both the instance lock and IPC socket under `$XDG_RUNTIME_DIR` with `0700`. | Assert socket path under `$XDG_RUNTIME_DIR`, mode 0700. | S | 3-03 |
| 3-14 | Suppress notification bodies while locked | P2 | `app/background.go:498-510` | Notification body = subject, summary = sender; on a locked screen this leaks sender + subject. | Subscribe to `org.gnome.ScreenSaver.ActiveChanged` / `login1.LockSignal`; suppress bodies while locked. | Lock screen; assert generic notification text. | S | — |
| 3-15 | Add D-Bus activation + fix startup notification | P1 | `build/linux/hsx2mail.desktop:41`; `main.go:142`; `build/linux/hsx2mail.desktop` (whole) | `StartupNotify=true` but the app never implements the GIO startup-notification protocol; no `DBusActivatable=true` and no companion service, so every launcher click forks a full process. | Implement startup notification (or set `StartupNotify=false`), ship `io.github.beheoxinh.Hsx2Mail.service`, set `DBusActivatable=true` and `Exec` to the bus name. | Launcher click raises the running instance without a fork; `desktop-file-validate` passes. | M | 3-03 |
| 3-16 | Use `ForceClose` on IDLE error paths | P1 | `internal/imap/idle.go:139,155,220`; `internal/imap/client.go:299-314` | `Close()` calls `Logout().Wait()` with no timeout; if the server stops responding, the IDLE goroutine blocks forever and `IdleManager.Stop()` can hang app shutdown. | Use `ForceClose()` on error paths; bound the graceful `Close()` with the already-declared `ShutdownTimeout` (`idle.go:50`). | Test: server that never answers LOGOUT; shutdown completes within the timeout. | S | — |

---

### Phase 4 — Frontend performance

**Objective:** Remove the multi-megabyte startup cost, the unbounded render cost, and the
listener/timer leaks.

**Exit criteria:**
- Entry bundle excludes the Iconify JSON; icons load on demand or from a subset.
- Message list renders only the viewport window.
- No leaked event listeners or timers across component lifecycles.
- No visible theme flash on launch.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 4-01 | Virtualize the message list | P0 | `frontend/src/lib/components/list/MessageList.svelte:1681,1706,1715`; `PAGE_SIZE :77` | The list renders every loaded row via `{#each}`; `@tanstack/svelte-virtual` is a declared dep never imported; the array grows without bound. | Add `createVirtualizer` with `getScrollElement`, `estimateSize` from row density, `measureElement`; cap the retained window. | Render 10k rows; assert DOM node count stays bounded. | L | — |
| 4-02 | Lazy-load / subset Iconify collections | P0 | `frontend/src/lib/iconify-offline.ts:5-9,12-16` | Five Iconify JSON collections are eagerly imported into the entry graph; the 5.78 MB-gzip chunk is `modulepreload`ed on every cold start. | Generate a build-time subset of referenced icons, or move `import()` inside `addCollection` so collections load on first use. | `dist/index.html` no longer preloads the icon chunk; audit entry graph size. | M | — |
| 4-03 | Use per-listener unsubscribe instead of `EventsOff` | P0 | `frontend/src/lib/components/list/MessageList.svelte:258-263`; `accounts.svelte.ts:59-60` | `EventsOff('folder:synced')` removes every listener for the event name; `accounts.svelte.ts` guards init so it never re-registers, leaving other subscribers dead. | Use the unsubscribe function returned by `EventsOn` (as `ConversationViewer.svelte:270,284,341,367` does) or namespace events per component. | Two components subscribe; unmount one; the other still receives events. | S | — |
| 4-04 | Prevent theme flash before first paint | P0 | `frontend/src/App.svelte:389-390`; `frontend/index.html:38` | Theme is applied only after `await loadSettings()` → `initTheme` in `onMount`; before that `data-theme` is absent and `--background` is undefined, flashing white for dark-mode users. | Add a synchronous inline `<script>` in `index.html` that reads the persisted theme (localStorage mirror or Go-injected value) and sets `data-theme`/`.dark` before first paint. | Cold launch in dark mode shows no flash (record trace). | S | — |
| 4-05 | Bound the inline-attachment cache | P1 | `frontend/src/lib/stores/inlineAttachmentCache.ts:12-13`; written at `EmailBody.svelte:632` | Every inline image ever rendered is kept in an unbounded cache. | Cap the cache (LRU by byte budget) and evict entries when the viewer changes messages. | Open many image-heavy mails; assert cache size bounded. | S | — |
| 4-06 | Fix listener/timer cleanup | P1 | `App.svelte:483-486`; `RecipientInput.svelte:43,67`; `MessageList.svelte:236-239`; `App.svelte:871-880` | `App.svelte` never removes the `mediaQuery` change listener; `RecipientInput` has no `onDestroy` for its debounce; `MessageList` polls at 2 Hz for its lifetime. | Store and remove the `mediaQuery` listener; add `onDestroy` cleanup for the debounce; convert the dialog poll to a state-driven one-shot. | Unmount components; assert no listeners/timers remain (spy). | S | — |
| 4-07 | Detect `srcset` and protocol-relative remote images | P1 | `frontend/src/lib/components/viewer/EmailBody.svelte:88-108,67` | The blocker regexes require `https?://`, so `//host/pixel.gif` and `srcset` are missed; the CSP is the real backstop, but the "images blocked" banner does not show. | Add `//` and `srcset` to the detector, or drive the banner off CSP-blocked `postMessage`. | Render a mail with `srcset`; banner appears. | S | — |
| 4-08 | Reduce per-row derived/state churn | P2 | `ConversationRow.svelte:216-242`; store `currentState = { ...currentState, ...updates }` | 9 `$derived` per row × N unvirtualized rows; every state update spreads the whole object. Addressed largely by 4-01. | After 4-01, memoize row derived values and replace whole-object spreads with targeted field updates. | Profile render of a large folder. | M | 4-01 |

---

### Phase 5 — Security hardening

**Objective:** Close the confirmed weak boundaries (IPC resource limits, OAuth callback XSS,
credential fail-open, refresh race, sandbox/header injection).

**Exit criteria:**
- IPC rejects oversized pre-auth frames and caps connections.
- OAuth callback HTML cannot be injected via query parameters.
- A keyring failure never silently downgrades secret storage without surfacing it.
- Token refresh is serialized; the message iframe cannot run scripts.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 5-01 | Bound the pre-auth IPC JSON buffer | P1(sec) | `internal/ipc/server.go:226` | Pre-auth `json.Decoder` has no `io.LimitReader`; `AuthTimeout` bounds time, not bytes, so a local client can exhaust memory. | Wrap the pre-auth reader in `io.LimitReader` (small cap) and reject oversized frames before decode. | IPC test: oversized frame is rejected. | S | — |
| 5-02 | Cap IPC connections | P1(sec) | `internal/ipc/server.go:163-164` | `AcceptLoop` has no connection cap and inserts clients into `s.clients` before auth. | Add a semaphore: reject beyond a fixed max before inserting into the map. | IPC test: N+1 connections rejected. | S | — |
| 5-03 | Fix OAuth callback reflected XSS | P1(sec) | `internal/oauth2/server.go:173`; template `:250` | `fmt.Fprintf(w, errorPageHTML, result.Error, result.ErrorDescription)` interpolates query-string values into HTML. | HTML-escape the values (`html/template` or `html.EscapeString`) before formatting. | Unit test: `<script>` in `error` is escaped in the response. | S | — |
| 5-04 | Stop silent keyring fail-open | P1(sec) | `internal/credentials/store.go:83`; `oauth.go:254,314`; `oauth_user_creds.go:74` | A runtime `gokeyring.Set` failure silently falls through to the AES DB and is never re-probed. | Make keyring failure fail-closed or surface a user-visible warning; re-probe rather than latch the first result. | Test: simulate keyring failure; assert an explicit error/warning path. | M | — |
| 5-05 | Serialize OAuth token refresh | P1(sec) | `app/compose.go:60` | `getValidOAuthToken` has no mutex; concurrent refresh with a rotating-refresh-token provider (Microsoft) can invalidate the rotated token. | Guard refresh-per-account with a mutex or `singleflight`. | Concurrent refresh test: one network refresh, both callers succeed. | S | — |
| 5-06 | Remove script capability from the message iframe | P1(sec) | `frontend/src/lib/components/viewer/EmailBody.svelte:785` | `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"` lets email content run scripts and escape popups. | Drop `allow-scripts` (and the escape token); rely on the isolated rendering contract. | Render a script-bearing mail; script does not execute. | M | — |
| 5-07 | Route every outbound header through sanitization | P1(sec) | `internal/smtp/mdn.go:81,83-85`; `internal/smtp/message.go:397,443-445`; `app/app.go:153` | MDN headers and attachment `Content-Type`/`Content-Disposition`/`Content-ID` are set from remote/user-controlled values without CRLF stripping; `sanitizeField` is applied only inside `parseMailtoURL`. | Funnel all header writes through `writeHeader`/`sanitizeField`; validate attachment MIME fields in the builder; sanitize `ComposeMessage` at the binding boundary. | Unit tests: CRLF and quote injection in each field is neutralized. | M | 1-12 |
| 5-08 | Tighten the HTML sanitizer | P2 | `internal/email/sanitizer.go:89,103,127`; `Composer.svelte:501-540`; `internal/smtp/message.go:101` | `data:` is allowed on all URL attributes including `<a href>`; `style` and data attributes are globally allowed; composer HTML is not run through bluemonday before `ToRFC822`. | Remove `data:` from `AllowURLSchemes` for navigable attributes; review global `style`/data-attr allowances; sanitize composer HTML at send. | Sanitizer test with `data:text/html` and style payloads. | M | — |
| 5-09 | Validate the credential-helper binary | P2 | `internal/oauth2/config.go:152,155` | `exec.Command` of the sibling `hsx2mail-creds` checks only existence (`os.Stat`), not ownership/permissions. | Require owner == current uid and mode without group/other write before exec. | Test: helper with wrong mode/owner is refused. | S | — |
| 5-10 | Bind verified PGP/S/MIME identity to the sender | P2 | `internal/pgp/verifier.go:167-200`; `internal/smime/verifier.go:259-325` | Verified signer identity is never compared to the message From, so a valid signature by any key is reported as valid. | Compare the verified key identity against the message `From` and surface mismatches. | Verifier test: wrong-key signature flagged. | M | — |

---

### Phase 6 — Documentation consolidation

**Objective:** Make `docs/` the single source of truth. Fix the verified-wrong pre-existing
docs, promote the analysis material into canonical owner docs, and remove duplicated prose
that guarantees drift.

**Exit criteria:**
- `docs/architecture.md` is rewritten or replaced and no longer carries a wrong migration map.
- `AGENTS.md` has no known-false structural claim (see `analysis/95` §5).
- Each topic in the `docs/README.md` map has one canonical owner.
- `docs/GAPS.md` items G1–G4 are closed; G9/G12/G13/G15/G16 have canonical docs.

| ID | Title | Sev | File:line(s) | Problem | Fix | Verification | Effort | Depends on |
|---|---|---|---|---|---|---|---|---|
| 6-01 | Write canonical `docs/DATABASE.md` | P2 | `internal/database/migrations.go`; `internal/message/attachment_store.go` (`ensureContentColumn`); extensions store `ext_kv` | No authoritative schema/table-to-migration map exists; every old map is wrong (G1). | Add a generated table-per-migration map, columns/purpose, index inventory, FK actions, `ErrSchemaTooNew`, runtime-added columns. | Map matches `migrations.go`; generation script cannot drift. | M | — |
| 6-02 | Write `docs/OPERATIONS.md` release procedure | P2 | `app/state.go:30`; `wails.json:12`; `frontend/package.json:4`; `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml` | `RELEASE.md` tells the releaser to edit a `CHANGELOG.md` that does not exist and omits version locations (G2 + `80-F-14,F-15`). | Document every version location in order, CI gates, tagging, artifacts, rollback. | A release following the doc touches all five version sites. | M | 0-06 |
| 6-03 | Add backup/recovery runbook + v40/v41 rollback | P2 | `internal/database/database.go`; `migrations.go`; `tools/db/` | `SQL_ROLLBACK.md` covers only v39→v30 and predates v40/v41; no corruption/WAL/migration-recovery procedure (G3). | Document DB/WAL locations, WAL-safe backup, corruption repair, and add `rollback-v41-to-v30.sql`; document the schema guard. | Restore a backup into a scratch DB; rollback script runs. | M | 6-01 |
| 6-04 | Write `docs/BACKGROUND.md` | P2 | `app/app.go:952-958,934`; `internal/platform/autostart_linux.go` | Startup, keep-running, autostart, sleep/wake and network transitions are spread across docs; no tray exists but `business-logic.md:815` says "minimize to tray" (G4 + audit). | Document `start_hidden`/`run_background` relationship, close semantics, single-instance handoff, and that "background" has no tray. | Doc matches source; search for "tray" finds one corrected note. | M | 3-01 |
| 6-05 | Write `docs/FRONTEND.md` | P2 | `frontend/src/lib/stores/`; `App.svelte`; `frontend/src/lib/components/` | The frontend reference predates runes-based stores and virtualization (G12). | Document store inventory, Go-event→render flow, virtualization, layout modes, extension UI registry. | Doc matches store list produced by `ls`. | M | 4-01 |
| 6-06 | Write `docs/CRYPTO.md` | P2 | `internal/pgp/`; `internal/smime/`; `internal/certificate/`; `internal/crypto/` | PGP/S/MIME/TOFU are spread across three partial docs with the wrong cert table name in two (G9). | Consolidate storage tables, sign/verify/encrypt/decrypt flows, WKD/HKP discovery, TOFU prompt; call out the unmaintained PKCS#7 dependency. | Doc table names match `migrations.go` (`trusted_certificates`). | M | — |
| 6-07 | Write `docs/PERFORMANCE.md` | P2 | `internal/message/store.go`; `internal/sync/`; `internal/database/migrations.go` | Analysis contains performance detail but no canonical tuning guide (G13). | Document the hot queries, their indexes, measured envelopes, and the v42 index set from Phase 2. | Guide links to EXPLAIN output. | M | 2-10 |
| 6-08 | Rewrite `docs/architecture.md` as canonical | P2 | `docs/architecture.md` §2,§4 | Stale: wrong migration map/count, single-instance transport, keyring library name; duplicates `AGENTS.md` §3 (audit). | Rewrite with a correct startup section (single owner), link to `DATABASE.md`, correct single-instance transport and keyring library. | `analysis/95` stale claims no longer present. | L | 6-01, 6-04 |
| 6-09 | Fix `AGENTS.md` false structural claims | P2 | `AGENTS.md:118,175,706`; table map `:254-261`; `:263,167,513,270,255` | Migration count v1..v39 (should be v41), wrong table map, non-existent `undo_commands`, single-instance "D-Bus", `certificates` vs `trusted_certificates`, `contact_records (v18+)` (v27). | Correct each against source or replace with links to `DATABASE.md`/`architecture.md`. | `analysis/95` §5 items 1–7 no longer reproduce. | M | 6-01, 6-08 |
| 6-10 | Fix `docs/BUILD.md` artifact names and runtime deps | P2 | `docs/BUILD.md:24`; `build/flatpak/build-flatpak.sh:65,71` | Doc names the wrong Flatpak artifact and omits runtime deps and CI (audit + `80-F-44`). | Correct to `Hsx2Mail-dev.flatpak` / `Hsx2Mail-<version>.flatpak`; list GTK3, WebKit2GTK 4.1, keyring, and CI gates. | Doc matches build scripts. | S | 6-02 |
| 6-11 | Fix `RELEASE.md` and `EXT_RULES.md` R32 | P2 | `docs/RELEASE.md:5,7`; `docs/EXT_RULES.md:149`; `tools/db/` | `RELEASE.md` references a nonexistent `CHANGELOG.md`; `EXT_RULES.md` expects `rollback-v<latest>-to-v30.sql` but only v39 exists and schema is v41. | Point release docs at 6-02; correct the rollback filename rule. | No dangling references. | S | 6-02, 6-03 |
| 6-12 | Finalize the doc index + maintenance rules | P2 | `docs/README.md`; `docs/GAPS.md` | The corpus needs a single entry point and drift rules once owners exist. | Keep `docs/README.md` current; close resolved GAPS entries; mark analysis immutable. | Index lists every doc with an owner/status. | S | 6-01…6-11 |

---

## 4. Quick wins (< 1 day, high value)

| ID | Task | Why it is high value | Phase task |
|---|---|---|---|
| QW-1 | Restore green `go test ./...` | Unlocks every other gate; catches a P0-class build break. | 0-01 |
| QW-2 | Add `&_txlock=immediate` to the DSN | One-line change removes intermittent `SQLITE_BUSY_SNAPSHOT` hard failures. | 1-10 |
| QW-3 | Pin Flatpak `runtime-version` to a Flathub version | Makes the primary distribution channel installable. | 0-03 |
| QW-4 | Drop the Flatpak autostart `commandline` | One-line fix makes autostart actually work on Flatpak. | 3-02 |
| QW-5 | Emit `app:ready` after D-Bus inits | Prevents lost new-mail events on hidden start. | 3-11 |
| QW-6 | Add `cmd.Wait()` for the composer child | One line removes a per-window zombie. | 3-10 |
| QW-7 | Sanitize MDN header values | Removes a latent header-injection primitive cheaply. | 1-12 |
| QW-8 | Inline theme bootstrap in `index.html` | Removes the white flash on every dark-mode launch. | 4-04 |
| QW-9 | Scope `DeleteOlderThan` to one folder | Stops silent loss of local history in untouched folders. | 1-14 |
| QW-10 | `journal_size_limit` + `wal_checkpoint(TRUNCATE)` | Bounds WAL/DB growth on long-lived installs. | 2-16 |

---

## 5. Verification and quality-gate plan

### Exact commands (CI must run these)

```sh
go build ./...
go test ./...
go vet ./...
go test -race ./internal/sync/... ./internal/imap/... ./app/...
golangci-lint run                       # config: .golangci.yml
cd frontend && npm run check            # svelte-check --tsconfig ./tsconfig.json
cd frontend && npm run lint             # eslint .
cd frontend && npm run knip             # unused deps (optional gate)
desktop-file-validate build/linux/hsx2mail.desktop
appstreamcli validate build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml
make flatpak                            # or flatpak-builder --force-clean on the manifest
```

`make test` currently exits 1 (two `internal/database` tests). `make lint` omits
`svelte-check` and `knip` (Phase 0 task 0-07). The gate is only real once both are fixed.

### Minimum tests that would catch the P0s

| Test file (new) | Asserts |
|---|---|
| `internal/database/migrations_test.go` | Re-migrate from v1..v41 is idempotent (catches the red tests and any future column-drift). |
| `internal/message/upsert_test.go` | A second header `Upsert` preserves `body_text`/`body_html`/`body_fetched` and the existing `id`, and does not cascade-delete attachments (catches 1-01, 1-02). |
| `internal/message/attachment_store_test.go` | `CreateBatch` twice with identical input yields no duplicate rows (catches 1-03). |
| `internal/draft/store_test.go` | The autosave path never overwrites `imap_uid`/`folder_id`/`sync_status` (catches 1-04). |
| `internal/sync/messages_test.go` | `DeleteOlderThan` removes only the target folder's rows (catches 1-14). |
| `internal/sync/fetch_test.go` | A persistent batch-write error terminates the body loop (catches 1-09); a partial response leaves missing IDs pending (catches 1-08); `-race` is clean on the heartbeat counters (catches 2-17). |
| `internal/sync/messages_test.go` | UIDValidity resync with an aborted re-fetch loses no messages (catches 1-06). |
| `app/undo_test.go` | A bulk move across two folders is fully restored by one undo; a failed undo keeps the command (catches 1-11). |
| `internal/sync/messages_test.go` | Header sync over 500 messages opens one transaction, not 500 (catches 2-01). |
| `internal/message/store_test.go` | Query-count assertions for deleted-UID reconciliation and `FindThreadID` (catches 2-03, 2-09). |

---

## 6. Risk register (top 10)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| 1 | Data-layer rewrites regress silently because `internal/message`/`internal/sync` have near-zero tests. | High | High | Land the §5 P0 tests before or with each Phase 1/2 change; gate on `-race`. |
| 2 | Removing `-mod=vendor` makes the Flatpak sandbox reach the network and fail offline. | Medium | High | Use the pre-generated `go.mod.yml` module archives; verify with a clean, network-restricted build in CI. |
| 3 | `runtime-version` pin is wrong (GNOME 48/49 availability). | Medium | High | Verify against Flathub at fix time; keep the version in one place and document the upgrade. |
| 4 | Attachment staging-id change breaks the IPC contract between main and composer processes of different versions. | Medium | High | Version the IPC message; keep the base64 path as a fallback for one release. |
| 5 | Tray (`systray`) integration is not thread-safe with the GTK main loop. | Medium | Medium | Run the tray on its own goroutine and marshal all UI actions through the existing Wails callbacks; test on GNOME Wayland. |
| 6 | `_txlock=immediate` increases write-lock contention and `SQLITE_BUSY` waits under concurrent sync. | Medium | Medium | Keep `busy_timeout=30000`; measure writer contention; back off write-heavy loops. |
| 7 | Autostart/systemd unit installs fail on systems without a portal or with hardened home dirs. | Medium | Medium | Detect the portal; fall back to an XDG `.desktop`; surface install errors to the UI (3-06). |
| 8 | Keyring fail-closed locks users out when no Secret Service is available. | Medium | High | Offer an explicit, user-confirmed AES fallback rather than a silent one; document in PRIVACY. |
| 9 | `ForceClose` changes increase connection churn and server-side load. | Low | Medium | Only force-close on error/timeout; keep graceful close on success with a bounded wait. |
| 10 | Phase ordering slips and a Phase 2 index/DDL lands before Phase 1 integrity fixes. | Medium | Medium | Enforce the phase gate in CI/review; DDL-only migrations do not depend on integrity fixes but must not precede 0-01. |

---

## 7. Definition of done per goal

**Goal 1 — Run great on Linux.**
- `make flatpak` and `make build` succeed on a clean host (0-02, 0-03, 0-04, 0-05).
- `desktop-file-validate` and `appstreamcli validate` pass; D-Bus activation works (3-15).
- Notifications route through the portal; no silent failure (0-05, and the fallback documented).
- `make test` and CI are green; a broken PR is blocked (0-01, 0-06, 0-07).

**Goal 2 — Background process checks mail frequently.**
- A hidden instance is recoverable via tray or documented equivalent (3-01).
- IDLE re-arms automatically after failures; push is not permanently disabled after a >10-cycle outage (3-09, 3-16).
- Notifications fire and do not leak subjects while locked (3-14).
- The process is supervised and restarts on crash (3-12).

**Goal 3 — Auto-start into the desktop session on boot.**
- Autostart works on both the native and Flatpak channels, with a quoted, upgrade-safe `Exec` (3-02, 3-04).
- Enabling autostart implies hidden background operation (3-05).
- A failed enable is visible and retryable (3-06).
- Boot does not open a visible window (3-05).

**Goal 4 — Sync mail continuously and efficiently.**
- Header sync writes in batches; deleted-UID reconciliation is O(1) queries per batch; no full UID materialization per sync (2-01, 2-02, 2-03).
- Folder-open, conversation-open, and search are index-driven per `EXPLAIN QUERY PLAN`; the v42 index set exists (2-04…2-10).
- No confirmed data-loss path remains: header upsert preserves bodies/attachments, draft sync columns are immutable from autosave, UIDValidity resync is transactional, retention pruning is folder-scoped (1-01…1-14).
- `go test -race ./internal/sync/... ./internal/imap/...` is clean (2-17).

---

## 8. Findings deliberately excluded

Per `docs/analysis/90-verification.md` §3, the following *impact* claims are REFUTED and must
not drive work: (a) "80-F-02 all notifications silently fail" (a direct-D-Bus fallback exists);
(b) "60-F-2 folder permanently empty/unrecoverable" (transient, self-healing); (c) "10-1 forces
full re-download" (pruned mail is outside the retention window). The underlying *mechanisms*
survive and are scheduled above; only the overstated impacts are discarded. Vaulted negatives
`NF-3` (no XSS/regex-panic in `highlightMatches`) are recorded so they are not re-flagged.

---

## Developer workflow scripts

Added after the remediation phases, so that a fresh clone needs three commands
and nothing else.

| Script | Purpose |
|---|---|
| `prepare.sh` | Installs everything needed to build and develop: system libraries (GTK3, WebKit2GTK 4.1, desktop-file-utils), the Go toolchain pinned by `go.mod`, Node, the Wails CLI pinned by `go.mod`, and `npm ci`. Then verifies the tree builds, type-checks and passes its gates. Idempotent. |
| `build.sh` | Produces a release binary plus the OAuth credential helper. Self-healing: runs `prepare.sh` when the toolchain is missing or mismatched. Runs the static checks and the Go test suite first. |
| `install.sh` | Builds, then installs (overwriting in place) the app, the credential helper, the icon at its real pixel size, and the desktop entry. Refreshes the desktop and icon caches. `--uninstall` reverses it. User data is never touched. |
| `build/icons/generate-icons.sh` | Regenerates every application icon (launcher, tray, webview, Windows .ico) from the single source `brand/icon.svg`, so the artwork cannot drift between surfaces. |

### Dock icon unread badge

`internal/launcherbadge` publishes the unread count over D-Bus so the shell can
draw a badge on the dock/dash icon.

**It only works if the shell implements the protocol.** Vanilla GNOME Shell does
not: nothing in the shell's own source reads a badge count, verified across the
`gnome-40`…`gnome-49` and `main` branches. The badge comes from the shells that
grew one as an extension — **Dash to Dock, Ubuntu Dock, Dash to Panel**. On a
stock GNOME session the feature is inert: the object is exported, the signal is
emitted, and nothing is drawn. That is a property of the desktop, not a defect in
the app, so the code logs at debug and never fails startup.

The implementation follows the protocol as the consumers actually implement it
(verified against `dash-to-dock`'s `launcherAPI.js` and `appIconIndicators.js`):

| Detail | Value | Why |
|---|---|---|
| Interface | `com.canonical.Unity.LauncherEntry` | the protocol's name |
| Object path | `/org/unity/launcherentry/io_github_beheoxinh_Hsx2Mail` | a D-Bus path element may only hold `[A-Za-z0-9_]`, so the reverse-DNS id's dots make the suffixed path **unexportable** (godbus refuses it) |
| `Update` first argument | `application://io.github.beheoxinh.Hsx2Mail.desktop` | the consumer runs `replace(/(^\w+:|^)\/\//, '')` and matches the result against `Shell.App.get_id()`, which **keeps** `.desktop` |
| Properties | `count` (u), `count-visible` (b), `urgent` (b), plus `updating`/`progress` | the consumers only react to the signal; they never call `GetAll` |
| Flatpak | `--own-name=com.canonical.Unity.LauncherEntry` | so the name is discoverable by name; the exported object and the signal work either way |

The count is republished from the places that already refresh the sidebar
unread numbers, so it drops to zero as soon as the last unread message is read.

`internal/launcherbadge/signal_live_test.go` asserts the emitted signal against
the live session bus through `gdbus monitor` — the same tooling the shells build
on — and pins all three of the details above; each was a real bug found this way.
 Launches the real binary under its own Xvfb and checks what the unit tests cannot: that it starts, creates and migrates its database, creates its data directories with the right permissions, checkpoints the WAL, and exits on SIGTERM. Wired into `make check` and CI. |

Shared toolchain logic lives in `scripts/toolchain.sh`, because `build.sh` and
`prepare.sh` must agree on which Go wins.

Two problems this surfaced, both of which had been silently present before:

1. **The Wails CLI cannot read a newer Go's export data.** The failure is
   `internal error: package "fmt" without types was imported from <pkg>`, naming
   a different package on each run, because the Wails CLI loads packages through
   `golang.org/x/tools`. `go.mod`, CI and the Flatpak Dockerfile all pin
   `1.25.0`, so both scripts resolve to exactly that version. `build.sh` also
   recognises the message and points at `prepare.sh` instead of failing obscurely.

2. **A `GOROOT` exported by the developer's shell defeats that.** With
   `GOROOT` pointing at another toolchain's tree, the chosen `go` binary drives
   the other tree's compiler and reports
   `compile: version "go1.27.1" does not match go tool version "go1.25.0"` on
   every standard-library package. `select_go` clears `GOROOT` and
   `GOTOOLCHAIN` so each `go` derives its own.

## Verification status

Every gate below is enforced in CI (`.github/workflows/ci.yml`) and passes:

| Gate | Command | Result |
|---|---|---|
| Go build | `go build ./...` | pass |
| Go vet | `go vet ./...` | pass |
| Go tests | `go test -count=1 ./...` | 38 packages, pass |
| Go lint | `golangci-lint run` | 0 issues |
| knip | `make unused-frontend` | 0 findings |
| Go race | `go test -race -count=1 ./...` | pass, no data races |
| Go format | `gofmt -l` on touched files | clean |
| Frontend types | `cd frontend && npm run check` | 0 errors |
| Frontend lint | `cd frontend && npx eslint .` | 0 problems |
| Frontend build | `cd frontend && npm run build` | pass |
| Offline icons | `node frontend/scripts/check-offline-icons.mjs` | pass |
| Desktop entries | `desktop-file-validate` on every `.desktop` | pass |
| Flatpak manifests | YAML parse + manifest invariants + `inline-sources.py` idempotency | pass |
| Schema docs | `go run ./tools/db/schemadump` + `python3 tools/db/gen-database-doc.py` | 42 migrations, 30 tables, 42 indexes, no drift |
| No read-only transactions | `make check-tx` (`go run ./tools/db/txcheck`) | 43 packages, clean |
| txcheck unit tests | `go test ./tools/db/txcheck/` | pass |
| Frontend tests | `make test-frontend` (`cd frontend && npm run test`) | 42 tests, pass |
| Environment prepare | `./prepare.sh --check` | reports only, changes nothing |

## Known remaining debt

Everything below is deliberate. Each says what is left, why, and what it
would take.

3. **`_txlock=immediate` makes every transaction a writer** — that is the point
   (it removes `SQLITE_BUSY_SNAPSHOT`), but it means read-only work must never
   open a transaction. This is now enforced mechanically by
   `tools/db/txcheck` (`make check-tx`, and the `go` CI job), which resolves the
   call graph inside each package and skips `func(*sql.Tx) error` wrappers. The
   one real offender, `internal/message.GetDeletedUIDInfo`, was fixed.

4. **Undo is in-memory and per-process.** It does not survive a restart, and a
   detached composer window keeps its own stack, so an action taken in the
   composer cannot be undone from the main window. Persisting the stack (or
   sharing it over the existing IPC) would fix both; neither was done, because
   it changes the shape of the undo system rather than hardening what exists.
   What was fixed here: `Undo()` no longer pops before the action succeeds (a
   failed IMAP call used to destroy the user's only retry), a cross-account move
   is a single undo entry, and undoing a move no longer pushes the inverse move.

5. **The virtualized message list is tested, including through the real
   component.** `virtualWindow.ts` holds the window reconciliation, row geometry
   and selection-index logic as pure functions with direct unit tests, and
   `VirtualListHarness.svelte` mounts the real `@tanstack/svelte-virtual` wiring.
   On top of that, `MessageList.test.ts` mounts the actual 1800-line component
   against a stubbed Wails bridge (`src/test/setup.ts`) and asserts windowing,
   row/index correspondence, keyboard navigation and selection. Two real bugs
   came out of writing it and are fixed: search rows carried no addressing
   attribute (breaking keyboard nav and select-next while searching), and a
   malformed `latestDate` unmounted the entire list.

6. **knip is clean and gates the build.** Unused dependencies, files and exports
   all report zero. What it needed to get there:
   - re-export barrels (`components/ui/**`, `kit/**`, `keyboard/shortcuts.ts`)
     are excluded because knip cannot see the object that consumes the
     re-exported names — `KEY.PANE_FOCUS_NEXT` is used in `App.svelte` yet read
     as dead;
   - the project scope stays `frontend/src`: the extension bundles are mounted by
     the Go host at runtime and have no static entry, so including them turns
     every extension component into an "unused file";
   - store modules are excluded for aliased re-imports
     (`import { setRunBackground as updateRunBackgroundStore }`), which knip does
     not follow either;
   - `ignoreDependencies` documents the four required without a direct import
     (`dictionary-*` copied by a build script, `date-fns-tz` used from
     `vite.config.ts`, `tslib` as a transitive peer, `@tanstack/virtual-core`
     imported for its types only).

   Roughly a dozen genuinely unreferenced helpers were deleted; the three that
   looked dead but had real consumers (`createComposerWindowApi`,
   `handleThemeChanged`, `registerExtensionShortcut`) were restored rather than
   removed.
