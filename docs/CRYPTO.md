# CRYPTO — PGP, S/MIME, TOFU, keyring, HTML sanitizing

**Status: canonical.** This file owns everything cryptographic: the two mail-encryption
subsystems, TLS certificate trust, credential storage, and the input-sanitizing rules
that surround them.

Four independent subsystems, often confused:

| Subsystem | Package | Protects |
|---|---|---|
| PGP / OpenPGP | `internal/pgp` | Message content: sign, verify, encrypt, decrypt |
| S/MIME | `internal/smime` | Message content: sign, verify, encrypt, decrypt |
| TLS trust (TOFU) | `internal/certificate` | Transport: IMAP, SMTP, CardDAV, CalDAV |
| Credential + fallback storage | `internal/credentials`, `internal/crypto` | Passwords, OAuth tokens, contact-source secrets |

`internal/crypto` is *not* a mail-encryption package: it is the AES-256-GCM helper that
backs the credential store's database fallback. It also holds the two identity predicates
in §7.

---

## 1. PGP and S/MIME side by side

Both subsystems expose the same four-role shape, constructed once in `App.Startup` and
injected into the sync engine.

| Role | PGP | S/MIME |
|---|---|---|
| Key store | `pgp.Store` → tables `pgp_keys`, `pgp_sender_keys`, `pgp_keyservers` | `smime.Store` → tables `smime_certificates`, `smime_sender_certs` |
| Signer | `pgp.NewSigner` → `SignMessage(accountID, fromEmail, …)` (`internal/pgp/signer.go:22-32`) | `smime.NewSigner` |
| Verifier | `pgp.NewVerifier` → `VerifyAndUnwrap(raw)` (`internal/pgp/verifier.go:23-38`) | `smime.NewVerifier` |
| Encryptor | `pgp.NewEncryptor` → `EncryptMessage`, `EncryptMessageToSelf` (`internal/pgp/encryptor.go:23-71`) | `smime.NewEncryptor` |
| Decryptor | `pgp.NewDecryptor` → `DecryptMessage`, `DecryptBytes` (`internal/pgp/decryptor.go:25-69`) | `smime.NewDecryptor` |
| Private key material | credential store, via `credsStore` | credential store, via `credsStore` |
| Key discovery | WKD + HKP | Directory / manual import |

Both encryptors and decryptors take a `*credentials.Store`: **private keys and
certificates are not in the database**, only the encrypted blobs they were imported from.
That is why losing the keyring loses the ability to decrypt — see
`OPERATIONS.md` §6.3 and `SQL_ROLLBACK.md` § Restoring credentials.

### Sign / verify on receive

Verification happens during **body fetch**, not on render. `sync.Engine` is given both
verifiers at startup (`SetSMIMEVerifier`, `SetPGPVerifier`), so a signed message is
verified once, when its body is first parsed, and the result is persisted with the
message. Re-rendering never re-verifies.

### Sign / encrypt on send

`buildMIMEMessage` in the compose pipeline runs S/MIME signing and PGP signing as
optional steps, controlled per draft (`drafts.sign_message`, `drafts.pgp_sign_message`,
`drafts.pgp_encrypted`, `drafts.encrypted`). Both can be set on the same message; the
order is fixed in the builder. See `analysis/30-compose-send-draft.md`.

---

## 2. PGP key discovery

### WKD (Web Key Directory) — preferred

`internal/pgp/wkd.go`. WKD is the standards-track mechanism: the domain publishes keys
at a well-known URL, addressed by a z-base-32 hash of the local part.

```go
fmt.Sprintf("https://openpgpkey.%s/.well-known/openpgpkey/%s/hu/%s?l=%s", …)
```

`zBase32Encode` implements the RFC 6637 local-part hash.

### HKP (keyserver) — fallback

`internal/pgp/hkp.go`. `LookupHKP(email, servers)` queries servers **sequentially** and
takes the first usable result. The default list, in order:

```go
var DefaultHKPServers = []string{
    "https://keys.openpgp.org",      // email-verified keys
    "https://keyserver.ubuntu.com",
    "https://pgp.mit.edu",
}
```

`keys.openpgp.org` is first because it only serves keys whose email address has been
verified, so a lookup that succeeds there means the key genuinely belongs to that
address. The other two are unverified: a key found there is a *claim*, and signature
verification still has to succeed before you trust the content.

`DefaultHKPServers` is editable in Settings, persisted in `pgp_keyservers`.

### Sender-key caching

`Verifier.cacheSenderKey` (`internal/pgp/verifier.go:262`) caches the resolved key per
sender so a conversation with N messages from one sender performs one lookup, not N.
The cache is in-memory and rebuilt on restart; it is not persisted.

---

## 3. TLS certificate trust (TOFU)

`internal/certificate`. Hsx2Mail uses **Trust On First Use**, not CA pinning, and not a
certificate dialog on every connection.

### The mechanism

`BuildTLSConfigDynamic(store)` (`internal/certificate/verifier.go:61-84`) is the only
`tls.Config` factory wired into a transport (used for the CardDAV/CalDAV transport,
`app/app.go:683`). Its shape is deliberate:

```go
return &tls.Config{
    InsecureSkipVerify: true, // real verification happens in VerifyConnection
    VerifyConnection: func(cs tls.ConnectionState) error {
        if len(cs.PeerCertificates) == 0 {
            return fmt.Errorf("no certificates presented")
        }
        leaf := cs.PeerCertificates[0]

        systemErr := verifyParsedWithSystemCAs(cs.PeerCertificates, cs.ServerName)
        if systemErr == nil {
            return nil                                  // CA-signed and valid
        }

        fingerprint := Fingerprint(leaf.Raw)
        if store != nil && store.IsTrusted(fingerprint) {
            return nil                                  // user previously accepted
        }
        // … else emit CertificateInfo so the UI can prompt
    },
}
```

`InsecureSkipVerify: true` is safe **only** because `VerifyConnection` performs the
full check and returns an error, which aborts the handshake. Do not "simplify" this by
dropping `VerifyConnection` — that turns the whole subsystem off.

Order of preference:

1. **System CA pool** validates the chain → accept, nothing stored, nothing prompted.
2. Otherwise, **fingerprint in `trusted_certificates`** → accept.
3. Otherwise, **prompt the user** with the chain, the fingerprint, and a classified
   reason. `classifyError` (`internal/certificate/verifier.go:189-210`) maps Go's TLS
   errors to the strings the UI shows:

   | Go error contains | Shown as |
   |---|---|
   | `signed by unknown authority` | self-signed or unknown certificate authority |
   | `certificate has expired` | certificate has expired |
   | `hostname` / `not valid for any names` / `cannot validate certificate` | certificate name mismatch |
   | anything else | the raw error string |

### Store API

`internal/certificate/store.go`, backed by `trusted_certificates` (migration v18):

| Method | Purpose |
|---|---|
| `IsTrusted(fingerprint)` | Is this fingerprint already accepted? |
| `AcceptPermanently(host, info)` | Record acceptance after the user says yes. |
| `List(hosts …)` | Enumerate trust decisions. |
| `Remove(fingerprint)` | Revoke a trust decision; the next connection re-prompts. |

`BuildTLSConfig(host, store)` is the per-host variant and is currently **unused** — only
the dynamic one is wired up. Do not add a second transport that uses it without first
deciding which of the two factories is canonical.

---

## 4. Credential storage

`internal/credentials`. Primary: the OS keyring. Fallback: AES-256-GCM ciphertext in the
database.

| Platform | Keyring |
|---|---|
| Linux / BSD | Secret Service over D-Bus (`gnome-keyring`, `kwallet`) |
| macOS | Keychain |
| Windows | Credential Manager |

Service name is `hsx2mail` (`internal/credentials/store.go:14`).

### Two modes, and the difference that matters

| Mode | When | Behaviour on a failed operation |
|---|---|---|
| Keyring | A probe at startup succeeded | Returns `ErrKeyringUnavailable`. **The write fails.** |
| AES-GCM fallback | The probe failed at startup | Uses the database copy. |

`ErrKeyringUnavailable` is returned rather than silently falling back, for a concrete
reason documented at `internal/credentials/keyring.go:12-18`: copying a secret into the
database is invisible to the user, the DB copy is later migrated back into the keyring,
and the process would then keep using the DB copy for the rest of its session — so the
user would believe a keyring write succeeded when it did not.

`keyringLive` is an `atomic.Bool` re-probed after a runtime failure, so a keyring that
comes back (screen unlocked, `gnome-keyring` restarted) is picked up. The re-probe is
rate-limited to one round trip per `probeInterval = 30 * time.Second`
(`internal/credentials/keyring.go:41`), so recovery can lag by up to 30 s.

The first degradation reason is recorded and surfaced as a warning rather than the store
vanishing silently into a log line.

### The AES-256-GCM fallback

`internal/crypto/crypto.go`:

| Constant | Value |
|---|---|
| `keyFileName` | `device.key` |
| `saltSize` | 32 bytes |
| `keySize` | 32 bytes (AES-256) |
| `pbkdf2Iterations` | 100 000 |

`deriveKey` (`internal/crypto/crypto.go:87-95`) is **not** a passphrase KDF over something
the user knows. It mixes machine-specific data with the salt:

```go
fmt.Sprintf("hsx2mail:%s:%s:%d", hostname, username, …)
return pbkdf2.Key([]byte(machineData), salt, pbkdf2Iterations, keySize)
```

The threat model is therefore "another local user with read access to the data directory",
not "someone who copies the database to another machine". Consequence, and it is a real
one: **an AES-fallback ciphertext does not survive being moved to a different machine or
under a different user account** — the hostname/username input changes, so the derived key
changes, so decryption fails. Plan credential re-entry into any restore procedure
(`SQL_ROLLBACK.md` § Restoring credentials).

`Encrypt` returns base64; `Decrypt` takes it back. Both use AES-256-GCM with a fresh
nonce from `crypto/rand` per operation.

---

## 5. HTML sanitizing

`internal/email/sanitizer.go` and `internal/email/composer.go`, using `bluemonday`.

### Display sanitizing (incoming mail)

`AllowURLSchemes("cid", "data", "http", "https", "mailto")` (`:92`). `data:` is allowed for
**display only**: inline images may arrive as `data:` URIs or `cid:` references. The
`StripUnsafeNavigationSchemes` post-pass (§5) is what stops that from becoming an
`href` target.

### Composer sanitizing (outgoing mail)

`internal/email/composer.go:48`:

```go
p.AllowURLSchemes("http", "https", "mailto", "cid")
p.AllowRelativeURLs(false)
p.RequireNoFollowOnLinks(true)
p.RequireNoReferrerOnLinks(true)
p.AddTargetBlankToFullyQualifiedLinks(true)
```

No `data:` and no relative URLs in mail you send. Inline images go out as `cid:`
attachments, not as `data:` URIs, because a `data:` URI survives into the recipient's
mail client as an opaque blob.

### `StripUnsafeNavigationSchemes`

`internal/email/sanitizer.go:150-170`. bluemonday applies one scheme list to *every* URL
attribute, so it cannot express "allow `data:` in `src` but never in `href`". This
post-pass fixes that: it walks the navigable attributes and rewrites any non-allowlisted
scheme.

The attribute set is `navigableAttrs` (`:149`):

```
href | src | action | formaction | cite | longdesc | background | poster
     | data | codebase | ping
```

`data:` is stripped from these because it turns a click into script execution rather than
navigation. If you add an attribute that can be a URL, add it here too.

---

## 6. Field-level input handling

Header injection is prevented at the boundary, not by escaping later. When adding a field
that reaches a MIME header, strip CR and LF before it is written — a header value
containing a newline is a header-injection primitive, and escaping at render time is too
late because the damage is already in the transmitted message.

The URL protocol allowlist for anything the app *opens* (links, attachments, external
browsers) is `http`, `https`, `mailto` only. `xdg-open` is invoked as
`exec.Command("xdg-open", url)` — an argument vector, never `sh -c` — so a URL cannot
become a shell command even if it escapes the allowlist.

---

## 7. Identity predicates

`internal/crypto/identity.go`, used by both the PGP and S/MIME paths:

| Function | Meaning |
|---|---|
| `EmailsMatch(a, b)` | Case-insensitive, and tolerant of `+tag` sub-addressing. |
| `SignerMatchesSender(signerEmail, fromEmail)` | Does the key/cert that signed match the `From:`? This is the check that turns "the message is signed" into "the message is signed *by the claimed sender*". |

A signature that verifies cryptographically but whose signer does not match `From:` is
**not** a valid signature. The UI must show it as such.

---

## 8. Security invariants

Do not weaken any of these without a threat model in the pull request.

| Invariant | Where |
|---|---|
| Database file is `0600`, data directory `0700` | `internal/database/database.go:49-53`, `:85-90` |
| Private keys and certificates live in the keyring, not the DB | `internal/pgp`, `internal/smime` constructors |
| A keyring write failure fails the write, it does not fall back | `internal/credentials/keyring.go:12-18` |
| TLS is verified in `VerifyConnection`, never skipped | `internal/certificate/verifier.go:61-84` |
| `data:` is never a navigable target | `internal/email/sanitizer.go:150-170` |
| Outgoing mail has no `data:` and no relative URLs | `internal/email/composer.go:48-54` |
| `exec.Command` with an argument vector, never a shell | all `xdg-open` call sites |
| IPC frames are size-capped and connections are capped | `internal/ipc/server.go:19-37` |
| DB schema is refused if newer than the binary | `internal/database/database.go:189-196` |
| Newlines stripped from header-bound fields | compose pipeline |

---

## 9. Related documents

| Topic | Owner |
|---|---|
| Crypto, TLS trust, keyring, sanitizing | this file |
| Schema for every table named above | `DATABASE.md` |
| Keyring troubleshooting | `OPERATIONS.md` §6.3 |
| Credential restore caveats | `SQL_ROLLBACK.md` |
| Compose/send pipeline | `analysis/30-compose-send-draft.md` |
| Extension capability boundary | `EXTENSIONS.md` |
| Security questionnaire answers | `CASAT2.md` |
