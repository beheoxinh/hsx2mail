# 10 — Email Sync Subsystem: IMAP Engine, Connection Pool, Scheduler & IDLE

Analysis date: 2026-09-29 · Commit: working tree at `v0.3.2` · Read-only review.
Scope: `internal/sync/**` (engine, messages, fetch, condstore, threading, folders, scheduler, search, helpers),
`internal/imap/**` (client, pool, idle, xoauth2, errors, events), `app/sync.go`, `app/background.go`,
plus the store/DB layer they depend on (`internal/message/store.go`, `internal/message/fts.go`,
`internal/message/attachment_store.go`, `internal/database/**`).

Companion docs: `40-message-store-search.md` covers the query layer in depth; this document covers
only the queries the **sync path** issues and the sync-specific correctness/perf problems.

---

## 1. Architecture Walkthrough

### 1.1 Entry points

There are **five** independent paths that can drive a sync, and they do not share a single
deduplication or back-pressure mechanism:

```
                       ┌──────────────────────────────────────────┐
                       │              app.App                     │
                       └──────────────────────────────────────────┘
   (a) SyncFolder(accountID, folderID)          app/sync.go:22
       ← Wails binding (user clicks folder)
       ← app/sync.go:193  (DeleteFolder cleanup)
       ← app/draft.go     (syncDraftToIMAP / DeleteDraft)
   (b) SyncAccountComplete(accountID)            app/sync.go:199
       ← SyncAllComplete()                      app/sync.go:350
   (c) SyncAllComplete()                        app/sync.go:350  (sidebar "sync all")
   (d) syncScheduler.syncAccountInbox(acc)      internal/sync/scheduler.go:212
       ← run() ticker          internal/sync/scheduler.go:144
       ← TriggerSync(acc)      internal/sync/scheduler.go:452
       ← TriggerSyncAll()      internal/sync/scheduler.go:473
   (e) syncScheduler.SyncAccountInboxBlocking() internal/sync/scheduler.go:489
       ← handleIdleNewMail     app/background.go:199
       ← handleIdleExpunge     app/background.go:436
```

Three independent dedup mechanisms exist, and they are **not** equivalent:

| Mechanism | Scope | Location |
|---|---|---|
| `syncLastRequest` + 500 ms debounce | per `accountID:folderID` | `app/sync.go:32-38` |
| `syncContexts` existence check | per `accountID:folderID` | `app/sync.go:42`, `app/background.go:188` |
| `Scheduler.syncing` bool | per `accountID` (INBOX only) | `internal/sync/scheduler.go:215` |

Paths (a)/(b)/(c) consult the first two; paths (d)/(e) consult only the third. A manual
`SyncFolder` and a scheduler `syncAccountInbox` for the same account **do not see each other**.

### 1.2 Two-phase sync (headers → bodies)

```
SyncMessages  (internal/sync/messages.go:33)
  │
  ├─ 1. pool.GetConnection                        messages.go:53
  ├─ 2. GetMailboxStatus  (STATUS, pre-SELECT)     messages.go:69   ← extra RTT, intentional
  ├─ 3. SelectMailbox (CONDSTORE if supported)     messages.go:77
  │       client.go:617-618 sets SelectOptions{CondStore:true}
  ├─ 4. UIDValidity change?  → DeleteByFolder, prevModSeq=0   messages.go:96-105
  ├─ 5. DeleteOlderThan(account, sinceDate)       messages.go:116  ⚠ account-wide
  ├─ 6. GetAllUIDs(folder)  → localUIDSet          messages.go:126  ⚠ full folder scan
  ├─ 7. fetchUIDsSince | fetchAllUIDs → remoteUIDs messages.go:145-150
  ├─ 8. set-diff:  newUIDs / deletedUIDs / existingUIDs   messages.go:183-246
  │       └─ deletedUIDs: per-UID GetByUID + 2× ExistsInFolder   messages.go:223-227  ⚠ N+1
  ├─ 9. runFlagSync(existingUIDs)                 messages.go:255 → condstore.go:118
  │       ├─ full  : syncMessageFlags(existingUIDs)        messages.go:502  (batch 500)
  │       └─ incr  : syncMessageFlagsChangedSince(prev)    condstore.go:189 (FETCH 1:* CHANGEDSINCE)
  ├─ 10. header batches of headerBatchSize=50      messages.go:281
  │       └─ fetchMessageHeaders → per-msg Upsert + FindThreadID   messages.go:815, 861  ⚠ N+1
  └─ 11. f.HighestModSeq = nextModSeq(...)         messages.go:372
          f.TotalCount = int(mailbox.Messages)    messages.go:373   ⚠ server total, not local

     ↓ caller spawns goroutine (app/sync.go:117) or Scheduler path skips it

FetchBodiesInBackground  (internal/sync/fetch.go:425)
  │
  ├─ 1. pool.GetConnection (one conn for whole run)  fetch.go:450
  ├─ 2. SelectMailbox ONCE                          fetch.go:459
  ├─ 3. CountMessagesWithoutBody                    fetch.go:465   ⚠ COUNT over OR predicate
  ├─ 4. heartbeat goroutine, 30 s ticker            fetch.go:516-531  ⚠ data race
  └─ 5. loop:
        ├─ GetMessagesWithoutBodyAndSize(200)      fetch.go:589   ⚠ no supporting index
        ├─ byte+count batching (512 KB / 50)        fetch.go:611-647
        ├─ GetMessageUIDsAndFolder(batchIDs)        fetch.go:659
        ├─ fetchMessageBodiesBatch (pipelined)      fetch.go:670
        ├─ markUnresolvedAsFailed(...)              fetch.go:551   ⚠ permanent-loss path
        ├─ UpdateBodiesBatch (1 txn)                fetch.go:554
        ├─ CreateBatch(attachments)   (1 txn)      fetch.go:566   ⚠ plain INSERT, no dedupe
        └─ next iteration consumes previous goroutine's channel  fetch.go:538
```

### 1.3 Connection pool

```
GetConnection(accountID)                     internal/imap/pool.go:121
  │
  ├─ reuse?  scan conns, take conn.mu, if !inUse && isHealthyLocked()  pool.go:125-137
  │            ⚠ 2 nested locks: p.mu → conn.mu, per candidate
  ├─ under MaxConnections (3)?  → createConnection                    pool.go:144
  └─ else → enqueue waiter chan (cap 1), block on                      pool.go:161-198
        ctx.Done()      → remove self, return ctx.Err()
        WaiterTimeout  → remove self, return error (2 min)

Release(conn)                                pool.go:295
  ├─ conn.mu: inUse=false, lastUsed=now
  ├─ p.mu: if unhealthy → DROP (never ForceClose! → socket leak)     pool.go:313-317
  ├─ if not in p.connections → drop                                    pool.go:328-333
  └─ hand to p.waiters[acct][0] or leave idle                          pool.go:336-348

Discard(conn)                                pool.go:356
  └─ p.mu held, conn.mu held, client.ForceClose()                     pool.go:364-370
     ⚠ ForceClose → Logout()+Close() under BOTH locks

CleanupIdle (every 1 min)                    pool.go:444
  └─ p.mu held, conn.mu held, client.ForceClose() per idle conn        pool.go:459-461
```

`p.mu` is a **single global mutex for all accounts**. Any blocking socket call made while holding it
stalls every account's connection acquisition.

### 1.4 IDLE

```
IdleConnection.run                            internal/imap/idle.go:148
  └─ loop:
       ├─ ctx.Done / stopCh / offline → return
       ├─ ensureConnected(ctx)                   idle.go:184, 230
       │    └─ new imapclient with UnilateralDataHandler
       │         Mailbox(EXISTS) → sendEvent(EventNewMail)   idle.go:245-256
       │         Expunge        → sendEvent(EventExpunge)    idle.go:258-270
       │         Fetch(FLAGS)   → sendEvent(EventFlagsChanged) idle.go:272-284
       │         client.Select("INBOX", nil)   ← NO CondStore option   idle.go:403
       │    ⚠ folder hardcoded "INBOX"                            idle.go:82
       ├─ backoff reset, attempts = 0                             idle.go:211-212
       └─ idleCycle(ctx)                                          idle.go:418
            ├─ NOOP health check (1 RTT)                          idle.go:431
            ├─ Idle() + timer(IdleTimeout = 10 min)               idle.go:440-444
            └─ select ctx / stopCh / timer                         idle.go:451-468
```

Consumption side:

```
processIdleEvents                             app/background.go:100
  └─ single goroutine, reads idleManager.Events() (cap 100)       idle.go:498
       EventNewMail      → go handleIdleNewMail        ⚠ unbounded goroutine  background.go:127
       EventExpunge      → AfterFunc(1 s) → handleIdleExpunge     background.go:136-141
       EventFlagsChanged → AfterFunc(1 s) → reconcileInboxFlags    background.go:145-152
```

Note the asymmetry: `FlagsChanged` and `Expunge` are debounced, `NewMail` is not.

---

## 2. Tuning constants

| Constant | Value | File:line | Note |
|---|---|---|---|
| `debounceMs` (SyncFolder) | 500 ms | `app/sync.go:23` | local const, not shared |
| cancel-then-sleep | 100 ms | `app/sync.go:47` | **releases global `syncMu` while sleeping** |
| `idleFlagResyncDebounce` | 1 s | `app/background.go:309` | |
| `idleExpungeDebounce` | 1 s | `app/background.go:314` | |
| `ownFlagEchoSuppress` | 5 s | `app/background.go:319` | |
| `idleFlagBusyRetryDelay` | 5 s | `app/background.go:348` | |
| `idleFlagBusyMaxRetries` | 1 | `app/background.go:349` | magic number, 1 attempt only |
| `syncCooldown` (post-wake) | 2 min | `app/background.go:781` | |
| folder-sync concurrency (app) | 2 | `app/sync.go:225` | magic, inline |
| folder-sync concurrency (scheduler) | 2 | `internal/sync/scheduler.go:358` | **duplicated literal** |
| `folderStatusWorkers` | 5 | `internal/sync/folders.go:22` | |
| `headerBatchSize` | 50 | `internal/sync/engine.go:32` | |
| `bodyBatchMaxBytes` | 512 KB | `internal/sync/engine.go:37` | |
| `bodyBatchMaxMessages` | 50 | `internal/sync/engine.go:38` | |
| `bodyBatchMinMessages` | 1 | `internal/sync/engine.go:39` | |
| `bodyBatchQueryLimit` | 200 | `internal/sync/engine.go:40` | |
| large-mailbox threshold | 1000 | `internal/sync/fetch.go:614` | magic, inline |
| large-mailbox batch msgs | 25 | `internal/sync/fetch.go:615` | |
| large-mailbox batch bytes | 256 KB | `internal/sync/fetch.go:616` | |
| unknown-size assumption | 10 KB | `internal/sync/fetch.go:634` | |
| `flagBatchSize` | 500 | `internal/sync/messages.go:508` | local const |
| `maxPartSize` | 10 MB | `internal/sync/engine.go:45` | |
| `maxMessageSize` | 50 MB | `internal/sync/engine.go:46` | |
| `maxInlineContentSize` | 5 MB | `internal/sync/engine.go:47` | |
| `maxMessageRetries` | 3 | `internal/sync/engine.go:66` | |
| `maxConnectionRetries` | 3 | `internal/sync/engine.go:67` | |
| `flagFullReconcileThreshold` | 2000 | `internal/sync/condstore.go:93` | |
| `flagFullSweepEvery` | 10 | `internal/sync/condstore.go:97` | |
| `Pool.MaxConnections` | 3 | `internal/imap/pool.go:57` | **per account** |
| `Pool.IdleTimeout` | 5 min | `internal/imap/pool.go:58` | |
| `Pool.ConnectTimeout` | 30 s | `internal/imap/pool.go:59` | **unused** — see TD-9 |
| `Pool.WaiterTimeout` | 2 min | `internal/imap/pool.go:60` | |
| pool cleanup interval | 1 min | `internal/imap/pool.go:487` | |
| pool retry on max-conns | 15 s | `internal/imap/pool.go:258` | |
| `Idle.IdleTimeout` | 10 min | `internal/imap/idle.go:45` | RFC 2177 allows 29 min |
| `Idle.ReconnectBackoff` | 1 s | `internal/imap/idle.go:46` | |
| `Idle.MaxReconnectBackoff` | 5 min | `internal/imap/idle.go:47` | |
| `Idle.MaxReconnectAttempts` | 10 | `internal/imap/idle.go:48` | **lifetime, not consecutive** |
| `Idle.EventSendTimeout` | 2 s | `internal/imap/idle.go:49` | drop on timeout |
| `Idle.ShutdownTimeout` | 5 s | `internal/imap/idle.go:50` | |
| `Idle.events` cap | 100 | `internal/imap/idle.go:498` | |
| scheduler `checkInterval` | 1 min | `internal/sync/scheduler.go:66` | |
| scheduler startup delay | 10 s | `internal/sync/scheduler.go:131` | |
| scheduler sync timeout | 30 min | `internal/sync/scheduler.go:223` | |
| `secondarySyncInterval` floor | 10 min | `internal/sync/scheduler.go:338` | |
| `MaxOpenConns` (SQLite) | 12 | `internal/database/database.go:23` | |
| `BaseIdleConns` / per-account | 3 / 1 | `internal/database/database.go:26,33` | |
| WAL `CheckpointInterval` | 5 min | `internal/database/database.go:37` | |
| WAL checkpoint after `SyncFolder` | every call | `app/sync.go:100` | **per-folder**, not per-sync |
| FTS `batchSize` | 200 | `internal/message/fts.go:155` | |

---

## 3. Findings

### P0

| # | Severity | File:line | Problem | Impact | Suggested fix |
|---|---|---|---|---|---|
| 1 | **P0** | `internal/message/store.go:1036-1040`, called from `internal/sync/messages.go:116` | `DeleteOlderThan(accountID, before)` issues `DELETE FROM messages WHERE account_id = ? AND date < ?` — **account-wide**, not folder-scoped, and it runs on *every* `SyncMessages` call. | Every sync hard-deletes (and CASCADE-orphans nothing, but drops) Sent/Trash/Archive/Drafts/Starred messages older than the sync period, in folders that were never asked to be pruned. `SELECT`-only folders silently lose local history that the server still has, forcing a full re-download on the next sync of those folders. Requires a **manual re-sync of every pruned folder**. | Scope by folder: `WHERE folder_id = ? AND date < ?`, and call it only from the folder actually being synced. Or move pruning to an explicit user-triggered "clean up old mail" action. |
| 2 | **P0** | `internal/message/store.go:1038` (index list `internal/database/migrations.go:137,139`) | The DELETE filters on `account_id = ? AND date < ?`, but the only indexes are `idx_messages_account(account_id)` and `idx_messages_date(date DESC)` — two **single-column** indexes. SQLite can use only one per scan. | Full `messages` table scan + row-by-row delete on every folder sync, every account. Cost grows linearly with total mail stored, not with the rows actually being deleted. | Add `CREATE INDEX idx_messages_account_date ON messages(account_id, date)` (v42 migration). |
| 3 | **P0** | `internal/sync/messages.go:223-227` | Per **deleted** UID: `GetByUID(folderID, uid)` + `ExistsInFolder(msg.MessageID, TypeTrash, ...)` + `ExistsInFolder(msg.MessageID, TypeSpam, ...)`. Three queries per UID, inside a loop, no batching, no transaction. | After a server-side bulk delete or a Gmail label change, N deleted UIDs ⇒ 3N queries. For a 5 000-message folder purge that is 15 000 round-trips to SQLite — while the IMAP connection is held. | One query: `SELECT message_id FROM messages WHERE folder_id = ? AND uid IN (...)`, then one `SELECT DISTINCT message_id, folder_type FROM ...` for trash/spam, both in a single transaction. |
| 4 | **P0** | `internal/message/store.go:620-634` (`ON CONFLICT ... DO UPDATE`) reached from `internal/sync/messages.go:815` and `internal/sync/header_recovery.go:137` | Header sync calls `Upsert` with `BodyText: ""`, `BodyHTML: ""`, `BodyFetched: false`. The `DO UPDATE SET` clause **unconditionally** overwrites `body_text=excluded.body_text, body_html=excluded.body_html, body_fetched=excluded.body_fetched`. | Any time a header `Upsert` lands on a UID that already has a downloaded body, the body is **destroyed** and the message is re-queued for a full re-download. Reachable via: (a) `recoverFailedHeaderBatch` re-fetching a UID that a prior partial pass already inserted, (b) a server that reuses a UID without bumping UIDVALIDITY, (c) any future caller that upserts headers onto existing rows. Silent, and expensive — full re-download of bodies + attachments. | Guard the update: `body_text=CASE WHEN excluded.body_fetched=0 THEN messages.body_text ELSE excluded.body_text END`, same for `body_html`/`body_fetched`. Better: split into `InsertHeader` (INSERT … ON CONFLICT DO NOTHING) and `UpdateBody` (targeted UPDATE). |
| 5 | **P0** | `internal/message/attachment_store.go:194-230` | `CreateBatch` is a bare `INSERT INTO attachments … VALUES (?,…)` in a transaction. No `ON CONFLICT`, no delete-first, and **nothing anywhere deletes a message's attachments before re-inserting** (only `DeleteAttachmentsForFolder`, `attachment_store.go:237`, is folder-scoped and unused by the sync path). | Every re-fetch of an already-processed body **duplicates every attachment row** for that message. Storage doubles per re-fetch, and the UI shows the same attachment N times. Directly triggered by finding #4. | `DELETE FROM attachments WHERE message_id = ?` inside the same transaction before the batch insert, or add a `UNIQUE(message_id, filename, content_id)` and use `ON CONFLICT DO UPDATE`. |
| 6 | **P0** | `internal/sync/fetch.go:361-397`, called from `fetch.go:551` and `fetch.go:739-746` | `markUnresolvedAsFailed` flags every requested ID whose body is empty **and which the server did not return at all**. `sizes[id]` is absent ⇒ `reported=0` ⇒ `shouldChargeFailure` returns `true` (fetch.go:327-329) ⇒ the ID is charged. | A single truncated/partial IMAP `FETCH` response, or a transient connection blip that drops the last few messages of a batch, **permanently** marks those messages `body_failed=1`. They are then excluded by `GetMessagesWithoutBodyAndSize` (store.go:955) and `CountMessagesWithoutBody` (store.go:1002) **forever** — no TTL, no retry budget, no user-facing "retry download". Silent permanent message-body loss. | Distinguish "server returned it, body was empty/unparseable" from "server did not return it". Only charge failures for IDs present in `sizes`. Give `body_failed` a retry counter (`body_failed_attempts`) and expire it after N cycles, plus a UI "retry failed downloads" action. |
| 7 | **P0** | `internal/sync/fetch.go:554-557` | On `UpdateBodiesBatch` error the code does `failed += result.fetchedCount` and continues. No state changed, so the next `GetMessagesWithoutBodyAndSize(folderID, 200, …)` (fetch.go:589) returns the **identical** candidate set (`ORDER BY date DESC LIMIT 200`, no cursor). | Infinite retry loop on any persistent DB write error, at full IMAP-fetch rate, until the process dies. The only escape is `markUnresolvedAsFailed` at fetch.go:551 — which is the permanent-loss path of finding #6. So a transient `SQLITE_BUSY` becomes permanent body loss plus a busy-loop. | On `UpdateBodiesBatch` failure, return the error (abort the body fetch) or mark those IDs `body_failed` with the attempt counter from #6. Add a bounded total-iteration guard. |
| 8 | **P0** | `internal/sync/messages.go:126-132` + `internal/sync/messages.go:183-196` | Every `SyncMessages` loads **all** local UIDs for the folder into a `map[uint32]bool` (`GetAllUIDs`, store.go:839 — `SELECT uid FROM messages WHERE folder_id = ? AND uid > 0`, no LIMIT) **and** all remote UIDs into a `map[uint32]bool` (fetch.go:654-690), then materialises three more slices. | Per sync, per folder: 2 full `[]uint32` + 2 `map[uint32]bool`. A 100 000-message folder ⇒ ~1.6 MB of UIDs + ~4.8 MB of map overhead per sync, ×2, for a comparison that is almost always "N new, 0 deleted". GC pressure scales with total mail, and this runs on the IMAP connection's critical path. | UID deltas are monotonic. Track `folders.uid_next` (already persisted, messages.go:368) and use `UID SEARCH UID <lastSeenUID+1>:*` for the new-message query, then a single `SELECT COUNT`-style reconciliation for deletions (or `UID SEARCH DELETED` when the server supports it). Replace the maps with a sorted-slice merge. |
| 9 | **P0** | `internal/message/store.go:944-970` and `994-1015` | `GetMessagesWithoutBodyAndSize` / `CountMessagesWithoutBody` filter on `(body_fetched = 0 OR (body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))) AND body_failed = 0` and `ORDER BY date DESC`. The only candidate index is `idx_messages_body_fetched(folder_id, body_fetched)` (migrations.go:358). | A disjunction on `body_fetched` plus three more unindexed columns defeats the index; SQLite must scan every row in the folder and materialise a temp B-tree for the `ORDER BY`. This query runs **once per body batch** (fetch.go:589) and the COUNT runs once (fetch.go:465). For a 100 k-row folder with 2 000 bodies remaining, that is ~50 full scans per body-fetch run. | Split the predicate into two indexed branches and `UNION` them, or add a partial index: `CREATE INDEX idx_messages_needs_body ON messages(folder_id, date DESC) WHERE body_fetched = 0 AND body_failed = 0`. The "self-heal empty body" branch should be a separate, separately-indexed, rate-limited repair pass, not part of the hot loop. |
| 10 | **P0** | `internal/message/store.go:1721-1743` (called from `internal/sync/messages.go:861`, once per message) | `FindThreadID` loops over **every** reference and issues one `SELECT COALESCE(thread_id, id) FROM messages WHERE account_id = ? AND (message_id = ? OR message_id = ? OR message_id = ?) LIMIT 1` per reference. | N+1 in the hottest loop of header sync. A message with 10 references ⇒ 10 queries, and because the 3-way `OR` is not a covering index match, each is a scan of `idx_messages_message_id`. A 500-message header batch with average 8 refs ⇒ **4 000 queries per batch**, on top of the 500 `Upsert` calls from finding #11. | One query for the whole batch: `SELECT thread_id, id, message_id FROM messages WHERE account_id = ? AND message_id IN (?,?,…)` with all refs from all messages in the batch, then resolve in memory. |
| 11 | **P0** | `internal/sync/messages.go:815` → `internal/message/store.go:637-651` | `fetchMessageHeaders` streams one message at a time and calls `e.messageStore.Upsert(m)` per message. `Upsert` is a bare `s.db.Exec` — **one autocommit transaction per message**, each an fsync'd WAL commit. | 500 messages ⇒ 500 separate transactions. With `synchronous(NORMAL)` (database.go:60) that is 500 WAL commits, each contending for SQLite's single writer lock against the concurrent FTS triggers (`messages_fts_insert`/`_update`, migrations.go:412-430) that fire on every one of those writes. This serialises against the pool's 12 connections and blocks every other writer (search, folder reads) in the app. | Collect the batch into a slice and add `UpsertBatch([]*Message)` doing one `BeginTx` / prepared statement / loop / `Commit`. Batches of 50 → 1 transaction instead of 50. |

### P1

| # | Severity | File:line | Problem | Impact | Suggested fix |
|---|---|---|---|---|---|
| 12 | **P1** | `internal/sync/fetch.go:516-531` vs `fetch.go:537,558,561` | The 30 s heartbeat goroutine reads `fetched`, `failed`, and `totalWithoutBody`; the main loop writes `fetched`/`failed`. No mutex, no atomics. | Genuine data race under the Go memory model — detectable by `-race`, and in practice can report wrong counts. It is a read of `int64`-sized locals from another goroutine, so it will not corrupt memory, but it is a correctness bug and it will fail CI the moment anyone runs `make dev-race`. | Guard with `sync/atomic` (`atomic.Int64`) or take a dedicated mutex around the counter block. Cheapest correct fix: `atomic.Int64` for `fetched`/`failed`. |
| 13 | **P1** | `internal/sync/messages.go:626-628`, `internal/sync/messages.go:668-670`, `internal/imap/client.go:625-628`, `internal/sync/search.go:112-118` | Four copies of the "run `cmd.Wait()` in a goroutine so the context can cancel it" pattern. On `ctx.Done()` the caller returns immediately; the goroutine and the in-flight IMAP command keep running. The channel is buffered (cap 1) so the goroutine does not leak on send, but the **command stays pending on the connection**, which is then `Release`d back to the pool. | The next borrower of that pooled connection sees an unexpected pending command and can get a protocol desync / spurious `BAD` response, surfacing as random sync failures. Also, the abandoned goroutine pins the client for as long as the server takes to answer. | Don't abandon in-flight commands on a pooled connection. Either (a) don't `Release` a connection whose command is still pending — mark it `Discard`-only, or (b) make the IMAP client support real cancellation (close the underlying connection on ctx cancel, which is what every IMAP library does). At minimum, extract this into one `waitWithContext` helper with the discard-on-cancel behaviour. |
| 14 | **P1** | `internal/imap/client.go:299-314`; callers `internal/imap/idle.go:139,155,220` | `Client.Close()` calls `c.client.Logout().Wait()` with **no timeout**. `ForceClose()` exists precisely to avoid this (comment at 316-318) but `idleCycle`'s error path (idle.go:220) and the `run` defer (idle.go:155) use `Close()`. | If the server stops responding mid-`LOGOUT`, the IDLE goroutine blocks forever. After `MaxReconnectAttempts` is burned, the account is permanently dead until app restart, and `IdleManager.Stop()` → `m.wg.Wait()` (idle.go:530) can hang, blocking app shutdown. | Use `ForceClose()` on all error paths, and put a `<-time.After(ShutdownTimeout)` around the graceful `Close()` in `Stop()`. `ShutdownTimeout` (idle.go:50) is currently declared but not used for this. |
| 15 | **P1** | `internal/imap/idle.go:184-212` | `attempts` is reset to 0 only immediately after a **successful connect** (idle.go:211-212), not after a successful IDLE cycle. A connection that connects fine but whose IDLE cycle errors repeatedly (server closing IDLE after N seconds, TLS middlebox, `NOOP` failing) increments `attempts` on every iteration without ever being reset. | After 10 such cycles — spanning minutes of perfectly normal operation between them — the account hits `MaxReconnectAttempts` and `run()` returns (idle.go:186-193). IDLE is now permanently dead for that account; only `restartIDLE()` (app/background.go:860), called on wake/sync-all, revives it. Mail arrives silently unseen until then. | Reset `attempts = 0` after a cycle that ran for at least one `IdleTimeout` (i.e. a genuinely successful cycle), not just after connect. Alternatively track consecutive failures with a decay. |
| 16 | **P1** | `app/sync.go:52-64` and `app/sync.go:85-96` | `a.syncContexts[syncKey] = cancel` is registered at line 53, and the cleanup `defer` lives inside the goroutine spawned at line 117. If `accountStore.Get` fails (line 61-64) or `SyncMessages` returns an error (line 71-96), the function returns **before** the goroutine starts. | Two leaks per occurrence: (a) the `context.WithCancel` child is never released — it stays attached to `a.ctx` until app shutdown, (b) the `syncContexts[syncKey]` entry never clears. Consequence of (b): the next `SyncFolder` for that folder sees the stale entry, calls `cancel()` on a dead context, and then **sleeps 100 ms** (sync.go:47) before proceeding — a permanent 100 ms tax on every subsequent sync of that folder. | Move the registration to immediately before the goroutine, or add `defer` cleanup on every early-return path after line 53. |
| 17 | **P1** | `app/sync.go:46-48` | `a.syncMu.Unlock(); time.Sleep(100*time.Millisecond); a.syncMu.Lock()` — the global sync mutex (shared by every account and folder) is deliberately released and re-acquired around a fixed sleep. | Every re-sync request serialises behind a 100 ms gap. With `SyncAllComplete` fanning out 2 concurrent folder syncs (sync.go:225) across N accounts, each restart adds 100 ms to a mutex that all of them need. | Do not hold the mutex across the sleep at all — capture the cancel func, unlock, sleep, relock once. Or replace the sleep with a `sync.WaitGroup`/channel the previous goroutine signals on completion. |
| 18 | **P1** | `app/background.go:435-436` + `internal/sync/scheduler.go:531,551` | `handleIdleExpunge` calls `SyncAccountInboxBlocking`, whose comment at background.go:412-413 promises a "lightweight deletion reconcile". In reality it runs `SyncFolders` (full IMAP `LIST` + up to 5 parallel `STATUS` per mailbox, folders.go:25-120) and then `SyncMessages` (full UID SEARCH + set diff). | Every EXPUNGE burst (a bulk delete in another client, which emits one EXPUNGE per message) is debounced to 1 s — but a 500-message bulk delete produces 500 EXPUNGEs, and the 1 s window will still let several full `SyncFolders` cycles through. `SyncFolders` alone is `N_mailboxes` STATUS round-trips. A 50-folder Gmail account ⇒ 50+ IMAP round-trips per reconcile, on top of the pool being capped at 3 connections/account. | Add a `SyncFolderExpunge(accountID, folderID)` that only does the UID diff (and ideally a `UID SEARCH DELETED` when QRESYNC is available). Do not re-run `SyncFolders` from an expunge path. |
| 19 | **P1** | `internal/sync/messages.go:443-461` (in `SyncFolderFlags`) | The IDLE flag fast path loads **all** local UIDs for the folder (`GetAllUIDs`, store.go:839 — see finding #8) purely so that `runFlagSync` receives a non-empty `existingUIDs`. On the incremental path (`preferIncremental=true`, condstore.go:132-134) that slice is **never read** — `syncMessageFlagsChangedSince` ignores it (condstore.go:151-153). | One full-folder UID scan per IDLE `FlagsChanged` event. Marking 20 messages read in another client emits up to 20 FETCH unilateral responses ⇒ 20 debounced rescans ⇒ 20 full-folder UID scans. | Reorder: check `shouldUseCondStore(...)` first; only call `GetAllUIDs` when the full-reconcile branch is actually taken. |
| 20 | **P1** | `app/background.go:127` | `go a.handleIdleNewMail(event)` — one unbounded goroutine per EXISTS event, with no semaphore and no dedup. `EventsChanged` and `EventExpunge` are debounced (background.go:136,145); `NewMail` is not. | A large delivery (or a server that re-sends EXISTS) fans out into N goroutines, each calling `SyncAccountInboxBlocking` (scheduler.go:489). The `s.syncing` guard (scheduler.go:496-501) makes all but one return immediately — so it's bounded work, but N goroutines × pool-waiter entries (pool.go:161-198, each with a 2-minute `WaiterTimeout` timer) is a burst of allocation and timer pressure. | Debounce `NewMail` the same way as the other two, and/or guard with a per-account in-flight check before spawning. |
| 21 | **P1** | `internal/sync/messages.go:161-177` | The "server returned 0 but we have local" safeguard only fires when `syncPeriodDays == 0`. With the default 30-day period, a search that legitimately returns 0 (empty folder, all messages older than 30 days) proceeds to the diff and deletes **every** local message. | The safeguard is inverted relative to its own comment ("unless we're using date filtering"). Combined with finding #1 (`DeleteOlderThan` already ran), a user who raises `SyncPeriodDays` from 30 to 90 briefly gets `remoteUIDs` filtered at 90 days while the local store was pruned at 30 — mass deletion. | Compare `remoteUIDs` against the local set restricted to the same date window, not against all local UIDs. Or apply the 0-result safeguard unconditionally and rely on `DeleteOlderThan` for pruning. |
| 22 | **P1** | `internal/sync/messages.go:372-373` + `internal/sync/scheduler.go:282-288,543-550` | `f.TotalCount = int(mailbox.Messages)` is the **server's** total for the whole mailbox, regardless of the sync period. `isSyncDue` and the new-mail notification both derive from `TotalCount` deltas. | With a 30-day window, `TotalCount` reflects 10 years of mail while the local store holds 30 days. The scheduler's `updatedInbox.TotalCount > previousCount` (scheduler.go:282) can fire a "3 new messages" notification for a change that is really `+1 new / -2 expunged`. And `f.TotalCount` is never decremented for local deletions, so the sidebar total is permanently the server's, not the local count. | Track a separate `LocalCount` incremented on insert and decremented on delete, and drive notifications off the delta of *that*. Or compute `newUIDs` count from the diff (which is already exact at messages.go:183-190) and pass it out of `SyncMessages`. |
| 23 | **P1** | `internal/sync/messages.go:202-209` | The >50 % mass-deletion check is `log.Warn()` only — it logs and proceeds. There is no abort, no confirmation, no user-facing dialog. | A single bad IMAP response (server returning a partial UID set) wipes a folder. The only protection is a log line nobody reads. | Make it a hard stop: return an error and surface `folder:syncError` so the UI can ask the user. At minimum, require two consecutive cycles of >50 % loss before deleting. |
| 24 | **P1** | `internal/imap/pool.go:313-317` | `Release` drops an unhealthy connection from the pool by simply `return`ing — it never calls `ForceClose()`. The `PooledConnection.client` is left holding an open socket. | A socket leak. A dead TCP connection that is never `Close()`d lingers in the process and on the server's side until the OS keepalive reaps it (often many minutes). Repeated flapping → FD exhaustion → `Too many open files`, which takes down the whole app. | `Release` must call `conn.client.ForceClose()` on the unhealthy branch, exactly as `Discard` does at pool.go:364-370. |
| 25 | **P1** | `internal/sync/condstore.go:189-273` | `syncMessageFlagsChangedSince` builds `uidSet.AddRange(imap.UID(1), imap.UID(0))` and issues `FETCH 1:* (FLAGS) CHANGEDSINCE n` **without a size guard**, then accumulates every returned message into `flagUpdates` before a single batched write. | On a large mailbox where the server's `CHANGEDSINCE` is broken (precisely the case this code exists to defend against, per the comment at condstore.go:88-97) the response is the entire folder. A 200 000-message folder ⇒ 200 000 `FlagUpdate` structs in RAM (~50 B each ⇒ ~10 MB) plus one gigantic transaction. Worse: `runFlagSync` forces the incremental path whenever `preferIncremental` is true (condstore.go:132-134), and `SyncFolderFlags` always passes true (messages.go:459) — so the IDLE path has no threshold protection at all. | Flush `flagUpdates` in chunks of `flagBatchSize` (500, as `syncMessageFlags` already does at messages.go:508) inside the read loop, so peak memory is bounded regardless of what the server returns. Additionally, cap the total number of `CHANGEDSINCE` results and fall back to full reconciliation if the cap is hit. |

### P2

| # | Severity | File:line | Problem | Impact | Suggested fix |
|---|---|---|---|---|---|
| 26 | P2 | `internal/sync/messages.go:882-930` | `parseMessageHeaderBuffer` is dead code — the comment at 882-883 says so explicitly. 49 lines. | Maintenance surface with no callers. | Delete. |
| 27 | P2 | `internal/sync/parse.go:825-831` | `parseMessageBodyFull` is dead code (comment at 825-826). | Same. | Delete. |
| 28 | P2 | `internal/message/fts.go:145-230` vs `internal/database/migrations.go:397-430` | `FTSIndexer.indexFolder` re-inserts every row of a folder into `messages_fts` — but `messages_fts` is an external-content FTS5 table with `AFTER INSERT`/`AFTER UPDATE` triggers that already maintain it. | `IndexAllFolders` (app.go:818) duplicates the entire index on every startup, writing N rows to a table the triggers populated in the first place. Pure waste — on a 100 k-message store this is a full re-index at every launch. | Drop `FTSIndexer` entirely, or restrict it to a one-time `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')` to repair a corrupted index. |
| 29 | P2 | `internal/sync/engine.go:99,113`; `app/background.go` (`ownFlagChangeAt`) | `flagSweepCounter` (per folder) and `ownFlagChangeAt` (per account) are never pruned. | Unbounded map growth over the app's lifetime, keyed by folder/account ID. Small in absolute terms, but these maps are never rebuilt on account deletion, so deleted folders leak entries. | Prune in the same place folder/account removal happens. |
| 30 | P2 | `app/sync.go:100-103` | `a.db.Checkpoint()` runs a **full WAL checkpoint after every single `SyncFolder` call**, including the debounced no-op path. `SyncAccountComplete` fans out 2 at a time across all folders (sync.go:225). | WAL checkpoint is O(size of WAL) and blocks writers. Doing it per-folder rather than on the `CheckpointInterval` timer (database.go:37) makes the 5-minute timer redundant and adds an fsync storm during bulk syncs. | Delete the per-folder checkpoint; rely on the existing timer. |
| 31 | P2 | `app/sync.go:121-124` | Identity comparison via `fmt.Sprintf("%p", cancelFn)` to check whether `syncContexts[key]` is still "ours". | Allocates a string on every sync completion just to compare func values. Works, but obscure. | Store a monotonically increasing generation counter per `syncKey` alongside the cancel, and compare ints. |
| 32 | P2 | `internal/sync/messages.go:141-144`, `internal/imap/client.go:603-660`, `internal/sync/condstore.go:189-273`, `internal/sync/messages.go:502-610` | Four near-identical "stream FETCH, map flags, batch-write" loops; two near-identical "UIDSet from slice" builders; two near-identical connection-recovery blocks (messages.go:313-350, fetch.go:692-719). | Any bug fix (e.g. the `flagBatchSize` chunking needed for finding #25) has to be applied in 2-4 places, and the copies have already drifted (`syncMessageFlags` batches at 500, `syncMessageFlagsChangedSince` does not batch at all). | Extract `fetchFlagsForUIDSet(ctx, client, uidSet) (<-chan message.FlagUpdate, error)` and one `recoverConnection` helper. |
| 33 | P2 | `internal/sync/search.go:49-70` | `buildSearchCriteria` builds a manually-nested `Or` tree, 4 levels deep, to express `FROM \| SUBJECT \| TO \| CC \| BODY`. The `imap.SearchCriteria` type has a flat `Or [][]SearchCriteria` precisely to avoid this. | Every server must parse a 4-deep nested OR; several will reject or mangle it. The doc comment claims "maximum compatibility" while producing the least compatible form. | Flatten to `Or: [][2]SearchCriteria{{from, subject}, {to, cc}, {body, body}}` — 3 pairs, no nesting. |
| 34 | P2 | `internal/imap/idle.go:403` | `client.Select(ic.folder, nil)` — the IDLE connection never enables CONDSTORE, unlike the pooled client (`internal/imap/client.go:617-618`). | The IDLE connection's `SELECT` response carries no `HIGHESTMODSEQ`, and it cannot use `QRESYNC`. Every IDLE cycle is a bare IDLE with no resync baseline, so a reconnect loses the change window. | Pass `&imap.SelectOptions{CondStore: true, QResync: true}` when the caps allow. |

---

## 4. Confirmed logic bugs (summary)

1. **Account-wide date pruning** — `store.go:1038` deletes across all folders, not just the one being synced (#1).
2. **`Upsert` destroys bodies** — `store.go:630` overwrites `body_text`/`body_html`/`body_fetched` from a header-only upsert (#4).
3. **Attachments duplicate on re-fetch** — `attachment_store.go:206` plain INSERT, nothing deletes first (#5).
4. **Transient fetch gaps become permanent body loss** — `fetch.go:551` / `fetch.go:739` charge `body_failed` for IDs the server never returned, and nothing ever retries them (#6).
5. **DB write failure ⇒ busy-loop + permanent loss** — `fetch.go:554` (#7).
6. **UID leak in `SyncFolder` error paths** — `app/sync.go:53` registers a context that the early returns at :64 and :96 never clean up, and the 100 ms sleep at :47 then taxes every later sync of that folder (#16, #17).
7. **IDLE reconnect budget is lifetime, not consecutive** — `idle.go:211` resets `attempts` on connect, not on a successful cycle, so 10 scattered IDLE failures permanently kill the account (#15).
8. **`Release` leaks sockets** — `pool.go:313` drops unhealthy connections without `ForceClose()` (#24).
9. **`Client.Close()` can block forever** — `client.go:308` `Logout().Wait()` with no timeout, on IDLE error paths (#14).
10. **Data race on body-fetch counters** — `fetch.go:525` reads `fetched`/`failed` while `fetch.go:558` writes them (#12).
11. **Mass-deletion guard is a log line** — `messages.go:202` warns and proceeds; a partial IMAP response wipes a folder (#23).
12. **Safeguard inverted for date-filtered syncs** — `messages.go:164` only applies when `syncPeriodDays == 0`, the opposite of what is safe (#21).
13. **New-mail notifications derived from a server total** — `messages.go:373` + `scheduler.go:282` compare `TotalCount` deltas against a value that ignores the sync window and is never decremented (#22).
14. **CONDSTORE incremental path has no memory bound** — `condstore.go:246` accumulates an unbounded `flagUpdates` slice, and the IDLE path forces it with no threshold check (#25).

**Verified as *not* a bug** (checked explicitly):

- *Duplicate INSERT on re-sync* — prevented. `UNIQUE(folder_id, uid)` (migrations.go:134) + `ON CONFLICT` (store.go:620).
- *Flag leakage into the local DB* — `applyFlagsToMessage` (helpers.go:16-31) maps all six flags, and `UpdateFlagsByUIDBatch` (store.go:734) is transactional. Correct, if heavy.
- *Off-by-one in UID ranges* — `fetchMessageHeaders` batches are `i:i+50` with a clamped `end` (messages.go:289-294); `syncMessageFlags` likewise at messages.go:515-520. No off-by-one.
- *`uidSet.AddRange(imap.UID(1), imap.UID(0))`* — correct; go-imap encodes `Stop=0` as `*` (documented at condstore.go:197-200).
- *UIDVALIDITY handling* — correct: full `DeleteByFolder` + `prevModSeq = 0` reset (messages.go:96-105), and `SyncFolderFlags` defers to the scheduled sync instead of reconciling against a stale universe (messages.go:446-449).
- *CONDSTORE fallback* — correct. `runFlagSync` (condstore.go:142-167) falls back to the authoritative full reconciliation on any incremental error, and `nextModSeq` (condstore.go:78-86) pins the baseline when the cycle was incomplete.
- *Busy-poll* — none. The scheduler is a 1-minute ticker (scheduler.go:139) with an `isSyncDue` check (scheduler.go:188-208), not a spin loop.
- *`FetchBodiesInBackground` connection leak* — none. Despite the "don't use defer" comment at fetch.go:455, every exit path releases (fetch.go:460, 467, 475, 582, 590, 829) or discards (694, 704).
- *Candidate-query infinite paging* — none. `ORDER BY date DESC LIMIT 200` with no cursor is safe *because* the DB commit happens before the next query (fetch.go:552-560), so rows leave the predicate. (The failure mode is finding #7, not paging.)

---

## 5. Tech debt

**Dead code**

- `internal/sync/messages.go:882-930` — `parseMessageHeaderBuffer`, 49 lines, self-documented as unused.
- `internal/sync/parse.go:825-831` — `parseMessageBodyFull`, self-documented as unused.
- `internal/imap/pool.go:59` — `PoolConfig.ConnectTimeout` is declared and defaulted to 30 s but never read; the actual connect timeout comes from the caller's context.
- `internal/imap/idle.go:50` — `IdleConfig.ShutdownTimeout` is defaulted to 5 s; `Stop()` uses it, but `Client.Close()` on the error paths does not (finding #14).

**Duplicated logic**

- The "cancel existing sync, then sleep" block: `app/sync.go:42-49` and `app/background.go:222-230` (a "double-check no sync started" variant) — near-identical, subtly different semantics.
- Folder-sync concurrency literal `2`: `app/sync.go:225` and `internal/sync/scheduler.go:358`. Change one, forget the other.
- Connection recovery loop: `internal/sync/messages.go:313-350` and `internal/sync/fetch.go:692-719` — same discard/get/reselect/continue, different counters.
- Flag parsing (`imap.FlagSeen` → `isRead`, …): `internal/sync/messages.go:560-575` and `internal/sync/condstore.go:228-245` — identical 7-case switches, 15 lines apart in behaviour and already divergent in the `"$Forwarded", "\\Forwarded"` case ordering.
- `runFlagSync` full path: `condstore.go:142-146` and the `SyncFolderFlags` caller at `messages.go:449-461` both independently re-derive the CONDSTORE decision.
- The three "emit `folder:synced` so the spinner stops" blocks: `app/sync.go:139-152`, `app/background.go:198-208`, `app/background.go:275-280`.

**Magic numbers not in the constants table**

`100` ms cancel sleep (`app/sync.go:47`), `1000` large-mailbox threshold (`internal/sync/fetch.go:614`), `10 * 1024` unknown-size guess (`internal/sync/fetch.go:634`), `15 * time.Second` max-connection retry (`internal/imap/pool.go:258`), `2` folder concurrency (three sites), `30 * time.Second` heartbeat (`internal/sync/fetch.go:517`), `2 * time.Minute` reconcile timeout (`app/background.go:384`), `2 * time.Minute` sync cooldown (`app/background.go:781`).

**Architecture notes**

- `Scheduler.syncing` (scheduler.go:52) and `App.syncContexts` (app/sync.go) implement the same exclusion twice, with different scopes (account vs account+folder) and neither aware of the other. This is the root cause of finding #18's blast radius.
- The IDLE client and the pooled client are entirely separate connections with separate capability negotiation. Any capability added to `internal/imap/client.go` must be mirrored by hand in `internal/imap/idle.go` — which is exactly how the CONDSTORE omission (#34) happened.
- IDLE watches `INBOX` only (`idle.go:82` hardcoded). Every other folder is poll-only. This is a defensible scope decision, but it is a hardcoded string, not a configuration, and `handleIdleNewMail` reinforces it (background.go:180-183: "Get the INBOX folder ID for events").

---

## 6. Test coverage gaps

Present (16 tests, all pure-function):

| Test | File |
|---|---|
| `TestShouldUseCondStore` | `internal/sync/condstore_test.go` |
| `TestNextModSeq_*` (4 cases) | `internal/sync/condstore_test.go` |
| `TestDueForFullFlagSweep` | `internal/sync/condstore_test.go` |
| `TestShouldChargeFailure` | `internal/sync/body_failed_test.go` |
| `TestGenerateSnippet*` (5) | `internal/sync/helpers_test.go` |
| `TestParseMultipartBody*` (2) | `internal/sync/parse_tnef_test.go` |
| `TestIsConnectionError` | `internal/imap/client_test.go` |
| `TestDefaultConfig` | `internal/imap/client_test.go` |
| `TestDefaultPoolConfig` | `internal/imap/client_test.go` |
| `TestDefaultIdleConfig` | `internal/imap/client_test.go` |

**Untested — highest risk first:**

1. **`Engine.SyncMessages` (messages.go:33)** — 380 lines, the core of the app, zero tests. Every P0 in this doc that lives in the sync path (UIDValidity reset at :96, the 0-result safeguard at :161, the Gmail trash/spam carve-out at :223, `nextModSeq` application at :372) is reachable only through it. Testable with a fake `imapclient` + in-memory SQLite.
2. **`Engine.fetchMessageHeaders` (messages.go:695)** — no test. The `Upsert` body-clobber (#4) is a one-line regression test once a store fixture exists.
3. **`Engine.FetchBodiesInBackground` (fetch.go:425)** — no test. The `markUnresolvedAsFailed` semantics (#6), the empty-batch short-circuit at :738, and the `DB failure ⇒ busy loop` (#7) are all untested.
4. **`message.Store.Upsert` ON CONFLICT semantics (store.go:620)** — no test asserting which columns are overwritten. This is the single highest-value test to add: it would have caught #4 immediately.
5. **`message.Store.DeleteOlderThan` (store.go:1036)** — no test asserting folder scoping. Would have caught #1.
6. **`message.Store.FindThreadID` (store.go:1697)** — no test, and no query-count assertion. A `COUNT` based test would flag #10.
7. **`message.Store.GetMessagesWithoutBodyAndSize` (store.go:944)** — no `EXPLAIN QUERY PLAN` test. See `40-message-store-search.md`; same technique applies.
8. **`message.AttachmentStore.CreateBatch` (attachment_store.go:194)** — no test for re-insert behaviour. Would have caught #5.
9. **`Pool.GetConnection` / `Release` / `Discard` / `CleanupIdle` (pool.go)** — only `DefaultPoolConfig` is tested. Not covered: waiter queueing and FIFO order, the 2-minute `WaiterTimeout` path, the `inPool` re-check at :319-333, the `Release` unhealthy branch (#24), concurrent `GetConnection` from N goroutines against `MaxConnections=3`.
10. **`IdleConnection.run` / `idleCycle` / `ensureConnected` (idle.go)** — only `DefaultIdleConfig` is tested. Not covered: the `attempts` reset semantics (#15), `sendEvent` drop-on-timeout (idle.go:87-98), the `stopCh`/`ctx.Done` exits, the `MaxReconnectAttempts` give-up path.
11. **`Scheduler` (scheduler.go)** — zero tests. Not covered: `isSyncDue`, the `syncing` guard, `syncAdditionalFolders` concurrency limit, `secondarySyncIntervalFor`.
12. **`App.SyncFolder` debounce (app/sync.go:22-58)** — zero tests (`app/` has no `sync_test.go` or `background_test.go` at all). The 500 ms debounce, the cancel-and-restart path, and the early-return leak (#16) are all untested.
13. **`app.processIdleEvents` / `handleIdle*` (app/background.go)** — zero tests. The debounce maps (background.go:109-112), the `busy` skip (#20), and the echo suppression (`noteOwnFlagChange` / `recentOwnFlagChange`, background.go:322-333) are untested.
14. **Race detector** — nothing in `internal/sync` or `app` is exercised under `-race`, which is why #12 has survived. `make dev-race` exists; there is no `make test-race`.

**Recommended first three tests**, each targeting a P0:

```go
// 1. store_test.go — catches P0 #4 (Upsert body clobber) and P0 #5 (attachment dup)
func TestUpsertHeaderDoesNotClobberFetchedBody(t *testing.T) {
    // insert message with BodyFetched:true, BodyText:"hello", body_html:"<p>hi</p>"
    // Upsert the same (folder,uid) with BodyFetched:false, BodyText:"", BodyHTML:""
    // assert body_fetched is still 1 and body_text is still "hello"
}

// 2. store_test.go — catches P0 #1 (account-wide prune)
func TestDeleteOlderThanIsFolderScoped(t *testing.T) {
    // seed Inbox + Sent messages dated 2 years ago
    // DeleteOlderThan(...)
    // assert the Sent row still exists  ← currently FAILS
}

// 3. condstore_test.go — catches P1 #25 (unbounded CHANGEDSINCE accumulation)
func TestSyncMessageFlagsChangedSinceBatchesLargeResponses(t *testing.T) {
    // fake client returning 5000 CHANGEDSINCE messages
    // assert the number of write transactions is 10 (500/batch), not 1
}
```
