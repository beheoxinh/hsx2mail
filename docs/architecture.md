# Hsx2Mail — Architecture

Canonical architectural reference for the Hsx2Mail desktop email client, describing the
v0.3.2 tree. Every structural claim below is grounded in the source with a `path:line`
citation. Where the older pre-rewrite version of this file (and `AGENTS.md`) disagreed with
the code, the code wins: the corrections are recorded in
`docs/analysis/90-verification.md` and `docs/analysis/95-existing-docs-audit.md`, and the
remaining open gaps are tracked in `docs/PLAN.md` and `docs/GAPS.md`.

## Where each topic lives

This file owns **structure**: how the pieces fit together, in what order, and which layer
owns what. It does *not* re-document topics that have their own canonical owner. If you
need depth on one of these, go to that file — do not read a summary here and trust it.

| Topic | Canonical owner | This file |
|---|---|---|
| Repository layout, module graph, startup order, data flow | **this file** | full detail |
| Schema, migrations v1..v42, tables, indexes, PRAGMAs, retention | `DATABASE.md` | §5 summary only |
| Build, test, release, troubleshooting | `OPERATIONS.md` | §12 summary only |
| Backup, restore, forward-only migrations, v40–v42 rollback | `SQL_ROLLBACK.md` | — |
| Startup, close semantics, autostart, tray, sleep/wake, network, scheduling | `BACKGROUND.md` | §11 summary only |
| Store graph, component tree, virtualized list, events, layout | `FRONTEND.md` | §10 summary only |
| PGP, S/MIME, TOFU, keyring, HTML sanitizing | `CRYPTO.md` | §9 summary only |
| Size limits, timeouts, batch sizes, cache budgets, index rationale | `PERFORMANCE.md` | — |
| Extension API surface and lifecycle | `EXTENSIONS.md`, `EXT_RULES.md` | §8 summary only |
| Document index and maintenance rules | `README.md` | — |

- **Product name:** Hsx2Mail. The runtime window title is still the legacy `Email Hub`
  (`main.go:139`).
- **Version:** 0.3.2 (`app/state.go:30`, `frontend/package.json:4`).
- **License / author:** see `PRIVACY.md`, `TERMS.md`, `brand/`; author beheoxinh.

---

## 1. Purpose, scope, and tech stack

Hsx2Mail is a native, cross-platform desktop email client. It is not Electron: the UI is a
Svelte 5 single-page app hosted in an OS WebView that Wails v2 drives from a Go process.
All mail, contact, calendar, crypto and persistence logic lives in Go; the frontend talks to
it through Wails bindings and Wails events.

| Layer | Technology | Evidence |
|---|---|---|
| Host shell | Wails v2.12 | `go.mod`, `wails.json` |
| Backend language | Go 1.25 | `go.mod:3` |
| Storage | SQLite via `modernc.org/sqlite` (pure Go, no CGO) | `internal/database/database.go` |
| Mail | `go-imap v2` beta (IMAP/IDLE/CONDSTORE), custom SMTP | `internal/imap/`, `internal/smtp/` |
| Frontend | Svelte 5 (runes), TypeScript, Vite 6 | `frontend/package.json`, `frontend/vite.config.ts` |
| CSS / UI | Tailwind 3, `bits-ui`, Iconify | `frontend/` |
| Rich-text editor | Tiptap v2 (ProseMirror) | `frontend/src/lib/components/composer/` |
| i18n | `svelte-i18n` (10 locales) | `frontend/src/lib/i18n/` |
| Crypto | PGP (`ProtonMail/go-crypto`), S/MIME PKCS#7, TLS TOFU | `internal/pgp/`, `internal/smime/`, `internal/certificate/` |
| Key storage | OS keyring (`github.com/zalando/go-keyring`) + AES-GCM DB fallback | `internal/keyring/keyring.go:7`, `internal/credentials/store.go` |
| Packaging | Flatpak (primary), native Linux, macOS `.app`, Windows NSIS | `Makefile`, `build/` |

### Repository layout

```
main.go, preflight.go       Entry point, CLI flags, single-instance, preflight before Wails
app/                        Wails-bound application layer (thin; no app/ -> app/ imports)
internal/                   Core business logic, no Wails imports
  account/ folder/ message/ draft/ database/ credentials/ oauth2/ settings/ appstate/
  imap/ smtp/ sync/ email/ pgp/ smime/ crypto/ carddav/ contact/ certificate/
  ipc/ platform/ notification/ keyring/ logging/ undo/ extensions/ core/api/v1/
extensions/calendar/        First-party Calendar extension (backend + frontend + manifest)
extensions/contacts/        First-party Contacts extension (backend + frontend + manifest)
frontend/                   Svelte 5 UI: src/lib/stores, src/lib/components, wailsjs/ bindings
cmd/hsx2mail-creds/         OAuth credential helper binary (sibling exec)
build/                      Flatpak, Linux, macOS, Windows packaging scripts and manifests
tools/db/                   Database rollback SQL
brand/                      Application icons
archive/                    Discontinued approaches (AppImage, old backups)
docs/                       Documentation tree (this file is canonical for architecture)
docs/analysis/              Adversarial analysis of the v0.3.2 tree (evidence, not canon)
```

---

## 2. High-level system map

Single main process plus zero-or-more detached-composer child processes.

```
┌────────────────────────────────────────────────────────────────────────────┐
│ main.go  ──flag parse──▶ single-instance lock ──▶ Preflight ──▶ wails.Run   │
│                                                     │                       │
│                                              paths, SQLite,                  │
│                                              migrations v1..v42,             │
│                                              credential store                │
└───────────────────────────────────────────────┬────────────────────────────┘
                                                 ▼
┌────────────────────────────────────────────────────────────────────────────┐
│ app.App (Wails-bound)                     app/app.go                        │
│                                                                            │
│  Stores                          Crypto                     Extensions      │
│  ├─ account / folder / message   ├─ smime.Store/Signer      ├─ contacts ext │
│  ├─ draft / settings / appstate  ├─ pgp.Store/Signer        │   Bridge      │
│  ├─ image allowlist              └─ certificate TOFU store  └─ calendar ext │
│  └─ contact / carddav                                         Bridge       │
│                                                                            │
│  Sync engine            ┌──────────────────────────────┐                   │
│  ├─ IMAP pool (3/acct)  │  IMAP IDLE (push, INBOX)     │                   │
│  ├─ scheduler (poll)    │  sync.Scheduler (poll tick)  │                   │
│  ├─ two-phase sync      └──────────────────────────────┘                   │
│  └─ FTS indexer                                                            │
│                                                                            │
│  IPC server (unix socket)  ◀── newline-delimited JSON ──┐                  │
│  OAuth2 manager                                          │                  │
│  Network / sleep-wake / theme monitors (D-Bus on Linux)  │                  │
└──────────────────────────────────────────────────────────┼─────────────────┘
                                                           │
                          spawn via exec.Command(self)      │ token on stdin
                                                           ▼
                          ┌──────────────────────────────────────────────┐
                          │ ComposerApp (hsx2mail --compose …)           │
                          │  own DB handle + own IMAP pool               │
                          │  draft save/send -> IPC -> main syncs        │
                          └──────────────────────────────────────────────┘
```

The frontend sandbox (WebView) holds only the Svelte app; it reaches Go through generated
Wails bindings (`frontend/wailsjs/`) and receives state through Wails events (`app:ready`,
`folder:synced`, `messages:updated`, `fts:progress`, `theme:system-pref`, `contacts:changed`).

---

## 3. Startup sequence (real ordering)

### 3.1 CLI and process mode (`main.go`)

```
main.go:45  flag.Parse()
main.go:49  --version short-circuit
main.go:55  --debug  -> platform.AttachConsole() on Windows
main.go:59  positional mailto: URL parsed into app.MailtoData
main.go:71  --compose  -> runComposerMode() (child process path)
main.go:75  otherwise    runMainMode()
```

### 3.2 Single-instance and preflight (main window)

```
main.go:87   lock := platform.NewSingleInstanceLock()
main.go:88   locked, _ := lock.TryLock(activateMsg)   // "show" or the raw mailto URL
main.go:92   if !locked { return }                    // second instance exits 0
main.go:99   settings.ReadNativeTitleBar(...)          // Frameless is init-time only
main.go:105  application := app.NewApp(DebugMode, *dbusNotify)
main.go:119  runPreflight(application)  -> app.Preflight (app/app.go:~420)
               ├─ logging.Init (zerolog; debug vs info)
               ├─ platform.GetPaths (XDG data/config/cache)
               ├─ paths.EnsureDirectories (0700)
               ├─ database.Open (WAL, busy_timeout 30 s, foreign_keys ON, 0600 file)
               ├─ db.Migrate (v1..v42, ErrSchemaTooNew guard)
               ├─ credentials.NewStore (keyring probe + AES fallback)
               └─ OAuth override wiring (UserOverrideLookup, SlotAliasLookup, ActiveChoiceLookup)
main.go:135  wails.Run(&options.App{ ... })
               ├─ Title "Email Hub", default size 3/4 x 4/5 of primary screen
               ├─ StartHidden: true                      main.go:142
               ├─ Frameless: !nativeTitleBar             main.go:141
               ├─ OnStartup / OnShutdown / OnBeforeClose
               ├─ Bind: App + dummy ComposerApp (bindings only)
               └─ Linux.WebviewGpuPolicyOnDemand, ProgramName io.github.beheoxinh.Hsx2Mail
```

Preflight runs **before** `wails.Run` on purpose: a failure yields a native error dialog and
exit without ever flashing a half-rendered window (`app/app.go:~430`).

### 3.3 `Startup(ctx)` — app/app.go:539

```
512  a.ctx = ctx
518  SingleInstanceLock.SetOnShow(...)        // registered early, before blocking D-Bus calls
535  construct stores: account, folder, message, attachment, contact, draft, settings,
     appstate, image allowlist, carddav, certificate (TOFU)
545  updateDBConnectionPool()                 // pool scales by account count
550  vCard scanner (20 min cache) + background scan
566  S/MIME store/signer/verifier/encryptor/decryptor
573  PGP store/signer/verifier/encryptor/decryptor
581  imap.NewPool(DefaultPoolConfig)          // MaxConnections=3 per account
599  sync.NewEngine
620  db.StartCheckpointRoutine               // WAL checkpoint every 5 min
623  CardDAV store + scheduler
642  cert-aware DAV transport (BuildTLSConfigDynamic), installed globally
646  extauth.Broker, extmail.API, extcompose.API, extui.Registry
657  contact/calendar extensions constructed; ext.Register(core) wires UI surfaces
708  CardDAV scheduler.Start(ctx)
711  undo stack (50 commands / 30 s, in-memory)
717  composeOps shared by App and ComposerApp
737  initIPC -> $TMPDIR/hsx2mail-<uid>/ipc.sock
742  initNetworkMonitor
745  initBackgroundSync: scheduler (1 min tick) + IdleManager (per account)
     sync pending drafts from last session
     FTS indexer callbacks
779  wailsRuntime.EventsEmit(ctx, "app:ready")
810  goroutine: sleep 5 s, then IndexAllFolders (background FTS)
828  autostart manager
```

### 3.4 Frontend mount (`frontend/src/main.ts`)

`waitForRuntime` polls `window.runtime`; once present it calls `WindowShow()`, then
`initI18n()`, then `waitForBackendReady()` which registers `EventsOn('app:ready')` (with a
one-shot `IsReady()` fallback, `app/app.go:843`), then mounts `App.svelte`.

---

## 4. Module dependency graph

```
main.go ─────────────▶ app/ ─────────────▶ internal/*  and  extensions/*/backend
                         │                    │
                         │                    └──▶ internal/core/api/v1 (surface types)
                         │
                         └──▶ extensions/* ──▶ internal/extensions, internal/core/api/v1
```

Rules enforced by convention and review:

- `app/` is the only Wails-bound layer. It may import `internal/*` and `extensions/*/backend`,
  but **not** other `app/` packages (`AGENTS.md` §15).
- `internal/*` contains business logic and **never imports Wails**; extension backends may
  import `internal/*` and `internal/core/api/v1`.
- `extensions/*/backend` are compiled into the binary (no dynamic loading).
- `frontend/wailsjs/` is generated output (`make generate`); do not edit it.

Real `internal/` package list (v0.3.2, 26 entries): `account`, `appstate`, `carddav`, `certificate`,
`contact`, `core/api/v1`, `credentials`, `crypto`, `database`, `draft`, `email`, `extensions`
(plus `extensions/auth`, `extensions/compose`, `extensions/mail`, `extensions/ui`), `folder`,
`imap`, `ipc`, `keyring`, `kit/davutil`, `logging`, `message`, `notification`, `oauth2`,
`pgp`, `platform`, `settings`, `smime`, `smtp`, `sync`, `tray`, `undo`.

`app/` files: `app.go`, `account.go`, `message.go`, `compose.go`, `draft.go`, `sync.go`,
`background.go`, `folder.go`, `oauth.go`, `oauth_creds.go`, `pgp.go`, `smime.go`,
`contact.go`, `carddav.go`, `search.go`, `settings.go`, `theme.go`, `undo.go`,
`attachment.go`, `certificate.go`, `window.go`, `actions.go`, `state.go`, `ipc.go`,
`eventbus.go`, `coreimpl.go`, `detached_composer.go`, `extension_calendar.go`,
`extension_contacts.go`, `extension_ui.go`, `log.go`, `recover.go`.

---

## 5. Data model

**Canonical owner: [`DATABASE.md`](DATABASE.md).** This section is a summary only. The
authoritative schema — every migration v1..v42, every table and column, all 42 explicit
indexes, the DSN and its PRAGMAs, foreign keys, transaction boundaries, retention rules,
the runtime-added `attachments.content` column, and the per-extension `ext_kv` database —
lives in `DATABASE.md`, whose generated sections are produced by
`tools/db/schemadump` + `tools/db/gen-database-doc.py` directly from `migrations.go`.

### 5.1 Shape of the system

One SQLite database per process, WAL mode, opened through `database.Open` so that every
connection in the pool gets the same pragmas:

```
<data>/hsx2mail.db          30 application tables + FTS5 index + migrations ledger
<data>/attachments/staging/ staged composer attachment blobs (7-day retention)
<data>/extensions/<name>/data.db   per-extension database with its own ext_kv table
<data>/keys/                device key for the AES-256-GCM credential fallback
```

The main window and every detached composer are **separate OS processes** against that one
file, which is why the DSN sets `_txlock=immediate`.

### 5.2 The three data domains

| Domain | Tables | Owner package |
|---|---|---|
| Mail | `accounts`, `identities`, `folders`, `messages`, `attachments`, `drafts`, `oauth_tokens` | `internal/account`, `internal/folder`, `internal/message`, `internal/draft` |
| Crypto & trust | `pgp_keys`, `pgp_sender_keys`, `pgp_keyservers`, `smime_certificates`, `smime_sender_certs`, `trusted_certificates` | `internal/pgp`, `internal/smime`, `internal/certificate` |
| Contacts | `contact_sources`, `contact_source_addressbooks`, `contact_source_oauth`, `contact_records` + six vCard sub-tables, `carddav_record_state` | `internal/contact`, `internal/carddav` |
| Configuration | `settings`, `app_state`, `image_allowlist`, `extension_secrets`, `fts_index_status`, `messages_fts` | `internal/settings`, `internal/appstate`, `internal/extensions` |

`undo` is deliberately **not** in the database. `internal/undo` keeps the undo stack in
memory only (`app/undo.go`); it does not survive a restart.

### 5.3 The three things that bite

1. **`attachments.content` is not a migration.** It is added at runtime by
   `AttachmentStore.ensureContentColumn` (`internal/message/attachment_store.go:26-48`).
   A database that has only been through `Migrate()` does not have it.
2. **Migrations are forward-only.** See `SQL_ROLLBACK.md`; the ledger cannot express a
   down path and `ADD COLUMN` is not cleanly reversible.
3. **Retention is the only thing that bounds growth**, and it is folder-scoped, not
   account-scoped, so Sent/Trash/Archive are never wiped by an INBOX retention pass.

## 6. Sync architecture

### 6.1 Entry points

Five independent paths can drive a sync (`docs/analysis/10-sync-imap-idle.md` §1.1):
`SyncFolder` (user click / draft/delete cleanup, `app/sync.go:22`), `SyncAccountComplete`,
`SyncAllComplete` (`app/sync.go:199/350`), the polling scheduler
(`internal/sync/scheduler.go:212`), and the IDLE-triggered blocking sync
(`app/background.go:199/436`). Dedup is split across three mechanisms (500 ms debounce per
`accountID:folderID` at `app/sync.go:23`, a `syncContexts` existence check, and the
scheduler's per-account `syncing` bool) that do not all see each other.

### 6.2 Two-phase sync (headers → bodies)

```
SyncMessages (internal/sync/messages.go:33)
  1. pool.GetConnection                              messages.go:53
  2. GetMailboxStatus (STATUS)                       messages.go:69
  3. SelectMailbox (CONDSTORE when supported)        messages.go:77 / client.go:617
  4. UIDValidity changed? -> DeleteByFolder, prevModSeq=0
  5. DeleteOlderThan(account, sinceDate)  (account-wide)
  6. GetAllUIDs -> localUIDSet
  7. fetchUIDs (headers)                             [headerBatchSize = 50]
  8. Upsert headers, compute ThreadID                threading.go:13
Phase 2 (bodies, batched)
  fetch.go: byte+count batching (512 KB / 50 msgs; 25 msgs / 256 KB for mailboxes > 1000)
  -> fetchMessageBodiesBatch (pipelined)
  -> UpdateBodiesBatch (1 txn)
  -> CreateBatch(attachments) (1 txn)
```

CONDSTORE is used when the server advertises it and UIDVALIDITY is unchanged
(`internal/sync/condstore.go:51`); otherwise sync falls back to UID-based incremental fetch.
Threading clusters by `References`/`In-Reply-To` and subject (`internal/sync/threading.go:13`).

### 6.3 Parallel model

- Multiple accounts sync concurrently (separate goroutines).
- Folders within one account sync sequentially; folder-status probing uses 5 workers
  (`internal/sync/folders.go:22`).
- Folder-sync concurrency cap is 2, duplicated as literals in `app/sync.go:225` and
  `internal/sync/scheduler.go:358`.

### 6.4 IDLE and polling

```
IdleManager (per account, INBOX only)          internal/imap/idle.go
  IdleTimeout = 10 min                          idle.go:45
  reconnect backoff 1s -> 5m, max 10 attempts   idle.go:20-26,162
  EXISTS/FETCH/EXPUNGE -> events chan
app/background.go:100 processIdleEvents
  EventNewMail      -> handleIdleNewMail        (not debounced)
  EventFlagsChanged -> 1s debounce
  EventExpunge      -> 1s debounce
Scheduler (fallback)                            internal/sync/scheduler.go
  checkInterval = 1 min ticker, per-account isSyncDue on sync_interval   scheduler.go:67
```

### 6.5 Tuning constants

| Constant | Value | Source |
|---|---|---|
| `headerBatchSize` | 50 | `internal/sync/engine.go:32` |
| `bodyBatchMaxBytes` | 512 KB | `internal/sync/engine.go:37` |
| `bodyBatchMaxMessages` | 50 | `internal/sync/engine.go:38` |
| `bodyBatchMinMessages` | 1 | `internal/sync/engine.go:39` |
| `bodyBatchQueryLimit` | 200 | `internal/sync/engine.go:40` |
| `maxPartSize` | 10 MB | `internal/sync/engine.go:45` |
| `maxRawMessageSize` | 50 MB | `internal/sync/engine.go:46` |
| `maxInlineContentSize` | 5 MB | `internal/sync/engine.go:47` |
| `flagBatchSize` | 500 | `internal/sync/messages.go:508` |
| SyncFolder debounce | 500 ms | `app/sync.go:23` |
| IMAP pool `MaxConnections` | 3 per account | `internal/imap/pool.go:57` |
| Connection idle close | 5 min | `internal/imap/pool.go:58` |
| IDLE cycle | 10 min | `internal/imap/idle.go:45` |
| WAL checkpoint routine | 5 min | `internal/database/database.go:37` |
| Background FTS start delay | 5 s | `app/app.go:813` |
| Post-wake sync cooldown | 2 min | `app/background.go:781` |
| `guardSyncPeriodDays` default | 30 | `internal/database/migrations.go:39` |

---

## 7. Compose, send, and draft pipeline

### 7.1 Autosave

`Composer.svelte` debounces keystroke/recipient/subject changes by 10 s
(`scheduleDraftSave`, `Composer.svelte:585`). Each save writes the local draft row first
(encrypted if enabled) and then re-builds the RFC822 message and APPENDs it to the IMAP
Drafts folder (`app/draft.go:326-380,589-606`). `SyncPendingDrafts` re-uploads unsynced
drafts on the next start (`app/draft.go:655`). Local loss on crash is prevented by writing
the DB before the IMAP append; the known risk is that the draft row's `imap_uid` is written
back from stale in-memory state (`docs/analysis/30-compose-send-draft.md`, F1).

### 7.2 Send ordering (`app/compose.go:307`)

```
resolve identity -> build RFC822 (msg.ToRFC822)
-> S/MIME sign/encrypt (if enabled)
-> PGP sign/encrypt (if enabled)
-> smtp.NewClient / Connect / Login
-> recipients = To + Cc + Bcc
-> client.SendMail(from, recipients, raw)
-> append to Sent folder unless provider auto-saves (log-only on failure)
-> add To/Cc to contact store
-> delete local draft (log-only on failure)
-> main window: go syncSentFolder
```

SMTP header injection is guarded by `writeHeader` (`internal/smtp/message.go:188`); the MDN
path does not use it (`internal/smtp/mdn.go:81`).

### 7.3 Detached composer + IPC

`OpenComposerWindow` (`app/ipc.go:196`) spawns `os.Executable()` with `--compose --account …
--ipc-address … --mode … --draft-id …` and writes a 256-bit token to the child's stdin. The
main process owns the IPC server; the composer is a client.

| Property | Value | Source |
|---|---|---|
| Transport (Unix) | `$TMPDIR/hsx2mail-<uid>/ipc.sock`, dir 0700, socket umask 0077 | `internal/ipc/server_unix.go:70-90` |
| Transport (Windows) | `\\.\pipe\hsx2mail-<username>` | `internal/ipc/server_windows.go:75` |
| **Framing** | **Newline-delimited JSON** via `json.Encoder`/`json.Decoder` — **not** length-prefixed | `internal/ipc/server.go:271`, `internal/ipc/client.go:205` |
| Auth | 64-char hex token over stdin, `subtle.ConstantTimeCompare` | `internal/ipc/token.go:23,60` |
| Composer → Main | `message_sent`, `draft_saved`, `draft_deleted`, `composer_ready`, `composer_closed` | `internal/ipc/message.go:12-33` |
| Main → Composer | `theme_changed`, `shutdown` | same |

Known gaps: pre-auth decode has no `io.LimitReader` and `AcceptLoop` has no connection cap
(`docs/analysis/70-platform-services.md` F-01/F-02).

---

## 8. Extension system

### 8.1 Core API surfaces (`internal/core/api/v1`)

`Core` exposes Mail (read-only), Composer, Contacts, Auth (HTTP/IMAP/SMTP with OAuth),
UI (rail tabs, settings tabs, context menus, hooks), Notifications, Storage (KV, Secrets,
read-only HostSecrets), Events (EventBus), Log, and HTML sanitization.

### 8.2 Lifecycle

Extensions are compiled in (no dynamic loading). `App.Startup` constructs the extension
structs and calls `ext.Register(core)` for **every** extension, enabled or not
(`app/app.go:657-679`). Registration is descriptive — it wires UI surfaces that persist
across enable/disable; the frontend filters by enabled state. The Wails-bound surface lives
on each extension's `Bridge` struct, embedded into `App` via method promotion.

### 8.3 Disabled-extension cost

A disabled extension contributes one `Bridge` struct to the `App` allocation. Measured cost
is **104 bytes per bridge** (`docs/analysis/70-platform-services.md` §1.4), not the ~80 bytes
claimed by older docs. No DB connection, goroutine, or network activity exists while disabled;
the extension stores are opened lazily on the first enabled call (`Bridge.ensureInit`,
`sync.Once`).

---

## 9. Security architecture

### 9.1 Credentials

`credentials.NewStore` probes the OS keyring once at startup with a real Set/Delete round-trip
and caches the result (`internal/credentials/store.go:26-67`). Secrets are AES-256-GCM
encrypted per call in `internal/crypto/crypto.go:122`. Fallback caveat: a runtime keyring
failure silently downgrades a write into the DB column and is never migrated back
(`docs/analysis/70-platform-services.md` F-15), and the AES fallback key is not a strong
boundary against a local attacker (F-16).

### 9.2 OAuth2

Authorization-code + PKCE (S256) with `state` validation
(`internal/oauth2/flow.go:164`), an embedded loopback HTTP callback server, provider presets
for Google/Microsoft plus custom providers, and token exchange/refresh. The OAuth callback
error page reflects query values into HTML without escaping
(`internal/oauth2/server.go:173`) — a known XSS gap.

### 9.3 TLS TOFU

`certificate.BuildTLSConfigDynamic` wraps IMAP/SMTP/CardDAV/CalDAV with
`InsecureSkipVerify: true` plus full manual verification against the
`trusted_certificates` store (`internal/certificate/verifier.go:19,66-83`). Fingerprint
mismatch triggers the trust dialog. WKD/HKP PGP key fetches bypass this stack (F-20).

### 9.4 Email crypto

| Feature | Sign | Verify | Encrypt | Decrypt |
|---|---|---|---|---|
| S/MIME | `smime.Signer` | `smime.Verifier` | `smime.Encryptor` | `smime.Decryptor` |
| PGP | `pgp.Signer` | `pgp.Verifier` | `pgp.Encryptor` | `pgp.Decryptor` |

PGP key discovery: WKD (5 s timeout, 1 MB limit) and HKP (5 s, 1 MB, sequential). Signer
identity is not bound to the `From:` header (F-19).

### 9.5 Hardening measures present

- `sanitizeField` strips CR/LF from address fields (`app/app.go:153`); used only by
  `parseMailtoURL`.
- URL protocol allowlist (`http`, `https`, `mailto`); `exec.Command` without a shell.
- HTML sanitization via `bluemonday` (`internal/email/sanitizer.go`). Note the message
  body iframe currently allows scripts (`frontend/src/lib/components/viewer/EmailBody.svelte:785`).
- SQLite file 0600, directory 0700.
- IPC token auth over stdin, constant-time compare.
- Randomness is `crypto/rand` everywhere; no `math/rand`, no hardcoded IVs/keys.

---

## 10. Frontend architecture

**Canonical owner: [`FRONTEND.md`](FRONTEND.md).** Svelte 5 with runes, Vite, Tailwind 3,
`bits-ui` primitives, Tiptap for the composer, `@tanstack/svelte-virtual` for the
conversation list, `svelte-i18n` with 10 locales. Full store graph, component tree,
event list, layout modes and the virtualizer configuration are in `FRONTEND.md`.

Two things that are structural rather than detail:

- **`app/` is the only Wails-bound layer.** The frontend reaches Go exclusively through the
  generated bindings in `frontend/wailsjs/` (regenerate with `make generate`, never edit)
  and Wails events. There is no HTTP surface.
- **The composer is a separate entry point, not a store.** `frontend/src/main.ts` +
  `App.svelte` are the main window; `frontend/src/composerMain.ts` + `ComposerApp.svelte`
  are a detached composer, which talks to the main window over the Unix-socket IPC server
  rather than through shared Svelte state. There is no `stores/composer.svelte.ts`.

## 11. Background operation and Linux session integration

**Canonical owner: [`BACKGROUND.md`](BACKGROUND.md).** Startup ordering,
`start_hidden`/`run_background` coupling, close semantics, autostart, the tray icon,
single-instance handoff, and the event-driven sleep/wake, network and session-lock
monitors are all documented there. Summary:

- **`run_background` on** → closing the window *hides* it; the process keeps syncing.
  `QuitApp` is the only real exit. With background mode on there is deliberately no
  window-closed way to quit.
- **A tray icon exists** (`internal/tray`, wrapping `fyne.io/systray`, because Wails v2 has
  no tray API). It is created exactly when the instance can be window-less:
  `run_background OR autostart`. Older prose describing `run_background` as "minimize to
  tray" predates the tray and is wrong.
- **Monitors are event-driven, not polled.** Network, sleep/wake, session lock and theme
  are all D-Bus-driven on Linux.
- **Boot-storm control**: a 3-slot semaphore, a 30 s per-account initial jitter draw, and
  exponential failure backoff from 2 min to 60 min, so a boot with N accounts does not open
  N connections at once and an outage does not become an N-connection retry storm.

## 12. Build and distribution

`Makefile` targets: `build`, `build-linux`, `dev`, `dev-race` (Wails builds with
`-tags webkit2_41`), `generate` (`wails generate module`), `test` (`go test ./...`), `lint`
(golangci-lint + ESLint), `fmt`, `clean`, `frontend-deps`, `install`/`uninstall` (Linux,
macOS), `build-windows-installer` (`wails build -nsis`), and the Flatpak targets.

Flatpak manifests live in `build/flatpak/`:

- `io.github.beheoxinh.Hsx2Mail-dev.yml` — packages a host-built binary; emits
  `build/bin/Hsx2Mail-dev.flatpak`.
- `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml` — the Flathub from-source manifest.

**Flatpak is currently broken (P0).** The Flathub manifest builds with `-mod=vendor` while
the repository has no `vendor/` directory (`docs/analysis/80-build-packaging.md` F-01), the
`org.freedesktop.portal.Desktop` talk-name required by the default notification path is
missing (F-02), the release tooling still references the old `hkdb`/`Aerion` identity (F-03),
and `runtime-version: '50'` is not yet available on Flathub. These blockers are owned by
`docs/PLAN.md` Phase 0.

OAuth credentials are injected at build time from `.env` into ldflags
(`internal/oauth2` package variables); the `cmd/hsx2mail-creds` helper is exec'd as a sibling
binary.

---

## 13. Testing strategy

- Go tests exist for most core packages, including `internal/account`, `appstate`, `carddav`,
  `certificate`, `contact`, `core`, `credentials`, `crypto`, `database`, `draft`, `email`,
  `extensions`, `extensions/auth`, `extensions/ui`, `folder`, `imap`, `ipc`, `message`,
  `notification`, `oauth2`, `pgp`, `platform`, `settings`, `smime`, `smtp`, `sync`, `undo`,
  plus `app/`, `main_test.go`, `cmd/hsx2mail-creds`, and the extension backends
  (`extensions/calendar/backend` 17 files, `extensions/contacts/backend` 8 files).
- **`internal/database` is red on the v0.3.2 tree**: `database_test.go:202` and `:379` fail
  re-migration with `duplicate column name: secondary_sync_interval` (the v41 column is
  missing from the test's drop list).
- **The frontend has zero tests**: no `*.test.ts` / `*.spec.ts`, no vitest/jest/playwright
  config (`docs/analysis/50-frontend.md` §0).
- **There are no CI workflows**: `.github/workflows/` does not exist, so lint and
  `svelte-check` never gate a merge.
- Run commands: `make test` (`go test ./...`) and `make lint`.

---

## 14. Known limitations and technical debt

Structure, not a defect ledger. Status of the remediation phases:

| Area | Status |
|---|---|
| Build/CI | **Fixed.** Flatpak builds (`make flatpak`, `build/flatpak/build-local.sh`); `make check` is the gate. |
| Data integrity | **Fixed.** `DeleteOlderThanInFolder` is folder-scoped; header upsert no longer blanks bodies; body writes are transacted. |
| Sync/store performance | **Fixed.** FTS and per-page aggregation rewrites; the eight v42 indexes. See `PERFORMANCE.md` §5. |
| Background/session | **Fixed.** Tray exists (`internal/tray`); Flatpak autostart uses `FLATPAK_ID`; single-instance TOCTOU addressed; composer lifetime bounded by the IPC connection cap. See `BACKGROUND.md`. |
| Frontend | **Fixed.** Virtualized list, byte-budgeted attachment cache, per-listener unsubscribe instead of `EventsOff`. See `FRONTEND.md`. |
| Security | **Fixed.** Keyring failure no longer silently downgrades; IPC frames size-capped and connections capped (`internal/ipc/server.go:19-37`). See `CRYPTO.md` §8. |
| Docs | **This consolidation.** `/docs` is the single source of truth; see `README.md` for the index and maintenance rules. |

The authoritative defect record remains `docs/analysis/90-verification.md`, the
post-implementation reviews `docs/analysis/97-post-implementation-review.md` and
`docs/analysis/98-post-fix-verification.md`, and any remaining open items
`docs/GAPS.md` / `docs/PLAN.md`.
| Docs | Old `architecture.md`/`AGENTS.md` claims corrected; other docs still partly stale | PLAN Phase 6 |

Historical note: this file was rewritten in v0.3.2 to correct verified-wrong claims in the
previous revision (migration range `v1..v39` (now v1..v42), a non-existent `undo_commands` table, D-Bus
single-instance, length-prefixed IPC framing, the wrong certificate table name, and the
disabled-extension byte figure). See `docs/analysis/95-existing-docs-audit.md`.
