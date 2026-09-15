# Nghiệp vụ Hệ thống Email Hub — Phân tích Nghiệp vụ Chi tiết

## Mục lục

1. [Quản lý Tài khoản Email](#1-quản-lý-tài-khoản-email)
2. [Đồng bộ Email (Sync)](#2-đồng-bộ-email-sync)
3. [Xem và Quản lý Email](#3-xem-và-quản-lý-email)
4. [Soạn thảo và Gửi Email](#4-soạn-thảo-and-gửi-email)
5. [Draft (Bản nháp)](#5-draft-bản-nháp)
6. [Bảo mật và Mã hóa](#6-bảo-mật-va-mã-hóa)
7. [Quản lý Danh bạ (Contacts)](#7-quản-lý-danh-bạ-contacts)
8. [Lịch (Calendar)](#8-lịch-calendar)
9. [Thiết lập và Cấu hình](#9-thiết-lập-va-cấu-hình)
10. [Tìm kiếm](#10-tìm-kiếm)
11. [Undo (Hoàn tác)](#11-undo-hoàn-tác)
12. [Thông báo (Notifications)](#12-thông-báo-notifications)
13. [OAuth2 Flow](#13-oauth2-flow)
14. [Multi-Window Composer](#14-multi-window-composer)
15. [Extension System](#15-extension-system)

---

## 1. Quản lý Tài khoản Email

### 1.1 Luồng tạo tài khoản mới

#### Người dùng nhập thủ công (Password Auth)
```
1. Frontend: User mở Settings → Accounts → Add Account
   ├── Chọn "Manual Setup"
   ├── Nhập: Email, Password, IMAP Host/Port/Security, SMTP Host/Port/Security
   └── Click "Test Connection"

2. Backend: AddAccount(config)
   ├── accountStore.Create(&config)
   │     └── INSERT INTO accounts (...)
   ├── credStore.SetPassword(accountID, password) → OS Keyring
   ├── Nếu có SMTP password riêng:
   │     └── credStore.SetSMTPPassword(accountID, smtpPassword)
   ├── updateDBConnectionPool() (tăng idle connections)
   ├── Nếu idleManager đang chạy:
   │     └── idleManager.StartAccount(accountID, accountName)
   └── Return Account object

3. Frontend: Nhận Account → load folder tree → chọn Inbox → sync
```

#### OAuth2 (Google / Microsoft)
```
1. Frontend: User chọn "Google" hoặc "Microsoft"

2. Backend: StartOAuthFlow(provider)
   ├── oauth2Manager.StartAuthFlow(ctx, provider)
   │     ├── Resolve provider config (user override → shipped)
   │     ├── Tạo state + PKCE challenge
   │     ├── Start embedded HTTP server (callback listener)
   │     └── Return authorization URL
   ├── PortalOpenURI(authURL) → mở browser
   │     └── Fallback: BrowserOpenURL (Wails)
   └── Goroutine: WaitForCallback(ctx)

3. Browser: User login + consent → callback URL → HTTP server nhận auth code

4. Backend (callback goroutine):
   ├── Exchange auth code → tokens (access + refresh)
   ├── Lưu pendingOAuthTokens + pendingOAuthEmail
   ├── Emit "oauth:success" {provider, email}

5. Frontend: Nhận "oauth:success" →
   ├── Hiển thị form nhập tên hiển thị, chọn sync period
   └── CompleteOAuthAccountSetup(displayName, syncPeriodDays)

6. Backend: CompleteOAuthAccountSetup
   ├── accountStore.Create(&config) với AuthType=OAuth2
   ├── credStore.SetOAuthTokens(accountID, tokens)
   ├── updateDBConnectionPool()
   ├── idleManager.StartAccount()
   ├── Xóa pendingOAuthTokens
   └── Return Account
```

#### Microsoft Shared Mailbox
```
1. Frontend: User chọn "Add Shared Mailbox" trong Microsoft account

2. Backend: AddMicrosoftSharedMailbox(primaryAccountID, sharedEmail, displayName)
   ├── Kiểm tra primary account tồn tại và là OAuth
   ├── Tạo account mới với shared email
   │     ├── IMAP username = sharedEmail (dùng token của primary)
   │     ├── SMTP username = sharedEmail
   │     └── AuthType = OAuth2 (dùng chung token)
   └── Return Account
```

### 1.2 Cập nhật tài khoản

```
UpdateAccount(accountID, config):
  ├── accountStore.Update(accountID, config)
  │     └── UPDATE accounts SET ...
  ├── Nếu password thay đổi:
  │     └── credStore.SetPassword(accountID, newPassword)
  ├── Nếu SMTP config thay đổi:
  │     └── credStore.SetSMTPPassword(accountID, newSMTPPassword)
  └── Nếu OAuth tokens thay đổi:
        └── credStore.SetOAuthTokens(accountID, newTokens)
```

### 1.3 Xóa tài khoản

```
RemoveAccount(accountID):
  ├── accountStore.Delete(accountID) — Cascade xóa folders, messages, identities
  ├── credStore.Delete(accountID) — Xóa passwords, OAuth tokens
  ├── updateDBConnectionPool() — Giảm idle connections
  ├── idleManager.StopAccount(accountID)
  └── imapPool.CloseAccountConnections(accountID)
```

### 1.4 Sắp xếp thứ tự

```
ReorderAccounts(accountIDs []string):
  └── accountStore.Reorder(accountIDs) — Cập nhật order_index
```

### 1.5 Folder Mapping

User có thể map thủ công special folders:
```
Account.GetFolderMapping(folderType string) → path
  ├── Nếu có mapping: dùng path đã map
  └── Nếu không: dùng auto-detected folder type
```

Special folder types: Inbox, Sent, Drafts, Trash, Spam, Archive, All

---

## 2. Đồng bộ Email (Sync)

### 2.1 Folder Sync (Chi tiết)

```
App.SyncFolder(accountID, folderID):
  ├── DEBOUNCE CHECK (500ms):
  │     ├── Nếu request < 500ms từ request trước → SKIP
  │     └── Update syncLastRequest[syncKey]
  ├── CANCEL CHECK:
  │     ├── Nếu có existing sync context → cancel (context.CancelFunc)
  │     └── Wait 100ms cho cleanup
  ├── Tạo context mới: ctx, cancel = context.WithCancel(a.ctx)
  ├── syncContexts[syncKey] = cancel (lưu để cancel sau này)
  ├── Lấy syncPeriodDays từ account (0 = all messages)

  └── syncEngine.SyncMessages(ctx, accountID, folderID, syncPeriodDays, force):
        ├── Phase 1: getOrCreateFolder()
        │     ├── Lấy thông tin folder từ IMAP (status)
        │     └── Upsert vào folders table
        │
        ├── Phase 2: SyncFolders() [chỉ khi force]
        │     └── Scan tất cả IMAP folders → upsert vào DB
        │
        ├── Phase 3: SyncMessages()
        │     ├── Kiểm tra CONDSTORE capability
        │     ├── Nếu có CONDSTORE:
        │     │     ├── Lấy highestModSeq từ folder store
        │     │     ├── IMAP FETCH CHANGEDSINCE {modseq}
        │     │     └── Update modseq sau mỗi batch
        │     └── Nếu không có CONDSTORE:
        │           ├── UID SEARCH ALL
        │           └── So sánh với messages trong DB → fetch missing
        │
        ├── Phase 4: Threading
        │     ├── Lấy messages mới từ DB (chưa có ThreadID)
        │     ├── Phân tích References + In-Reply-To headers
        │     ├── Subject-based clustering (strip Re:/Fwd:)
        │     └── Cập nhật ThreadID trong DB
        │
        ├── Phase 5: Body fetch (nếu automatic hoặc chưa fetch)
        │     ├── SELECT messages WHERE BodyFetched = false
        │     ├── Batch: 512KB hoặc 50 messages (whichever hits first)
        │     ├── IMAP FETCH BODY[] for batch
        │     ├── Parse MIME: multipart/alternative → text + HTML
        │     ├── S/MIME verify (nếu multipart/signed)
        │     ├── PGP verify (nếu application/pgp-signature)
        │     ├── HTML sanitize (bluemonday)
        │     ├── Extract attachments
        │     └── Update message body + attachments trong DB
        │
        └── Phase 6: Cleanup
              ├── WAL checkpoint
              ├── Sync cleanup tracking
              └── Emit "folder:synced" event

  └── Error handling:
        ├── Nếu context.Canceled → log debug "sync cancelled"
        ├── Nếu certificate.Error → emit "certificate:untrusted"
        └── Nếu other error → emit "folder:syncError"
```

### 2.2 Background Sync Scheduler

```
Scheduler.Start(ctx):
  ├── Với mỗi enabled account:
  │     └── Tạo goroutine với ticker (sync period)
  │           ├── Mỗi tick: lấy danh sách folders subscribed
  │           ├── Nếu online: SyncFolder(accountID, folderID) cho từng folder
  │           └── Nếu offline: skip tick
  └── Network change callback:
        ├── Online → start sync scheduler + IDLE reconnect
        └── Offline → pause scheduler + IDLE
```

### 2.3 IDLE Push (Real-time)

```
IdleManager:
  ├── Mỗi account: 1 TCP connection
  ├── IDLE command: chờ server push (EXISTS, FETCH, EXPUNGE)
  │
  ├── Khi nhận EXISTS response (new mail):
  │     └── processIdleEvents goroutine:
  │           ├── SyncFolder(accountID, folderID)
  │           ├── Nếu có unseen messages mới:
  │           │     ├── handleNewMailNotification()
  │           │     └── Notifier.Show() → OS notification
  │           └── Emit "folder:synced"
  │
  ├── Khi FLAGS changed (other client):
  │     ├── Kiểm tra ownFlagChangeAt[accountID]
  │     ├── Nếu là echo của flag STORE của chính mình → skip
  │     └── Nếu không → SyncFolder (CONDSTORE fast path)
  │
  ├── Connection lifecycle:
  │     ├── Kết nối → Login → SELECT INBOX → IDLE
  │     ├── Mất kết nối → retry with backoff (5s, 10s, 30s, 60s)
  │     └── Network online → reconnect all
  │
  └── Sleep/Wake:
        ├── Sleep → đóng tất cả IDLE connections
        └── Wake → reconnect all + SyncAllComplete
```

### 2.4 Sync All Complete

```
SyncAllComplete():
  ├── CancelAllSyncs() — cancel tất cả sync contexts đang chạy
  ├── Với mỗi account + folder:
  │     └── SyncFolder(accountID, folderID) — tạo context mới
  └── Dùng wakeSyncing flag để guard concurrent calls
```

### 2.5 Own Flag Change Tracking

```
Khi app STORE flag change:
  ├── ownFlagChangeAt[accountID] = time.Now()
  └── IMAP STORE flags

Khi IDLE nhận FLAGS response:
  ├── Nếu time.Since(ownFlagChangeAt[accountID]) < 2s → skip
  └── Nếu không → process as real change (từ client khác)
```

---

## 3. Xem và Quản lý Email

### 3.1 Danh sách hội thoại (Conversations)

```
GetConversations(accountID, folderID, offset, limit, sortOrder, filter):
  ├── sortOrder: "newest" (mặc định) hoặc "oldest"
  ├── filter: "" (all), "unread", "starred", "attachments"
  └── messageStore.ListConversationsByFolder(folderID, offset, limit, sortOrder, filter)
        ├── SELECT từ messages table
        ├── GROUP BY ThreadID
        ├── ORDER BY MAX(date) DESC/ASC
        └── Return Conversation[]

Unified Inbox:
  ├── GetUnifiedInboxConversations(offset, limit, sortOrder, filter)
  │     └── SELECT từ messages JOIN folders
  │           WHERE folder_type = 'inbox'
  │           GROUP BY ThreadID
  └── GetUnifiedInboxUnreadCount()
        └── SELECT COUNT(*) WHERE folder_type = 'inbox' AND flags & 1 = 0
```

### 3.2 Xem hội thoại

```
GetConversation(threadID, folderID):
  └── messageStore.GetConversation(threadID, folderID)
        ├── SELECT messages WHERE ThreadID = ?
        ├── Sắp xếp theo date
        └── Return Conversation với []Message (bao gồm body)
```

### 3.3 On-demand Body Fetch

```
FetchMessageBody(messageID):
  ├── Get message từ store
  ├── Nếu BodyFetched = true → return ngay
  └── Nếu BodyFetched = false:
        ├── syncEngine.FetchMessageBody(ctx, accountID, messageID)
        └── Return message với body mới
```

### 3.4 View Message Source

```
GetMessageSource(messageID):
  ├── Get message từ store (lấy accountID, folderID, UID)
  ├── syncEngine.FetchRawMessage(ctx, accountID, folderID, UID)
  │     └── IMAP FETCH UID BODY[] (raw RFC822)
  └── Return string (raw source)
```

### 3.5 Actions (Flag Operations)

```
MarkAsRead(messageID):
  ├── Nếu là conversation (threadID): MarkConversationAsRead
  │     └── Đánh dấu tất cả messages trong thread
  ├── Nếu là single message:
  │     ├── ownFlagChangeAt[accountID] = now
  │     ├── messageStore.SetFlags(messageID, \Seen)
  │     └── IMAP STORE UID +FLAGS.SILENT (\Seen)
  │           └── Nếu thất bại: rollback flag trong DB
  └── Emit "folders:countsChanged" với unread count mới

MarkAsUnread(messageID):
  └── Reverse của MarkAsRead: IMAP STORE -FLAGS.SILENT (\Seen)

Star / Unstar:
  ├── messageStore.SetFlags(messageID, \Flagged)
  ├── IMAP STORE UID +FLAGS.SILENT (\Flagged)
  ├── Nếu thất bại: rollback
  └── Undo: queue FlagCommand

Archive:
  ├── Chuyển message sang Archive folder
  ├── IMAP COPY to Archive + STORE +FLAGS.SILENT (\Deleted) + EXPUNGE
  └── Undo: queue MoveCommand

Trash / Delete:
  ├── Chuyển message sang Trash folder
  ├── IMAP COPY to Trash + STORE +FLAGS.SILENT (\Deleted) + EXPUNGE
  └── Undo: queue MoveCommand

MarkAsSpam / MarkAsNotSpam:
  ├── Chuyển message sang Spam/Inbox folder
  └── Tương tự Archive flow
```

### 3.6 Undo System

```
Undo():
  ├── undoStack.Undo()
  │     ├── Pop command từ stack
  │     ├── Execute undo operation
  │     └── Nếu timeout (30s): command expired → skip
  └── Emit "messages:updated"

Các command types:
  - MoveCommand:
        ├── Forward: move message từ folder A → folder B
        └── Undo: move message từ folder B → folder A
  - DeleteSentCommand:
        ├── Forward: xóa sent message khỏi Sent folder
        └── Undo: không thể undo (sent là irreversible)
  - FlagCommand:
        ├── Forward: set flag
        └── Undo: unset flag
```

---

## 4. Soạn thảo và Gửi Email

### 4.1 Tạo Message Mới

```
OpenComposer:
  ├── Từ frontend: show Composer overlay (in-window)
  ├── Hoặc OpenComposerWindow() → spawn detached composer
  │
  └── PrepareReply(accountID, messageID, mode):
        ├── Lấy original message từ store
        ├── mode = "reply":
        │     ├── To = original.From
        │     ├── Subject = "Re: " + original.Subject
        │     ├── References = original.References + original.MessageID
        │     └── Body = "\n\nOn [date], [name] wrote:\n> [original body]"
        ├── mode = "reply-all":
        │     ├── To = original.From + original.To (trừ user)
        │     └── Subject = "Re: " + original.Subject
        ├── mode = "forward":
        │     ├── Subject = "Fwd: " + original.Subject
        │     └── Body = original body
        └── Chọn identity (From):
              ├── Nếu có 1 identity → dùng luôn
              ├── Nếu nhiều identity: phức tạp hơn:
              │     ├── Ưu tiên identity trùng với original.To
              │     ├── Nếu không có: chọn default identity
              │     └── Frontend hiển thị dropdown nếu cần
              └── ReplyIdentity test: kiểm tra identity selection logic
```

### 4.2 Attachment Handling

```
Add attachment:
  ├── PickAttachments() — native file dialog (platform.FileChooser)
  ├── Mỗi attachment:
  │     ├── Đọc file → base64 encode
  │     ├── Detect Content-Type (mime.TypeByExtension)
  │     └── Lưu vào composer state (ComposerAttachment[])
  │
  └── Giới hạn: maxInlineContentSize = 5MB (inline images)

Remove attachment:
  └── Xóa khỏi composer state array

Open attachment:
  ├── DownloadAttachment(accountID, messageID, attachmentID, targetPath)
  │     └── syncEngine.DownloadAttachment() → write to targetPath
  └── platform.OpenURL/OpenPath → mở file với default app
```

### 4.3 Send Pipeline (Chi tiết)

```
App.SendMessage(accountID, composeMessage):
  ├── 1. Resolve Identity
  │     ├── Lấy identity từ composeMessage (From)
  │     └── Lấy signature HTML/text nếu có
  │
  ├── 2. getValidOAuthToken(accountID) [nếu OAuth]
  │     └── Kiểm tra expiry → refresh nếu cần
  │
  ├── 3. Build MIME Message (smtp.BuildMessage)
  │     ├── Headers:
  │     │     ├── From: Identity.Name <Identity.Email>
  │     │     ├── To: join addresses
  │     │     ├── Cc: join addresses (nếu có)
  │     │     ├── Bcc: join addresses (nếu có) — KHÔNG include trong envelope
  │     │     ├── Subject: sanitizeField(subject)
  │     │     ├── Date: RFC5322 format
  │     │     ├── Message-ID: <uuid@hostname>
  │     │     ├── In-Reply-To: [nếu reply]
  │     │     ├── References: [nếu reply]
  │     │     └── User-Agent: "Email Hub Mail Client"
  │     │
  │     ├── Body:
  │     │     ├── multipart/alternative (text + HTML)
  │     │     │     ├── text/plain (UTF-8, quoted-printable)
  │     │     │     └── text/html (UTF-8, quoted-printable)
  │     │     └── Nếu có chữ ký: signature_html → append vào HTML body
  │     │
  │     ├── Attachments:
  │     │     ├── multipart/mixed (hoặc related nếu có inline images)
  │     │     ├── Mỗi attachment: base64 encoded
  │     │     ├── Inline: Content-ID + Content-Disposition: inline
  │     │     └── Attached: Content-Disposition: attachment
  │     │
  │     ├── S/MIME Sign (nếu enabled):
  │     │     ├── Build multipart/signed với application/pkcs7-signature
  │     │     └── smimeSigner.Sign(message)
  │     │
  │     ├── S/MIME Encrypt (nếu enabled):
  │     │     ├── Build application/pkcs7-mime (enveloped-data)
  │     │     └── smimeEncryptor.Encrypt(message, recipients)
  │     │
  │     ├── PGP Sign (nếu enabled):
  │     │     ├── Build multipart/signed với application/pgp-signature
  │     │     └── pgpSigner.Sign(message)
  │     │
  │     └── PGP Encrypt (nếu enabled):
  │           ├── Build multipart/encrypted với application/pgp-encrypted
  │           └── pgpEncryptor.Encrypt(message, recipients)
  │
  ├── 4. SMTP Send
  │     ├── smtp.Connect(config):
  │     │     ├── Dial TCP (host:port)
  │     │     ├── STARTTLS (nếu security=starttls)
  │     │     └── TLS handshake (nếu security=tls)
  │     ├── smtp.Auth(credentials):
  │     │     ├── LOGIN/PLAIN (password auth)
  │     │     └── XOAUTH2 (OAuth2 auth) — base64 encoded token
  │     ├── smtp.Send(message):
  │     │     ├── MAIL FROM:<from>
  │     │     ├── RCPT TO:<to> (mỗi recipient, kể cả BCC)
  │     │     ├── DATA → write MIME message
  │     │     └── QUIT
  │     └── Error handling:
  │           ├── Retry: 3 lần với exponential backoff
  │           ├── OAuth token expired → refresh + retry
  │           └── Nếu thất bại → emit "oauth:reauth-required"
  │
  ├── 5. Post-Send
  │     ├── imap.AppendSentMessage():
  │     │     ├── Kết nối IMAP (pool connection)
  │     │     ├── SELECT Sent folder
  │     │     └── IMAP APPEND Sent folder (message bytes + \Seen flag)
  │     │
  │     ├── Nếu là draft:
  │     │     ├── Delete draft từ IMAP Drafts folder
  │     │     └── draftStore.Delete(draftID)
  │     │
  │     ├── Nếu detached composer: IPC message_sent → main window
  │     ├── Undo: queue DeleteSentCommand (30s timeout)
  │     └── Return SendResult {success, messageID, threadID}
  │
  └── 6. Frontend Response
        ├── Show success toast
        ├── Close composer (hoặc keep open nếu detached)
        └── Nếu lỗi: show error toast + keep composer open
```

### 4.4 MDN (Read Receipts / Message Disposition Notification)

```
Khi mở message có Disposition-Notification-To:
  ├── Check settings: "Request Read Receipts"
  ├── Nếu enabled:
  │     ├── Build MDN email (smtp.BuildMDN):
  │     │     ├── To: original From
  │     │     ├── Subject: "Read: [original subject]"
  │     │     ├── Content-Type: message/disposition-notification
  │     │     └── Fields: Disposition, Original-Message-ID, etc.
  │     └── smtp.Send(mdnMessage)
  └── Nếu disabled hoặc user từ chối: skip
```

---

## 5. Draft (Bản nháp)

### 5.1 Save Draft

```
SaveDraft(accountID, draftData):
  ├── draftStore.SaveDraft(accountID, draftData)
  │     ├── Nếu draftID mới: INSERT INTO drafts
  │     ├── Nếu draftID cũ: UPDATE drafts
  │     └── Nếu body encrypted:
  │           ├── Encrypt body (PGP/SMIME nếu enabled)
  │           └── Lưu encrypted body
  │
  ├── syncDraftToIMAP(accountID, draftID):
  │     ├── Get special folder: type=Drafts
  │     ├── Build draft MIME message (giống send nhưng \Draft flag)
  │     ├── Nếu draft chưa upload:
  │     │     └── IMAP APPEND Drafts folder (message + \Draft + \Seen)
  │     └── Nếu draft đã upload (có IMAP UID):
  │           └── IMAP STORE UID REPLACE (cập nhật message)
  │
  └── Auto-save timer: 30 giây (frontend setInterval)
        └── Gọi SaveDraft mỗi 30s nếu có thay đổi
```

### 5.2 Load Draft

```
Load draft:
  ├── GetDraft(draftID)
  │     └── draftStore.Get(draftID) → Draft object
  │
  ├── Nếu encrypted: decrypt body
  │
  └── Frontend: pre-fill composer với draft data
        ├── To, Cc, Bcc
        ├── Subject
        ├── Body (HTML hoặc text)
        └── Attachments
```

### 5.3 Sync Pending Drafts (Startup)

```
syncAllPendingDrafts():
  ├── Lấy tất cả drafts chưa sync (Draft.SyncStatus != SyncStatusSynced)
  ├── Với mỗi draft:
  │     └── syncDraftToIMAP(accountID, draftID)
  └── Chạy ở startup (App.Startup → goroutine)
```

---

## 6. Bảo mật và Mã hóa

### 6.1 PGP

#### Key Management
```
PGP_ImportKey(armoredKey):
  ├── pgpStore.Import(armoredKey)
  │     ├── Parse armored key (ProtonMail/go-crypto)
  │     ├── Extract: fingerprint, identities, creation time
  │     ├── Lưu key vào DB (encrypted private key)
  │     └── Index cho search
  └── Return KeyInfo {fingerprint, identities, algorithm}

PGP_ListKeys():
  ├── pgpStore.List()
  └── Return KeyInfo[]

PGP_DeleteKey(fingerprint):
  └── pgpStore.Delete(fingerprint)

Key discovery:
  ├── WKD (Web Key Directory):
  │     └── pgp.LookupViaWKD(email) → fetch key từ domain
  └── HKP (HTTP Keyserver Protocol):
        └── pgp.LookupViaHKP(email, keyserver) → fetch từ keyserver
```

#### Sign & Verify
```
PGP Sign:
  pgpSigner.Sign(message, signerFingerprint)
  ├── Load private key từ store
  ├── Decrypt private key (passphrase từ keyring)
  └── Build multipart/signed signature

PGP Verify:
  pgpVerifier.Verify(message, signature)
  ├── Tìm public key theo sender email (store → WKD → HKP)
  ├── Verify signature
  └── Return SignatureResult {valid, fingerprint, signer, error}
```

### 6.2 S/MIME

#### Certificate Management
```
SMIME_ImportCertificate(certData, password):
  ├── Parse PKCS12 (software.sslmate.com/go-pkcs12)
  ├── Extract: certificate chain, private key
  ├── Lưu vào smime.Store
  └── Return CertificateInfo {serial, issuer, subject, expiry}

SMIME_ImportCACertificate(certData):
  ├── Parse PEM certificate
  ├── Thêm vào CA store (cho verify)
  └── Return CertificateInfo
```

#### Sign & Verify
```
S/MIME Sign:
  smimeSigner.Sign(message, certFingerprint)
  ├── Load certificate + private key từ store
  ├── Build application/pkcs7-signature (detached)
  └── Return multipart/signed message

S/MIME Verify:
  smimeVerifier.Verify(message, signature)
  ├── Parse pkcs7 signed data (go.mozilla.org/pkcs7)
  ├── Verify chain (CA store + TOFU certs)
  └── Return SignatureResult {verified, chain, subject, error}
```

### 6.3 Certificate Trust (TOFU)

```
Kết nối IMAP/SMTP lần đầu:
  ├── certificate.BuildTLSConfigDynamic(certStore)
  │     └── TLS config với VerifyPeerCertificate callback
  ├── Callback:
  │     ├── Lấy fingerprint từ server certificate
  │     ├── Kiểm tra trong certStore
  │     ├── Nếu fingerprint mới: allow + lưu vào store
  │     ├── Nếu fingerprint khác: → certificate.Error
  │     └── Emit "certificate:untrusted" event

User dialog:
  ├── Frontend nhận "certificate:untrusted"
  ├── Show CertificateDialog (fingerprint, issuer, subject)
  ├── User chọn:
  │     ├── Accept (lưu fingerprint mới → allow)
  │     └── Reject (close connection)
  └── AcceptCertificate(accountID, certInfo) → certStore.Add()
```

---

## 7. Quản lý Danh bạ (Contacts)

### 7.1 Contact Sources

```
ListContactSources():
  └── carddavStore.ListSources() → ContactSource[]

AddCardDAVSource(name, url, username, password):
  ├── carddavStore.CreateSource(name, url, username, type=CardDAV)
  ├── credStore.SetCardDAVPassword(sourceID, password)
  └── carddavScheduler.SyncSource(sourceID)

LinkAccountContactSource(accountID, name, syncInterval):
  ├── Tạo contact source mới linked đến account
  ├── type = account-specific (Google, Microsoft)
  └── SyncSource(sourceID) → fetch contacts từ provider API

UpdateContactSource(sourceID, config):
  └── carddavStore.UpdateSource(sourceID, config)

RemoveContactSource(sourceID):
  ├── carddavStore.DeleteSource(sourceID)
  └── credStore.Delete(sourceID)

SyncContactSource(sourceID):
  └── carddavSyncer.SyncSource(sourceID)
        ├── Nếu CardDAV: PROPFIND → REPORT (addressbook-query)
        ├── Nếu Google: Google People API
        ├── Nếu Microsoft: Microsoft Graph API
        ├── Upsert vào contact_records table
        └── Update sync_state

SyncAllContactSources():
  └── Với mỗi source: SyncContactSource(sourceID)
```

### 7.2 Contact Search

```
SearchContacts(query, limit):
  ├── Tìm trong contact_records (unified)
  ├── Tìm trong contacts table (local + vCard)
  ├── Tìm trong Google Contacts (nếu OAuth account)
  ├── Tìm trong vCard scanner (file system .vcf)
  └── Trả về kết quả hợp nhất (deduplicated by email)

Matching:
  ├── Bắt đầu bằng query (name hoặc email)
  ├── Fuzzy matching (LIKE '%query%')
  └── Giới hạn: limit (default 20)
```

### 7.3 vCard Scanner

```
VCardScanner:
  ├── Scan paths: ~/.local/share/contacts/, ~/.config/evolution/
  ├── Cache TTL: 20 phút
  ├── Parse .vcf files (go-vcard)
  └── Populate contacts table
```

---

## 8. Lịch (Calendar)

### 8.1 CalDAV Source Management

```
Calendar_AddCalDAVSource(name, url, username, password):
  ├── Lưu vào per-extension SQLite (calendar.db)
  └── credStore.SetSecret(extensionID, key, value) — OAuth/password

Calendar_ListCalDAVSources():
  └── API.ListSources() → CalDAVSource[]

Calendar_RemoveCalDAVSource(sourceID):
  ├── API.DeleteSource(sourceID)
  └── credStore.DeleteSecret(extensionID, key)
```

### 8.2 Event Sync

```
Calendar sync:
  ├── CalDAV: PROPFIND → REPORT (calendar-query)
  │     └── Parse iCalendar (go-ical) → events
  ├── Microsoft: Microsoft Graph Calendar API
  ├── Google: Google Calendar API
  ├── RRULE expand: teambition/rrule-go
  └── Lưu vào calendar.db

Calendar_GetEvents(sourceID, startDate, endDate):
  └── API.ListEvents(sourceID, start, end) → Event[]
```

### 8.3 Alarm Scheduling

```
AlarmScheduler:
  ├── Kiểm tra events sắp đến hạn
  ├── Nếu alarm trong vòng 5 phút:
  │     └── Notifications.Show() — OS notification
  └── Schedule: mỗi phút kiểm tra
```

---

## 9. Thiết lập và Cấu hình

### 9.1 Settings Store

```
Settings CRUD:
  GetSetting(key) → value (string)
  SetSetting(key, value)
  DeleteSetting(key)

Setting keys (từ migrations.go):
  run_background          — Boolean: minimize to tray
  start_hidden            — Boolean: start minimized
  native_title_bar        — Boolean: use OS title bar
  compose_mode            — "new-window" | "inline"
  mailto_mode             — "new-window" | "inline"
  default_font_size       — Number
  theme                   — String: theme name
  composer_font_size      — Number
  image_allowlist_enabled — Boolean
```

### 9.2 Image Allowlist

```
ImageAllowlist:
  ListAllowedSenders() → []AllowedSender
  AllowSender(domain) → Add to image_allowlist
  DisallowSender(domain) → Remove from image_allowlist
  IsAllowed(senderDomain) → boolean

Logic:
  ─ Khi hiển thị email HTML:
    ├── Nếu sender domain trong allowlist: hiển thị remote images
    └── Nếu không: block remote images + show "Show Images" button
```

### 9.3 App State Persistence

```
AppState:
  SaveState(key, value) → app_state table
  LoadState(key) → value

Persisted state keys:
  sidebar_width          — Number
  message_list_width     — Number
  viewer_width           — Number
  split_position         — Number
  last_selected_folder   — JSON {accountId, folderId}
  active_extension       — String (calendar/contacts)
  expanded_folders       — JSON string[] (folders đang mở)
  composer_window_bounds — JSON {x, y, width, height}
```

### 9.4 Theme System

```
Theme:
  SetTheme(themeName) → lưu vào settings
  GetSystemTheme() → "light" | "dark" (XDG Settings Portal)
  ApplyTheme() → set data-theme attribute + .dark class

Frontend:
  ├── initTheme(storedMode, getSystemTheme)
  ├── applyThemeFromMode(mode)
  │     ├── Nếu mode = "system": dùng portal theme hoặc prefers-color-scheme
  │     └── Nếu mode = "dark" / "light": apply trực tiếp
  └── Xử lý events:
        ├── theme:changed → backend báo theme thay đổi
        └── mediaQuery change → system dark/light switch

Theme files:
  frontend/src/themes/*.css — 30+ themes (Adwaita, Breeze, Catppuccin, Dracula, etc.)
```

---

## 10. Tìm kiếm

### 10.1 Full-Text Search (FTS)

```
Search(query, accountID, folderID, limit, offset):
  ├── Nếu query rỗng: return empty
  └── messageStore.Search(query, accountID, folderID, limit, offset)
        ├── SQLite FTS5 trên messages table
        ├── Tìm trong: subject, bodyText, bodyHTML
        ├── Sắp xếp: date DESC
        └── Return SearchResult[]

FTS Indexer:
  ├── IndexAllFolders(ctx):
  │     ├── Với mỗi folder:
  │     │     ├── SELECT messages WHERE FTSIndexed = false
  │     │     ├── Insert vào FTS virtual table
  │     │     └── Emit progress
  │     └── Delay khởi động: 5s (để initial sync hoàn tất)
  │
  └── Callbacks:
        ├── ProgressCallback(folderID, indexed, total)
        └── CompleteCallback(folderID)
```

---

## 11. Undo (Hoàn tác)

### 11.1 Undo Stack

```
Stack:
  ├── Max size: 50 commands
  ├── Timeout: 30 giây (mỗi command tự động expired)
  ├── Khi stack đầy: pop oldest, push newest
  └── Khi undo: pop newest, execute reverse

Commands:
  MoveCommand:
    ├── state: {messageID, fromFolderID, toFolderID}
    ├── execute: IMAP COPY + STORE \Deleted
    └── undo: IMAP COPY + STORE \Deleted ngược lại

  DeleteSentCommand:
    ├── state: {messageID, sentFolderID}
    ├── execute: IMAP STORE \Deleted + EXPUNGE
    └── undo: KHÔNG THỂ (sent message đã xóa)

  FlagCommand:
    ├── state: {messageID, flagType, oldValue, newValue}
    ├── execute: IMAP STORE flag
    └── undo: IMAP STORE flag cũ
```

---

## 12. Thông báo (Notifications)

### 12.1 OS Notifications

```
New mail notification:
  background.go → handleNewMailNotification(newMailInfo):
    ├── Kiểm tra: có message mới unseen không?
    ├── Lấy: from, subject, snippet
    ├── Build notification:
    │     └── Title: [accountName] - [from]
    │     └── Body: subject + snippet
    └── notifier.Show(notification):
          ├── Linux: D-Bus notification (org.freedesktop.Notifications)
          │     └── Fallback: xdg-desktop-portal
          ├── macOS: NSUserNotification (Objective-C)
          └── Windows: Toast notification (COM)

Click handling:
  ── User clicks notification:
    ├── ShowWindow() — focus main window
    └── Emit "notification:clicked" → frontend
```

### 12.2 Extension Notifications

```
coreapi.Notifications.Show(req):
  └── notifier.Show(notification) với ExtensionID + Path trong data
        └── Click → route đến extension
```

---

## 13. OAuth2 Flow

### 13.1 Standard Flow (Google / Microsoft)

```
1. Frontend: User chọn OAuth provider
2. Backend: StartOAuthFlow(provider)
   ├── Lấy provider config (shipped credentials)
   ├── Tạo authorization URL (state + PKCE)
   ├── Mở browser
   └── Đợi callback (embedded HTTP server)

3. Browser: User login → consent → redirect tới localhost callback

4. Backend callback handler:
   ├── Kiểm tra state (chống CSRF)
   ├── Exchange auth code → tokens
   │     ├── POST token endpoint
   │     └── Parse response: access_token, refresh_token, expires_in, id_token
   ├── Lưu pending tokens
   └── Emit "oauth:success"

5. Frontend: CompleteOAuthAccountSetup(displayName, syncPeriodDays)
   ├── accountStore.Create()
   ├── credStore.SetOAuthTokens(accountID, tokens)
   └── Sync folders
```

### 13.2 Custom OAuth Provider (BYOA)

```
1. Frontend: User nhập custom OAuth credentials
   ├── ClientID, ClientSecret
   ├── Auth URL, Token URL
   └── Scopes

2. Backend: StartCustomOAuthFlow(customConfig)
   ├── Tạo provider config từ user input
   ├── Flow giống standard OAuth
   └── Kết thúc: CompleteCustomOAuthAccountSetup
         └── Lưu custom provider config + tokens vào credStore
```

### 13.3 Token Refresh

```
Automatic refresh:
  ├── Khi token expires trong 5 phút
  ├── oauth2Manager.RefreshToken(provider, refreshToken)
  ├── Update tokens trong store
  └── Retry failed IMAP/SMTP operation

Error handling:
  ├── Nếu refresh thất bại:
  │     ├── Emit "oauth:reauth-required"
  │     └── Frontend show "Re-authorization required" dialog
  └── User clicks → StartOAuthFlow → re-authorize
```

### 13.4 Fixed OAuth Slot Aliases

User có thể chỉ định slot alias cho mỗi config slot:
```
SlotAliasLookup(configID):
  ├── Nếu user đã chọn alias: return target configID
  └── Nếu không: return "", false

Ví dụ:
  - Slot "google-contacts" alias → "google-mail"
  - Email Hub sẽ dùng google-mail credentials cho google-contacts
```

---

## 14. Multi-Window Composer

### 14.1 Spawn Composer Window

```
App.OpenComposerWindow(accountID, mode, messageID, draftID, mailtoURL):
  ├── Kiểm tra IPC server + token manager available
  ├── Lấy executable path (os.Executable)
  ├── Build args:
  │     ├── --compose
  │     ├── --account <accountID>
  │     ├── --ipc-address <address>
  │     ├── [--mode <mode>] (reply/reply-all/forward)
  │     ├── [--message-id <id>]
  │     ├── [--draft-id <id>]
  │     └── [--mailto <url>]
  ├── exec.Command(execPath, args...)
  ├── Viết IPC token vào stdin pipe
  └── cmd.Start()
```

### 14.2 Composer Process Startup

```
ComposerApp.Startup(ctx):
  ├── Đọc IPC token từ stdin
  ├── Preflight (mở DB, credStore) — giống main process
  ├── Kết nối IPC client tới main window
  │     └── ipcClient.Connect(ipcAddress, token)
  ├── Mở stores (chỉ đọc): account, folder, message, contact, draft, settings
  ├── Mở IMAP pool riêng
  ├── Init S/MIME + PGP
  ├── Lấy draft (nếu draftID) hoặc PrepareReply (nếu reply/forward)
  ├── Emit "composer:ready" → main window
  └── Frontend mount ComposerApp.svelte
```

### 14.3 IPC Communication

```
Composer → Main:
  ├── "message_sent" → main window sync Sent folder + show toast
  ├── "draft_saved" → main window sync Drafts folder
  ├── "draft_deleted" → main window sync Drafts folder
  ├── "composer_ready" → main window log
  └── "composer_closed" → main window log

Main → Composer:
  ├── "shutdown" → composer save draft + close
  └── "theme_changed" → composer update theme
```

### 14.4 Composer Shutdown

```
Main window đóng:
  ├── Broadcast "shutdown" → tất cả composer windows
  ├── Wait 500ms cho composers save drafts
  └── Stop IPC server

Composer window đóng:
  ├── Nếu có unsaved changes: prompt save
  ├── Emit "composer_closed" (có draftID nếu save)
  └── Close DB, IMAP pool, IPC client
```

---

## 15. Extension System

### 15.1 Extension Registration Flow

```
App.Startup → Extension Registration:
  └── Với mỗi extension trong knownExtensions:
        ├── newCoreForExtension(app, ext):
        │     └── coreImpl {app, extensionID, manifest}
        │
        └── ext.Register(core):
              ├── ext.RegisterRailTab(): UI rail button
              │     └── Contacts: "Contacts" tab
              │     └── Calendar: "Calendar" tab
              ├── ext.RegisterSettingsTab(): settings page
              │     └── Calendar: CalDAV source config
              ├── ext.RegisterAccountSetupHook():
              │     └── Contacts: suggest link contacts
              └── Return unregister function
```

### 15.2 Bridge Method Pattern

Mỗi bridge method tuân theo pattern:
```
func (b *CalendarBridge) Calendar_GetEvents(sourceID string, start, end time.Time) ([]Event, error) {
    // 1. Gate check
    if !b.gateEnabled() {
        return nil, ErrExtensionDisabled
    }

    // 2. Lazy init (sync.Once)
    b.initOnce.Do(func() {
        b.api, b.syncer, b.alarms, b.initErr = b.initExtension()
    })
    if b.initErr != nil {
        return nil, b.initErr
    }

    // 3. Business logic
    return b.api.ListEvents(sourceID, start, end)
}
```

### 15.3 Extension Storage

```
Calendar extension:
  └── DB path: ~/.local/share/hsx2mail/extensions/calendar/calendar.db
        ├── caldav_sources (url, username, sync settings)
        ├── events (UID, summary, description, start, end, rrule, etc.)
        ├── alarms (trigger time, action, related event)
        └── sync_state (last sync, delta token)

Contacts extension:
  └── Tables trong main DB:
        ├── contact_records (unified contact schema)
        └── contact_sources (shared với host's carddav store)
```

### 15.4 Extension Enable/Disable

```
Settings → Extensions tab:
  ├── Toggle extension on/off
  ├── Gọi SetExtensionEnabled(extensionID, enabled)
  │     └── Lưu vào settings store
  └── Frontend refresh extension UI

gateEnabled():
  ├── Đọc IsExtensionEnabled(id) từ settings store
  ├── Nếu disabled: return false
  └── Nếu enabled: return true
```

---

## Luồng Dữ Liệu Nội Bộ

### Account Creation → First Sync

```
AddAccount → accountStore.Create()
  ↓
updateDBConnectionPool() (tăng idle connections)
  ↓
Nếu enable: idleManager.StartAccount()
  ↓
Frontend nhận account → GetFolderTree(accountID) → load folder tree
  ↓
User chọn folder → SyncFolder(accountID, folderID)
  ↓
syncEngine.SyncMessages() → Phase 1-6
  ↓
Emit "folder:synced" → frontend render MessageList
```

### New Mail Notification → User Reads

```
IDLE EXISTS response
  ↓
processIdleEvents → SyncFolder(accountID, folderID)
  ↓
syncEngine detects new unseen messages
  ↓
handleNewMailNotification (nếu có unseen mới)
  ↓
notifier.Show() → OS notification
  ↓
User clicks notification → ShowWindow()
  ↓
Focus conversation → GetConversation(threadID)
  ↓
Nếu body chưa fetch: FetchMessageBody(messageID)
  ↓
Render ConversationViewer
```

### User Sends Email → Folder Updates

```
User clicks Send
  ↓
SendMessage pipeline (build MIME → SMTP → append Sent)
  ↓
Nếu draft: delete draft
  ↓
Return success → frontend toast + close composer
  ↓
Nếu detached: IPC "message_sent" → main window
  ↓
Main window: syncSentFolder(accountID) → SyncFolder(Sent folder)
  ↓
Emit "folder:synced" → frontend update Sent folder count
```
