# PERFORMANCE — where the limits are and why

**Status: canonical.** This file owns every size limit, timeout, batch size, cache
budget and concurrency cap, with the reason each one exists. It is a catalogue of
trade-offs, not a tuning guide for your machine — the values are chosen, not discovered,
and changing one changes behaviour.

---

## 1. Size limits (memory exhaustion guards)

`internal/sync/engine.go:44-46`:

| Constant | Value | Guards |
|---|---|---|
| `maxPartSize` | `10 MB` | A single MIME part. Stops one malformed `Content-Length` from allocating unbounded memory. |
| `maxMessageSize` | `50 MB` | The entire raw message. |
| `headerBatchSize` | `50` | Messages per header-fetch batch. |
| `bodyBatchMaxBytes` | `512 KB` | Per body-fetch batch. |
| `bodyBatchMaxMessages` | `50` | Per body-fetch batch. |
| `bodyBatchMinMessages` | `1` | Floor, so a batch always makes progress. |
| `bodyBatchQueryLimit` | `200` | Query more candidates than a batch can carry, so the fetch loop is not limited by the query. |

Body batching is deliberately **hybrid** — byte *and* count — following Geary. Either
limit alone is wrong: a count limit lets fifty 4 MB messages allocate 200 MB, and a byte
limit lets one oversized message blow the budget. Whichever limit is hit first ends the
batch.

`internal/sync/parse.go:807` has `maxFallbackSize = 10 * 1024` for a charset-detection
fallback — a deliberately small guess, because the guess only picks an encoding; it never
becomes message content.

---

## 2. Concurrency caps

| Cap | Value | Where | Why |
|---|---|---|---|
| `MaxConcurrentAccountSyncs` | `3` | `internal/sync/scheduler.go:34` | Boot-storm control. A 3-slot semaphore channel. |
| `InitialJitterWindow` | `30 s` | `internal/sync/scheduler.go:38` | Spreads a boot's account fan-out over 30 s instead of one spike. |
| `BackoffBase` / `BackoffMax` | `2 min` / `60 min` | `internal/sync/scheduler.go:42-47` | Exponential failure backoff, doubling, clamped. |
| `MaxConnections` (IMAP pool) | `3` per account | `internal/imap/pool.go:55-62` | One per concurrent sync + IDLE + slack. |
| `MaxConnections` (IPC) | `32` | `internal/ipc/server.go:37` | Composer windows are short-lived; a handful concurrent is generous. |
| `MaxOpenConns` (SQLite) | `12` | `internal/database/database.go:23` | WAL allows one writer; more connections add contention. |
| `MaxIdleConns` (SQLite) | `6` | `internal/database/database.go:30` | Ceiling after per-account scaling. |
| `MAX_CACHE_BYTES` (inline attachments) | `50 MB` | `frontend/src/lib/stores/inlineAttachmentCache.ts:19` | Byte-budgeted LRU. |

The SQLite numbers are deliberately modest and are the reason `_txlock=immediate` exists
— see `DATABASE.md` §2.

---

## 3. Timeouts

### Network

| Timeout | Value | Where |
|---|---|---|
| IMAP connect | `30 s` | `internal/imap/client.go:82`, `DefaultPoolConfig` |
| IMAP pool waiter | `2 min` | `internal/imap/pool.go:60` — "don't wait forever for a connect" |
| IMAP pool idle connection | `5 min` | `internal/imap/pool.go:57` |
| SMTP connect | `30 s` | `internal/smtp/client.go:80` |
| IDLE cycle | `10 min` | `internal/imap/idle.go:52` — a shorter cycle than RFC 2177's 29 min, for better recovery |
| IDLE reconnect backoff | `1 s` → `5 min` | `internal/imap/idle.go:53-54` |
| IDLE reconnect attempts | `10` | `internal/imap/idle.go:55` |
| IDLE event send | `2 s` | `internal/imap/idle.go:56` — never block forever on a channel send |
| Socket teardown | `5 s` | `internal/imap/idle.go:47` (`closeTimeout`) — bounds graceful-then-hard teardown for both the IDLE shutdown wait and `Client.Close`'s LOGOUT. A server that stops answering must never be able to block shutdown. |
| Post-wake network wait | `30 s` | `app/background.go` — see `BACKGROUND.md` §6 |

### Database

| Timeout | Value | Where |
|---|---|---|
| `busy_timeout` | `30000` (30 s) | DSN pragma, `internal/database/database.go:67` |
| WAL checkpoint interval | `5 min` | `internal/database/database.go:37` |
| Keyring re-probe | `30 s` | `internal/credentials/keyring.go:41` |

`busy_timeout` is a floor, not a fix: it does **not** help with `SQLITE_BUSY_SNAPSHOT`,
which is why the DSN also sets `_txlock=immediate`. See `DATABASE.md` §2.

---

## 4. Debounce and delay

| Constant | Value | Where | Why |
|---|---|---|---|
| `SyncFolder` debounce | `500 ms` | `app/sync.go:23` | A burst of `SyncFolder` calls for the same folder (notification click + manual refresh + IDLE wake) collapses into one IMAP round trip. |
| FTS indexing delay | `5 s` | `app/app.go:856` | Let the initial sync finish before indexing, or the indexer competes with the folder fetches it depends on. |
| `ownFlagEchoSuppress` | `5 s` | `app/background.go:320` | Suppresses the flag echo of our own own-flag change coming back from the server, which would otherwise loop. |

---

## 5. Indexes that exist for a specific query

v42 added eight indexes, each for one named query. Full definitions and rationale are in
`DATABASE.md` §11; the mapping is:

| Index | Query it serves |
|---|---|
| `idx_messages_needs_body` (partial) | `GetMessagesWithoutBody*` — the "never fetched and not permanently failed" branch becomes an index range scan already in date order |
| `idx_messages_account_date` | Account-wide date walks; retention prune |
| `idx_messages_folder_thread_date` | `GetConversation` — folder, then thread, then chronological |
| `idx_messages_folder_conv` (expression) | Conversation list and count — feeds `GROUP BY COALESCE(thread_id, id)` from the index instead of a temp B-tree per page |
| `idx_messages_thread_norm` (expression) | Thread lookup with `REPLACE()`-stripped angle brackets |
| `idx_messages_message_id_norm` (expression) | Same for `Message-ID` |
| `idx_messages_in_reply_to_norm` (expression) | Batched `FindThreadID` — one lookup per reference |
| `idx_messages_account_message_id` | Batched `FindThreadID`, keyed `(account_id, message_id)` |

Two general rules when adding an index here:

1. **An index is a write cost.** Every `INSERT`/`UPDATE` on `messages` pays for all eight.
   Do not add one speculatively.
2. **The normalisation indexes exist because IMAP delivers angle brackets.** If you write
   a query against `thread_id`, `message_id` or `in_reply_to` **without** stripping them,
   the index will not be used and you will get a full scan. That is the single most
   common way to lose the benefit of v42.

Verify with:

```bash
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db \
  'EXPLAIN QUERY PLAN <your query>;'
```

If the plan says `SCAN messages` where you expected a `SEARCH` with `USING INDEX`, the
predicate does not match the index.

---

## 6. Frontend

### Virtualized conversation list

`MessageList.svelte:1262-1300`. `@tanstack/svelte-virtual`, `overscan: 8`, with
per-density height estimates (`micro 64`, `compact 80`, `standard 96`, `large 112`)
corrected on mount by each row's `ResizeObserver`. See `FRONTEND.md` §5 for the full
configuration and the three invariants.

`overscan` is the tuning knob. Higher smooths fast scrolling at the cost of creating rows
you cannot see; 8 is the chosen balance.

### Byte-budgeted attachment cache

`inlineAttachmentCache.ts` uses `MAX_CACHE_BYTES = 50 MB`, evicting LRU on overflow.
A **count**-based limit would be wrong: inline attachments vary by three orders of
magnitude, so any count that is safe for small images is unbounded for large ones.

---

## 7. Retention as a performance control

The retention rules in `DATABASE.md` §12 are the only mechanism that keeps the database
from growing forever. In particular:

- Message retention is **folder-scoped** and derived from the folder's sync period. An
  account-wide delete would wipe Sent/Trash/Archive along with INBOX, which is why
  `DeleteOlderThanInFolder` refuses to do that.
- `messages.body_failed` stops an unparseable message from being re-fetched from IMAP on
  every sync cycle forever.
- Staged attachment blobs are swept after 7 days, with a reference check so two drafts
  sharing identical bytes do not strip each other.

**No `VACUUM` runs on a schedule.** Freed pages are reused, so the file does not grow, but
it also does not shrink. Run `VACUUM` manually with the app closed if you need the space
back.

---

## 8. Measuring

There is no log file and no metrics endpoint. To measure, use the structured log at debug
level plus SQLite's own instrumentation.

```bash
# Structured timings from the sync engine.
hsx2mail --debug 2>&1 | grep -E '"component":"(sync|imap|message|database)"'

# What SQLite actually does with a query.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db 'EXPLAIN QUERY PLAN <query>;'

# Where the space went.
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db \
  "SELECT name, SUM(pgsize)/1048576.0 AS mb FROM dbstat GROUP BY name ORDER BY mb DESC LIMIT 15;"

# WAL growth (should be bounded by journal_size_limit = 64 MB).
ls -la ~/.local/share/hsx2mail/hsx2mail.db*
```

`dbstat` needs the `SQLITE_ENABLE_DBSTAT_VTAB` build option; if the query above returns
nothing, use `SELECT page_count * page_size / 1048576.0 FROM pragma_page_count,
pragma_page_size;` for the total and `dbstat` per table only if available.

---

## 9. When changing a number here

| Change | Must also update |
|---|---|
| Any timeout or batch size | The `// Why` comment at its declaration, and this table |
| Any new index | `DATABASE.md` §11 (regenerate: `go run ./tools/db/schemadump`) and the write-cost note in §5 |
| Any new cache | A byte budget, not a count |
| Any new concurrency cap | A reason it is *not* larger, and what breaks if it is |

A limit without a stated reason is a limit nobody dares raise. A limit with a stated
reason is a decision someone can revisit.

---

## 10. Related documents

| Topic | Owner |
|---|---|
| Limits, timeouts, budgets, indexes rationale | this file |
| Index definitions, PRAGMAs, retention | `DATABASE.md` |
| Store graph, virtualization, cache | `FRONTEND.md` |
| Scheduler, boot storm, IDLE, sleep/wake | `BACKGROUND.md` |
| Build and targeted test commands | `OPERATIONS.md` |
