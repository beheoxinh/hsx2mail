# Kiến trúc Hệ thống Email Hub — Phân tích Kiến trúc

## Tổng quan

Email Hub là email client hiện đại, đa nền tảng được xây dựng trên **Wails v2** (Go 1.25 backend + Svelte 5/TypeScript frontend). Phiên bản hiện tại: v0.3.2. Tác giả: beheoxinh.

**Đặc điểm kiến trúc nổi bật:**
- Desktop app native (KHÔNG phải Electron) — dùng Wails + WebView2GTK/Cocoa/WebView2
- Pure Go SQLite (modernc.org/sqlite) — KHÔNG dùng CGO, dễ cross-compile
- Mô hình đa tiến trình (multi-process) với composer window tách rời
- Hệ thống extension nhúng (embedded), không dynamic loading
- TOFU (Trust On First Use) cho TLS certificate

---

## 1. Mô hình Multi-Process

Email Hub chạy với **hai loại tiến trình**:

### Main Process (App struct)
- Khởi tạo từ `main.go` với `--compose=false` (mặc định)
- Sở hữu toàn bộ stores, sync engine, IDLE manager, OAuth2 manager, extensions
- Mở một cửa sổ Wails duy nhất chứa toàn bộ UI email client
- Chạy IPC server (Unix socket) để giao tiếp với composer window

### Composer Process (ComposerApp struct)
- Khởi tạo từ `main.go` với `--compose=true`
- Là tiến trình con được spawn từ main process qua `exec.Command()`
- Sở hữu DB connection riêng, IMAP pool riêng
- Kết nối tới main process qua IPC (Unix socket + token auth)
- Khi send/save draft → gửi IPC message → main process sync IMAP

### Single-Instance Lock
- Dùng `platform.SingleInstanceLock` (Unix socket hoặc D-Bus trên Linux)
- Khi user mở instance thứ hai: kích hoạt instance cũ (ShowWindow) + thoát
- Nếu kèm `mailto:` argument: forward cho instance cũ xử lý

---

## 2. Startup Sequence (Chi tiết)

### Phase 0: CLI Parsing
```
main.go → flag.Parse()
  --debug       : Enable debug logging (hoặc env HSX2MAIL_DEBUG=1)
  --compose     : Chạy ở chế độ composer window
  --account     : Account ID (cho composer)
  --ipc-address : IPC server address (cho composer)
  --mode        : new/reply/reply-all/forward
  --message-id  : Message gốc (cho reply/forward)
  --draft-id    : Draft để resume
  --mailto      : mailto: URL
  --version     : In version
  Positional args: mailto: URL
```

### Phase 1: Preflight (TRƯỚC khi tạo Wails window)
```
App.Preflight()
  ├── logging.Init()            — zerolog, debug/fatal level
  ├── platform.GetPaths()       — XDG paths
  │     ~/.local/share/hsx2mail/   (data)
  │     ~/.config/hsx2mail/       (config)
  │     ~/.cache/hsx2mail/        (cache)
  ├── paths.EnsureDirectories() — mkdir -p 0700
  ├── database.Open()           — SQLite WAL mode (modernc.org/sqlite)
  │     PRAGMA: busy_timeout(30000), journal_mode=WAL, synchronous=NORMAL
  │     File permissions: 0600 (owner read/write only)
  ├── db.Migrate()              — v1..v39, ErrSchemaTooNew guard
  └── credentials.NewStore()    — Keyring + AES-GCM fallback
      └── OAuth wiring: UserOverrideLookup, SlotAliasLookup, ActiveChoiceLookup
```

### Phase 2: Wails Startup (SAU khi tạo window)
```
App.Startup(ctx)
  ├── Single-instance onShow callback (mailto forwarding)
  ├── Construct ALL stores:
  │     account.Store, folder.Store, message.Store, attachment.Store
  │     contact.Store + draft.Store + settings.Store + appState.Store
  │     imageAllowlist.Store + certificate.Store + carddav.Store
  ├── Scale DB pool: BaseIdleConns + (numAccounts * 1), cap at MaxIdleConns (6)
  ├── Initialize crypto:
  │     S/MIME: Store, Signer, Verifier, Encryptor, Decryptor
  │     PGP: Store, Signer, Verifier, Encryptor, Decryptor
  ├── Initialize IMAP Pool + Sync Engine
  ├── Wire S/MIME + PGP verifiers into Sync Engine (signature verification)
  ├── Initialize CardDAV Syncer + Scheduler
  ├── OAuth2 Manager + Auth Broker (for extensions)
  ├── Extension system:
  │     davTransport (TOFU-aware) → davutil.SetDefaultBaseTransport
  │     authBroker, mailAPI, composerAPI, uiRegistry
  │     Construct CalendarBridge + ContactsBridge
  │     For each extension: newCoreForExtension → ext.Register(core)
  │       → Register UI rail tabs, hooks
  ├── Google Contacts client
  ├── IPC server (Unix socket + token auth)
  ├── Network monitor (D-Bus on Linux, event-driven, zero-polling)
  ├── Background sync:
  │     sync.Scheduler (polling period per account: 30-60 min)
  │     imap.IdleManager (push, per-account TCP connection)
  │     processIdleEvents goroutine
  ├── Sync pending drafts from last session
  ├── FTS Indexer + background indexing (5s delay từ initial sync)
  ├── EMIT "app:ready" → frontend mounts App.svelte
  ├── Desktop notifications (OS-native per platform)
  ├── Sleep/wake monitor (D-Bus on Linux)
  ├── Theme monitor (XDG Settings Portal on Linux)
  └── Autostart manager
```

### Phase 3: Frontend Bootstrap (song song với Phase 2)
```
main.ts → bootstrap()
  ├── waitForRuntime()         — Poll window.runtime (2s timeout)
  ├── WindowShow()             — Show OS window sau khi runtime inject
  ├── initI18n()               — Load locale JSON
  ├── waitForBackendReady()    — EventsOn("app:ready") + IsReady() fallback
  └── mount(App.svelte)        — Mount Svelte app
```

---

## 3. Module Dependency Graph & Package Responsibilities

### app/ — Wails Binding Layer

| File | Binding Prefix | Responsibility |
|------|---------------|----------------|
| `app.go` | - | App struct, Preflight, Startup, Shutdown, lifecycle |
| `account.go` | - | Account CRUD (Add/Update/Remove/Reorder) + Microsoft Shared Mailbox |
| `message.go` | - | Message/conversation retrieval + on-demand body fetch |
| `compose.go` | - | Send pipeline (SMTP + MIME build) + OAuth token refresh |
| `draft.go` | - | Draft save/load + sync to IMAP Drafts folder |
| `sync.go` | - | SyncFolder with debounce/cancel/restart + SyncAllComplete |
| `folder.go` | - | Folder tree + special folder detection (Inbox/Sent/Drafts/Trash/Spam/Archive) |
| `background.go` | - | Sync scheduler + IDLE manager + notifications + sleep/wake/network events |
| `oauth.go` | - | OAuth flow (StartOAuthFlow, CompleteOAuthAccountSetup, StartCustomOAuthFlow) |
| `pgp.go` | PGP_* | PGP key management bindings (import/list/delete/sign/verify) |
| `smime.go` | SMIME_* | S/MIME certificate management bindings |
| `contact.go` | Contacts_*, Contact_* | Contact search (vCard + CardDAV + Google) |
| `carddav.go` | - | CardDAV source CRUD + account linking + sync |
| `search.go` | - | Full-text search |
| `settings.go` | - | Settings CRUD |
| `theme.go` | - | Theme switching (dark/light/system) |
| `undo.go` | - | Undo stack (move/delete/flag operations) |
| `attachment.go` | - | Attachment management + download + inline images |
| `certificate.go` | - | Certificate trust dialog trigger |
| `window.go` | - | Window visibility + compositor ready notification |
| `actions.go` | - | Bulk operations (mark read, star, archive, trash) |
| `state.go` | - | Application state persistence |
| `ipc.go` | - | IPC server + OpenComposerWindow (spawn child process) |
| `eventbus.go` | - | coreapi.EventBus (Go subscribers + frontend EventsEmit fan-out) |
| `coreimpl.go` | - | coreapi.Core cho extensions (Auth/Mail/UI/Storage/Events/Log/HTML/Contacts) |
| `detached_composer.go` | - | ComposerApp struct + lifecycle (startup/shutdown cho composer window) |
| `recover.go` | - | recoverPanic() — goroutine panic recovery |

### internal/ — Core Business Logic

| Package | Responsibility | Key Types |
|---------|---------------|-----------|
| `account/` | Account model + Store (CRUD) | Account, AccountConfig, Store |
| `folder/` | Folder model + Store (CRUD, tree ops) | Folder, FolderTree, Store |
| `message/` | Message model + Store (CRUD, FTS, threading) | Message, MessageHeader, Conversation, Store, FTSIndexer |
| `draft/` | Draft model + Store | Draft, Store |
| `email/` | Attachment extraction, HTML sanitizer, download | AttachmentExtractor, Sanitizer |
| `imap/` | IMAP client (go-imap v2 wrapper) | Client, Pool, IdleManager, XOAUTH2 |
| `smtp/` | SMTP client + message builder + MDN | Client, AuthType, SecurityType |
| `sync/` | Sync engine + scheduler + threading + CONDSTORE + search | Engine, Scheduler, threader |
| `database/` | SQLite wrapper + migrations (v1-v39) | DB, Migration, ErrSchemaTooNew |
| `credentials/` | Keyring-backed credential store | Store, OAuthTokens, credential stores |
| `oauth2/` | OAuth2 flow + providers + server | Manager, ProviderConfig, TokenResponse |
| `settings/` | Settings key-value store + image allowlist | Store, ImageAllowlistStore |
| `appstate/` | UI state persistence | Store |
| `certificate/` | TOFU certificate store + verifier | Store, Verifier, Error, Info |
| `pgp/` | PGP key store + sign/verify + encrypt/decrypt + HKP + WKD | Store, Signer, Verifier, Encryptor, Decryptor |
| `smime/` | S/MIME store + sign/verify + encrypt/decrypt + PKCS12 | Store, Signer, Verifier, Encryptor, Decryptor |
| `crypto/` | Shared crypto utilities | |
| `carddav/` | CardDAV client + model + syncer | Client, Store, Syncer |
| `contact/` | Contact model + Google + Microsoft + vCard | Store, GoogleContactsClient |
| `notification/` | OS notifications (darwin/linux/windows) | Notifier, Notification, NotificationData |
| `ipc/` | IPC client/server (token-based auth) | Server, Client, Message, TokenManager |
| `keyring/` | OS keyring abstraction | |
| `logging/` | zerolog wrapper | Config, WithComponent |
| `undo/` | Undo stack + commands | Stack, Command (MoveCommand, DeleteSentCommand, FlagCommand) |
| `platform/` | OS abstractions | Paths, NetworkMonitor, SleepWakeMonitor, ThemeMonitor, SingleInstanceLock |
| `extensions/` | Extension API implementations | auth.Broker, mail.API, compose.API, ui.Registry |
| `core/api/v1/` | Extension API surface types | Core, Mail, Auth, UI, Storage, EventBus, Logger, HTML |

### extensions/ — First-Party Extensions

| Extension | Backend Path | Frontend Path | Storage |
|-----------|-------------|---------------|---------|
| Calendar | `extensions/calendar/backend/` | `extensions/calendar/frontend/` | Per-extension SQLite |
| Contacts | `extensions/contacts/backend/` | `extensions/contacts/frontend/` | Tables in main DB |

---

## 4. Database Migrations (v1 → v39)

### Migration History

| Version | Changes |
|---------|---------|
| v1 | Initial: accounts, identities, folders |
| v2 | Messages table |
| v3 | Attachments table |
| v4 | Contacts table |
| v5 | Settings table |
| v6 | app_state table |
| v7 | image_allowlist table |
| v8 | Certificates table (TOFU) |
| v9-v14 | Incremental schema refinements |
| v15 | contact_sources table |
| v16-v17 | Schema refinements |
| v18 | contact_records (unified contact schema) |
| v19 | Schema refinements |
| v20 | undo_commands table |
| v21-v39 | Incremental improvements, index optimizations |

### Schema Guard

```go
type ErrSchemaTooNew struct {
    DBVersion    int
    BuildVersion int
}
```

Khi DB có schema version > max known version → từ chối mở, hướng dẫn user rollback.

### WAL Management

- Checkpoint interval: 5 phút (tự động)
- Mode: PASSIVE (không block)
- Pool: MaxOpenConns=12, MaxIdleConns=6, BaseIdleConns=3
- File permissions: 0600
- Directory permissions: 0700

---

## 5. Email Sync Architecture (Chi tiết)

### Two-Phase Sync Model

**Phase 1: Header Sync**
```
IMAP FETCH 1:* (FLAGS INTERNALDATE RFC822.SIZE
               BODY.PEEK[HEADER.FIELDS (From To Cc Subject
               Date Message-ID In-Reply-To References)]
               CHANGEDSINCE {modseq})

Batch: 50 messages/batch
Optimization: CONDSTORE (modseq-based fast path)
Fallback: UID-based sync khi server không hỗ trợ CONDSTORE
```

**Threading (sau Phase 1)**
```
Algorithm: References-based + Subject-based clustering
Input: In-Reply-To + References headers
Input: Subject (strip Re:/Fwd: prefixes)
Output: ThreadID (grouped by conversation)
```

**Phase 2: Body Sync**
```
IMAP FETCH BODY[] for messages with BodyFetched=false

Batch strategy (hybrid byte + count):
  - max 512KB per batch (memory safety)
  - max 50 messages per batch
  - min 1 message per batch (cho oversized emails)
  - query 200 candidate messages (cho byte-based batching)

Post-fetch processing:
  - Parse MIME → text body + HTML body
  - S/MIME verification (nếu có signature)
  - PGP verification (nếu có signature)
  - HTML sanitization (bluemonday)
  - Attachment extraction
```

### Parallel Sync Model

```
Account 1 ──▶ Sync Folder INBOX ──▶ goroutine
           ──▶ Sync Folder SENT  ──▶ goroutine (sequential)

Account 2 ──▶ Sync Folder INBOX ──▶ goroutine

Mỗi account+gói folder chạy song song (goroutine riêng)
Mỗi account: các folder sync tuần tự (một folder một lần)
Debounce: 500ms giữa các SyncFolder request
Cancel: context.WithCancel per account+folder pair
```

### Push vs Poll

**IDLE Push (real-time)**
```
Per-account TCP connection → IDLE command
On EXISTS response → SyncFolder(accountId, folderId)
Reconnect on: network change, sleep/wake, connection drop
```

**Polling Scheduler (fallback)**
```
Per-account timer (30-60 min, configurable)
Fallback khi IDLE không available (server không hỗ trợ)
Skip tick khi offline (network.IsConnected())
```

### Size Limits

| Limit | Value | Purpose |
|-------|-------|---------|
| Max MIME part | 10MB | Memory safety |
| Max raw message fetch | 50MB | Memory safety |
| Max inline image content (DB) | 5MB | DB size control |
| Body fetch batch | 512KB / 50 msgs | Memory + throughput balance |
| Header batch | 50 msgs | Progressive loading |

### Error Recovery

```
maxMessageRetries    = 3  // Retries per message
maxConnectionRetries = 3  // Connection recovery attempts
Retry delay: exponential backoff (internal to go-imap)
```

### Periodic Full Flag Sweep

- Cho large mailboxes trên CONDSTORE fast-path
- Driver: `flagSweepCounter` — per-folder counter
- Chạy trong `runFlagSync()` / `condstore.go`
- Đảm bảo flag changes từ clients khác được đồng bộ

---

## 6. Compose & Send Pipeline (Chi tiết)

### Compose Message Flow

```
Frontend (Composer.svelte)
  │
  ├── RecipientInput (autocomplete từ contacts store)
  ├── Subject, Body (Tiptap rich text hoặc plain text)
  ├── Attachments (drag & drop hoặc file picker)
  │
  ├── Identity selection (From: identity/alias)
  │     └── Encrypt/Sign toggle (PGP hoặc S/MIME)
  │
  ├── Auto-save (30s interval)
  │     ├── draftStore.SaveDraft() — local SQLite
  │     ├── Encrypt body nếu PGP/SMIME enabled
  │     └── syncDraftToIMAP() — upload lên IMAP Drafts folder
  │
  └── Send (SendMessage)
        ├── ResolveIdentity() — From name/email
        ├── buildMIMEMessage():
        │     ├── RFC822 headers (From, To, Cc, Bcc, Subject, Date, Message-ID)
        │     ├── go-message builder (text + HTML multipart/alternative)
        │     ├── Attachments (base64, inline hoặc attached)
        │     ├── Multipart/signed: S/MIME sign (nếu enabled)
        │     ├── Multipart/encrypted: S/MIME encrypt (nếu enabled)
        │     ├── PGP/MIME sign (nếu enabled)
        │     └── PGP/MIME encrypt (nếu enabled)
        ├── smtp.Send():
        │     ├── Connect (STARTTLS → TLS, hoặc implicit TLS)
        │     ├── Auth (LOGIN, PLAIN, hoặc XOAUTH2)
        │     └── DATA (send message)
        ├── AppendSentMessage() — IMAP APPEND vào Sent folder
        ├── DeleteDraft() — IMAP STORE +FLAGS.SILENT (\Deleted) → EXPUNGE
        ├── Emit "composer:messageSent" → main window (nếu detached)
        └── Undo: queue DeleteSentCommand (30s timeout)
```

### Undo System

```
Stack size: 50 commands max
Timeout: 30 giây
Command types:
  - MoveCommand: undo move/trash
  - DeleteSentCommand: undo sent message deletion
  - FlagCommand: undo flag changes
```

### Draft Encryption

- Body được mã hóa khi save nếu PGP/S/MIME enabled
- Encrypted body lưu dạng base64 trong DB
- Khi mở draft: decrypt tự động

---

## 7. OAuth2 Architecture (Chi tiết)

### Provider Resolution Chain

```
1. UserOverrideLookup: kiểm tra user-supplied credentials
2. SlotAliasLookup: kiểm tra slot alias (cho extension cross-routing)
3. ActiveChoiceLookup: kiểm tra explicit picker choice
4. Default provider chain: ldflags → shipped providers
```

### Supported Providers

| Provider | Client ID Source | Scopes |
|----------|-----------------|--------|
| Google (Mail) | ldflags (GOOGLE_CLIENT_ID/SECRET) | gmail.modify, openid, profile, email |
| Google (Testing) | ldflags (GOOGLE_TESTING_CLIENT_ID/SECRET) | contacts.readonly, calendar.* |
| Microsoft | ldflags (MICROSOFT_CLIENT_ID) | Mail.ReadWrite, Mail.Send, offline_access, openid, profile |
| Custom (BYOA) | User-supplied via Settings UI | User-configured |

### Token Refresh Flow

```
OAuth token expiring ≤ 5 phút:
  1. oauth2Manager.RefreshToken(provider, refreshToken)
  2. Nếu thành công: update tokens trong credentials.Store
  3. Nếu thất bại:
     a. Emit "oauth:reauth-required" event → frontend
     b. Frontend hiển thị "Re-authorization required" dialog
     c. User click → StartOAuthFlow → re-authorize
```

---

## 8. Extension System Architecture

### Design Principles

1. **Embedded, không dynamic loading**: Simpler security model
2. **Lightweight-by-default**: Extension disabled = ~80 bytes (Bridge struct)
3. **Lazy initialization**: Store + API constructs on first enabled call
4. **Per-extension DB**: Calendar có SQLite riêng, Contacts dùng main DB
5. **Method prefix convention**: `Calendar_*`, `Contacts_*` — tránh collision khi embed vào App

### Core API Surface (v1)

```
Core interface:
  ├── Mail()          → ListMessages, GetFolderTree, SearchMessages (read-only Phase 1)
  ├── Composer()      → OpenComposerWindow (Phase 1)
  ├── Contacts()      → SearchContacts, ListSources, LinkAccountSource (Phase 1)
  ├── Auth()          → HTTPClient (OAuth-bearer), IMAPClient, SMTPClient (Phase 1)
  ├── UI()            → RegisterRailTab, RegisterSettingsTab, RegisterContextMenuItem
  │                     RegisterInboxView, RegisterAccountSetupHook (Phase 1)
  ├── Notifications() → Show (desktop notification) (Phase 1)
  ├── Storage()       → Secrets (keyring), HostSecrets (read-only), KV (stub) (Phase 1)
  ├── Events()        → Publish, Subscribe (EventBus) (Phase 1)
  ├── Log()           → Debug/Info/Warn/Error (Phase 1)
  └── HTML()          → Sanitize (bluemonday) (Phase 1)
```

### Extension Lifecycle

```
Build time:
  - Extension code compiled vào binary (không dynamic linking)

App.Startup:
  ├── NewExtension() → construct Extension struct (manifest + Register only)
  ├── NewBridge()    → construct Bridge struct (Wails-bound surface, embedded vào App)
  ├── Register(core) → wire UI surfaces, hooks
  │     ├── ContactsExt: RegisterRailTab("Contacts"), RegisterAccountSetupHook
  │     └── CalendarExt: RegisterRailTab("Calendar"), RegisterSettingsTab
  └── extensionUnregs = append(unregs)

Runtime:
  - Mỗi bridge method gọi gateEnabled() → kiểm tra settings
  - Nếu disabled: return early (không work, không DB access)
  - Nếu enabled: lazy-init (sync.Once) → mở per-extension DB → execute

Shutdown:
  - extensionUnregs[i]() → cleanup UI registrations
```

### First-Party Extensions Detail

#### Calendar Extension
```
Backend (Go):
  ├── bridge.go          — Wails-bound methods (Calendar_* prefix)
  │                        gateEnabled → ensureInit → API call
  ├── api.go             — Calendar API (events CRUD, CalDAV sync)
  ├── caldav.go          — CalDAV client (go-webdav + go-ical)
  ├── sync.go            — CalDAV sync engine
  ├── store.go           — Per-extension SQLite (calendar.db)
  ├── rrule_expand.go    — RRULE expansion (teambition/rrule-go)
  ├── microsoft/         — Microsoft Graph calendar API
  └── google/            — Google Calendar API

Frontend (Svelte/TS):
  ├── components/        — Calendar UI (EventCard, views, dialogs)
  ├── stores/            — Calendar state
  ├── i18n/              — 10 locales
  └── hooks/             — Keyboard shortcuts, deep links
```

#### Contacts Extension
```
Backend (Go):
  ├── bridge.go          — Wails-bound methods (Contacts_* prefix)
  ├── store.go           — Tables in main DB (contact_records)
  ├── google_api.go      — Google Contacts API
  ├── google_convert.go  — Convert Google → internal contact
  ├── google_write.go    — Write operations (create/update Google contacts)
  ├── microsoft_api.go   — Microsoft Graph contacts API
  ├── microsoft_convert.go — Convert Microsoft → internal contact
  ├── microsoft_write.go   — Write operations (create/update Microsoft contacts)
  ├── ms_sidecar.go      — Microsoft sidecar sync
  ├── oauth_client.go    — OAuth HTTP client for contacts API
  ├── imaging/           — Contact photo processing
  └── convert.go         — Generic contact conversion

Frontend (Svelte/TS):
  ├── components/        — Contact UI (ContactDetail, AddContactDialog, fields)
  ├── stores/            — Contacts state
  ├── i18n/              — 10 locales
  └── hooks/             — Keyboard shortcuts
```

---

## 9. Security Posture

### Authentication & Credentials

```
Layer 1: OS Keyring (primary)
  - Linux: Secret Service (D-Bus) via golang-keyring
  - macOS: Keychain
  - Windows: Credential Manager

Layer 2: AES-GCM Encrypted SQLite (fallback)
  - Khi OS keyring không available (headless Linux, container, etc.)
  - Encryption key derived từ machine-specific values
```

### Input Validation

| Field | Max Length | Validation |
|-------|-----------|------------|
| Email address | 254 (RFC 5321) | Contains @, non-empty local-part + domain |
| Subject | 998 (RFC 5322 line length) | sanitizeField (strip CR/LF) |
| Body | 64KB | sanitizeField (strip CR/LF) |
| mailto: URL | 2KB | Prefix check, URL decode, query parse |

### Header Injection Prevention

`sanitizeField()` strips `\r` và `\n` từ tất cả email fields (to, cc, bcc, subject, etc.)

### URL Protocol Allowlist

```
Allowed: http://, https://, mailto:
Denied: file://, javascript:, vbscript:, data:, etc.
```

### HTML Sanitization

- Library: bluemonday (strict policy)
- Strips: scripts, event handlers, remote images (trừ khi allowed)
- Same policy shared giữa mail viewer và extensions (HTML() surface)

### TLS Certificate Handling

- **TOFU** (Trust On First Use): lưu fingerprint vào `certificates` table
- Khi fingerprint mismatch: show dialog → user accept/reject
- Dynamic TLS config: `certificate.BuildTLSConfigDynamic()`
- Applied to: IMAP, SMTP, CardDAV, CalDAV

### IPC Security

- Unix domain socket (file-based permissions)
- Token auth: per-session random token, truyền qua stdin pipe
- Không dùng TCP socket hay mạng

---

## 10. Frontend Architecture (Chi tiết)

### Store Pattern

Svelte 5 stores dùng class-based pattern với runes:

```typescript
class AccountStore {
  accounts = $state<AccountWithFolders[]>([])
  selectedFolder = $state<SelectedFolder | null>(null)
  syncProgress = $state<Record<string, Record<string, SyncProgress>>>({})
  // ...
}

export const accountStore = new AccountStore()
```

### Component Communication

1. **Wails Bindings**: Go methods → TypeScript bindings (auto-generated)
   - `app/account.go` → `wailsjs/go/app/App.js` → `GetAccounts()`
2. **Wails Events**: `EventsEmit` (Go) → `EventsOn` (frontend)
   - `sync:progress`, `folder:synced`, `oauth:success`, `app:ready`
3. **Svelte Props**: Parent → child component
4. **Svelte Stores**: Global state (cross-component)
5. **Extension UI Registry**: Extension đăng ký rail tabs → App.svelte render

---

## 11. Đánh giá Kiến trúc

### Điểm mạnh

1. **No CGO SQLite**: modernc.org/sqlite cho phép cross-compile dễ dàng, Flatpak build không cần C toolchain
2. **Preflight pattern**: Tách initialization khỏi Wails Startup → tránh flash window lỗi
3. **Multi-process composer**: Không block main window khi compose, IPC an toàn
4. **Event-driven monitors**: D-Bus-based (sleep/wake, network, theme) — zero polling
5. **TOFU certificate handling**: User-friendly, không phụ thuộc CA
6. **Extension isolation**: Per-extension DB, lightweight disabled state
7. **CONDSTORE sync**: Optimized cho IMAP (modseq-based incremental sync)
8. **Atomic migrations**: Transaction-based rollback on failure

### Điểm yếu / Rủi ro

1. **go-imap v2 beta**: Dependency vào beta package (API có thể thay đổi, bug tiềm ẩn)
2. **Frameless window mặc định**: Có thể gây issue trên một số WM (đã có native option)
3. **Single process DB pool**: Mỗi composer process mở DB riêng → lock contention tiềm ẩn
4. **Extension không dynamic loading**: Muốn thêm extension mới phải rebuild binary
5. **No rate limiting trên IMAP**: Không có rate limiting cho sync requests (có thể bị IMAP server block)
6. **Spellcheck worker chưa kiểm tra**: nspell + hunspell dictionaries trong web worker
7. **No automated UI tests**: Chỉ có Go unit tests, không có integration/E2E tests
8. **DB migration rollback phải manual**: tools/db/rollback*.sql — user phải tự chạy

### Khuyến nghị

1. **Thêm rate limiting** cho sync engine (tránh bị IMAP server rate limit)
2. **Go-imap v2 stabilization**: Theo dõi upstream, plan migration lên stable
3. **Add integration tests** với test IMAP/SMTP server (GreenMail, etc.)
4. **Extension hot-reload**: Cho phép dev mode load extension từ filesystem
5. **DB migration versioning**: Thêm forward-only guard mạnh hơn
6. **Connection pool monitoring**: Add metrics cho IMAP pool utilization

---

## 12. Build & Distribution

| Target | Command | Output |
|--------|---------|--------|
| Linux native | `make build` | `build/bin/hsx2mail` |
| Linux Flatpak | `make flatpak` | Flatpak package |
| macOS | `make build` | `build/bin/Hsx2Mail.app` |
| Windows | `make build` | `build/bin/hsx2mail.exe` |
| Windows installer | `make build-windows-installer` | NSIS installer |
| Dev mode | `make dev` | Vite hot-reload + Wails dev |

### Build Tags

- `webkit2_41`: Linux WebKit2GTK 4.1
- `bindings`: Wails binding generation (skips preflight)
- `linux, production`: Production Linux build

### LDFLAGS (OAuth Credentials)

```
-X internal/oauth2.GoogleClientID
-X internal/oauth2.GoogleClientSecret
-X internal/oauth2.MicrosoftClientID
-X internal/oauth2.GoogleTestingClientID
-X internal/oauth2.GoogleTestingClientSecret
```

Nguồn: `.env` hoặc `.env.local`
