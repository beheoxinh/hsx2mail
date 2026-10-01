# 30 — Compose, Draft, Send & Undo Pipeline

Analysis date: 2026-09-29 · Commit: working tree at `v0.3.2` · Read-only review.

Scope: `app/{compose,draft,detached_composer,undo,message,actions,ipc,settings,attachment,app}.go`,
`internal/{draft,smtp,email,ipc,undo}/**`, `frontend/src/lib/components/composer/**`,
`frontend/src/lib/composerApi.ts`.

---

## 1. Executive summary

| Question | Answer |
|---|---|
| Is autosave full-rebuild + full re-encrypt + full re-upload? | **Yes, twice.** `encryptDraftBody` (`app/draft.go:111`) encrypts body+attachments for the local row, then `syncToIMAP` (`app/draft.go:326-380`) rebuilds RFC822, re-signs and re-encrypts the whole message (attachments included) and IMAP-APPENDs it. Two full crypto passes per 10 s debounce window. |
| Do attachments round-trip base64 through Wails IPC? | **Yes, every save.** `content_base64` JSON string per attachment (`Composer.svelte:534-540`), cap is **100 MB** per file (`Composer.svelte:355`) → ~133 MB base64 string marshalled on every autosave. |
| Can a draft be lost on crash? | Local DB is written **before** the IMAP append (`app/draft.go:589-597` then `:606`), and `SyncPendingDrafts` (`app/draft.go:655`) re-uploads on next start. **Local loss: no.** Server copy: yes — see F2. |
| Does every save create a new IMAP draft? | No — it deletes the old UID first (`app/draft.go:306-314`) then APPENDs. But the UID can be **lost by a race** (F1), which orphans the old copy permanently. |
| Sent-copy failure leaves the draft? | No — ordering is correct: SMTP send → Sent APPEND (failure is log-only) → draft delete (`app/compose.go:446-471`). Draft is only orphaned if `deleteDraftCore` itself fails. |
| Header injection? | Compose path is protected by `writeHeader` (`internal/smtp/message.go:188`). **MDN path is not** (S1). `sanitizeField` (`app/app.go:153`) exists but is used *only* by `parseMailtoURL`. |
| Undo restores wrong state? | **Yes, two ways** — bulk move across N folders pushes N commands but pops one (F4); and Undo re-pushes a fresh command via `MoveToFolder` (F3). |
| IPC token auth holes? | No exploitable hole found. 256-bit token, constant-time compare, socket dir `0700`, token only via stdin pipe, never on argv or disk. Residual: no post-auth read deadline / no message size cap (S8). |

**Bottom line.** The *logic* of this area is better than typical (crash-safe local draft ordering, correct send→sent→delete ordering, cancel-and-wait guards on the delete path, RFC 5322 Reply-To preference, pooled IMAP connections). The two structural defects are: (a) **the draft autosave path is O(message size) in crypto + IPC + network on every pause in typing**, and (b) **the draft row's `imap_uid` is written back from stale in-memory state**, which can orphan server copies and duplicate drafts.

---

## 2. Pipeline diagrams

### 2.1 Autosave (triggered by every keystroke pause > 10 s)

```
Tiptap onUpdate  (Composer.svelte:1342)          plain-text onInput (Composer.svelte:2044)
 recipients/subject/flags $effect (Composer.svelte:681-686)
        │
        ▼
 scheduleDraftSave()            Composer.svelte:585   ── clearTimeout + reset indicator
        │  setTimeout 10_000   Composer.svelte:345,596
        ▼
 hasContent() + getContentHash()  Composer.svelte:576-581   (hash = recipients|subject|body|att names)
        │  (skipped if hash unchanged)
        ▼
 saveDraft()                    Composer.svelte:609
        │  buildMessage()      Composer.svelte:501   ── every attachment → base64 string
        ▼  ══ Wails IPC (JSON) ══
 App.SaveDraft                  app/draft.go:562
        │
        ├─ draftStore.Get(existingDraftID)          ← reads IMAPUID (may be STALE)
        │
        ├─ draftOps.encryptDraftBody                app/draft.go:111
        │     resolveAttachmentContent()            app/draft.go:95   ← base64-decode ALL attachments
        │     json.Marshal(body + ALL attachments)  app/draft.go:131
        │     smimeEncryptor.EncryptBytes  / pgpEncryptor.EncryptBytes
        │     json.Marshal(attachments)             app/draft.go:176  ← attachments_data
        │
        ├─ draftOps.saveDraftToDB                   app/draft.go:182
        │     localDraft.SyncStatus = Pending       app/draft.go:201
        │     draftStore.Update(localDraft)         internal/draft/store.go:73
        │        └─ writes sync_status, imap_uid, folder_id, attachments_data  ◄── F1
        │
        └─ go draftOps.syncToIMAP                   app/draft.go:284   (background goroutine)
              getSpecialFolder(Drafts)              app/draft.go:288
              poolConn = imapPool.GetConnection     app/draft.go:296
              DeleteMessageByUID(old UID) + UIDEXPUNGE   app/draft.go:306-314  ◄── F2
              msg.ToRFC822()                        internal/smtp/message.go:101
              smimeSigner.SignMessage               app/draft.go:332
              smimeEncryptor.EncryptMessageToSelf   app/draft.go:340
              pgpSigner / pgpEncryptor              app/draft.go:357-366
              conn.AppendMessage(\Draft,\Seen, now) app/draft.go:390
              post-APPEND cancel/delete guard       app/draft.go:397-419
              draftStore.UpdateSyncStatus(uid)      app/draft.go:422
              emitStatus → "draft:syncStatusChanged"
```

### 2.2 Send

```
App.SendMessage                  app/compose.go:542        sendMessage(ctx, id, msg, nil)  ◄── F5
ComposerApp.SendMessage          app/detached_composer.go:653   sendMessage(..., c.currentDraft)
        │
        ▼
 composeOps.sendMessage          app/compose.go:307
        ├─ accountStore.Get  / NoOutgoingServer guard              :316-329
        ├─ msg.ToRFC822()                                            :330
        ├─ S/MIME sign  → encrypt                                   :342-360
        ├─ PGP sign    → encrypt  (if !EncryptMessage)              :362-382
        ├─ smtp.NewClient / Connect / Login                         :383-442
        ├─ recipients = msg.AllRecipients()   (To+Cc+Bcc)           :443
        ├─ client.SendMail(from, recipients, rawMsg)                :446
        ├─ if !providerAutoSavesSentMail: saveToSentFolder (APPEND)  :450-458   (log-only on failure)
        ├─ contactStore.AddOrUpdate for To/Cc                       :459-466
        └─ if d != nil: draftOps.deleteDraftCore(ctx, d)            :468-471   (log-only on failure)
        │
        ▼ (inline)  App.SendMessage → go a.syncSentFolder            app/compose.go:547-553
        ▼ (inline)  Composer.svelte:1185  api.deleteDraft(currentDraftId)  ← fire-and-forget  ◄── F5
        ▼ (detach)  ComposerApp → notifyMessageSent → IPC TypeMessageSent → app/ipc.go:118
```

### 2.3 Detached-composer IPC

```
main app: initIPC                app/ipc.go:18
   ipc.NewTokenManager()  → 32 crypto/rand bytes, hex            internal/ipc/token.go:23
   ipc.NewServer(tm)  (unix: /tmp/hsx2mail-<uid>/, dir 0700)      internal/ipc/server_unix.go:75-88
        │
        ├─ accept → authenticateClient (5 s deadline, ConstantTimeCompare)  internal/ipc/server.go:213-253
        └─ readLoop → OnMessage → App.handleIPCMessage             app/ipc.go:55-97
              message_sent / draft_saved / draft_deleted / composer_ready / composer_closed
              (theme_changed & shutdown are main→composer only)   app/detached_composer.go:318-340
        │
        ▼ spawn
 exec.Command(self, --compose --account .. --ipc-address ..
              --draft-id/--mode/--message-id)                    app/ipc.go:234-243
   stdin.Write(token); stdin.Close()                              app/ipc.go:247-252
        │
        ▼ child
 io.ReadAll(os.Stdin) → c.ipcToken                               app/detached_composer.go:287
 database.Open(paths.DatabasePath())   ← SAME SQLite file as main (WAL, busy_timeout 30 s)  :150
 SaveDraft / SendMessage / DeleteDraft on the shared drafts table
```

---

## 3. Feature inventory

| # | Feature | Entry point | Notes |
|---|---|---|---|
| F-01 | Autosave to local DB | `app/draft.go:562` | 10 s debounce, hash-gated (`Composer.svelte:345,585,602`) |
| F-02 | Autosave to IMAP Drafts | `app/draft.go:284` | delete-old-UID → APPEND `\Draft \Seen`; INTERNALDATE = `time.Now()` (`:390`) |
| F-03 | Re-upload pending drafts on start | `app/draft.go:655` | covers crash between local write and IMAP append |
| F-04 | Cancel/await in-flight sync on delete | `app/draft.go:546` (`App`), `app/detached_composer.go:686` (`ComposerApp`, does **not** wait) | F11 |
| F-05 | Post-APPEND orphan cleanup | `app/draft.go:397-419` | deletes just-appended UID if draft was cancelled mid-APPEND |
| F-06 | S/MIME + PGP draft body encryption | `app/draft.go:111-181` | `draftBodyPayload` JSON, encrypt-to-self |
| F-07 | S/MIME + PGP send signing/encryption | `app/compose.go:342-382` | policy from account settings, per-message override |
| F-08 | Sent-folder APPEND | `app/compose.go:197` (`saveToSentFolder`), skipped for providers that auto-save | |
| F-09 | MDN / read receipts | `internal/smtp/mdn.go:31`, `app/settings.go:389` | raw header writes, S1 |
| F-10 | Reply / Reply-all / Forward | `app/compose.go:632`, `app/detached_composer.go:869` | `selectReplyFromIdentity` (`:955`) prefers the alias that was addressed |
| F-11 | Inline attachment carry-over on reply | `app/compose.go:795-816` | `cid:` attachments re-encoded from DB per save |
| F-12 | Forward attachment carry-over | `app/compose.go:837` | re-fetches raw MIME from IMAP |
| F-13 | Undo: move | `internal/undo/commands.go:125`, pushed `app/actions.go:544` | only move is undoable |
| F-14 | Undo: flag change | `internal/undo/commands.go:28` | **dead code**, never constructed |
| F-15 | `mailto:` deep link | `app/app.go:60-151` | only place `sanitizeField` is applied |
| F-16 | Detached composer | `app/detached_composer.go` + `app/ipc.go` | own Wails window, own process, shared SQLite |
| F-17 | HTML sanitize (inbound mail) | `internal/email/sanitizer.go:14` | bluemonday + `removeScriptTags` |
| F-18 | Remote-image blocking + allowlist | `internal/email/sanitizer.go:141` | |
| F-19 | Attachment open/save | `app/attachment.go:63` (`DownloadAttachment`), `:236` (`OpenPath`) | `exec.Command`, no shell |

---

## 4. Findings

### 4.1 Performance

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| **F-01** | P0 | `app/draft.go:200-203` + `internal/draft/store.go:73-87` | `saveDraftToDB` sets only `SyncStatus = Pending` and then calls `draftStore.Update(localDraft)`, which writes `sync_status, imap_uid, folder_id` from the **in-memory** object loaded at the *start* of `SaveDraft` (`app/draft.go:566-583`). If a background `syncToIMAP` from the previous save already recorded `IMAPUID=100`, the next save writes `IMAPUID=0` back. | UID 100 is **orphaned on the server forever**. Next sync appends 101. Drafts folder accumulates one invisible duplicate per occurrence; the user sees a growing pile of "ghost" drafts on other clients. | Make the sync-state columns write-only from the sync path: drop `imap_uid`/`folder_id`/`sync_status` from the generic `Update`, or re-read the row inside `saveDraftToDB` under the same lock and only overwrite body/flag columns. Add a targeted `UpdateContent(id, ...)` used by autosave. |
| **F-02** | P1 | `app/draft.go:306-314` (delete) → `app/draft.go:390` (append); `internal/imap/client.go:781-812` (`UIDEXPUNGE` when UIDPLUS) | Server-side draft is **permanently expunged before** the replacement is APPENDed. Failure/crash/network drop in between destroys the server copy. | Recoverable only from the local SQLite row, and only after a restart (`SyncPendingDrafts`). A user syncing from a second device, or a second browser, sees the draft vanish. | APPEND first, then expunge the old UID (post-APPEND guard at `:397` already handles cancel). If `AppendMessage` returns a UID equal to the old one or the expunge fails, leave the old copy in place and log. |
| **F-03** | P1 | `Composer.svelte:534-540` + `Composer.svelte:355` | Every autosave marshals **all** attachment bytes as `content_base64` JSON strings through the Wails IPC bridge. Cap is `MAX_ATTACHMENT_SIZE = 100 * 1024 * 1024` per file. A 50 MB attachment ⇒ ~67 MB base64 string ⇒ `JSON.stringify` + IPC + Go `json.Unmarshal` on **every 10 s pause in typing**. | UI jank, multi-hundred-MB transient allocations, slow `SaveDraft` round trip; the debounce is defeated because each save takes longer than the keystroke interval. | Pass attachments by reference: keep content in a Go-side staging table keyed by a `staging_id`; send only the id + metadata over IPC and let `saveDraftToDB`/`syncToIMAP` read the bytes locally. Add a cheap size+hash guard so unchanged attachments are not re-sent. |
| **F-04** | P1 | `app/draft.go:111-181` (pass 1) + `app/draft.go:326-380` (pass 2) | The full message — including every attachment's base64 — is encrypted **twice per save**: once as the JSON `draftBodyPayload` for the local row (`:131-141` S/MIME, `:143-166` PGP), then again as the assembled RFC822 inside `syncToIMAP` (`:340` S/MIME, `:366` PGP). Signing likewise runs twice (`:332`, `:357`). | RSA/AES + CMS/PKCS7 work is O(total bytes) and repeated on every pause. For a 30 MB encrypted draft this is seconds of CPU per autosave, on the UI-blocking Wails call. | Do not re-encrypt attachments: store attachment bytes once, encrypted, keyed by content hash; the local row holds a manifest. Or, simplest: skip the *local* encryption pass and keep the DB column plaintext-protected-at-rest only when the draft is actually flagged for encryption *and* has no attachments. |
| **F-05** | P2 | `app/draft.go:83` (`attachments_data`) + `internal/draft/store.go:82` | `attachments_data` (base64 of all attachments) is re-written into SQLite on every autosave. | Full-blob `UPDATE` per save → WAL churn, page rewrite amplification, periodic 5-min checkpoint stalls. | Same staging-id fix as F-03. |
| **F-06** | P2 | `app/compose.go:795-816` (`GetInlineByMessage`) + `app/compose.go:837-880` (`fetchForwardAttachments`) | Replying/forwarding re-encodes every inline (and, for forward, every regular) attachment to base64 on **every** autosave, even though the content never changes. | Replies to image-heavy threads are the worst case for F-03/F-04. | Reuse the staging id from F-03; attachments carried over from the original message should never be re-sent to the backend. |
| **F-07** | P2 | `app/draft.go:390` | `AppendMessage(..., time.Now(), rawMsg)` uses "now" as INTERNALDATE for **every** autosave. | Draft re-sorts to the top of Drafts on each save; IMAP `SORT`/`THREAD` results jitter. | Preserve the draft's original `created_at` (or the previous INTERNALDATE) for re-appends. |
| **F-08** | P3 | `app/compose.go:446` | No aggregate message-size guard before `DATA`. 100 MB per file × N files is fully buffered in RAM (`bytes.Buffer` + base64 expansion) and uploaded before the server rejects it. | Multipart body is built ~1.33× over the wire size in memory; a rejected send wastes the whole upload. | Cap total composed size (e.g. 50 MB) at `buildMessage()` in the frontend and again in `ToRFC822()`; fail fast with a clear message. |
| **F-09** | — | `Composer.svelte:576-581, 596` | `getContentHash()` stringifies the whole HTML body. | Mitigated — the comment at `:583-584` and the placement inside the `setTimeout` mean it runs once per 10 s window, not per keystroke. | none |

### 4.2 Logic / correctness

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| **F-10** | P1 | `app/actions.go:533-554` + `internal/undo/undo.go:69-84` | A bulk move that spans N source folders pushes **N** `MoveCommand`s (`for sourceFolderID, msgs := range byFolder`). `App.Undo()` (`app/undo.go:20`) calls `Pop()`, which returns and removes exactly **one**. | Undo silently restores only one partition; the rest stay in the destination folder while the toast says "undo completed". | Wrap the per-folder commands in a single composite `BatchCommand`, or push one command carrying `[]sourceFolderID` + `map[sourceFolderID][]msgID`. |
| **F-11** | P1 | `app/undo.go:20-25` + `internal/undo/undo.go:73-84` | `Pop()` **removes** the command before `cmd.Undo()` runs. Any transient failure (offline, IMAP timeout, DB busy) destroys the undo entry. | Undo is one-shot even on error; user cannot retry. | `Peek()` → `Undo()` → `Pop()` on success. `Peek()` already exists (`internal/undo/undo.go:117`). |
| **F-12** | P1 | `app/undo.go:167-169` → `app/actions.go:382` → `app/actions.go:544-553` | `MoveCommand.Undo()` calls `undoCtx.MoveMessagesToFolder` → `App.MoveToFolder`, which **pushes a new `MoveCommand` onto the same stack**. | Undo is a toggle: the stack grows on every undo, `maxSize` fills with ping-pong pairs, and the user can never reach an older command. | Add an unexported `moveToFolderInternal(ids, dest, recordUndo bool)`; `MoveMessagesToFolder` passes `false`. |
| **F-13** | P2 | `app/detached_composer.go:686-696` vs `app/draft.go:546-557` | `ComposerApp.cancelDraftSync()` signals the goroutine and returns immediately; `App.cancelDraftSync()` signals **and waits** on `done`. `ComposerApp.SendMessage` (`:655-661`) then calls `sendMessage(..., c.currentDraft)` which `deleteDraftCore`s the IMAP draft while an APPEND may still be in flight. | Partially covered by the post-APPEND guard (`app/draft.go:397-419`) which deletes the newly appended UID. Residual: the APPEND can land *after* the delete's `SelectMailbox`, leaving a duplicate. | Make `ComposerApp.cancelDraftSync()` wait on `done` exactly like the `App` version. |
| **F-14** | P2 | `Composer.svelte:1182-1188` + `app/compose.go:542-544` | Inline send calls `api.sendMessage(...)` then `deleteDraft()` **fire-and-forget**. `App.SendMessage` passes `nil` as the draft, so the backend never cleans up; cleanup depends entirely on the second Wails call landing. The detached path (`app/detached_composer.go:659`) *does* clean up server-side. | Wails call fails / window closed / tab backgrounded ⇒ a fully-sent message leaves a ghost draft in Drafts. Behaviour also differs between inline and detached composers. | Give the inline path the same treatment: pass the draft id to `SendMessage` and delete server-side, or at minimum `await` the delete before firing the success toast. |
| **F-15** | P2 | `Composer.svelte:1235-1256` | "Save and Close" awaits `saveDraft()` — which returns as soon as the **local** row is written and the IMAP goroutine is spawned — then calls `onClose()`. For the detached composer this kills the process mid-APPEND. | Draft is local-only until the next main-window start triggers `SyncPendingDrafts`; the server copy is stale or briefly inconsistent. | Add a `DraftResult` field / Wails method that resolves when `syncToIMAP` reaches a terminal state, and await it (bounded) before close. |
| **F-16** | P2 | `app/detached_composer.go:150` + `app/draft.go:589-597` | Main window and detached composer are **separate OS processes** writing the same `drafts` row in the same SQLite file. `a.syncMu` / `c.draftSyncMu` are per-process and provide no mutual exclusion across the two. | Two windows editing one draft (e.g. "detach" while the inline window is still open) race: last `Update` wins; both can APPEND; the loser's server copy is orphaned (compounds F-01). | Take an advisory lock (`flock` on a per-draft lockfile, or an atomic `UPDATE drafts SET owner=? WHERE id=? AND owner=''`) before `saveDraftToDB`. |
| **F-17** | P2 | `internal/smtp/mdn.go:55-56`, `:80` | `BuildMDN` writes `originalMsg.ReadReceiptTo`, `originalMsg.Subject` and `extractEmailAddress(...)` straight into the RFC822 with `fmt.Sprintf("...%s\r\n")` — **bypassing** `writeHeader`'s CRLF stripping (`internal/smtp/message.go:188`). | Currently blocked upstream: `net/textproto` unfolds the stored header (so CRLF is already gone) and `net/smtp.validateLine` rejects CRLF in `RCPT TO`. But the sink is unguarded — any future change that stores a raw header value turns this into SMTP header injection into a message sent to a third party. | Route every MDN header through `writeHeader` (or a shared `sanitizeField`). Cheap and removes the latent primitive. |
| **F-18** | P3 | `internal/smtp/mdn.go:105-114` and `app/settings.go:525-534` | Neither `extractEmailAddress` nor `extractEmailFromHeader` strips CR/LF; both just do `TrimSpace` + bracket slicing. | Same as F-17 — relies entirely on upstream unfolding plus `net/smtp`. | Add `strings.NewReplacer("\r","","\n","")`. |
| **F-19** | P3 | `internal/undo/commands.go:28-61` | `FlagChangeCommand` stores a **single** `previousState bool` for a whole batch of message ids. It is currently dead code (no caller), but the shape is wrong: a bulk "mark read" over 5 messages where 2 were already read would undo all 5 to the same value. | Latent corruption if wired up. | Store `map[string]bool` (messageID → previous state), or delete the type. |
| **F-20** | P3 | `internal/smtp/message.go:31-33` | `Address.String()` only RFC2047-encodes the display name when it contains non-ASCII. An ASCII name with RFC5322 specials (`Doe, John`, `Foo "Bar"`, `(Acme)`) is emitted unquoted ⇒ malformed `From:`/`To:` display name. | Cosmetic/interop; some strict MTAs reject. | Use `mail.Address{Name, Address}.String()` from `net/mail`, which handles quoting and encoding. |
| **F-21** | P3 | `app/compose.go:827-829` | `forward` mode still sets `InReplyTo` and `References` from the original message. | Forwarded mail merges into the original thread in most clients. | Only set threading headers for `reply`/`reply-all`. |
| **F-22** | P3 | `app/compose.go:955-972` | `selectReplyFromIdentity` returns the **first** identity matching any of To/Cc/Bcc. If the original was addressed to two of the user's aliases, the choice is DB-order dependent. | Reply may go out from an unexpected alias. | Prefer the `IsDefault` identity if it is among the addressed aliases; otherwise keep current behaviour. |
| **F-23** | P3 | `app/compose.go:685-689` | `selfEmails` is built from `identities` only. If the account's primary address has no `identities` row, replying to your own message still Cc's yourself. | Cosmetic self-Cc on reply-all. | Seed `selfEmails` with `acc.Email` too. |
| **F-24** | P3 | `app/draft.go:668-671` (`SyncPendingDrafts`) | A crash between `AppendMessage` (`:390`) and `UpdateSyncStatus` (`:422`) leaves the local row with the *old* `IMAPUID` and status `Pending`. On restart the draft is re-appended without deleting the pre-crash copy. | One orphaned server draft per crash during an autosave window. | On startup, before re-appending a pending draft, search the Drafts folder for an existing copy by `Message-ID`/subject+timestamp and reuse its UID. |

### 4.3 Security findings

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| **S-01** | P1 (latent) | `internal/smtp/mdn.go:55-56`, `:80` | Untrusted, remote-supplied header values written into an outbound RFC822 without CRLF stripping; `writeHeader`'s protection (`internal/smtp/message.go:188`) is not used on this path. | SMTP header injection into the MDN (e.g. injected `Bcc:`/`X-` headers on a message hsx2mail sends to a third party). Currently mitigated by header unfolding at parse time and `net/smtp.validateLine` on `RCPT TO` — so not directly exploitable today, but one refactor away from being. | Send all MDN headers through `writeHeader`. |
| **S-02** | P1 (latent) | `internal/smtp/message.go:397`, `:443-445` | `header.Set("Content-Type", contentType)`, `Content-Disposition: ... filename=%q` and `Content-ID: <%s>` are set from `att.ContentType` / `att.ContentID` / `att.Filename`. For forwarded and inline-carried attachments these come straight from the **received** message (`internal/email/attachment.go:65-99`, `internal/sync/parse.go:733`) via the DB. Go's `multipart.Writer` does **not** validate header values for CR/LF. | MIME part header injection in a message the user sends. `Content-Type` is `mime.ParseMediaType`-validated and `Content-ID` is unfolded, so today the values are well-formed — but nothing in the *builder* enforces it. | Validate in the builder: reject any attachment field containing CR/LF or NUL; clamp `filename` length; fall back to `application/octet-stream` for a non-token media type. |
| **S-03** | P2 | `app/app.go:153` (`sanitizeField`) | `sanitizeField` is applied **only** inside `parseMailtoURL` (`app/app.go:98, 113`). `SaveDraft`/`SendMessage`/`PrepareReply` apply no field-level sanitization; the compose path relies entirely on `writeHeader` at render time. | Single point of defence. Any new header write path (a DKIM-Signature, a `List-*` header, an MDN — see S-01) silently becomes injectable. | Apply `sanitizeField` + length caps at the Wails binding boundary (`SaveDraft`, `SendMessage`) so `ComposeMessage` fields are clean by construction. |
| **S-04** | P2 | `internal/email/sanitizer.go:89` | `p.AllowURLSchemes("cid", "data", "http", "https", "mailto")` — `data:` is allowed for **all** URL attributes including `<a href>`. | `data:text/html;base64,…` links. Mitigated because the viewer iframe is `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"` **without** `allow-same-origin` (`EmailBody.svelte:785`) and bluemonday's `AddTargetBlankToFullyQualifiedLinks` only rewrites http/https. | Restrict `data:` to `img@src` only (`p.AllowURLSchemes(...).OnElements("img")`); drop it for `a@href`. |
| **S-05** | P2 | `internal/email/sanitizer.go:103`, `:127` | `p.AllowAttrs("style").Globally()` and `p.AllowDataAttributes()`. | `style` on every element is needed for email layout but also enables UI-redressing inside the frame; `data-*` on every element plus globally-allowed `id`/`class` (`:104`) enables DOM clobbering of the *parent* document if `allow-same-origin` is ever added. | Restrict `style` to the layout-relevant set; keep `id`/`class` off or accept and document. Re-evaluate if the sandbox ever gains `allow-same-origin`. |
| **S-06** | P2 | `Composer.svelte:501-540` → `internal/smtp/message.go:101` | Composer HTML goes to `ToRFC822()` with **no** bluemonday pass; only the Tiptap schema constrains it. The backend sanitizer exists (`internal/email/sanitizer.go:14`, exposed to extensions as `coreapi.HTML().Sanitize` at `app/coreimpl.go:197`) but is not called on the compose path. | Self-XSS only (the user sanitizes their own draft) — low impact, but there is no defence-in-depth if the ProseMirror schema is ever widened or a `data:`/remote-image restore path (`Composer.svelte:521-525`) introduces markup the schema did not produce. | Run the shared sanitizer over `htmlContent` in `buildMessage()` (or in `ToRFC822` behind a compose flag) using the identical policy as the viewer. |
| **S-07** | P3 | `internal/ipc/server.go:270-287` | `readLoop` uses `json.NewDecoder(reader)` with **no** `io.LimitReader` and **no** read deadline once the client is authenticated. | A process holding the session token (a leaked composer child) can stream unbounded JSON into the main process's heap. | `io.LimitReader(conn, MaxMessageSize)` + `SetReadDeadline` per message. |
| **S-08** | — | `internal/ipc/server_unix.go:75-88`, `internal/ipc/token.go:33-45`, `app/ipc.go:247-252` | **No hole found.** 32 bytes from `crypto/rand`, hex-encoded; `subtle.ConstantTimeCompare`; socket dir `/tmp/hsx2mail-<uid>` created `0700` and re-`chmod`ed if wrong; umask set before `net.Listen` to close the TOCTOU; token delivered only over the child's stdin pipe, never on argv, never written to disk or logged. | — | — |
| **S-09** | — | `app/attachment.go:236-238`, `:304-307` | Attachment open/reveal uses `exec.Command("xdg-open", path)` / `platform.OpenPathWindows(path)` — **no shell**, so `;`/`&`/`$(…)` in a filename are inert. Save path comes from the OS save dialog (`app/attachment.go:200`), and the message's own filename is never used to build a path, so there is no path traversal on write. | — | — |
| **S-10** | — | `internal/email/sanitizer.go:131-138` | `Sanitize()` runs `removeScriptTags` then `bluemonday`. The `<style>` block is intentionally kept (sandboxed iframe, no `allow-same-origin`). | — | — |

---

## 5. Priority-ordered remediation

| Order | Item | Why first |
|---|---|---|
| 1 | **F-01** — stop writing `imap_uid`/`folder_id`/`sync_status` from the autosave path | Actively creates invisible duplicate drafts; one-line-ish fix; user-visible data mess. |
| 2 | **F-10 + F-11 + F-12** — undo: one command per user action, peek-then-pop, no re-push on undo | Three independent ways undo is wrong; all in ~40 lines. |
| 3 | **F-03 + F-04** — staging-id attachments + single encryption pass | The only thing that makes large-attachment composing usable. |
| 4 | **F-02** — APPEND before EXPUNGE | Removes the only server-side data-loss window. |
| 5 | **S-01 + S-02 + S-03** — route every outbound header through `writeHeader`; validate attachment MIME fields at the builder; sanitize `ComposeMessage` at the binding boundary | Cheap, closes the latent injection sinks before they become live. |
| 6 | **F-13/F-14/F-15/F-16** — composer draft lifecycle races | Needs a design pass (per-draft advisory lock, awaited close); do together. |
| 7 | **S-04/S-05/S-06** — sanitizer tightening | Low urgency given the sandbox; do alongside the next sanitizer change. |

---

## 6. Things that are correct (do not "fix")

- **Send ordering** (`app/compose.go:446-471`): SMTP `DATA` → Sent APPEND (failure is log-only, *not* fatal) → draft delete. A Sent-append failure does **not** lose the draft, and a draft-delete failure does not lose the mail. This is the right order.
- **Local-before-remote draft write** (`app/draft.go:589-606`) + `SyncPendingDrafts` (`app/draft.go:655`): crash-safe on the local side.
- **Delete-race guard** (`app/draft.go:397-419`): post-APPEND cleanup of orphans when a draft is deleted mid-flight.
- **Reply-To precedence** (`app/compose.go:691-694`): correctly prefers `Reply-To` over `From` per RFC 5322.
- **BCC never emitted as a header** (`internal/smtp/message.go:114-115`), but included in the envelope via `AllRecipients()` (`internal/smtp/message.go:88`).
- **`writeHeader` CRLF stripping** (`internal/smtp/message.go:188`) covers every header the compose path writes.
- **SMTP envelope injection** is blocked by `net/smtp.validateLine` — no need for a second layer there.
- **IPC auth** (`internal/ipc/{token,server,server_unix}.go`): correct token handling, correct permissions, no TOCTOU on the socket.
- **Attachment open** uses `exec.Command` with no shell and the save path is dialog-supplied.
- **Viewer sandbox** (`EmailBody.svelte:785`) correctly omits `allow-same-origin`.

---

## 7. Method / confidence

Read the full bodies of `app/compose.go`, `app/draft.go`, `app/detached_composer.go`, `app/undo.go`, `app/ipc.go`, `app/actions.go` (move paths), `internal/smtp/{message,mdn,client}.go`, `internal/ipc/{server,server_unix,token}.go`, `internal/undo/{undo,commands}.go`, `internal/draft/store.go`, `internal/email/{sanitizer,attachment}.go`, plus the composer frontend save/send/close paths. `app/message.go` and most of `app/actions.go` are read-path and were confirmed out of scope for compose semantics.

**Confidence:** high for F-01, F-02, F-10, F-11, F-12, F-13, F-14, S-01, S-08, S-09 (each read end-to-end in the actual call path). High for the *existence* of F-03/F-04/F-05/F-06 and medium for their *magnitude* (no runtime measurement was taken; the cost is inferred from the code path, not profiled). F-24 is a plausible-but-unobserved sequence.
