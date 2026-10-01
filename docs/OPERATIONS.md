# OPERATIONS — build, test, release, troubleshoot

**Status: canonical.** This file owns "how do I build, verify, ship and rescue this
thing". `BUILD.md` is the short version; `RELEASE.md` is the checklist that points here.
If a build flag, target name or version location appears in more than one place, this
file is the one to trust.

Current version: **0.3.2**.

---

## 1. Prerequisites

| Need | Why | Linux package |
|---|---|---|
| Go 1.25+ (module declares `go 1.25`) | Backend | `sudo dnf5 install -y golang` |
| Wails v2 CLI | `make build`, `make dev` | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| Node.js + npm | Frontend build | `sudo dnf5 install -y nodejs` |
| `webkit2gtk-4.1` dev headers | Linux WebView | `sudo dnf5 install -y webkit2gtk4.1-devel` |
| GTK 3 dev headers | Linux window | `sudo dnf5 install -y gtk3-devel` |
| `flatpak-builder` | Flatpak only | `sudo dnf5 install -y flatpak-builder` |
| `golangci-lint` | `make lint-go` | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |

`BUILD_TAGS := webkit2_41` is set unconditionally in the `Makefile:52`. On Linux the
build therefore needs **WebKit2GTK 4.1**, not the older 4.0. On macOS and Windows the
system WebView / WebView2 is used and the tag is inert.

### OAuth credentials

OAuth client IDs are injected at link time from `.env`, never read at runtime.

```bash
cp .env.example .env     # committed-to-nothing; .env.local overrides .env
```

| Variable | Purpose |
|---|---|
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Production mail client (Gmail). Also backs first-party extension scopes. |
| `MICROSOFT_CLIENT_ID` | Microsoft mail. Also backs the Contacts + Calendar extensions; add `Contacts.ReadWrite` and `Calendars.ReadWrite` to that registration. |
| `GOOGLE_TESTING_CLIENT_ID` / `GOOGLE_TESTING_CLIENT_SECRET` | Separate unverified client, surfaced in the picker as "Email Hub - Google (Testing)". |

The `Makefile` injects them into `internal/oauth2` package variables via
`LDFLAGS` (`Makefile:29-64`). `make build` prints a warning and continues when both
`GOOGLE_CLIENT_ID` and `MICROSOFT_CLIENT_ID` are empty — the app builds, but Gmail and
Outlook OAuth login will not work. That is the intended local-only path for password
auth accounts.

`.env.local` overrides `.env` and is the one to use for personal credentials; do not
commit either file.

---

## 2. Build targets

All targets live in the root `Makefile`. Run `make help` for the annotated list.

| Target | What it does | Output |
|---|---|---|
| `make build` | Production Wails build. `wails build -ldflags "$(LDFLAGS)" -tags webkit2_41`. On macOS it ad-hoc re-signs the bundle (required for notifications). | `build/bin/hsx2mail` (`.app` on macOS) |
| `make build-linux` | Same, with extra tags `linux,production`. | `build/bin/hsx2mail` |
| `make dev` | `wails dev` with hot reload. | live |
| `make dev-race` | Dev mode with `-race`. ~5–10× runtime overhead; instruments every memory access. | live |
| `make flatpak` | `./build/flatpak/build-local.sh` — local Flatpak build. | `build/bin/Hsx2Mail-<version>.flatpak` |
| `make flatpak-dev` | `./build/flatpak/build-flatpak.sh` — dev manifest (`-dev` id suffix). | `build/bin/Hsx2Mail-dev.flatpak` |
| `make build-windows-installer` | NSIS installer. | `build/bin/hsx2mail-amd64-installer.exe` |
| `make install-linux` / `make install-darwin` | Install from a local build. | `/usr/local/bin` + `.desktop` + icons, or `/Applications` |
| `make clean` | Removes `build/bin`, `frontend/dist`, `AppDir`, `hsx2mail`. | — |
| `make frontend-deps` | `cd frontend && npm install`. | — |
| `make generate` | Regenerate Wails TypeScript bindings (`wails generate module`). | `frontend/wailsjs/` |

`build/bin/aerion` and `build/go_build_github_com_hkdb_aerion*` are stale artefacts from a
previous module path (`github.com/hkdb/aerion`). They are not built by any current
target. Ignore them; `make clean` will not remove them.

---

## 3. Tests and linters

| Command | Runs |
|---|---|
| `make test` | `go test ./...` |
| `make test-race` | `go test -race -count=1 ./...` — the sync engine runs many goroutines; use this when touching `internal/sync`, `internal/imap` or the IDLE manager |
| `make vet` | `go vet ./...` |
| `make lint` | `lint-go lint-frontend check-frontend check-offline-icons unused-front` |
| `make lint-go` | `golangci-lint run`. `.golangci.yml` is v2 format: it only *disables* `unused` (Wails-bound methods, OpenPGP/S/MIME helper APIs and OAuth callbacks look unreferenced but are reachable at runtime), so the effective set is the v2 default minus `unused` — `errcheck`, `govet`, `ineffassign`, `staticcheck` (with `QF*` and `ST*` checks turned off) |
| `make lint-frontend` | `cd frontend && npm run lint` (ESLint) |
| `make check-frontend` | `svelte-check` on Svelte + TypeScript |
| `make check-offline-icons` | `node scripts/check-offline-icons.mjs` — verifies every Iconify icon has an offline JSON bundle |
| `make unused-front` | `knip` — reports unused dependencies/exports. Non-fatal (`|| true`) |
| `make check` | **The gate: `build` + `vet` + `test` + `lint`.** Run this before opening a PR. |

Targeted runs while iterating:

```bash
go test ./internal/database/ -run TestMigrate -v
go test ./internal/sync/ -race -count=1 ./internal/sync/
cd frontend && npm run check        # svelte-check
```

---

## 4. Cutting a release

### 4.1 Version locations, in the order they must be edited

There is no single source of version truth. Five sites carry the number; miss one and
the artefact, the UI and the package manager disagree.

| # | File | Field | Currently |
|---|---|---|---|
| 1 | `app/state.go:30` | `const Version` — the value `--version` prints and the About tab shows | `0.3.2` |
| 2 | `wails.json:12` | `productVersion` | `0.3.2` |
| 3 | `frontend/package.json:4` | `version` | `0.3.2` |
| 4 | `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml:288` | new `<release version=… date=…>` entry at the top of `<releases>` | `0.3.2` |
| 5 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml` | *(no version field — see below)* | n/a |

`frontend/package-lock.json` mirrors `package.json`; `npm install` rewrites it, so
regenerate it in the same commit rather than hand-editing.

**There is no `CHANGELOG.md` in this repository.** Release notes live in the AppStream
metainfo `<releases>` entries, which is what Flathub renders. An earlier version of
`RELEASE.md` told the releaser to edit a `CHANGELOG.md` that does not exist; that step
has been removed.

The Flatpak manifest deliberately carries no version. `build/flatpak/build-local.sh:78`
derives the bundle name from git:

```bash
VERSION=$(git describe --tags --exact-match 2>/dev/null || echo "dev")
BUNDLE_NAME="Hsx2Mail-${VERSION}.flatpak"
```

So the tag must exist **before** the Flatpak build, or the bundle is named
`Hsx2Mail-dev.flatpak`.

### 4.2 Procedure

```bash
# 0. Work from a clean tree on the default branch.
git status --porcelain          # must be empty
make check                      # build + vet + test + lint

# 1. Bump the version in all four places (see the table above).
#    app/state.go, wails.json, frontend/package.json, metainfo.xml <releases>
grep -rn '0\.3\.2' app/state.go wails.json frontend/package.json \
    build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml

# 2. Refresh the frontend lockfile so it matches package.json.
make frontend-deps
git add frontend/package-lock.json

# 3. Add the AppStream release entry: version, date, and the user-visible changes.
#    The newest <release> must be first inside <releases>.

# 4. Regenerate the schema doc if migrations changed. An empty diff means they did not.
go run ./tools/db/schemadump > /tmp/schema_raw.txt
python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt
git diff --exit-code docs/DATABASE.md || echo "update the hand-written sections too"

# 5. Commit, then tag. The tag is what build-local.sh reads.
git commit -am "release: v0.3.3"
git tag -a v0.3.3 -m "v0.3.3"
git push origin main --follow-tags

# 6. Build artefacts.
make build                        # -> build/bin/hsx2mail
make flatpak                      # -> build/bin/Hsx2Mail-v0.3.3.flatpak
make build-windows-installer      # -> build/bin/hsx2mail-amd64-installer.exe
```

### 4.3 Artefact names

| Artefact | Path |
|---|---|
| Linux binary | `build/bin/hsx2mail` |
| macOS bundle | `build/bin/Hsx2Mail.app` |
| Windows installer | `build/bin/hsx2mail-amd64-installer.exe` |
| Flatpak bundle (tagged) | `build/bin/Hsx2Mail-v<version>.flatpak` |
| Flatpak bundle (untagged tree) | `build/bin/Hsx2Mail-dev.flatpak` |
| Flatpak app id | `io.github.beheoxinh.Hsx2Mail` (dev: `…Hsx2Mail-dev`) |

Note the **versioned** Flatpak filename. `flatpak --user install build/bin/Hsx2Mail.flatpak`
(the old instruction in `BUILD.md`) does not exist; the file is
`build/bin/Hsx2Mail-v0.3.2.flatpak`.

### 4.4 Release gate

Do not tag unless all of these are green:

- `make check`
- `git diff --exit-code docs/DATABASE.md` after regenerating (no uncommitted schema drift)
- `hsx2mail --version` prints the tag you are about to create
- the metainfo `<release>` date is today, in `YYYY-MM-DD`

### 4.5 Rolling back a release

See `SQL_ROLLBACK.md` for the database side. For the code side: re-tag the previous
commit and rebuild. Note that migrations are **forward-only** — an older binary will
refuse to open a newer database with `ErrSchemaTooNew` rather than corrupt it, which is
the intended behaviour. The practical rollback for users is therefore "reinstall the
previous version **and** restore their database backup", not "just downgrade".

---

## 5. Installing the Flatpak

```bash
# One-time host setup (build-local.sh does this for you, but the runtimes are
# large enough that doing it explicitly gives better error messages).
flatpak remote-add --if-not-exists --user flathub https://flathub.org
flatpak install -y --user flathub org.gnome.Platform
flatpak install -y --user flathub org.freedesktop.Sdk.Extension.node24

# Build from this tree.
make flatpak
ls build/bin/*.flatpak            # Hsx2Mail-v0.3.2.flatpak

# Install and run.
flatpak install --user build/bin/Hsx2Mail-v0.3.2.flatpak
flatpak run io.github.beheoxinh.Hsx2Mail

# Update in place later.
flatpak install --user -y build/bin/Hsx2Mail-v0.3.3.flatpak
```

The manifest requests these permissions
(`build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:10-30`); each maps to a
symptom in §6:

| Permission | Needed for |
|---|---|
| `--share=network` | IMAP/SMTP/OAuth |
| `--share=ipc` | X11 and Wayland |
| `--socket=wayland`, `--socket=fallback-x11` | Display |
| `--device=dri` | GPU |
| `--talk-name=org.freedesktop.portal.Notification` | Notifications (with portal fallback when the portal is unavailable) |
| `--talk-name=org.freedesktop.secrets` | Secret Service (`gnome-keyring` / `kwallet`) for the credential store |

Under Flatpak the data directory is `~/.var/app/io.github.beheoxinh.Hsx2Mail/`, and
autostart launches `flatpak run io.github.beheoxinh.Hsx2Mail --start-hidden`
(`internal/platform/autostart_linux.go:276-282`).

---

## 6. Troubleshooting

### 6.0 Logging first

There is **no log file**. `logging.Init` is called with `Console: true` and no `File`
(`app/app.go:455-458`, `app/detached_composer.go:130`), so everything goes to the
process's stderr. In release builds the level is `fatal`; pass `--debug` (or set
`HSX2MAIL_DEBUG=1`) to get `debug`.

```bash
hsx2mail --debug                                  # foreground, read stderr
HSX2MAIL_DEBUG=1 flatpak run io.github.beheoxinh.Hsx2Mail
journalctl --user -f -o cat /usr/bin/hsx2mail     # systemd user unit, if installed
```

Frontend messages reach the same stream via `App.LogFrontend` (`app/log.go:10`). There
is no in-app log viewer; if a settings tab is supposed to show one, it does not exist.

### 6.1 Notifications do not appear

**Cause chain, in order of likelihood:**

1. **Wrong backend.** Linux tries the XDG desktop portal first and falls back to direct
   `org.freedesktop.Notifications` D-Bus (`internal/notification/notifier_linux.go:15-27`,
   `:66-88`). If neither is reachable, `Show` is a silent no-op. Force the direct backend
   to isolate it:
   ```bash
   hsx2mail --dbus-notify
   ```
   (`main.go:35`, threaded through `app.NewApp` → `App.useDirectDBus` → `notification.New`.)
2. **Do Not Disturb.** The portal silently drops notifications when the desktop is in DND.
3. **Notifications disabled in the app.** Check Settings → General → notifications.
4. **Flatpak permission missing.** `--talk-name=org.freedesktop.portal.Notification` must
   be in the manifest finish-args; a rebuilt manifest without it produces no
   notification and no error.
5. **Nothing to notify.** Notifications fire on *new unread* mail from IDLE or sync
   completion, not on folder selection. Sync the folder manually and see whether the
   badge count moves — if it does, the problem is the notifier, not the sync.

Diagnose with `dbus-monitor` on the notification bus:

```bash
dbus-monitor --session "interface='org.freedesktop.Notifications'"
```

### 6.2 Autostart does not fire

```bash
# 1. Is the setting actually on? It writes an XDG autostart .desktop file.
ls -l "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/" | grep -i hsx
cat "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/"*hsx*.desktop

# 2. The entry always launches hidden.
#    Linux:   hsx2mail --start-hidden
#    Flatpak: flatpak run <FLATPAK_ID> --start-hidden
#    (internal/platform/autostart_linux.go:276-282)
```

Causes:

- **Under Wayland, some desktops only run autostart entries from the session's
  autostart directory.** `XDG_CONFIG_HOME` set inconsistently between the settings UI and
  the session means the file is written somewhere the session never reads.
- **Enabling autostart also force-enables background mode** and `start_hidden`
  (`app/settings.go:211`), so a *successful* autostart is invisible by design. You are
  looking for the tray icon or a notification-click activation, not a window.
- **`--start-hidden` requires background mode to be on**, otherwise `GetStartHiddenActive`
  returns false and the window shows anyway (`app/app.go:999-1008`).
- **Executable path is resolved at write time** via `os.Executable()` and quoted per the
  Desktop Entry spec (`internal/platform/autostart_linux.go:243-274`). If you moved or
  renamed the binary after enabling autostart, the entry points at a dead path. Toggle
  the setting off and on to rewrite it.
- The desktop's own "Startup Applications" UI may show the entry as disabled even though
  the file is correct.

### 6.3 OS keyring unavailable

Symptom in the log: `OS keyring not available, using encrypted database storage`
(`internal/credentials/store.go:33-36`), or a WARN naming the degradation reason
(`internal/credentials/keyring.go:40-50`).

The app does **not** silently copy secrets into the database. When the keyring is
reachable but a runtime operation fails, the store returns `ErrKeyringUnavailable` and
the write fails rather than falling back (`internal/credentials/keyring.go:12-18`) —
copying the secret would be invisible to the user, the DB copy would later be migrated
back, and the process would keep using it for the rest of the session.

| Situation | Behaviour |
|---|---|
| No Secret Service at all (headless, minimal session) | Falls back to AES-GCM encrypted values in the database. Works; secrets are protected by the file's `0600` mode. |
| Keyring present but **locked** (screen locked, `gnome-keyring` not yet unlocked) | Operations fail with `ErrKeyringUnavailable`. Unlock the keyring; it is re-probed. |
| Secret Service crashed / D-Bus restarted | Same error. The re-probe is rate-limited to one round trip per `probeInterval = 30s` (`internal/credentials/keyring.go:41`), so recovery can lag by up to 30 s. |
| Flatpak without `--talk-name=org.freedesktop.secrets` | Keyring unreachable; silent permanent fallback. |

Check what the process can see:

```bash
busctl --user list | grep -i secret          # Secret Service present?
busctl --user introspect org.freedesktop.secrets /org/freedesktop/secrets
```

### 6.4 Composer windows

Detached composers are **separate OS processes** of the same executable, spawned with
`--compose --account <id> --ipc-address <addr>`, and they authenticate to the main
window's IPC server with a per-session token written to their **stdin**
(`app/ipc.go:192-250`, `internal/ipc/`). Protocol: newline-delimited JSON over a Unix
domain socket (a TCP port plus a lock file on Windows), token-authenticated.

| Symptom | Cause and fix |
|---|---|
| Window never opens; nothing in the log | `OpenComposerWindow` returns `nil` and swallows the reason when `a.ipcServer`/`a.ipcTokenMgr` is nil, when `StdinPipe` fails, or when `cmd.Start()` fails. Check the `app.ipc` component log. Restart the main window so IPC re-initialises. |
| Composer opens then closes immediately | The composer could not reach the IPC server or failed auth. The pre-auth frame is capped at `MaxAuthFrameBytes = 4 KiB` and there is a `5s` auth timeout; a client that stalls is dropped (`internal/ipc/server.go:19-37`). |
| "connection limit reached" warnings | `MaxConnections = 32` concurrent IPC clients. Each composer holds one for its lifetime, so a handful of leaked composer processes exhausts the pool. Kill stray `hsx2mail --compose` processes. |
| Composers see stale data | The composer opens the **same** database file. Concurrent access is safe because the DSN sets `_txlock=immediate` (see `DATABASE.md` §2), but the composer does not own a sync engine — it relies on the main window. Keep the main window running. |
| Composers die on "duplicate column name" | A migration race. The re-check inside the migration transaction exists to prevent exactly this; if you see it, the guard was bypassed — do not edit `internal/database/database.go` around `applyMigration`. |

Force a single-instance handoff test: launch `hsx2mail` twice; the second process must
exit immediately and the first window must come forward
(`internal/platform/singleinstance_linux.go`).

### 6.5 "database is locked"

`busy_timeout` is 30 s and `_txlock=immediate` is set, so a genuine lock error means
something bypassed the DSN. Check, in order:

```bash
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db 'PRAGMA journal_mode;'   # must be wal
sqlite3 ~/.local/share/hsx2mail/hsx2mail.db 'PRAGMA foreign_keys;'    # must be 1
ls -la ~/.local/share/hsx2mail/hsx2mail.db*
```

An old binary, a build from before `_txlock=immediate` landed, or a connection opened
outside `database.Open` will all lack the pragmas. Never open the DB from ad-hoc code
without going through `database.Open`.

### 6.6 Layout looks wrong after a window resize

Svelte 5 runes plus a virtualized message list. See `PERFORMANCE.md` for the virtualizer
configuration and `FRONTEND.md` for the store graph. Set the window to a known size and
reload before assuming the CSS is at fault.

### 6.7 App starts hidden and you cannot find it

```bash
hsx2mail --version         # proves the binary runs
pkill -f hsx2mail          # then relaunch without --start-hidden
```

`--start-hidden` overrides the stored setting for one boot
(`main.go:40`, `app/app.go:998-1008`). With background mode on, closing the window hides
it rather than quitting (`app/app.go:894-908`) — use the tray **Quit** item, or
`pkill`, to actually exit. There is no other exit path by design.

---

## 7. Command-line reference

All flags from `main.go:27-40`:

| Flag | Effect |
|---|---|
| `--debug` | Debug-level logging to stderr. Also `HSX2MAIL_DEBUG=1`. |
| `--compose` | Run as a detached composer window. Set by the main window, not by hand. |
| `--account <id>` | Account the composer belongs to. |
| `--ipc-address <addr>` | Main window's IPC server address. |
| `--mode new\|reply\|replyall\|forward` | Composer mode. |
| `--message-id <id>` | Message being replied to. |
| `--draft-id <id>` | Draft to resume. |
| `--mailto <url>` | Pre-populate from a `mailto:` URL. |
| `--dbus-notify` | Force direct D-Bus notifications instead of the portal. |
| `--start-hidden` | Window-less boot, overriding the stored setting. |
| `--version` | Print version and exit. |

---

## 8. Related documents

| Topic | Owner |
|---|---|
| Build targets in detail | this file; `BUILD.md` is the short form |
| Release checklist | `RELEASE.md` (points here) |
| Schema, migrations, PRAGMAs | `DATABASE.md` |
| Backup, restore, rollback | `SQL_ROLLBACK.md` |
| Background mode, autostart, sleep/wake | `BACKGROUND.md` |
| Frontend architecture | `FRONTEND.md` |
| Performance tuning | `PERFORMANCE.md` |
| System structure | `architecture.md` |
