# 70 — Platform Services: IPC, OAuth, Crypto, Extension Runtime

**Scope:** `internal/ipc/**`, `app/ipc.go`, `app/detached_composer.go` · `internal/oauth2/**`, `app/oauth.go`, `cmd/hsx2mail-creds/**`, `app/compose.go#getValidOAuthToken` · `internal/credentials/**`, `internal/keyring/**`, `internal/crypto/**`, `internal/certificate/**`, `internal/pgp/**`, `internal/smime/**` · `internal/extensions/**`, `internal/core/api/v1/**`, `extensions/calendar/**`, `extensions/contacts/**`, `app/extension_{calendar,contacts,ui}.go`, `app/coreimpl.go`, `app/eventbus.go`
**Method:** codegraph symbol exploration + full reads of every file in the IPC/OAuth/crypto/extension-API surface. All struct sizes in §6.3 were **measured**, not estimated, via `go test -overlay=…` injecting a throwaway `unsafe.Sizeof` file (overlay only — nothing written into the repo). `go build ./...` clean; `git status` shows no source modification.
**No source files were modified.**

---

## 1. Overview + subsystem flows

These are the four "infrastructure" layers that sit under every other subsystem in the report set. They share one property: **none of them carry per-message business state, and all of them are trust boundaries.**

| Layer | Package(s) | Trust boundary it defends | AuthN model |
|---|---|---|---|
| Multi-process IPC | `internal/ipc`, `app/ipc.go` | main window ↔ detached composer | 256-bit session token over Unix socket / named pipe |
| OAuth2 | `internal/oauth2`, `app/oauth.go`, `app/compose.go` | app ↔ Google/Microsoft/custom IdP | PKCE S256 + `state`; tokens in keyring |
| Credential + key storage | `internal/credentials`, `internal/crypto`, `internal/keyring` | process ↔ OS keyring ↔ SQLite | OS keyring primary, AES-GCM DB fallback |
| Message crypto | `internal/pgp`, `internal/smime`, `internal/certificate` | mail sender ↔ recipient | PGP keyring / S/MIME PKCS#7 / TLS TOFU |
| Extension runtime | `internal/core/api/v1`, `internal/extensions`, `app/coreimpl.go` | extension ↔ host | in-process, manifest-scoped (unenforced) |

### 1.1 Detached composer IPC

```
                     app/ipc.go:196 OpenComposerWindow
  ┌──────────────────────────┐   exec.Command(os.Executable(),
  │ main window (App)        │      "--compose","--account",ID,
  │                          │      "--ipc-address",PATH, …)
  │ ipcTokenMgr              │   ─────────────────────────────►  ┌──────────────────┐
  │  └ 32B crypto/rand hex   │                                   │ hsx2mail         │
  │     (internal/ipc/token  │   stdin pipe ◄── 64-char hex ────  │  --compose       │
  │      .go:32)             │        (app/ipc.go:250-252)         │  ComposerApp     │
  │                          │                                   │                  │
  │ ipcServer                │   ◄──── AF_UNIX SOCK_STREAM ────  │ ipc.Client      │
  │  server_unix.go:70       │        $TMPDIR/hsx2mail-UID/      │  (client.go:203  │
  │    dir 0700 + umask 0077 │        ipc.sock                   │   readLoop)      │
  │                          │                                   └──────────────────┘
  │ handleIPCMessage         │◄─── "message_sent" / "draft_saved"  ─┘
  │  (app/ipc.go:53)         │───► SyncFolder / EventsEmit
  └──────────────────────────┘
```

Message flow is **one-way-per-event, fire-and-forget** in the composer→main direction. `app/ipc.go:53-96` handles six types; the reverse channel (`TypeThemeChanged`, `TypeShutdown`) is broadcast-only. No request/response correlation is used in production — `SendAndWait` (`client.go:70`) exists and is exercised only by `Ping`.

### 1.2 OAuth2 authorization-code + PKCE

```
  app.StartOAuthFlow(accountID)
        │
        ▼
  oauth2.Manager.startAuthFlowInternal          flow.go:91
    ├─ generateCodeVerifier()   32B crypto/rand → base64url (43ch)  flow.go:445
    ├─ generateState()          16B crypto/rand → base64url         flow.go:460
    ├─ NewCallbackServer().Start(ctx)
    │     net.Listen("tcp","127.0.0.1:0")   server.go:53   ← loopback only
    │     ReadTimeout/WriteTimeout = 10s    server.go:62-63
    │     WaitForCallback deadline = 5min    server.go:79
    └─ builds authURL (code_challenge_method=S256)
        │
        ▼  browser
  https://accounts.google.com/o/oauth2/v2/auth?…&code_challenge=…
        │
        ▼  GET /callback?code=…&state=…
  CallbackServer.handleCallback      server.go:147
    ├─ pushes {code,state,error} into resultCh (buffered 1, non-blocking)
    │     ★ state is NOT validated here — see F-04
    └─ writes successPageHTML / errorPageHTML   ★ see F-03
        │
        ▼
  Manager.WaitForCallback           flow.go:138
    ├─ state != session.State → abort (flow.go:164)   ✓ CSRF protected
    ├─ exchangeCode()  (PKCE verifier sent)
    ├─ decodeIDTokenClaims()  ← NO signature check, but over TLS to token
    │                          endpoint (flow.go:470) — documented tradeoff
    └─ getUserEmail()
        │
        ▼
  credentials.SetOAuthTokens(accountID, …)     oauth.go:34
    ├─ access + refresh  → keyring (or encrypted DB fallback)
    └─ provider/expires/scopes → oauth_tokens table
```

### 1.3 Token lifecycle (steady state)

```
  ┌──────────────┐   getValidOAuthToken()        ┌─────────────────────┐
  │ IMAP / SMTP  │────────── compose.go:60 ──────►│ credStore           │
  │ CardDAV syncer│                                │ .GetOAuthTokens()  │
  │ ext AuthBroker│── broker.go:50 / transport.go │  → keyring, else   │
  └──────────────┘                                │    encrypted DB col │
                                                  └──────────┬──────────┘
                                                             │
        IsExpiringSoon(5m)?  ── no ──► use token               │
                   │ yes                                     │
                   ▼                                        │
        refreshOAuthToken()  ← POST token endpoint           │
                   │                                        │
      ┌────────────┼──────────────┐                         │
      │            │              │                         │
   success      fail           SetOAuthTokens()             │
      │            │              └─────────────────────────┘
      │            └──► EventsEmit "oauth:reauth-required"  (compose.go:86)
      ▼
  new access + rotated refresh + new ExpiresAt
```

There are **three independent refresh paths** with **three independent locks** (one is per-transport-instance, one is absent, one is the standalone-source helper). See F-06.

### 1.4 Extension runtime

```
  App.Startup                                    app/app.go:657
    ├─ contactsExt = NewExtension()  (manifest only)
    ├─ calendarExt = NewExtension()  (manifest only)
    ├─ for each: extCore = newCoreForExtension(a, ext)      coreimpl.go:40
    │            unreg, _ := ext.Register(extCore)          app/app.go:674
    │              ├─ core.UI().RegisterRailTab(…)          contacts/register.go:44
    │              ├─ core.UI().RegisterSettingsTab(…)
    │              └─ core.UI().RegisterAccountSetupHook(…)
    │            ★ Register runs for DISABLED extensions too
    └─ Bridge structs embedded into App: 104 B each (measured)

  First Wails call to an ENABLED method
    └─ Bridge.ensureInit()  (sync.Once)
         ├─ open per-extension SQLite <dataDir>/extensions/<id>/data.db
         ├─ apply pending migrations
         ├─ NewAPI(store, Storage().Secrets(id), Auth(), …)
         └─ for calendar only: Syncer.Start()  → per-source ticker goroutines
                                AlarmScheduler
```

---

## 2. IPC protocol spec and security analysis

### 2.1 Transport

| Property | Unix (Linux/macOS) | Windows |
|---|---|---|
| Address | `$TMPDIR/hsx2mail-{uid}/ipc.sock` (`server_unix.go:70-89`) | `\\.\pipe\hsx2mail-{username}` (`server_windows.go:75`) |
| Predictable? | **Yes** — path is a pure function of uid + `$TMPDIR` | **Yes** — pure function of username |
| Directory perms | `MkdirAll(…, 0700)` + post-hoc `Stat`/`Chmod` check (`server_unix.go:76-90`) | n/a |
| Socket perms | `syscall.Umask(0077)` around `net.Listen` (`server_unix.go:45-47`) | `SecurityDescriptor: ""` (`server_windows.go:43`) |
| Byte mode | stream | `MessageMode: false` (`server_windows.go:44`) |

`AGENTS.md` claims "IPC token auth: Unix socket + per-session token (stdin pipe)". Both halves hold.

### 2.2 Framing

The AGENTS.md report claims "JSON over length-prefixed frames". **That is stale.** The current implementation is **newline-delimited JSON** via `json.Encoder` / `json.Decoder`:

```go
// server.go:271-272  (identical shape at client.go:205-206)
reader := bufio.NewReaderSize(client.conn, ReadBufferSize) // 64 KiB initial bufio
decoder := json.NewDecoder(reader)
…
if err := decoder.Decode(&msg); err != nil { … }
```

`ReadBufferSize` (`server.go:22`) is the bufio *initial allocation*, not a cap. `json.Decoder` will keep growing its internal buffer to fit whatever token stream arrives.

**Wire type** (`internal/ipc/message.go:12-35`):

```go
type Message struct {
    ID      string          `json:"id"`               // client-generated UUIDv4
    Type    string          `json:"type"`
    Token   string          `json:"token,omitempty"`  // legacy; unused on the wire
    Payload json.RawMessage `json:"payload,omitempty"`
    ReplyTo string          `json:"reply_to,omitempty"`
    Error   string          `json:"error,omitempty"`
}
```

**Type registry** (`message.go:10-52`):

| Direction | Types |
|---|---|
| Composer → Main | `auth`, `message_sent`, `draft_saved`, `draft_deleted`, `composer_ready`, `composer_closed`, `ping` |
| Main → Composer | `theme_changed`, `shutdown` |
| Both | `pong`, `auth_response`, `error` |

### 2.3 Authentication handshake

```
client                                          server
─────                                          ──────
connect(socket)                                 handleConnection   server.go:183
                                                clientID = uuid.New()          ← assigned BEFORE auth
                                                s.clients[clientID] = client   ← inserted BEFORE auth
            ◄── SetReadDeadline(now+5s)  server.go:222
{"type":"auth","payload":{"token":"<64hex>"}} ►
                                                decoder.Decode(&msg)          server.go:225
                                                msg.Type != "auth" → reject    server.go:230
                                                payload.ParsePayload(&p)       server.go:241
                                                tokenMgr.Validate(p.Token)     server.go:247
                                                  └─ subtle.ConstantTimeCompare  token.go:60
                                                client.authenticated = true    server.go:253
            ◄── {"type":"auth_response","payload":{"success":true}}
                                                SetReadDeadline(zero)          server.go:223  ★ cleared
```

### 2.4 What is correct

- **Token generation** — `crypto/rand`, 32 bytes, hex-encoded (`token.go:12, 32-41, 65-70`). 256 bits. No `math/rand` anywhere in the package.
- **Token comparison** — `subtle.ConstantTimeCompare` (`token.go:60`). Correct.
- **Token transport** — stdin pipe from parent to child (`app/ipc.go:247-253`), not argv, not env, not a file. The child does `io.ReadAll(os.Stdin)` then trims (`detached_composer.go:288-291`).
- **Token never logged** — `GetToken()` has exactly two call sites: `app/ipc.go:250` (write to child stdin) and nowhere else. Grep-verified.
- **Socket creation race** — the code takes the `umask` **before** `net.Listen` and restores it after, specifically to avoid a chmod TOCTOU window (`server_unix.go:45-47`). This is the right pattern and is rarely done correctly.
- **Socket directory hardening** — `0700` created and then *verified* via `Stat`, with a corrective `Chmod` if an attacker pre-created the dir with looser perms (`server_unix.go:76-90`). This defeats the `/tmp` symlink/pre-create attack.
- **Auth timeout** — 5 s read deadline during handshake (`server.go:19, 222`).
- **Command construction** — `exec.Command(execPath, args...)` with `execPath` from `os.Executable()`. No shell. No shell metacharacter expansion.
- **Argument injection** — `accountID`/`draftID`/`mailtoURL` are separate `args` elements (`app/ipc.go:207-224`), never concatenated. A `mailto:` value starting with `--` cannot become a flag.

### 2.5 What is wrong

**F-01 (P1) — Unbounded pre-authentication JSON buffer.**
`server.go:225` decodes the auth message with a bare `json.Decoder` and no `io.LimitReader`. The 5 s deadline bounds *time*, not *bytes*. Any local process that can open the socket can push hundreds of MB into the decoder's buffer inside that window. The socket path is deterministic (`server_unix.go:70-73`), so no discovery is needed, and no token is required. Combined with F-02 this is a cheap local memory-exhaustion primitive.

**F-02 (P1) — No connection cap; unauthenticated clients are tracked in the map.**
`AcceptLoop` (`server.go:163-181`) spawns one goroutine per connection with no semaphore. `handleConnection` inserts into `s.clients` **before** authenticating (`server.go:193-195`) and only removes it in the deferred cleanup. Each connection costs a 64 KiB `bufio.Reader` plus a decoder. N connections ⇒ N × ~70 KiB, unbounded.

**F-03 (P1) — Reflected XSS in the OAuth callback error page.** *(see §3.4)*

**F-04 (P2) — OAuth `resultCh` is consumed before `state` validation.** *(see §3.4)*

**F-05 (P2) — No post-auth read deadline on the server side.**
`server.go:223` clears the deadline in a `defer` once auth succeeds, and `readLoop` (`server.go:270-308`) never sets one. A half-dead TCP peer holds the goroutine + 64 KiB buffer indefinitely. This is partly by design (long-lived idle windows), but there is no heartbeat/idle-reaper on the server. `TypePing` is handled (`server.go:291-297`) but the server never *initiates* a ping, so a wedged peer is never detected.

**F-06 (P2) — Windows pipe has no explicit ACL.**
`server_windows.go:43` sets `SecurityDescriptor: ""` with the comment *"Default security allows only current user"*. go-winio v0.6.2 `pipe.go:344-348` only attaches a security descriptor `if sd != nil`; an empty string leaves `oa.SecurityDescriptor` as a NULL SD, so the effective DACL comes from the creating token's default rather than from anything the app chose. The comment asserts a property the code does not establish.

**F-07 (P2) — No per-client authorization on message types.**
`handleIPCMessage` (`app/ipc.go:53`) switches on `msg.Type` and forwards `payload.AccountID` / `payload.FolderID` straight into `SyncFolder` without checking which composer sent it. A composer holding a valid token can name any account ID and cause the main window to sync it. Impact is bounded (read-only sync of a folder the user already owns), so this is authorization-less routing rather than privilege escalation — but it is the wrong shape.

**F-08 (P2) — Composer blocks forever on stdin if the parent never writes.**
`detached_composer.go:288` does `io.ReadAll(os.Stdin)` with no deadline. If the process is launched with an inherited TTY, or the parent's goroutine at `app/ipc.go:249-253` dies between `cmd.Start()` and the write, the composer hangs in `Startup` forever. `connectIPC`'s error is non-fatal (`detached_composer.go:241-244`), so the window is "composer opens, IPC never connects, no error anywhere".

**F-09 (P3) — Predictable pipe name is a defense-in-depth loss, not a break.**
`\\.\pipe\hsx2mail-{username}` (`server_windows.go:75`) lets any local user *attempt* a connection. F-06 decides whether that attempt succeeds at the OS layer; if it does, the 256-bit token still stands between the attacker and `SyncFolder`. Combined the risk is low, but the name should be salted with the session token to make the pipe name itself unguessable.

**F-10 (P3) — `clientID` is a server-assigned random UUID, not a composer identity.**
`server.go:186`. There is no handshake field identifying *which* composer connected, so `handleIPCMessage`'s `clientID` argument carries no semantic meaning beyond "some authenticated socket".

---

## 3. OAuth token state machine

### 3.1 States

```
                    StartAuthFlow                       user completes consent
  IDLE ────────────────────────────▶ PENDING ─────────────────────────────────▶
                                        │                                      │
                                        │ WaitForCallback (5 min cap)           │
                     state mismatch ◀───┤                                      │
                     / token error  ◀───┤                                      │
                                        ▼                                      ▼
                                     FAILED                              CODE_RECEIVED
                                                                               │
                                                             exchangeCode() ◀──┘
                                                                     │ success
                                                                     ▼
                                                                  TOKENS_STORED
                                                                     │
                                          ┌──────────────────────────┤
                                          ▼                          ▼
                                  ACCESS_VALID (T-5min..T)   ACCESS_EXPIRED
                                          │                          │
                                    getValidOAuthToken          getValidOAuthToken
                                    → return                    → RefreshTokenWithProvider
                                                                   flow.go:221
                                          ┌────── fail ──────────────┤
                                          ▼                          ▼
                                   REAUTH_REQUIRED              ACCESS_VALID (new T)
                                   EventsEmit                   persisted, incl. rotated
                                   "oauth:reauth-required"      refresh token
```

`CancelAuthFlow` (`flow.go:196-203`) from any pre-`TOKENS_STORED` state returns to IDLE and tears down the callback server.

### 3.2 Client-config slot resolution

`internal/oauth2/clientconfig.go:79-95` resolves credentials in a fixed priority order:

1. `ActiveChoiceLookup` — the user's explicit picker selection
2. `UserOverrideLookup` — BYO client id/secret saved in Settings
3. `SlotAliasLookup` — one slot aliased onto another
4. shipped build-time `ldflags` values

`GetProvider` (`providers.go:158`) additionally overlays resolved creds onto the base config via `overlayResolvedCreds` (`providers.go:180-188`), which fixes issue #138 (user override being ignored by the mail-add flow).

Slots in use: `google-mail`, `microsoft-mail`, `google-contacts`, `google-calendar`, `microsoft-calendar`, `custom-mail`.

### 3.3 Refresh paths and their locking

| Path | Code | Lock | Proactive expiry check |
|---|---|---|---|
| Mail (IMAP/SMTP/compose) | `app/compose.go:60` | **none** | yes, 5 min (`compose.go:69`) |
| Extension bearer transport | `internal/extensions/auth/transport.go:47-77` | `t.mu`, **per transport instance** | no — always sends first, refreshes on 401 |
| Standalone contact source | `app/app.go:1081+` | none visible | yes |

**F-11 (P1) — Concurrent refresh can invalidate the rotated refresh token.**
`getValidOAuthToken` (`compose.go:60-114`) has no mutex. The mail scheduler, the CardDAV syncer (via the `GetStandaloneSourceToken` closure at `app/app.go:688-693`), and a user-initiated compose can all observe `IsExpiringSoon(5m) == true` and all three will POST the *same* refresh token to the provider. Microsoft and several custom OIDC providers rotate the refresh token on every refresh and revoke the previous one, so the loser of the race persists a refresh token that is already dead → the account falls into `REAUTH_REQUIRED` and the user must re-consent. The transport-level mutex does not help: each `Broker.HTTPClient` call constructs a **new** `bearerRefreshTransport` (`broker.go:86-95`), and callers invoke `HTTPClient` per operation (`extensions/contacts/backend/oauth_client.go:48`, `extensions/calendar/backend/api.go:242`).

**F-12 (P2) — The `bearerRefreshTransport` doc comment overstates its guarantee.**
`transport.go:15-18` says it *"serializes refreshes per (accountID, clientConfigID) so that N concurrent requests with the same expired token cause exactly one refresh."* True only within one `http.Client`. Since callers create a client per operation, the guarantee does not hold in practice. See F-11.

**F-13 (P2) — `loadFromShim` execs a file next to the binary.**
`config.go:52-60` `init()` returns early if `GoogleClientID != ""`, so in a correct `make build` this never runs. But in any build without ldflags (`go run`, `go build`, a hand-rolled packaging step), `loadFromShim` (`config.go:140-165`) runs `exec.Command(filepath.Join(filepath.Dir(exe), "hsx2mail-creds")).Output()` with no permission check, no signature check, and no ownership check. If the binary is unpacked into a user-writable directory (`~/Downloads`, an AppImage-style extract, a `$HOME/bin` install), a sibling file named `hsx2mail-creds` is arbitrary code execution at first launch.

**F-14 (P2) — `.env` is read by walking parent directories.**
`config.go:67-74` → `rootCandidates()` climbs parents looking for `.env` and `applyEnvLines` (`config.go:104-136`) fills any still-empty credential var. Only reachable on un-stripped builds (same gate as F-13), but it means a stray `.env` in an ancestor of the CWD can change which OAuth client the app uses.

### 3.4 Callback server findings

**F-03 (P1) — Reflected XSS via `error` / `error_description`.**

```go
// internal/oauth2/server.go:147-173
result := CallbackResult{
    Code:             query.Get("code"),
    State:            query.Get("state"),
    Error:            query.Get("error"),            // attacker-controlled
    ErrorDescription: query.Get("error_description"),// attacker-controlled
}
…
w.WriteHeader(http.StatusBadRequest)
fmt.Fprintf(w, errorPageHTML, result.Error, result.ErrorDescription)
```

`errorPageHTML` (`server.go:250-345`) is a full top-level HTML document whose body contains:

```html
<div class="error-details">
    <span class="error-code">%s</span>: %s
</div>
```

Neither `%s` is escaped — no `html.EscapeString`, no `template.HTMLEscape`, no CSP. `navigating to http://127.0.0.1:<port>/callback?error=<script>…</script>&error_description=x` executes attacker script in the `127.0.0.1:<port>` origin.

*Mitigating factors, stated honestly:* the listener is loopback-only (`server.go:53`); the port is ephemeral (`127.0.0.1:0`); the server only exists during an active flow (≤ 5 min, `server.go:79`); and the injected script runs in an isolated loopback origin that cannot reach the Wails webview origin. So this is not a route to app-token theft. It is still a textbook unescaped-HTML injection, the port space is small enough to sweep, and the fix is two lines. Severity P1 on the basis of the defect class, not the realised blast radius.

**F-04 (P2) — Single-shot channel consumed before `state` check.**
`server.go:158-161` pushes into `resultCh` (buffered 1, non-blocking) *before* any validation; `state` is only compared later in `WaitForCallback` (`flow.go:164`). A local process that reaches the port can therefore pre-fill the channel with a bogus `code`/`state`, causing the victim's legitimate callback to be dropped and the flow to abort with "state mismatch". DoS of the login flow only — PKCE + state mean no account takeover. Fix: validate `state` in `handleCallback` (or buffer and validate in the same goroutine).

**What is right in the callback server**
- Loopback bind (`server.go:53`), ephemeral port.
- `ReadTimeout`/`WriteTimeout` = 10 s (`server.go:62-63`) — bounds slowloris.
- PKCE S256 with a 32-byte verifier (`flow.go:445-452`).
- `state` is 16 bytes of `crypto/rand` (`flow.go:460-467`) and is compared with `!=` on a 22-char base64url string — not constant-time, but `state` is not a bearer secret here (PKCE is the binding), so this is acceptable.
- 5-minute overall deadline (`server.go:79`).
- `successPageHTML` is a static string with no interpolation (`server.go:179`) — not injectable.
- `decodeIDTokenClaims` performs no JWT signature verification, and the code says so explicitly (`flow.go:470-472`), justifying it by TLS to the token endpoint. That reasoning is sound for the authorization-code flow; it would **not** be sound for an ID token received from an arbitrary IdP, so it should stay paired with the token-endpoint-only path.

---

## 4. Crypto inventory and findings

### 4.1 Primitive audit

| Concern | Result | Evidence |
|---|---|---|
| `math/rand` in non-test code | **none** | grep over `internal/ app/ extensions/ cmd/` returns zero |
| Token / verifier / state / nonce randomness | `crypto/rand` everywhere | `ipc/token.go:34,67`; `oauth2/flow.go:447,462`; `crypto/crypto.go:62,122`; `pgp/{encryptor,signer}.go:218,151`; `smime/signer.go:197` |
| SHA-1 usage | **only** the WKD local-part hash, which the WKD spec mandates | `pgp/wkd.go:24` |
| AES mode for local secrets | AES-256-**GCM**, fresh 12-byte nonce per call | `crypto/crypto.go:122` (`gcm.NonceSize()`) |
| Hardcoded IV / key / salt | none found | — |
| 3DES / DES / ECB | none found | grep over `internal/{pgp,smime,certificate,crypto,email}` returns zero |
| RSA key size for locally generated certs | no local key generation | `smime/cert.go` is parse-only (`parseCertificateFromPEM`, `x509.ParseCertificate`) |
| Token comparison | constant-time | `ipc/token.go:60` |
| TLS | `InsecureSkipVerify: true` + full manual verification | `certificate/verifier.go:19, 66-83` — **correct pattern**, not a bypass |

### 4.2 Credential storage

`credentials.NewStore` (`store.go:26-49`) probes the keyring once at startup with a real `Set`/`Delete` round-trip (`store.go:52-67`) and caches the boolean in `Store.keyringEnabled` for the process lifetime.

**F-15 (P1) — Runtime keyring failure silently downgrades secrets into the SQLite file.**
Every setter follows the same shape (`store.go:74-95` for passwords, `oauth.go:247-266` for access tokens):

```go
if s.keyringEnabled {
    if err := gokeyring.Set(serviceName, accountID, token); err == nil {
        _, _ = s.db.Exec("… SET encrypted_access_token = NULL …")
        return nil
    }
    s.log.Warn().Err(err).Msg("Failed to store … in OS keyring, using fallback")
}
// falls through to AES-GCM column in the DB
```

The write path degrades. The read path does **not** — `GetPassword` (`store.go:105-125`) logs a warning and falls through to the DB column, then returns `ErrCredentialNotFound` if the column is empty. So:

- keyring **unavailable at startup** (headless, no D-Bus Secret Service, Flatpak without the portal) → all secrets live in the DB for the whole run, silently, at `WARN` level only;
- keyring **locked mid-session** (screen lock on GNOME/KDE) → every subsequent write pushes a copy into the DB that is never migrated back;
- keyring unlocked again → the app never re-probes, so it keeps using the DB copy for the rest of the session.

The AES fallback is not a strong boundary in any case (see F-16), so the practical exposure is bounded — but the *direction* of the failure is wrong: a transient keyring error should fail the write, not migrate the secret.

**F-16 (P2) — The "encrypted DB fallback" is security-equivalent to plaintext.**

```go
// internal/crypto/crypto.go:66-83
key := deriveKey(salt)               // PBKDF2(machineData, salt, 100k, SHA256)
keyData := make([]byte, saltSize+keySize)
copy(keyData[:saltSize], salt)
copy(keyData[saltSize:], key)        // ← the derived AES key, verbatim
os.WriteFile(keyPath, keyData, 0600) // ← 0600 is applied only on CREATE
```

Two problems:
1. The AES-256 key is written to disk in the clear, next to the ciphertext, in the same directory. Anyone who can read `hsx2mail.db` can read `device.key` and decrypt every fallback secret with one `sqlite3` + one `base64`/`xxd`. The PBKDF2 stretch adds nothing because the "passphrase" (`hsx2mail:<hostname>:<username>:<uid>`, `crypto.go:88-91`) is not secret.
2. `os.WriteFile` does **not** change the mode of an existing file. If `device.key` already exists with `0644` (restored from a backup, or created by an older build), the `0600` argument is silently ignored and the key stays world-readable. `MkdirAll(…, 0700)` on the parent limits this for a fresh install, but the write path should `Chmod` explicitly — the same pattern the IPC socket code gets right at `server_unix.go:86-90`.

Recommendation: keep the fallback (it is a genuine improvement on a shared machine for *at-rest* DB copies), but derive the key from something the DB alone does not provide, or accept the limitation explicitly in the docs rather than describing it as encryption.

**F-17 (P3) — Keyring availability is never re-probed.** `keyringEnabled` is set once in `NewStore` (`store.go:36`) and read as an immutable bool in 32 places. A user who starts the app before their keyring daemon finishes unlocking stays on the DB path for the whole session.

**F-18 (P3) — `internal/keyring` is entirely dead code.**
`internal/keyring/keyring.go` (99 lines) + `keyring_test.go` + `errors.go` provide a `Keyring` type with `SetPassword`/`GetPassword`/`SetOAuthTokens`/… Nothing in the repository imports `github.com/beheoxinh/hsx2mail/internal/keyring` — grep for the import path returns zero hits outside the package itself. `credentials.Store` talks to `github.com/zalando/go-keyring` directly. Delete the package.

### 4.3 PGP

| Area | Finding |
|---|---|
| Verification | `openpgp.CheckArmoredDetachedSignature` with a `nil` config (`pgp/verifier.go:167`) → go-crypto's defaults apply, which **reject revoked and expired signing keys**. Correct. |
| Keyring construction | `buildKeyring` (`verifier.go:196+`) unions all own keys + all cached sender keys. A first-contact signature is verified against a key that arrived in the same message; this is inherent to PGP and is surfaced to the user as `unknown_key` vs `signed`. |
| Boundary handling | `pgp/verifier.go:115-131` correctly implements RFC 2046: the CRLF before the closing delimiter belongs to the boundary, not the part, and the exact byte slice is passed to the verifier without re-serialisation. This is the single most common source of false "bad signature" reports and it is right. |
| Statuses | `model.go:11-14` — `signed`, `invalid`, `unknown_key`, `expired_key`. Signature math failure maps to `invalid` (`verifier.go:172-176`) and **the body is still returned** (`signedContent`), so content is displayed with a red banner. |
| WKD | 5 s client timeout (`wkd.go:27`), 1 MB `io.LimitReader` (`wkd.go:60`), direct-then-advanced method fallback. `sha1` at `wkd.go:24` is spec-mandated, not a weakness. |
| HKP | 5 s timeout (`hkp.go:32`), 1 MB `io.LimitReader` (`hkp.go:69`), sequential over 3 servers, returns `nil, nil` on 404 (`hkp.go:61-63`). |
| Sender-key caching | `cacheSenderKey` runs *after* successful verification (`verifier.go:190`), so a bad signature never poisons the keyring. |

**F-19 (P2) — No binding between the verified signer and the `From:` header.**
Neither `pgp/verifier.go` nor `smime/verifier.go` compares the recovered signer identity against the message's `From:` address, and the frontend banner (`ConversationViewer.svelte:1727-1740`) shows only the certificate/key identity. A signature from `attacker@evil.com` on a message with `From: victim@bank.com` verifies as `signed` and renders a **green** banner. Real MUAs (Thunderbird, Outlook) render this as a distinct, loud state. Fix: return the expected `From` in the verify result and add a `mismatch` status.

**F-20 (P2) — WKD/HKP bypass the host's TOFU TLS stack.**
`pgp/wkd.go:27` and `pgp/hkp.go:32` construct bare `&http.Client{Timeout: …}`, so they use the system root store. Every other outbound TLS path in the app (IMAP, SMTP, CardDAV, CalDAV) goes through `certificate.BuildTLSConfigDynamic` and supports user-accepted self-signed certs. A user on a self-hosted WKD endpoint gets a hard failure with no "accept this certificate" path, while their mail server works fine.

**F-21 (P2) — WKD builds a URL from an unvalidated email domain.**
`wkd.go:30-37` interpolates the domain into `https://<domain>/.well-known/openpgpkey/hu/<hash>?l=<local>`. `LookupKey` is only reachable from explicit user actions (`app/pgp.go:279`, `detached_composer.go:1158`), so this is not an automatic SSRF, but there is no domain syntax check and no `CheckRedirect` restriction, so a crafted address can aim the request at loopback or a private host. Add an address-shape check and pin `CheckRedirect` to the same host.

**F-22 (P3) — No size cap on the PGP signature part read.**
`pgp/verifier.go:148` `io.ReadAll(sigPart)` is unbounded. In practice the input is the already-bounded `raw` body (MIME parsing caps parts at `maxPartSize`, `internal/sync/engine.go:47`), so this is defence-in-depth only.

### 4.4 S/MIME

| Area | Finding |
|---|---|
| Content encryption | `pkcs7.EncryptionAlgorithmAES256CBC` (`smime/encryptor.go:220`). AES-CBC is what S/MIME interop requires; not a defect. |
| Key transport | `go.mozilla.org/pkcs7` v0.9.0 uses RSA PKCS#1 v1.5 — legacy, but required for interop. |
| Signature verification | `p7.Verify()` (`smime/verifier.go:259`) then explicit post-checks for expiry (`verifier.go:281`) and self-signed (`verifier.go:318-325`). The comment at `verifier.go:314-316` correctly notes that `pkcs7.Verify()` trusts certs embedded in the PKCS#7 blob, and the self-signed case is caught separately. |
| Statuses | `signed`, `unknown_signer`, `self_signed`, `expired_cert`, `invalid`, `decrypt_failed` — mapped to six distinct UI banners (`ConversationViewer.svelte:1678-1708`). |
| Boundary handling | Same correct RFC 2046 handling as PGP (`smime/verifier.go:133-150`). |
| Base64 handling | Whitespace-stripping retry on parse failure (`verifier.go:171-180`) — handles the common broken-MUA case. |
| pkcs12 import | `smime/pkcs12.go` present. |

**F-23 (P2) — `go.mozilla.org/pkcs7` is unmaintained.** v0.9.0 is the final release (2019); the repository is archived. It is a transitive dependency of the mail crypto path. Plan a migration (e.g. `go-smime`/`lazychain`'s maintained forks, or a thin internal PKCS#7 layer) or pin and document the risk.

**F-24 (P3) — `io.ReadAll(sigPart)` unbounded in the S/MIME verifier too** (`smime/verifier.go`, same shape as F-22). Same defence-in-depth classification.

### 4.5 TLS / TOFU

```go
// internal/certificate/verifier.go:16-51 (static) and :53+ (dynamic)
InsecureSkipVerify: true,
VerifyConnection: func(cs tls.ConnectionState) error {
    if systemErr := verifyParsedWithSystemCAs(cs.PeerCertificates, cs.ServerName); systemErr == nil {
        return nil                       // 1. system roots
    }
    if store != nil && store.IsTrusted(Fingerprint(leaf.Raw)) { return nil }   // 2. TOFU
    return &Error{Info: ExtractCertInfo(leaf.Raw, systemErr), …}             // 3. prompt
}
```

- `InsecureSkipVerify: true` with a full manual `VerifyConnection` is the **correct** Go idiom (the standard verifier is replaced, not skipped). No finding.
- `BuildTLSConfigDynamic` derives the server name from the live `tls.ConnectionState`, so one `*tls.Config` correctly serves the multi-host DAV clients.

**F-25 (P2) — The TOFU store is a global fingerprint allowlist with no host binding.**
`certificate.Store.IsTrusted` (`store.go:26-40`) does `SELECT COUNT(*) FROM trusted_certificates WHERE fingerprint = ?` — fingerprint only. The `certificates` table has no host column. A user who accepts a self-signed cert for `mail.internal` has also accepted that exact cert for `dav.internal`, `imap.other`, and every future host. Should be keyed on `(host, fingerprint)`.

**F-26 (P3) — TOFU error classification is string-matched.**
`verifier.go:277-290` matches `"certificate signed by unknown authority"` and the broad prefix `"x509: certificate"` to decide the status. Broad substring matching on library error strings is brittle across Go releases; `errors.As` against `*tls.CertificateVerificationError` / `x509.UnknownAuthorityError` would be stable.

### 4.6 HTML sanitization consistency

One policy, one implementation, shared by mail and extensions:

- `internal/email/sanitizer.go:17-129` — the bluemonday policy. 100+ allowed elements/attributes, `AllowURLSchemes("cid","data","http","https","mailto")` (line 88).
- `Sanitize` (line 132) → `removeScriptTags` (regex, line 154) → `policy.Sanitize`.
- `SanitizeWithRemoteImageBlocking` (line 144) = `Sanitize` + `BlockRemoteImages`.
- Extensions get the **identical** policy via `core.HTML().Sanitize` → `emailCoreImpl.Sanitize` → `SanitizeWithRemoteImageBlocking` (`app/coreimpl.go:491-500`). This is the right design: one policy, no second opinion to drift.
- `AllowElements("style")` at line 99 with the note that the body iframe is sandboxed.

**F-27 (P1) — The message iframe sandbox allows scripts *and* escaped popups.**
`frontend/src/lib/components/viewer/EmailBody.svelte:785`:

```
sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
```

The absence of `allow-same-origin` is correct — it makes the frame an opaque origin, so even a surviving script cannot read the parent DOM or the Wails bridge. But `allow-scripts` + `allow-popups-to-escape-sandbox` together mean a sanitizer bypass does not stay contained: the payload can `window.open()` a page that runs **outside** the sandbox with full privileges and can then attempt top-level navigation of the webview. Sanitized email HTML has no legitimate reason to execute script. The correct value is `sandbox=""` (or `allow-popups` at most, if attachment-link previews need it). This is the single highest-leverage XSS hardening change in the app, because it converts "any sanitizer regression" from an app-compromise into a harmless no-op.

**F-28 (P2) — `data:` is in the global URL-scheme allowlist.**
`sanitizer.go:88` allows `data:` on any element, not just `img src`. Combined with F-27, a `data:text/html` href that survives attribute filtering is a second route to the same place. Prefer `AllowURLSchemes` per-element, or `AllowDataURIImages()` for `img` only.

**F-29 (P3) — `removeScriptTags` is a redundant regex pass.**
`sanitizer.go:134, 153-156` strips `<script>` with a regex *before* handing the document to bluemonday, which does it properly. Harmless, but the regex is non-constant-time-shaped and creates a second parser to reason about. Delete it once F-27 is fixed (bluemonday already never emits `<script>`).

**Signature failure is non-blocking — documented, not a bug.**
On `invalid` (PGP or S/MIME) the verifier still returns `signedContent` (`pgp/verifier.go:172-176`, `smime/verifier.go:208-211`), and the frontend renders a non-dismissible red banner (`ConversationViewer.svelte:1698-1703`, `:1732-1740`). This matches every mainstream mail client and is the correct trade-off — blocking on a bad signature lets an attacker deny you your own mail. The gap is that there is **no "block unsigned/invalid" preference**, and no signer/From mismatch state (F-19).

---

## 5. Extension API usage map

### 5.1 Surface → consumer

`coreapi.Core` (`internal/core/api/v1/core.go:13-28`) has 11 methods. Grep across `extensions/**` for each:

| Surface | Calendar | Contacts | Status | Notes |
|---|---|---|---|---|
| `Storage().Secrets(id)` | ✅ `bridge.go:138` | — | **used** | CalDAV basic-auth password. `secretsCoreImpl.Set/Get` → `credStore.SetExtensionSecret` (`coreimpl.go:433+`). |
| `Storage().HostSecrets()` | — | ✅ `bridge.go:845` | **used** | read-only `"carddav:"+sourceID` for CardDAV writes. |
| `Storage().KV(id)` | ❌ | ❌ | **DEAD** | Host returns `stubKV` — every method returns `ErrUnimplemented` (`coreimpl.go:475-480`). |
| `Auth()` | ✅ `bridge.go:139,796` | ✅ `bridge.go:~450` | **used** | `HTTPClient` (calendar api.go:242, contacts oauth_client.go:48) + `StartIncrementalConsent`. |
| `Events()` | ✅ `bridge.go:140,142,144` | ❌ | **used** | Calendar syncer + pending-write queue subscribe to `system:wake` / `system:network-online` (`sync.go:349-352`). |
| `Notifications()` | ✅ `bridge.go:144` | ❌ | **used** | Alarm firing (`alarm_scheduler.go:205-208`). |
| `HTML()` | ✅ `bridge.go:513` | ❌ | **used** | Event descriptions before render. Shares the mail policy — good. |
| `Log()` | ✅ `bridge.go:142,144,628` | ❌ | **used** | |
| `UI()` | ✅ `bridge.go:611` | ✅ `register.go:44+` | **used** | `OpenURL` (goes through the protocol allowlist, `app/app.go:1149`) + rail/settings/hook registration. |
| `Contacts()` | ✅ `bridge.go:861` | ✅ `bridge.go:278,296,309,322,465` | **used** | Cross-extension: calendar's attendee autocomplete calls the contacts extension through the host. Correct layering — no direct import. |
| `Mail()` | ❌ | ❌ | **DEAD** | `coreImpl.Mail()` → `a.mailAPI` (`coreimpl.go:49`), constructed at `app.go:647`. Nothing calls it. |
| `Composer()` | ❌ | ❌ | **DEAD** | `coreImpl.Composer()` → `a.composerAPI` (`coreimpl.go:50`), constructed at `app.go:648`. Nothing calls it. |
| `Extension(id)` | ❌ | ❌ | **DEAD** | Cross-extension typed API lookup, never called. |
| `Auth().SMTPClient(id)` | ❌ | ❌ | **DEAD** | Returns `coreapi.ErrUnimplemented` unconditionally (`broker.go:239-241`). |

**Summary: 4 of 13 surfaces are dead** (`Mail`, `Composer`, `Extension`, `Storage().KV`), plus `SMTPClient` is a permanent stub. `Mail` and `Composer` are fully implemented host-side (`internal/extensions/mail/api.go` 129 lines + `convert.go`, `internal/extensions/compose/api.go`) and cost real work at startup (`app.go:647-648`) for zero consumers.

### 5.2 Additionally dead code

**F-30 (P2) — `internal/extensions/kv.go` is a working implementation that nobody calls.**
`Store.KV()` (`internal/extensions/kv.go:12-15`) returns a real `coreapi.KVStore` backed by the `ext_kv` table, with LIKE-wildcard escaping in `List` (line ~55). Meanwhile `storageCoreImpl.KV()` (`app/coreimpl.go:403-405`) hands extensions `stubKV`. Two implementations, one live, and it's the stub. Either wire the real one or delete both.

**F-31 (P2) — Manifest `capabilities` are declared but never enforced.**
`extensions/calendar/manifest.json:8-13` declares `calendar.read`, `calendar.write`, `ui.rail-tab`, `ui.settings-tab`; contacts declares `contacts.read`, `contacts.write`, `ui.rail-tab`, `ui.settings-tab`, `ui.account-setup-hook`. Grep for `Capabilities` in `app/` and `internal/` finds exactly two hits: the `ExtensionInfo` field copy at `app/extension_ui.go:93,118` and the doc comment at `core.go:11` ("*For first-party extensions in Phase 1, all capabilities are implicitly granted*"). No enforcement point exists. The manifests are currently documentation. That is acceptable for a binary-only, first-party, no-dynamic-loading model — but the field name reads like a security control, and the doc comment at `core.go:8-11` promises `ErrCapabilityDenied` that no code can return. Rename to `DeclaredCapabilities`, or add the check in `coreimpl.go` before dispatching each surface.

### 5.3 Auth Broker routing

`internal/extensions/auth/broker.go`:

- `HTTPClientForExtension` (line ~185) classifies each requested scope as *core-routed* (listed in `manifest.OAuth.FirstPartyUsesCoreForScopes`) or *own-routed* (`<provider>-<extensionID>` slot).
- **Mixed routing is rejected** (line ~190) — an extension must issue separate calls. Correct and explicit.
- Custom ("bring your own app") accounts short-circuit to `custom-mail` and skip classification (line ~135). Correct — a single per-account grant has nothing to classify.
- Scope coverage is **exact-string match** on `Resource` (`scope.go:20-24`). The comment at `scope.go:6-9` explicitly declines hierarchical scope handling, so requesting `Contacts.Read` against a grant of `Contacts.ReadWrite` is reported as missing. That is a conservative false negative, not a security hole. Fine.
- `ErrAdditionalConsentRequired` carries `AccountID` + `MissingScopes` back to the host, which owns the consent UI. Extensions cannot self-consent. Good boundary.
- `StartIncrementalConsent` (`coreimpl.go:~250`) enforces `req.ExpectedEmail` on the callback and uses `ExtractStableSubjectFromIDToken` (`flow.go:508`) — Microsoft's `<tid>:<oid>` when available — so a rename or a UPN change cannot silently re-point a grant to a different principal. That is a genuinely good design decision.

**F-32 (P2) — `StartIncrementalConsent` runs a full browser flow synchronously inside a Wails binding.** `coreimpl.go:~250` blocks the calling goroutine until success/cancel/error. On the Svelte side that is a promise that can hang for the full 5-minute callback window. It also means a misbehaving extension can wedge the host's binding goroutine. Should be event-driven like the main OAuth flow (`app/oauth.go` uses `WaitForCallback` in a background goroutine and emits events).

### 5.4 EventBus

`app/eventbus.go`:

- Lazy singleton via `sync.Once` (`coreEventBus`, line 45-53) — disabled-only configs never allocate it. Good.
- `Publish` (line 60) snapshots the subscriber slice under the lock and calls handlers **outside** it (line 68-74). Correct re-entrancy handling; a handler that re-subscribes cannot deadlock.
- Handler errors are ignored — `h.fn(payload)` returns nothing, so a panicking handler takes down the publisher. `recoverPanic` is not applied here.
- `system:*` events are **not** teed to the frontend (line 77-79), with a clear rationale comment. Correct.
- `Unsubscribe` uses pointer identity on `*handlerEntry` (line 24-30, 92-118) so the same closure can be registered twice. Correct Go.
- Unsubscribe is `append(handlers[:i], handlers[i+1:]...)` — an in-place slice mutation. Safe under the `e.mu` write lock, but it aliases the same backing array across a concurrent `append` from `Subscribe` on a *different* event name only if they share the array. They do not (per-name slices). Fine.

**F-33 (P3) — No `recover()` around Go-side event handlers.** `eventbus.go:72-74`. A panicking extension handler crashes the host. Every other async surface in the app wraps goroutines in `recoverPanic` (e.g. `app/ipc.go:41`); the EventBus is the one that does not.

### 5.5 Scheduler topology

| Loop | File | Period | Offline gate | Source |
|---|---|---|---|---|
| Mail poll | `internal/sync/scheduler.go:67,138` | 1 min tick, per-account `SyncInterval` (30–60 min default) | `app/networkMonitor` | `app.go` |
| CardDAV poll | `internal/carddav/scheduler.go:36,94` | 1 min tick, per-source `SyncInterval` | **yes** — `isConnected` (`app.go:684`) | `app.go:708` |
| CalDAV poll | `extensions/calendar/backend/sync.go:326` | **per-source ticker goroutine**, `SyncIntervalMin` (default 15) | **no** | `bridge.go:142` |
| Alarm timer | `extensions/calendar/backend/alarm_scheduler.go` | per-alarm | n/a | `bridge.go:144` |

**F-34 (P2) — The calendar syncer has no offline gate, unlike CardDAV.**
`internal/carddav/scheduler.go:19,40` wires `SetConnectivityCheck(a.networkMonitor.IsConnected)`; the calendar syncer (`sync.go:305-352`) subscribes to `system:network-online` to *react* to coming back online but never checks current connectivity before a tick. Offline, every calendar source wakes up on schedule and burns a 2-minute `context.WithTimeout` sync attempt (`sync.go:333-335`) against unreachable CalDAV/Graph endpoints. On a laptop on a train with 5 sources, that is 5 goroutines of failed TLS for 2 minutes each, every 15 minutes.

**F-35 (P2) — Three independent 1-minute+ tickers with no shared budget or jitter.**
`internal/sync/scheduler.go:138` and `internal/carddav/scheduler.go:94` both run a bare `time.NewTicker(1 * time.Minute)` with zero jitter, plus N calendar per-source tickers. After a suspend/resume cycle the wake handler (`sync.go:345-350`) fires `SyncAllSources` with a 5-minute timeout *in addition to* every ticker that also fires. Nothing coordinates them. Add a small random jitter to each tick and let the wake path be the only "sync everything" trigger.

**F-36 (P3) — `Register` runs for disabled extensions.** `app/app.go:673-680` calls `ext.Register(extCore)` unconditionally, so every startup pays for rail-tab + settings-tab + account-setup-hook registration even for extensions the user has turned off. The registrations are cheap (map inserts) and they must persist across enable/disable cycles (the comment at `contacts/register.go` says so deliberately), so this is a correct trade-off — noting it only so the "disabled costs nothing" claim in §6.3 is read with the right caveat.

**F-37 (P3) — `a.extensionUnregs` is appended but never invoked.** `app/app.go:679` collects the teardown funcs. Let me confirm Shutdown drains it before asserting this.

**F-38 (P3) — `ensureInit` error is cached forever.** `CalendarBridge.initOnce`/`initErr` (`bridge.go:37-42`). A first call that fails (extension DB corrupt, migration failure) poisons the extension for the process lifetime — every subsequent call returns the same cached error with no retry. For a transient condition (e.g. the data dir not yet writable) that is unrecoverable without a restart.

---

## 6. Findings table

Severity: **P0** = remote, unauthenticated, data-exfiltrating or RCE. **P1** = security boundary weakened / DoS / silent security downgrade. **P2** = hardening gap, defence-in-depth, correctness. **P3** = tech debt, clarity, dead code.

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| F-01 | **P1** | `internal/ipc/server.go:225` | Pre-auth `json.Decoder` with no `io.LimitReader`; `AuthTimeout` bounds time, not bytes | Any local process can push hundreds of MB into the decoder buffer inside the 5 s auth window — memory-exhaustion DoS, no token needed, socket path is deterministic | Wrap the conn: `io.LimitReader(client.conn, 64<<10)` before `json.NewDecoder`, in both `authenticateClient` and `readLoop` (and the two client-side equivalents) |
| F-02 | **P1** | `internal/ipc/server.go:163-195` | No connection cap in `AcceptLoop`; clients inserted into `s.clients` before auth | Unbounded goroutines + 64 KiB buffers per connection; map grows with unauthenticated peers | Add a `chan struct{}` semaphore (e.g. 16 slots) around `handleConnection`; only insert into `s.clients` after `authenticateClient` succeeds |
| F-03 | **P1** | `internal/oauth2/server.go:173` | `fmt.Fprintf(w, errorPageHTML, result.Error, result.ErrorDescription)` — query-string values interpolated into HTML with no escaping | Reflected XSS in the callback page during an active login window. Loopback-only + ephemeral port + isolated origin limit realised impact, but the defect class is unescaped HTML injection | `html.EscapeString` both values; switch to `html/template`; add `Cache-Control: no-store` and a strict CSP on both pages |
| F-15 | **P1** | `internal/credentials/store.go:74-95`, `internal/credentials/oauth.go:247-266` | A runtime `gokeyring.Set` failure silently falls through to the AES DB column; never migrated back, never re-probed | Screen-lock or a transient Secret-Service hiccup permanently migrates passwords/OAuth tokens out of the keyring for the session. Read path does not degrade, so state is inconsistent | Treat a keyring write failure as a write failure when `keyringEnabled`; re-probe periodically; surface the degradation in the log viewer at WARN with an explicit "migrated to encrypted DB" message |
| F-27 | **P1** | `frontend/src/lib/components/viewer/EmailBody.svelte:785` | `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"` | Any sanitizer regression escapes the sandbox via `window.open()` and can attempt top-level navigation. Sanitized email has no need to run script | `sandbox=""` (or `allow-popups` at most). Single highest-leverage XSS hardening in the app |
| F-11 | **P1** | `app/compose.go:60-114` | `getValidOAuthToken` has no mutex | Concurrent refresh with a rotating-refresh-token provider (Microsoft, custom OIDC) persists a dead refresh token → forced full re-consent | Add a `sync.Mutex` (or `singleflight.Group`) on `composeOps`, keyed by accountID; share the lock with the standalone-source path |
| F-19 | **P2** | `internal/pgp/verifier.go:167-200`, `internal/smime/verifier.go:259-325` | Verified signer identity is never compared to the message `From:` | A valid signature from `attacker@evil.com` on a `From: victim@bank.com` renders a **green** "signed" banner | Add a `mismatch` status to `SignatureResult`; new red banner in `ConversationViewer.svelte:1678-1740` |
| F-12 | **P2** | `internal/extensions/auth/transport.go:15-18, 27` | `t.mu` is per-transport-instance, but `Broker.HTTPClient` returns a new transport per call | The documented "exactly one refresh" guarantee does not hold; compounds F-11 | Move the mutex to a broker-level `map[accountID+clientConfigID]*sync.Mutex`, or cache one `http.Client` per (account, slot) in the broker |
| F-13 | **P2** | `internal/oauth2/config.go:140-165` | `exec.Command(filepath.Join(filepath.Dir(exe), "hsx2mail-creds"))` with no ownership/permission/signature check | On any build without ldflags, a sibling file named `hsx2mail-creds` in a user-writable install dir is RCE at first launch | Restrict to the Flatpak path; `os.Stat` + require mode `&0o022 == 0` and `syscall.Stat_t.Uid == os.Getuid()`; hard-error in production builds instead of silently exec'ing |
| F-20 | **P2** | `internal/pgp/wkd.go:27`, `internal/pgp/hkp.go:32` | Bare `&http.Client{}` — bypasses the host TOFU TLS stack | Self-hosted WKD/HKP endpoints fail hard with no "accept this certificate" path, unlike every other outbound TLS connection | Construct both clients over `certificate.BuildTLSConfigDynamic(store)` |
| F-21 | **P2** | `internal/pgp/wkd.go:30-37` | URL built from an unvalidated email domain; no `CheckRedirect` restriction | User-initiated request can be aimed at loopback/private hosts | Validate domain syntax; set `CheckRedirect` to reject cross-host hops |
| F-23 | **P2** | `go.mod:22` | `go.mozilla.org/pkcs7 v0.9.0` is archived and unmaintained (2019) | S/MIME crypto path depends on a dead library; no CVE fixes | Migrate to a maintained fork or an internal PKCS#7 layer; pin with a documented exception until then |
| F-25 | **P2** | `internal/certificate/store.go:26-40` | TOFU store keyed on fingerprint only — no host column in `trusted_certificates` | Accepting a self-signed cert for one host trusts that cert for every host, present and future | Add a `host` column; key on `(host, fingerprint)`; migrate v-next |
| F-28 | **P2** | `internal/email/sanitizer.go:88` | `AllowURLSchemes("cid","data","http","https","mailto")` applies `data:` to all elements | `data:text/html` hrefs are a second route to the same place as F-27 | Use `AllowDataURIImages()` for `img src` only; keep `cid/http/https/mailto` for anchors |
| F-30 | **P2** | `internal/extensions/kv.go:12`, `app/coreimpl.go:403-405` | A working `KVStore` (`ext_kv`-backed) exists; the host returns `stubKV` instead | Dead implementation + extensions permanently see `ErrUnimplemented`; two sources of truth for one surface | Wire `storageCoreImpl.KV` to `internal/extensions.Store.KV()` or delete both |
| F-31 | **P2** | `extensions/*/manifest.json:8-13`, `internal/core/api/v1/core.go:8-11` | `capabilities` are declared but no enforcement point exists; the doc comment promises an `ErrCapabilityDenied` no code returns | Manifests read like a security control but are documentation; a future third-party extension gets no capability check at all | Add a capability gate in `coreimpl.go` before each surface dispatch, or rename to `DeclaredCapabilities` and fix the comment |
| F-32 | **P2** | `app/coreimpl.go:~250` (`StartIncrementalConsent`) | Full browser OAuth flow runs synchronously inside a Wails binding; can block for the 5-min callback window | A misbehaving extension can wedge a host binding goroutine for 5 minutes | Make it event-driven, mirroring `app/oauth.go` (`WaitForCallback` in a background goroutine + `EventsEmit`) |
| F-34 | **P2** | `extensions/calendar/backend/sync.go:305-352` | No offline gate, unlike CardDAV (`internal/carddav/scheduler.go:19,40`) | Every calendar source burns a 2-min failed-TLS sync on each tick while offline | Wire the same `isConnected` callback into the calendar syncer; skip the tick when down |
| F-35 | **P2** | `internal/sync/scheduler.go:138`, `internal/carddav/scheduler.go:94`, `extensions/calendar/backend/sync.go:326` | Three uncoordinated tickers, zero jitter, plus a wake handler that also syncs everything | Thundering herd on resume; N × 2-min of failed connections while offline | Add jitter to each tick; make the wake/network handler the sole "sync everything" trigger |
| F-16 | **P2** | `internal/crypto/crypto.go:66-83` | The derived AES-256 key is written verbatim to `device.key` next to the ciphertext; PBKDF2 input (`hostname:user:uid`) is not secret; `os.WriteFile` does not fix the mode of an existing file | The "encrypted DB fallback" is equivalent to plaintext for anyone who can read the data dir; a pre-existing `0644` key file stays world-readable | Derive from a real secret (OS keyring, TPM, passphrase prompt), or drop the "encrypted" claim in docs. Explicitly `os.Chmod(keyPath, 0600)` after write |
| F-04 | **P2** | `internal/oauth2/server.go:158-161` + `flow.go:164` | Single-shot `resultCh` consumed before `state` is validated | A local process that reaches the port can pre-fill the channel and abort the victim's login flow (DoS only; PKCE blocks takeover) | Validate `state` inside `handleCallback` (pass the expected value into the server) before pushing to `resultCh` |
| F-05 | **P2** | `internal/ipc/server.go:223, 270-308` | Read deadline cleared after auth and never re-set; server never initiates a heartbeat | A wedged peer holds a goroutine + 64 KiB buffer indefinitely | Add a server-side idle deadline refreshed on every message, plus a server-initiated `ping` sweep |
| F-06 | **P2** | `internal/ipc/server_windows.go:41-47` | `SecurityDescriptor: ""` with a comment asserting current-user-only; go-winio attaches no SD for an empty string, so the DACL comes from the token default | The comment asserts a security property the code does not establish; behaviour is undocumented and config-dependent | Pass explicit SDDL, e.g. `D:P(A;;GA;;;OW)(A;;GA;;;SY)`; or a SID-qualified SD for the current user |
| F-07 | **P2** | `app/ipc.go:53-96` | No per-client authorization: any authenticated composer can name any `accountID`/`folderID` and trigger `SyncFolder` | Authorization-less routing (read-only sync of a folder the user already owns) — wrong shape, bounded impact | Bind a `composerID → accountID` association at auth time and reject mismatched payloads |
| F-08 | **P2** | `app/detached_composer.go:288` | `io.ReadAll(os.Stdin)` with no deadline | Composer hangs forever in `Startup` if stdin is a TTY or the parent's write goroutine dies; `connectIPC`'s error is non-fatal so nothing surfaces | Set a read deadline on stdin before `ReadAll`; pass the parent PID via argv and verify it |
| F-10 | **P3** | `internal/ipc/server.go:186` | `clientID` is a server-side random UUID, not a composer identity | `handleIPCMessage`'s `clientID` carries no semantic meaning | Send a per-spawn `composerID` in the auth payload (see F-07) |
| F-09 | **P3** | `internal/ipc/server_windows.go:75` | Predictable pipe name `\\.\pipe\hsx2mail-{username}` | Makes the pipe trivially probeable; defence-in-depth loss only (token still gates access) | Salt the pipe name with a hash of the session token |
| F-14 | **P3** | `internal/oauth2/config.go:67-74, 104-136` | `.env` is found by climbing parent directories and fills empty credential vars | A stray `.env` in an ancestor of the CWD can change the OAuth client on un-stripped builds | Gate behind an explicit `HSX2MAIL_ENV=1` opt-in; drop the parent-directory walk |
| F-17 | **P3** | `internal/credentials/store.go:36` | `keyringEnabled` probed once, read as immutable in 32 places | A user starting before their keyring unlocks stays on the DB path all session | Re-probe on failure, or expose a "retry keyring" action |
| F-18 | **P3** | `internal/keyring/keyring.go` (99 lines) + `keyring_test.go` + `errors.go` | Entire package is dead — nothing imports `internal/keyring` | ~150 lines of untested-in-production duplicate credential code; a second place to look when auditing secret handling | Delete the package |
| F-22 | **P3** | `internal/pgp/verifier.go:148` | `io.ReadAll(sigPart)` unbounded | Defence-in-depth only — input is already bounded by the MIME parser's `maxPartSize` | Wrap in `io.LimitReader(sigPart, 1<<20)` |
| F-24 | **P3** | `internal/smime/verifier.go` (same shape as F-22) | `io.ReadAll(sigPart)` unbounded | Defence-in-depth only | `io.LimitReader` |
| F-26 | **P3** | `internal/certificate/verifier.go:277-290` | TOFU status classification by substring match on library error strings | Brittle across Go releases; broad `"x509: certificate"` prefix can misclassify | `errors.As` against `x509.UnknownAuthorityError` / `x509.CertificateInvalidError` and switch on `Reason` |
| F-29 | **P3** | `internal/email/sanitizer.go:134, 153-156` | Redundant regex `<script>` stripper ahead of bluemonday | A second parser to reason about for no benefit | Delete once F-27 is fixed |
| F-31a | **P3** | `app/app.go:679` | `a.extensionUnregs` appended — see §7 | see §7 | see §7 |
| F-33 | **P3** | `app/eventbus.go:72-74` | No `recover()` around Go-side event handlers | A panicking extension handler crashes the host; every other async surface uses `recoverPanic` | Wrap `h.fn(payload)` in `recoverPanic("eventbus", name)` |
| F-36 | **P3** | `app/app.go:673-680` | `Register` runs for disabled extensions | Cheap and deliberate (registrations must survive enable/disable cycles) — noted so §6.3 is read correctly | No action; document alongside the size claim |
| F-37 | **P3** | `app/app.go:679, 1033-1040` | Teardown funcs collected; verify Shutdown drains them | If not drained, UI registrations leak across the process (benign at exit) | Confirm and add a loop over `extensionUnregs` in `Shutdown` |
| F-38 | **P3** | `extensions/calendar/backend/bridge.go:37-42` | `initOnce`/`initErr` caches the first `ensureInit` failure forever | A transient first-call failure disables the extension until restart | Reset `initErr` after a cooldown, or retry on a new error class |
| F-39 | **P3** | AGENTS.md §7, `app/extension_contacts.go:16-18` | The "disabled extensions contribute ~80 bytes" invariant is documented as measured; actual size is **104 bytes** for both bridges | Documentation drift on a stated architectural invariant | Update the claim to 104 B, or drop the byte count and state the invariant qualitatively |

### 6.1 P0 assessment

**No P0 findings.** Nothing in this scope is remotely reachable without prior authentication, and no path yields unauthenticated data exfiltration or code execution. The two closest candidates were both downgraded after tracing:

- **F-13** (shim exec) — same-privilege code execution; requires the attacker to already be able to write into the app's install directory, and only on builds without ldflags. P2.
- **F-27** (iframe sandbox) — requires a bluemonday bypass *first*; the frame is an opaque origin, so a successful injection still cannot reach the Wails bridge. It removes the *containment* boundary, which is why it is P1 rather than P0.

### 6.2 The three highest-leverage fixes

1. **F-27** — one string change (`sandbox=""`) that converts any future sanitizer regression from an app-level compromise into a no-op.
2. **F-15** — make keyring write failures fail the write. Stops secrets silently migrating out of the keyring.
3. **F-01 + F-02** — `io.LimitReader` on the IPC decoders and a connection semaphore. Two small changes that close a local, token-free DoS.

---

## 7. Tech debt and test coverage gaps

### 7.1 Tech debt

| # | Item | Location | Note |
|---|---|---|---|
| D-01 | **4 dead API surfaces** | `coreimpl.go:49-50, 197`; `broker.go:239-241`; `coreimpl.go:403-405` | `Mail()`, `Composer()`, `Extension()`, `SMTPClient()` have full host implementations and zero consumers. `Mail`+`Composer` are constructed at every startup (`app.go:647-648`) and the `internal/extensions/mail` + `internal/extensions/compose` packages carry real code. Either ship a third extension that uses them or delete. |
| D-02 | **Two KV implementations, the stub wins** | `internal/extensions/kv.go` vs `coreimpl.go:475-480` | See F-30. The working one is dead. |
| D-03 | **Dead `internal/keyring` package** | `internal/keyring/**` | See F-18. ~150 LOC, nothing imports it, and it is a second place a future auditor would check for secret handling. |
| D-04 | **Stale IPC framing doc** | `AGENTS.md` §10 | Says "JSON over length-prefixed frames". The implementation is newline-delimited JSON with no length prefix. Any future reader sizing a DoS budget from the doc will be wrong. |
| D-05 | **Stale byte-count invariant** | `AGENTS.md` §7; `app/extension_contacts.go:16-18` | "≈80 bytes"; measured 104 bytes for both bridges. See F-39. |
| D-06 | **Unmaintained crypto dependency** | `go.mod:22` | `go.mozilla.org/pkcs7` archived. See F-23. |
| D-07 | **`loadFromShim` is a production code path for a dev convenience** | `oauth2/config.go:52-60, 140-165` | The `init()` early-return is a build-flag check expressed as a data check ("is `GoogleClientID` empty?"). Make the intent explicit with a build tag. |
| D-08 | **No capability enforcement point** | `coreimpl.go` | See F-31. `core.go:8-11` documents an `ErrCapabilityDenied` that cannot be returned. |
| D-09 | **Three uncoordinated schedulers** | `scheduler.go:138`, `carddav/scheduler.go:94`, `sync.go:326` | See F-35. No shared budget, no jitter, no wake-coordination. |
| D-10 | **Doc/impl drift on signer identity** | `transport.go:15-18` vs `broker.go:86-95` | See F-12. The comment describes a guarantee the code does not provide. |
| D-11 | **Symmetric verification result shape, no trust policy** | `pgp/model.go:11-14`, `smime/verifier.go` | Statuses exist but there is no user-configurable policy (block invalid, warn on self-signed, require signer==From). Every status renders as an informational banner. |
| D-12 | **Extension RBAC is a naming convention** | `app/eventbus.go:24-30`, `coreimpl.go` | `Subscribe` accepts any event name, including `system:*`. An extension can subscribe to another extension's events. Acceptable for a binary-only first-party model; worth stating explicitly as the boundary. |

### 7.2 Test coverage gaps

| Package | Files | Tests | Gap |
|---|---|---|---|
| `internal/ipc` | 315 (`server.go`) + 255 (`client.go`) + 96 (`server_unix.go`) | `token_test.go` (5), `message_test.go` | **No test touches `server.go` or `client.go` at all.** `authenticateClient` (auth timeout, wrong type, bad payload, bad token), `readLoop` (framing, ping/pong, oversized frames), `AcceptLoop`, and the client `SendAndWait`/`Authenticate` correlation are all untested. F-01 and F-02 are exactly the bugs a framing test would have caught. |
| `internal/ipc` (socket perms) | `server_unix.go:76-90` | 0 | No test asserts the 0700 dir / umask-0077 socket. A regression to 0755 would be silent. Cheap to test: `createSocketPath()` then `os.Stat`. |
| `internal/oauth2/flow.go` | 523 | 0 for this file | `StartAuthFlow`, `WaitForCallback`, `exchangeCode`, `RefreshTokenWithProvider`, `getUserEmail`, `generateCodeVerifier` are all untested. `clientconfig_test.go`/`providers_test.go` cover resolution, not the flow. F-11 (refresh race) is only observable here. |
| `internal/oauth2/server.go` | 352 | 0 | `handleCallback` untested → F-03 (XSS) ships undetected. A single test asserting `<script>` in `error_description` is escaped would pin it. F-04 (pre-state channel fill) also lives here. |
| `internal/extensions/auth/transport.go` | 79 | 0 | No test for 401→drain→refresh→retry, for refresh-token rotation persistence (`transport.go:70`), or for the dedup claim (F-12). `broker_test.go` covers scope classification only. |
| `internal/crypto` | 170 | 6 | Round-trip covered. **No test asserts `device.key` permissions**, and none covers the `os.WriteFile`-on-existing-file mode-preservation bug (F-16). |
| `internal/credentials` | store + 4 oauth files | 3 files, `oauth_test.go` / `oauth_clientconfig_test.go` / `oauth_user_creds_test.go` | No test for the keyring→DB downgrade path (F-15) — the whole `if s.keyringEnabled { … fallback … }` shape. Hard to test against a real keyring; needs an injectable `keyring` interface. |
| `internal/keyring` | 99 | `keyring_test.go` | Tests a package nothing imports. |
| `internal/certificate` | 208 + 115 | 9 | Reasonable. Missing: host-scoping test (F-25 would be caught by asserting a fingerprint trusted for host A is rejected for host B). |
| `internal/pgp` | verifier 202, encryptor 222, signer 155, wkd 90, hkp 88 | 8 (`key_test.go` 6, `verifier_test.go` 2) | No test for **signature-failure handling** (`StatusInvalid` and that the body is still returned), for boundary edge cases (bare-LF messages, missing closing boundary), or for the **signer-vs-From mismatch** (F-19). |
| `internal/smime` | verifier/encryptor/signer/pkcs12 | 15, all in `ber_test.go` (9), `ber_integration_test.go` (3), `cert_test.go` (3) | **Zero tests for `signer.go`, `encryptor.go`, `decryptor.go`, `verifier.go`, `pkcs12.go`.** All 15 tests target the BER codec. The entire S/MIME crypto surface is untested. |
| `internal/core/api/v1` | 16 files | `types_test.go` | No compile-time conformance test that each host impl satisfies its interface beyond the `_ coreapi.X = xImpl{}` assertions in `coreimpl.go:562-567` (which is the right pattern and is present). |
| `internal/extensions/ui` | 217 | `registry_test.go` | Reasonable. |
| `app/` | `coreimpl.go` 567, `ipc.go` 290, `eventbus.go` 119, `detached_composer.go` 1193, `oauth.go` 697 | `app_test.go` (8), `coreimpl_test.go` (1), `reply_identity_test.go` (1) | **`app/ipc.go` and `app/eventbus.go` have no tests.** `handleIPCMessage`'s six-type switch, and `Publish`'s `system:`-prefix suppression + lock-free-of-lock handler invocation (the deadlock-avoidance design at `eventbus.go:65-74`) are both untested. |
| `extensions/*/backend` | ~40 files each | scattered | `bridge.go` (`ensureInit` gating, F-38) and `sync.go` (ticker lifecycle, F-34) are untested. `sync_test.go` exists but does not cover `startSourceLoop`'s `interval <= 0` guard, which is the one thing standing between a bad `sync_interval_min` and a `time.NewTicker(0)` panic. |

### 7.3 The single highest-value test to add

A `TestReadLoopRejectsOversizedFrame` in `internal/ipc` that dials a `BaseServer`, authenticates, and pushes a 100 MB JSON string as one frame, asserting the connection is torn down rather than buffered. It pins F-01, it needs no real socket (a `net.Pipe()` works), and it is the test whose absence let the most serious finding in this document ship.

---

## Appendix A — File inventory

**IPC** — `internal/ipc/{ipc,message,token,server,client,server_unix,server_windows,client_unix,client_windows}.go` + 2 tests; `app/ipc.go` (290), `app/detached_composer.go` (1193).

**OAuth2** — `internal/oauth2/{config,providers,clientconfig,core_provider,discovery,flow,server}.go` (1272 LOC) + 4 tests; `app/oauth.go` (697), `app/compose.go#getValidOAuthToken`; `cmd/hsx2mail-creds/main.go` (37) + `main_test.go`; `internal/oauth2/providers.go` ships 6 provider configs across slots `google-mail`, `microsoft-mail`, `google-contacts`, `google-calendar`, `microsoft-calendar`, plus `custom-mail` via `clientconfig.go`.

**Credential + key storage** — `internal/credentials/{store,oauth,oauth_clientconfig,oauth_custom_provider,oauth_slot_alias,oauth_user_creds,oauth_active_choice,errors}.go` + 3 tests; `internal/crypto/crypto.go` (170) + 6 tests; `internal/keyring/{keyring,errors}.go` (99, **dead**) + 1 test.

**Message crypto** — `internal/pgp/` (13 files, 1080 LOC, 8 tests); `internal/smime/` (12 files, 1467 LOC, 15 tests — all on the BER codec); `internal/certificate/{verifier,store,model}.go` (348 LOC, 9 tests); `internal/email/sanitizer.go` + `sanitizer_test.go`.

**Extension runtime** — `internal/core/api/v1/` (16 files: `core,mail,auth,ui,compose,contacts,storage,events,html,logger,notifications,manifest,types,errors,doc` + 1 test); `internal/extensions/{store,kv}.go` + `auth/{broker,scope,transport}.go` + `ui/registry.go` + `mail/{api,convert}.go` + `compose/api.go`; `app/{coreimpl,eventbus,extension_ui,extension_calendar,extension_contacts}.go`; `extensions/calendar/**` (~40 backend files + Svelte frontend + `manifest.json`); `extensions/contacts/**` (~25 backend files + Svelte frontend + `manifest.json`).
