# 90 — Adversarial Verification of Analysis Docs 10-80

Verification date: 2026-09-29 · Repo: `/mnt/WannaBeTheGuy/WwW/MyOwn/hsx2mail` (v0.3.2 working tree) · Read-only.

Scope: every P0 finding and every P1 finding claiming data loss, a security hole, or a hard build failure, across
`10-sync-imap-idle.md`, `20-background-autostart-linux.md`, `30-compose-send-draft.md`, `40-message-store-search.md`,
`50-frontend.md`, `60-database.md`, `70-platform-services.md`, `80-build-packaging.md`.

## 1. Method

- Extracted every `P0` row plus the critical `P1` rows into a numbered queue (40 items).
- For each item, opened the cited source with `codegraph_explore`, then confirmed exact lines with `read` and
  `grep`. Line numbers were checked against the actual file, not the doc's copy.
- Static claims (wrong line, missing index, unconditional overwrite) were accepted on source evidence. Runtime
  magnitudes were not executed, so those sub-claims are marked unverified inline.
- The foreign-key interaction in finding NF-1 was reproduced with a standalone `sqlite3` schema.
- Lead-verified environment facts were reused: `go build ./...` passes; `go test ./...` fails only in
  `internal/database` (2 tests at `database_test.go:202` and `:379`, `duplicate column name: secondary_sync_interval`);
  no `vendor/` directory; zero `svelte-virtual` usage in `frontend/`.

Verdict codes: **CONFIRMED** (mechanism and location match) · **PARTIALLY CONFIRMED** (mechanism real, but the line,
the scope, or the impact is overstated/wrong) · **REFUTED** (claim false) · **UNVERIFIABLE** (cannot be checked here).

## 2. Verification results

| Claim ID | Source doc | Sev | Claim (abridged) | Verdict | Evidence file:line | Correction |
|---|---|---|---|---|---|---|
| 10-1 | 10-sync | P0 | `DeleteOlderThan` is account-wide (not folder-scoped) and runs on every `SyncMessages` | PARTIALLY CONFIRMED | `internal/message/store.go:1036-1038`; call `internal/sync/messages.go:117` | Mechanism real. Cited line `:116` is the comment; the call is `:117`. Impact "forces full re-download" is wrong — pruned mail is outside the retention window and never re-fetched. Runs only when `SyncPeriodDays > 0` (default 30, `migrations.go:39`). |
| 10-2 | 10-sync | P0 | `DELETE ... account_id=? AND date<?` has no composite index | PARTIALLY CONFIRMED | `internal/database/migrations.go:137,139` | No `(account_id,date)` index exists. "Full table scan" overstated: `idx_messages_account` narrows to the account before the date filter. |
| 10-3 | 10-sync | P0 | Per-deleted-UID: `GetByUID` + 2× `ExistsInFolder`, in a loop, no batching | PARTIALLY CONFIRMED | `internal/sync/messages.go:223-227` | Location exact, but the 3 queries fire only for Gmail accounts (`if isGmail` guard at `:223`). Non-Gmail is a single `DeleteByUID` (`:236`). |
| 10-4 | 10-sync | P0 | Header `Upsert` unconditionally overwrites `body_text/body_html/body_fetched` with empty values | CONFIRMED | `internal/message/store.go:620,631-632` (ON CONFLICT DO UPDATE) | Impacts a re-upsert onto a row that already has a body. |
| 10-5 | 10-sync | P0 | `attachmentStore.CreateBatch` is a bare INSERT with no conflict handling and no delete-first | CONFIRMED | `internal/message/attachment_store.go:194,206` | `DeleteAttachmentsForFolder` (`:237`) is folder-scoped and only used on force-resync; the sync body path never clears first. |
| 10-6 | 10-sync | P0 | `markUnresolvedAsFailed` permanently flags requested-but-unreturned IDs | CONFIRMED | `internal/sync/fetch.go:361,367-368,393`; `shouldChargeFailure` `:327-328` | Missing IDs have `reported=0` and are charged. Deferral exists for truncated fetches, so it is not universally permanent. |
| 10-7 | 10-sync | P0 | On `UpdateBodiesBatch` error the loop retries the identical candidate set forever | CONFIRMED | `internal/sync/fetch.go:554-557,588,600-603` | No iteration cap; a persistent write error re-selects the same rows. |
| 10-8 | 10-sync | P0 | Every sync materialises all local and remote UIDs into maps | CONFIRMED | `internal/sync/messages.go:126-132`; `internal/message/store.go:838` (`GetAllUIDs`, no LIMIT) | Structural; allocation figures unverified. |
| 10-9 | 10-sync | P0 | `GetMessagesWithoutBodyAndSize`/`CountMessagesWithoutBody` predicate defeats `idx_messages_body_fetched` | PARTIALLY CONFIRMED | `internal/message/store.go:944-957,994-1017`; index `migrations.go:358` | Query shape confirmed; index-defeat and "~50 full scans" are unverified by execution. |
| 20-F-01 | 20-bg | P0 | Background mode has no tray icon, no restore affordance | CONFIRMED | `go.mod` (no `systray` dependency); `app/app.go:858,892-902,912` present | Fact confirmed; classification is usability, not data loss/security. |
| 20-F-02 | 20-bg | P0 | Flatpak autostart writes host-side `Exec=hsx2mail` | CONFIRMED | `internal/platform/autostart_linux.go:118` | `"commandline": []string{"hsx2mail"}` exact. |
| 20-F-03 | 20-bg | P0 | `EADDRINUSE` treated as stale; live-but-slow instance is unlinked | PARTIALLY CONFIRMED | `internal/platform/singleinstance_linux.go:54-71` | Race real. "Silent DB corruption" overstated — SQLite WAL serialises writers; the outcome is duplicate instances/sync, not corruption. |
| 30-F-01 | 30-compose | P0 | Draft autosave rewrites `imap_uid` from a stale in-memory object, orphaning the server UID | CONFIRMED | `app/draft.go:201-203,575-581`; `internal/draft/store.go:84` | Race-dependent (background `UpdateSyncStatus` must land between `Get` and `Update`). `Update` writes `sync_status, imap_uid, folder_id` from memory. |
| 30-S-01 | 30-compose | P1 (sec) | MDN writer emits remote-supplied headers without CRLF stripping | CONFIRMED | `internal/smtp/mdn.go:81,83-85` | `Original-Message-ID` / `Final-Recipient` written via raw `fmt.Sprintf`. |
| 40-1 | 40-store | P0 | FTS search joins `messages`→`messages_fts` so FTS is re-scanned per outer row | CONFIRMED | `internal/message/store.go:2234` (count), main query `:2271` region | Join shape confirmed; 171 s/178 s figures unverified. |
| 40-2 | 40-store | P0 | `GetConversation` wraps thread/message/in-reply in `REPLACE()` → non-sargable | CONFIRMED | `internal/message/store.go:1558-1560` | Function starts `:1455`. |
| 40-3 | 40-store | P0 | `ListConversationsByFolder` + `CountConversationsByFolder` are two full aggregations per page | PARTIALLY CONFIRMED | `internal/message/store.go:1300-1319` and `:1439` | Query confirmed; "two independent full aggregations fired in parallel" is an app-layer pattern, not proven in the store. |
| 40-4 | 40-store | P0 | `GetByIDs` selects `body_text, body_html` for callers needing only uid/folder | CONFIRMED | `internal/message/store.go:2111` (within SELECT `:2108-2117`) | — |
| 50-F-01 | 50-front | P0 | `EventsOff(...)` removes every listener for the event name | PARTIALLY CONFIRMED | `frontend/src/lib/components/list/MessageList.svelte:258-263` (fts events at `:262-263`) | Call sites confirmed. Wails `removeListener()` semantics were not verified against runtime source. |
| 50-F-02 | 50-front | P0 | 16.3 MB Iconify JSON eagerly imported into the entry graph | PARTIALLY CONFIRMED | `frontend/src/lib/iconify-offline.ts:5-9` (5 collections), `:12-16` `addCollection` | Eager imports confirmed; byte size unverified. |
| 50-F-03 | 50-front | P0 | Message list is not virtualized | CONFIRMED | `MessageList.svelte` `{#each}`; lead-verified zero `svelte-virtual` usage | — |
| 50-F-04 | 50-front | P0 | Theme applied only after `await loadSettings()`→`initTheme` in `onMount` | CONFIRMED | `frontend/src/App.svelte:389-390` (inside `onMount` at `:292`) | — |
| 60-F-1 | 60-db | P0 | DSN has no `_txlock=immediate`; read-then-write transactions hit `SQLITE_BUSY_SNAPSHOT` | CONFIRMED | `internal/database/database.go:60` | DSN contains `_pragma=...` only. |
| 60-F-2 | 60-db | P0 | `UIDValidity` change calls un-transacted `DeleteByFolder` before re-fetch | PARTIALLY CONFIRMED | `internal/sync/messages.go:97`; `internal/message/store.go:798-799` | Mechanism exact. "Permanently empty / unrecoverable" is wrong — the server retains the mail and the next sync self-heals (UIDValidity still differs → re-delete + re-fetch). |
| 60-F-3 | 60-db | P0 | Three non-transacted writes; `UpdateBody` ignores `RowsAffected` | CONFIRMED | `internal/sync/fetch.go:88,95,102`; `internal/message/store.go:877` | `UpdateBody` executes and ignores rows affected. |
| 60-F-5 | 60-db | P0 | `GetConversation` `REPLACE()` unindexable | CONFIRMED | `internal/message/store.go:1558-1560` | Duplicate of 40-2. |
| 60-F-11 | 60-db | P1 | `internal/database` suite is red on `main` | CONFIRMED | lead-verified: `database_test.go:202,379`, `duplicate column name: secondary_sync_interval` | Missing `secondary_sync_interval` (v41) in the drop list. |
| 70-F-01 | 70-plat | P1 (sec) | Pre-auth `json.Decoder` has no `io.LimitReader` | CONFIRMED | `internal/ipc/server.go:226`; no `LimitReader` in `internal/ipc` | Cited `:225`, actual decoder `:226`. |
| 70-F-02 | 70-plat | P1 (sec) | No connection cap in `AcceptLoop` | CONFIRMED | `internal/ipc/server.go:163-164` | No semaphore found. |
| 70-F-03 | 70-plat | P1 (sec) | OAuth callback interpolates query values into HTML | CONFIRMED | `internal/oauth2/server.go:173`; template `:250` | — |
| 70-F-11 | 70-plat | P1 (sec) | `getValidOAuthToken` has no mutex | CONFIRMED | `app/compose.go:60`; no `Mutex`/`singleflight` in file | — |
| 70-F-13 | 70-plat | P2 | `exec.Command` of sibling `hsx2mail-creds` with no permission/signature check | CONFIRMED | `internal/oauth2/config.go:152` (`os.Stat`), `:155` (`exec.Command`) | Only existence is checked, not mode/owner. |
| 70-F-15 | 70-plat | P1 (sec) | Keyring write failure silently falls through to AES DB, never re-probed | CONFIRMED | `internal/credentials/store.go:83`; `oauth.go:254,314`; `oauth_user_creds.go:74` | — |
| 70-F-27 | 70-plat | P1 (sec) | Body iframe sandbox allows scripts + escaping popups | CONFIRMED | `frontend/src/lib/components/viewer/EmailBody.svelte:785` | `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"`. |
| 80-F-01 | 80-build | P0 | Flatpak manifest builds `-mod=vendor` with no `vendor/` dir | CONFIRMED | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:47`; `NO_VENDOR` | Exact. |
| 80-F-02 | 80-build | P0 | `--talk-name=org.freedesktop.portal.Desktop` absent → all notifications fail | PARTIALLY CONFIRMED | `io.github.beheoxinh.Hsx2Mail.yml:10-21` (no `portal`); fallback at `internal/notification/notifier_linux.go:80-83` | Talk-name genuinely absent. "All notifications silently fail" is wrong: the notifier falls back to direct `org.freedesktop.Notifications` when the portal call fails. |
| 80-F-03 | 80-build | P0 | Flathub release tooling hardcoded to old `hkdb`/`Aerion` identity | CONFIRMED | `build/flatpak/flathub/release.sh:39-41` | — |
| 80-F-04 | 80-build | P0 | `make test` exits 1 from the two migrations tests | CONFIRMED | lead-verified `database_test.go:202,379` | Duplicate of 60-F-11. |
| 80-F-05 | 80-build | P0 | Dockerfile pins Go 1.23 / GNOME 47 against a Go 1.25 module | CONFIRMED | `build/flatpak/Dockerfile:17` (Go 1.23.0), `:38-39` (GNOME 47); `go.mod:3` (`go 1.25.0`) | Node version not re-checked. |
| 80-F-06 | 80-build | P0 | `runtime-version: '50'` is not on Flathub | PARTIALLY CONFIRMED | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:3`; source manifest `:3` | `'50'` confirmed. Flathub availability is an external fact, unverifiable offline. |

Counts (40 items): CONFIRMED 29 · PARTIALLY CONFIRMED 11 · REFUTED 0 · UNVERIFIABLE 0.

## 3. Refuted findings + correct truth

No finding was wholly false. Three findings carry an impact claim that is refuted; the mechanism survives.

1. **80-F-02 — "all notifications silently fail in Flatpak."** Refuted as worded. `internal/notification/notifier_linux.go:80-83`
   catches a portal failure and retries over direct D-Bus (`org.freedesktop.Notifications`). The missing portal
   `--talk-name` is real and degrades the preferred path, but the unconditional-failure statement does not hold.
2. **60-F-2 — "the folder is permanently empty and messages are unrecoverable."** Refuted. After `DeleteByFolder`
   at `internal/sync/messages.go:97`, the stored `UIDValidity` is only updated later; a crash leaves the old value,
   so the next sync repeats the delete-and-refetch against the server. The server is the source of truth; local loss
   is transient, not permanent.
3. **10-1 — "forces a full re-download on the next sync of those folders."** Refuted. `DeleteOlderThan` removes mail
   outside the retention window (`migrations.go:39`, default 30 days); the sync filter (`sinceDate`) excludes that
   same mail, so it is never re-fetched. The genuine defect is account-wide scope plus per-folder-sync cost, not
   re-download churn.

Also corrected: doc 70-F-04's premise that the OAuth `state` is unverified is wrong. `internal/oauth2/flow.go:164`
performs `if result.State != session.State`.

## 4. New findings others missed

| ID | Sev | File:line | Problem | Impact |
|---|---|---|---|---|
| NF-1 | P1 | `internal/message/store.go:620-621`, `internal/sync/messages.go:787-816`; `internal/database/migrations.go:147` | On every header re-upsert the code assigns a fresh UUID and writes `id=excluded.id`. `attachments.message_id` is `REFERENCES messages(id) ON DELETE CASCADE` with no `ON UPDATE` action. Reproduced with `sqlite3`: updating the parent key raises `FOREIGN KEY constraint failed (19)`. | A re-upsert of an existing message that already has attachments fails in `Upsert`; `messages.go:816-818` logs it as a warning and `continue`s, so the refreshed header/flags are silently dropped. Distinct from 10-4 (body blanking) — here the whole row update is rejected. |
| NF-2 | P2 | `internal/notification/notifier_linux.go:79-83` | Portal-first design has an undocumented direct-D-Bus fallback that bypasses `xdg-desktop-portal`. | The Flatpak hardening intent of doc 80-F-02 is partially defeated; the fallback path's permissions are not covered by any analysis doc. |
| NF-3 | P2 | `internal/message/store.go:2502-2537` | `highlightMatches` escapes text correctly (`html.EscapeString` at `:2508`) and uses `regexp.Compile` with error fallback at `:2527-2529`. | No XSS and no regex-panic here — recorded as a verified negative so it is not re-flagged later. |

## 5. Confidence summary per domain

| Domain | Confidence | Basis |
|---|---|---|
| Sync / IMAP / IDLE (doc 10) | High on mechanisms, medium on severity | All P0 call sites read directly; two lines off by one (`messages.go:117`, `store.go:838`); perf magnitudes unexecuted. |
| Background / autostart Linux (doc 20) | High | Autostart command and single-instance flow confirmed by source; tray absence confirmed via `go.mod`. |
| Compose / draft (doc 30) | Medium-high | Draft race reproduced by reading; it is timing-dependent and not runtime-tested. |
| Message store / search (doc 40) | High on query shape, low on timing | All cited SQL read; 171 s / 2.67 GB numbers not reproduced. |
| Frontend (doc 50) | Medium | Structure confirmed; Wails runtime listener semantics and 16.3 MB size unverified. |
| Database (doc 60) | High | DSN, index list, and `UpdateBody` all read; FK consequence reproduced in `sqlite3`. |
| Platform / IPC / OAuth / crypto (doc 70) | High | Every cited line located; doc 70 correctly self-reports no P0. |
| Build / packaging (doc 80) | High | Manifest flags, Dockerfile versions, repo identity, and the red test all confirmed; Flathub availability is external. |
| Cross-cutting false negatives | Medium | Priority areas checked (sync, draft, SQLITE_BUSY, memory, credential logging, HTML, notifications). NF-1 is the only high-value miss; no credential/token value was found entering logs. |
