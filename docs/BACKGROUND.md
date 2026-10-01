# BACKGROUND — startup, window lifecycle, autostart, sleep/wake, network

**Status: canonical.** This file owns every question of the form "when does this thing
run, and what happens when I close the window". It supersedes any prose about
"minimizing to tray" elsewhere; see §4 for the correction.

---

## 1. Startup sequence

`main.go` does flag parsing and a single-instance check *before* Wails creates a window,
so a preflight failure never produces a half-rendered window.

```
main.go
  ├─ parse flags (--debug, --compose, --start-hidden, --dbus-notify, --version, …)
  ├─ parse mailto: from positional args
  ├─ platform.MonitorGBMErrors()
  ├─ single-instance lock
  │    └─ if another instance holds the lock → activate it (ShowWindow) → exit 0
  └─ app.NewApp(...).Preflight()          ← before wails.Run
       ├─ logging.Init (fatal, or debug if --debug / HSX2MAIL_DEBUG=1)
       ├─ platform.GetPaths + EnsureDirectories (0700)
       ├─ database.Open + Migrate          (v1..v42, see DATABASE.md)
       ├─ credentials.NewStore             (keyring, AES fallback)
       └─ OAuth override wiring
  └─ wails.Run
       └─ Startup(ctx)
            ├─ single-instance onShow callback
            ├─ construct every store
            ├─ scale the DB pool by account count
            ├─ PGP + S/MIME signers/verifiers/encryptors/decryptors
            ├─ IMAP pool + sync engine
            ├─ tray (conditional — §4)
            ├─ extensions registration
            ├─ IPC server for detached composers
            ├─ monitors: network, sleep/wake, session-lock, theme
            ├─ sync scheduler + IDLE manager
            ├─ pending-draft sync, FTS indexer
            └─ emit "app:ready"  → frontend mounts App.svelte
       └─ Shutdown(ctx)
            ├─ broadcast shutdown to composer windows
            ├─ stop scheduler, IDLE, monitors, notification listener
            ├─ close IMAP connections
            └─ close SQLite
```

Window creation is configured with `StartHidden=true` so there is no white flash before
the frontend mounts.

### Start hidden

Two independent mechanisms, easy to confuse:

| Mechanism | Effect |
|---|---|
| `--start-hidden` (flag, `main.go:40`) | Forces a window-less boot for **one** launch, overriding the stored setting. This is what the autostart entry uses. |
| `start_hidden` (stored setting) | Only takes effect if `run_background` is also on. See §2. |

`App.GetStartHiddenActive()` (`app/app.go:998-1008`) returns true only when **both**
`start_hidden` and `run_background` are enabled. That coupling is enforced at the setter
too: `SetStartHidden(true)` force-enables `run_background`
(`app/settings.go:212-218`), and `SetRunBackground(false)` force-disables
`start_hidden` (`app/settings.go:186-190`).

Frontend sequence (parallel): wait for the Wails runtime, `WindowShow` after the runtime is
detected, `initI18n`, `waitForBackendReady` (the `app:ready` event with an `IsReady`
fallback), then `mount(App.svelte)`.

---

## 2. Settings that govern "still running"

All four live in the `settings` table as string keys
(`internal/settings/store.go:25-26` and neighbours).

| Key | Meaning |
|---|---|
| `run_background` | Closing the window **hides** it instead of quitting. |
| `start_hidden` | Boot without showing the window. Requires `run_background`. |
| `autostart` | Launch on desktop session login. Also implies background + hidden. |
| tray | Derived — see §4. Not a setting. |

### Close semantics

`App.BeforeClose` (`app/app.go:894-908`):

```go
if shuttingDown { return false }                  // already quitting → really quit

runBg, _ := a.settingsStore.GetRunBackground()
if runBg {
    wailsRuntime.WindowHide(a.ctx)                // hide, keep running
    a.windowHidden = true
    return true                                   // veto the close
}
// normal shutdown: emit "app:shutting-down", wait 150 ms for the UI to render
// the overlay, then wailsRuntime.Quit
return true
```

So:

- **`run_background` on** → the close button hides the window. The process lives on, keeps
  syncing, keeps IDLE connections open. The window comes back from the tray, a
  notification click, single-instance activation, or the launcher.
- **`run_background` off** → close shows a shutdown overlay, then quits after 150 ms.
- **`QuitApp()`** (`app/app.go:982`) forces a real quit, bypassing background mode. The
  tray **Quit** item and the frontend both call it. There is no other exit path.

Consequence: with background mode on, there is no window-closed way to quit. That is
intentional, but it surprises people. `pkill -f hsx2mail` always works.

---

## 3. Autostart

| Platform | Mechanism |
|---|---|
| Linux / BSD | XDG autostart `.desktop` file in `$XDG_CONFIG_HOME/autostart` (`~/.config/autostart`), written by `internal/platform/autostart_linux.go` |
| macOS | Login item (`internal/platform/autostart_darwin.go`) |
| Windows | Registry `Run` key (`internal/platform/autostart_windows.go`) |

The Linux entry is a **background** entry — it always launches hidden
(`internal/platform/autostart_linux.go:264-274`):

```go
func autostartCommandline() []string {
    if id := os.Getenv("FLATPAK_ID"); id != "" {
        return []string{"flatpak", "run", id, "--start-hidden"}
    }
    return []string{"hsx2mail", "--start-hidden"}
}
```

`FLATPAK_ID` is set by the Flatpak sandbox, so the same code produces the right command
under Flatpak and outside it. The executable path is resolved with `os.Executable()` at
write time and quoted per the Desktop Entry spec, because `os.Executable` resolves
`argv[0]` through `PATH` and can otherwise produce a path that does not exist
(`internal/platform/autostart_linux.go:243-274`).

**A successful autostart is invisible by design.** Because the entry passes
`--start-hidden`, and `--start-hidden` only takes effect when `run_background` is on
(§1), an autostart that is working looks exactly like an autostart that failed. Look for
the tray icon (§4) or activate via `hsx2mail` (single-instance handoff) or a
notification click.

Turning autostart off when background mode is also off tears the tray icon down
(`app/tray.go`, `tray.Stop()` is called on both being disabled).

Troubleshooting lives in `OPERATIONS.md` §6.2.

---

## 4. The tray icon — and the "minimize to tray" correction

**There is a tray.** `internal/tray/` wraps `fyne.io/systray`, because Wails v2 exposes
no tray API. It runs on its own goroutine with its own D-Bus event loop and must not run
on the Wails main thread (`internal/tray/tray.go:1-8`, `:41-56`).

Older documentation (notably `business-logic.md:815`, which described
`run_background` as "minimize to tray") predates the tray and is wrong. Corrected
statement:

> `run_background` **hides** the window. It does not by itself create a tray icon.

The icon exists exactly when the instance can be window-less, i.e. when it would otherwise
be unreachable (`app/tray.go`, `App.trayWanted()`):

```
tray icon exists  ⟺  run_background  OR  autostart
```

Users who enable neither keep the previous, tray-free behaviour. When you disable both,
`tray.Stop()` runs and the icon disappears. `tray.Start` is idempotent
(`started.CompareAndSwap(false, true)`) and `tray.Stop` sets a `quitting` flag so a
second `systray.Run` cannot race the first one's teardown
(`internal/tray/tray.go:44-56`, `:96-110`).

Menu: **Show** → `App.ShowWindow`; **Sync** → `App.SyncAllComplete`; **Quit** →
`App.QuitApp`.

### Platform caveat

On Linux the icon is a `StatusNotifierItem` on the session bus. Desktops that do not host
a StatusNotifierItem watcher — **stock GNOME without the AppIndicator /
KStatusNotifierItem extension** — will not render it. This is a desktop limitation, not a
bug in the app, and the notification-click path is the fallback there
(`internal/tray/tray.go:5-8`).

---

## 5. Single instance and window handoff

`internal/platform/singleinstance_linux.go` uses a **Unix domain socket** as the lock
(Windows uses a TCP port plus a lock file; macOS has its own variant). No D-Bus is
involved.

Behaviour: a second launch detects the lock, sends its arguments to the running instance
over the socket, the running instance calls `ShowWindow` and focuses, and the second
process exits. This is also the mechanism behind `hsx2mail --compose` failing to open a
window (§ `OPERATIONS.md` §6.4) and behind clicking the launcher icon when the app is
already running minimized-to-background.

`windowHidden` (`app/app.go`) tracks whether the window is currently hidden, so handoff
can skip a redundant show.

---

## 6. Sleep, wake, lock and network — event-driven, no polling

All four monitors are D-Bus-driven on Linux and are started in `Startup`
(`app/app.go:828` and neighbours). None of them polls on a timer.

| Monitor | Source | Handler |
|---|---|---|
| Network | `platform.NewNetworkMonitor()` — `internal/platform/network_linux.go` | `app/background.go:598-641` |
| Sleep/wake | `platform.NewSleepWakeMonitor()` — `internal/platform/sleep_linux.go` | `app/background.go:663-694`, `:703-719` |
| Session lock | `platform.NewSessionLockMonitor()` | `app/background.go:682-694` — redacts notification content while locked |
| Theme | `platform.NewThemeMonitor()` — XDG Settings Portal `org.freedesktop.portal.Settings` | `app/theme.go:17` |

### On sleep

`handleSystemSleep()` (`app/background.go:722-...`) stops the IDLE manager and
invalidates/closes pooled IMAP connections, so a suspended machine does not wake holding
a dead socket.

### On wake

`handleSystemWake()`:

1. Does **not** call `Invalidate()` or `CloseAll()` again. The comment at
   `app/background.go:755-762` is explicit: sleep already did both, and repeating it
   would race portal signals that may already have arrived — `Invalidate` would reset
   `connected=false` after the portal set it true, and `CloseAll` would kill in-progress
   sync connections.
2. Waits for network connectivity, event-driven, with a **30-second timeout** so it does
   not block forever if the network never returns.
3. Calls `syncAfterWake()`.

`syncAfterWake()` is shared: it updates `LastSync`, runs a full sync, then restarts IDLE.
It is reached both from `handleSystemWake` and from `processNetworkEvents` when
connectivity is restored, and **both paths may fire on the same wake** — the function is
written to tolerate that.

### On network change

`processNetworkEvents` (`app/background.go:620-641`):

- `Connected` → log, emit `network:online` to the frontend, publish
  `system:network-online` on the Go-side event bus (extensions such as the calendar
  syncer subscribe), then `syncAfterWake()`.
- Disconnected → log, emit `network:offline`, publish `system:network-offline`.

The two directions therefore converge on the same post-reconnect sync. This is the whole
of the "pause/resume sync on network change" behaviour: sync is not explicitly paused,
connections simply fail, and the reconnect path drives recovery.

---

## 7. Sync scheduling and the boot storm

Two independent mechanisms drive mail fetching:

```
┌─────────────────────────────────┐   ┌──────────────────────────────────┐
│ IDLE manager (push)             │   │ Polling scheduler (fallback)     │
│ imap.IdleManager, per account   │   │ sync.Scheduler, per account      │
│ IDLE → new mail → SyncFolder    │   │ timer, sync_interval days        │
└─────────────────────────────────┘   └──────────────────────────────────┘
             both funnel into the same per-folder sync
```

### Boot-storm control

Without it, every account is "due" on the first tick (nothing has a `LastSync` yet), so a
boot with N accounts opens N connections at once; and every *failing* account retries
every tick, so an outage becomes an N-connection retry storm
(`internal/sync/scheduler.go:26-38`). Three constants prevent it
(`internal/sync/scheduler.go:32-48`):

| Constant | Value | Effect |
|---|---|---|
| `MaxConcurrentAccountSyncs` | `3` | A buffered channel of 3 semaphore slots. A due account that cannot get a slot waits for the next tick rather than stacking a connection. |
| `InitialJitterWindow` | `30 * time.Second` | Each account's *first* due time is drawn once, from a random offset in this window, so a boot fans out over 30 s instead of one spike. Drawn per account, tracked by the `seeded` map. |
| `BackoffBase` | `2 * time.Minute` | First failure backoff. |
| `BackoffMax` | `60 * time.Minute` | Ceiling on the exponential backoff; each further consecutive failure doubles, clamped here. |

`backoffRemaining(accountID)` is consulted **before** dispatch, so an account in failure
backoff is skipped even if it is nominally due. A failure backoff already in force takes
precedence over the jitter draw. Logs are tagged with `inFlight` so you can see the
semaphore working.

`SyncFolder` also has a **500 ms debounce**, so a burst of `SyncFolder` calls (for example
a notification click plus a manual refresh) collapses into one IMAP round trip.

Per-folder `SyncFolder` calls are debounced 500 ms; a **secondary** sync interval
(`accounts.secondary_sync_interval`, migration v41, default `0`) reconciles secondary
folders like Archive more often than INBOX while keeping unread badges accurate; `0`
derives from `sync_interval` with a 10-minute floor.

---

## 8. Notification click handling

`app/background.go:555-...` installs the click handler:

```go
a.notifier.SetClickHandler(func(data notification.NotificationData) {
    a.ShowWindow()          // raise the window
    // … then emit `extension:open` for an extension deeplink,
    //     or the mail-click path to open the conversation
})
```

This is the primary way to get a window back when the app started hidden and no tray
icon is rendered. See `OPERATIONS.md` §6.1 when notifications do not appear at all.

---

## 9. Related documents

| Topic | Owner |
|---|---|
| Startup, close semantics, autostart, tray, sleep/wake, network, scheduling | this file |
| Notification backend, keyring, autostart troubleshooting | `OPERATIONS.md` §6 |
| Sync engine, IMAP, IDLE, threading internals | `analysis/10-sync-imap-idle.md` |
| System structure | `architecture.md` |
