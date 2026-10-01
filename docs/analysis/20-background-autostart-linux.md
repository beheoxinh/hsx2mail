# 20 — Background Operation & Autostart on Linux

Analysis date: 2026-09-29 · Commit: working tree at `v0.3.2` · Read-only review.

Scope: `main.go`, `app/{app,background,window,settings,ipc,theme}.go`, `internal/platform/**`,
`internal/{notification,imap,sync,ipc}/**`, `build/**`.

---

## 1. Executive summary

| Question | Answer |
|---|---|
| Does the process stay alive when the window is closed? | **Yes**, if `run_background=true` (`app/app.go:853-861`, `app/app.go:909-915`). Otherwise it quits. |
| Does the process sync in the background? | **Yes** — polling scheduler + IMAP IDLE, both event-driven with network gating (`app/background.go:23-97`). |
| Does it autostart on login? | **Partially.** Non-Flatpak: yes via a hand-written `~/.config/autostart` `.desktop` (`internal/platform/autostart_linux.go:164-188`). Flatpak: **broken** — it writes `Exec=hsx2mail` on the host (`internal/platform/autostart_linux.go:118`). |
| Can the user get the hidden window back? | **Only** by launching the binary again (single-instance socket) or clicking a notification. No tray icon, no global shortcut, no in-app affordance. |
| Is there a systemd user unit? | **No.** No `.service`, `.socket`, or `.timer` file exists in the repo. `NotifyStartupComplete` is `gdk_notify_startup_complete()`, not `sd_notify`. |
| Is single-instance D-Bus based? | **No.** Unix socket only. `AGENTS.md` §3 claims "Unix socket + D-Bus on Linux" — the D-Bus half does not exist. |

**Verdict:** the *sync engine* is production-grade. The *daemon shell around it* — tray, reliable autostart, systemd integration, boot-storm control, restore affordance — is not built. The app can run headless-ish but cannot be operated headless-ish.

---

## 2. Current behaviour walkthrough

### 2.1 Two window-close paths, one duplicated decision

```
                    ┌──────────────────────────────────────────┐
  title-bar X  ────▶│ frontend/src/App.svelte:171 handleClose()│
                    │        └─▶ CloseWindow()                 │
                    └───────────────────┬──────────────────────┘
                                        ▼
                              app.CloseWindow  (app/app.go:904)
                                        │
                    ┌───────────────────┴──────────────────────┐
  WM close   ──────▶│ app.BeforeClose  (app/app.go:848)        │  main.go:150
  / logout   ──────▶│   OnBeforeClose (returns bool)           │  app/app.go:865
                    └───────────────────┬──────────────────────┘
                                        ▼
                    ┌───────────────────────────────────────────┐
                    │ run_background ?                          │
                    │  app/app.go:854  /  app/app.go:910       │
                    ├───────────────────────────────────────────┤
                    │  true  → WindowHide(a.ctx)               │  :858 / :912
                    │          a.windowHidden = true           │
                    │          return true  (window NOT closed)│
                    ├───────────────────────────────────────────┤
                    │  false → shuttingDown = true             │  :867 / :922
                    │          emit "app:shutting-down"         │
                    │          sleep 150ms → Quit()            │  :876 / :929
                    └───────────────────────────────────────────┘
```

Two things to note:

1. The same `run_background` branch is implemented **twice** (`app/app.go:853-861` and
   `app/app.go:909-915`) with slightly different bodies (only one sets `a.windowHidden`).
2. `BeforeClose` is registered at `main.go:150` but there is **no** `HideWindowOnClose` and
   **no** tray. Once hidden, the only way back is `ShowWindow` (`app/app.go:892-902`),
   which is called from exactly two places:
   - `App.SingleInstanceLock.SetOnShow` callback — `app/app.go:518-526`
   - notification click handler — `app/background.go:545`

### 2.2 Startup sequence (X11/Wayland, single instance, network up)

```
t=0ms    main()  main.go:45
         ├─ platform.MonitorGBMErrors()              main.go:46
         ├─ flag.Parse()                             main.go:47
         ├─ mailto: scan of flag.Args()              main.go:62-69
         └─ runMainMode()                            main.go:79
              ├─ lock.TryLock("show"|mailto)         main.go:88
              │    ├─ Listen /tmp/hsx2mail-UID/instance.sock  → WIN
              │    └─ else Dial + write + exit               → LOSE (activate existing)
              ├─ defer lock.Unlock()                  main.go:96
              ├─ settings.ReadNativeTitleBar()        main.go:101
              ├─ app.NewApp()                         main.go:105
              └─ runPreflight(application)            main.go:119
                   └─ paths.EnsureDirectories 0700
                   └─ database.Open  (WAL)
                   └─ db.Migrate  (v1..v39)
                   └─ credentials.NewStore (keyring + AES fallback)

t≈80ms   wails.Run()                                 main.go:135
         ├─ StartHidden: true                        main.go:142   ← window never mapped
         ├─ Frameless: !nativeTitleBar               main.go:141
         └─ Linux.ProgramName = io.github...Hsx2Mail main.go:157
                   (WM_CLASS / StartupWMClass match)

t≈100ms  Startup(ctx)                                app/app.go:512
         ├─ a.ctx = ctx                                          :513
         ├─ SingleInstanceLock.SetOnShow(...)  ★ MUST BE EARLY  :518
         ├─ construct ~12 stores                               :535-560
         ├─ SMIME + PGP signers/verifiers/encryptors           :566-577
         ├─ imap.NewPool(MaxConnections=3)                     :581
         ├─ sync.NewEngine                                     :599
         ├─ db.StartCheckpointRoutine (5 min)                  :620
         ├─ carddav syncer + scheduler                         :623-625
         ├─ extension bridges + Core instances                 :665-673
         ├─ initIPC  → /tmp/hsx2mail-UID/ipc.sock              :737
         ├─ initNetworkMonitor (portal → NM → "none")          :742
         ├─ initBackgroundSync                                 :745
         │    ├─ sync.NewScheduler  (1 min ticker)   background.go:27
         │    ├─ SetNewMailCallback / SetSyncCompletedCallback  :30 / :35
         │    ├─ SetConnectivityCheck(network.IsConnected)     :60
         │    ├─ syncScheduler.Start(ctx)                       :64
         │    ├─ imap.NewIdleManager(backoff 1s→5m, max 10)    :69
         │    ├─ idleManager.SetConnectivityCheck(...)          :73
         │    ├─ idleManager.Start(ctx)                         :76
         │    └─ go processIdleEvents(ctx)                      :94
         ├─ go syncAllPendingDrafts()                          :748
         ├─ FTS indexer (+5 s delayed background pass)         :751-826
         ├─ a.autostartMgr = NewAutostartManager()             :829
         ├─ a.ready = true; emit "app:ready"  ★ TOO EARLY      :778-779
         ├─ initNotifications (portal or direct D-Bus)          :782
         ├─ initSleepWakeMonitor (system D-Bus login1)          :785
         └─ initThemeMonitor (portal)                           :788

t≈?      frontend mounts (App.svelte)
         ├─ main.ts called WindowShow() at module load  → splash visible
         ├─ EventsOn("app:ready")                          App.svelte:504
         ├─ shouldStartHidden = GetStartHiddenActive()     app.go:953
         │    start_hidden && run_background ?
         ├─   true  → WindowHide()                          App.svelte:506
         └─   false → WindowShow()                          App.svelte:509
```

### 2.3 Background steady state

```
                     ┌──────────────────────────────────────┐
                     │      app/background.go               │
                     │                                      │
   ┌─────────────────┤  scheduler 1-min ticker               │
   │ every 60s       │  syncDueAccounts()  scheduler.go:152  │
   │                 │    offline?           scheduler.go:154  │  ← network gate
   │                 │    acc.Enabled?       scheduler.go:166  │
   │                 │    SyncInterval > 0?  scheduler.go:171  │
   │                 │    isSyncDue?         scheduler.go:200  │  ← LastSync==nil ⇒ true
   │                 │    30-min ctx timeout  scheduler.go:225  │
   │                 │    per-account mutex   scheduler.go:214  │
   │                 └──────────┬───────────────────────────┘
   │                            ▼
   │                 ┌──────────────────────────────────────┐
   │                 │  IMAP IDLE  (INBOX only)              │
   │                 │  IdleTimeout 10 min   idle.go:44      │
   │                 │  backoff 1s→5m, max 10 tries          │
   │                 │    idle.go:46-48, 188-191             │
   │                 │  EXISTS / FETCH / EXPUNGE             │
   │                 │    → events chan (cap 10, drop)       │
   │                 │  port 143/tls per account             │
   │                 └──────────┬───────────────────────────┘
   │                            ▼
   │                 ┌──────────────────────────────────────┐
   │                 │  processIdleEvents  background.go:100 │
   │                 │   EventNewMail  → debounce → handle…   │
   │                 │   EventFlagsChanged → 1s debounce     │
   │                 │   EventExpunge → 1s debounce          │
   │                 └──────────┬───────────────────────────┘
   │                            ▼
   │                 ┌──────────────────────────────────────┐
   │                 │  handleNewMailNotification :460       │
   │                 │  → notifier.Show(portal|D-Bus) :514   │
   │                 │    no actions, only "default"         │
   │                 └──────────────────────────────────────┘
   │
   ├─ D-Bus sleep/wake   org.freedesktop.login1 PrepareForSleep
   │                     sleep_linux.go:49
   │                       → idleManager.Stop() + pool.CloseAll()  :697-704
   │                       → networkMonitor.Invalidate()           :708
   │                     wake
   │                       → WaitForConnection(30 s timeout)       :737-740
   │                       → syncAfterWake()  (2-min cooldown)     :781
   │                       → SyncAllComplete() → restartIDLE()     :846-853
   │
   └─ D-Bus network      org.freedesktop.portal.NetworkMonitor
                         or org.freedesktop.NetworkManager
                         offline → idleManager.Stop() + pool.CloseAll()  :631-636
                         online  → pool.CloseAll() + syncAfterWake()      :620-627
```

### 2.4 What "hidden" actually means

`WindowHide` on a Wails/GTK window unmaps the toplevel. The GTK main loop, all Go
goroutines, all D-Bus listeners, and the WebKit process keep running. Consequences:

- Sync, IDLE, notifications, CardDAV, FTS all keep working. **This part is correct.**
- The frontend's Svelte tree, virtual list, and WebKit renderer stay resident.
  There is no teardown, so a backgrounded instance holds the full memory footprint.
- The `wailsjs` IPC bridge stays live, so `EventsEmit` calls from the background
  sync land in a mounted frontend that nobody is looking at. Harmless, but it is
  why the code comments warn about IPC saturation (`app/app.go:840-842`).
- **No visible surface exists.** `grep -rn "systray\|getSystemTray" --include=*.go`
  returns only a comment at `app/app.go:934` ("future tray menu"). `go.mod` has no
  tray dependency. The frontend has no tray code.

---

## 3. Autostart matrix

### 3.1 Mechanisms

| # | Mechanism | Implemented? | Code |
|---|---|---|---|
| M1 | XDG autostart `.desktop` in `~/.config/autostart` | **Yes** (non-Flatpak) | `internal/platform/autostart_linux.go:164-216` |
| M2 | XDG **Background portal** | **Yes** (Flatpak only) | `internal/platform/autostart_linux.go:70-161` |
| M3 | `systemd --user` service (`Type=notify`) | **No** | no `.service` file in repo |
| M4 | GNOME "Startup Applications" | Alias of M1 (GNOME reads `~/.config/autostart`) | — |
| M5 | KDE autostart | Alias of M1 (KDE reads `~/.config/autostart`) | — |
| M6 | D-Bus activation (`DBusActivatable=true`) | **No** | `build/linux/hsx2mail.desktop` has no such key |
| M7 | Flatpak `xdg-autostart` permission | **No** (portal used instead) | — |

Branch selection is a single env test, `internal/platform/autostart_linux.go:38`:
`isFlatpak: os.Getenv("FLATPAK_ID") != ""`.

### 3.2 Mechanism × desktop environment

| Environment | Non-Flatpak (M1) | Flatpak (M2) | systemd user unit (M3) |
|---|---|---|---|
| **GNOME 4x/5x, Wayland** | ✅ works | ⚠️ writes host `Exec=hsx2mail` → **fails on Flatpak-only hosts** (F-02). Portal shows a confirmation dialog every enable. | ❌ absent |
| **GNOME, X11** | ✅ works | ⚠️ same as above | ❌ absent |
| **KDE Plasma 6, Wayland** | ✅ works | ⚠️ same F-02; `plasma-xdg-desktop-portal` supports Background but the commandline override still breaks it | ❌ absent |
| **KDE Plasma, X11** | ✅ works | ⚠️ same F-02 | ❌ absent |
| **Hyprland / Sway (wlr portal)** | ✅ works (M1 is FS-only) | ❌ `wlr/hyprland` portal does **not** implement `org.freedesktop.portal.Background` → `RequestBackground` errors (`autostart_linux.go:123`) | ❌ absent |
| **Xfce / MATE / LXQt** | ✅ works (their autostart agent reads the same dir) | ⚠️ depends on `xdg-desktop-portal-gtk`; Background unsupported on many builds | ❌ absent |
| **Headless / TTY / no session bus** | ⚠️ `SessionBus()` fails → `Enable()` errors; `IsConnected()` stays `true` (`network_linux.go:57-61`) | ❌ | ❌ absent |
| **NixOS / Guix / read-only `/usr`** | ⚠️ `Exec` is `os.Executable()` → `/nix/store/…` hash path; the autostart entry breaks on every rebuild (F-05) | n/a | ❌ absent |
| **AppImage** | ⚠️ same F-05 — the `.desktop` pins a hash-suffixed `/tmp/.mount_XXXX/hsx2mail` path | n/a | ❌ absent |

### 3.3 Autostart payload — what actually gets written

M1 (`internal/platform/autostart_linux.go:16-25`, `177`, `232-238`):

```ini
[Desktop Entry]
Type=Application
Name=Email Hub
Comment=Email Hub Email Client
Exec=/home/u/.local/bin/hsx2mail          # os.Executable(), UNQUOTED
Icon=io.github.beheoxinh.Hsx2Mail
Terminal=false
Categories=Network;Email;
X-GNOME-Autostart-enabled=true
```

Written to `~/.config/autostart/io.github.beheoxinh.Hsx2Mail.desktop`, mode `0644`.

Missing and consequential:

- **No `--hidden` / `--start-hidden` argument.** Autostart boots the app; the window
  is only hidden if the *user separately* enabled both `run_background` **and**
  `start_hidden` (`app/app.go:953-960`). F-06.
- **No `X-GNOME-Autostart-Delay`.** Every login fires an immediate IMAP connect for
  every account, colliding with the rest of the autostart herd. F-23.
- **Unquoted `Exec`.** A path containing a space silently produces an invalid entry.
- **No `X-GNOME-Autostart-Notification=false`** — a toast pops at every login for no reason.
- **No `DBusActivatable=true`** — see F-20.

M2 (`internal/platform/autostart_linux.go:110-119`):

```go
options := map[string]dbus.Variant{
    "handle_token": dbus.MakeVariant(handleToken),
    "reason":       dbus.MakeVariant("Start automatically on login and sync email in the background"),
    "autostart":    dbus.MakeVariant(autostart),
    "commandline":  dbus.MakeVariant([]string{"hsx2mail"}),   // ← F-02
}
```

The in-tree comment at `autostart_linux.go:110-113` states the intent: *"Pass commandline
explicitly to avoid broken escaping from the portal's auto-generated flatpak run command."*
The escape is fixed; the launch is broken. `xdg-desktop-portal`'s `Background` backend
writes the `.desktop` **on the host**, outside the sandbox. `Exec=hsx2mail` there only
resolves if the user also has a host-native build. For a Flatpak-only user the autostart
entry exists, is listed by GNOME's Startup Applications panel, and fails on login.

---

## 4. Boot-sequence timeline (autostart + offline / flaky network)

The worst realistic case: fresh login, autostart fires, WiFi has not associated yet.

```
t=0.0s   session start, ~/.config/autostart/*.desktop executed
t=0.1s   main.go:88  TryLock  → WIN
t=0.1s   runPreflight: sqlite open + 39 migrations         (WAL, main DB)
t=0.3s   Startup: stores, pool (MaxConnections=3/account), engine
t=0.4s   initNetworkMonitor
           ├─ portal NameHasOwner(portal) → false at t=0.4s (portal not yet activated)
           ├─ system-bus NetworkManager  → not present (GNOME uses NetworkManager? yes)
           └─ method="none", m.connected stays TRUE   network_linux.go:57-61
t=0.5s   initBackgroundSync
           ├─ scheduler.Start  → 1st tick at t=60.0s
           └─ idleManager.StartAccount × N accounts
                 each opens TLS to imap host:993  → fails (ENETUNREACH)
                 backoff 1s → 2s → 4s … capped 5m, MaxReconnectAttempts=10  idle.go:162-208
t=0.5s   a.ready = true; emit "app:ready"                  app.go:778-779
t=0.5s   initNotifications   → portal/dbus session bus    app.go:782
t=0.5s   initSleepWakeMonitor → system D-Bus login1        app.go:785
t=0.5s   initThemeMonitor     → portal (may block)        app.go:788
t=0.6s   frontend mounts, WindowHide() (if start_hidden)  App.svelte:506

t=1s     IDLE attempt 2 …  attempt 10  ⇒  GIVE UP, goroutine returns   idle.go:188-191
         (1+2+4+8+16+32+64+128+256 ≈ 8.5 min of retrying, then permanently dead)
         while network is still down. Nothing revives it except wake / network event.

t=60s    scheduler tick 1 → syncDueAccounts
         isSyncDue: inbox.LastSync == nil → TRUE      scheduler.go:200-202
         SyncFolders → TLS fail → return              scheduler.go:245-252
         LastSync still nil  ⇒  due again next tick
t=120s   scheduler tick 2 → same
         …
t=+60s   ┌─ OAuth accounts additionally hit the token endpoint every tick:
         │    getValidOAuthToken: IsExpiringSoon(5m) → RefreshToken → DNS fail
         │    → emit "oauth:reauth-required"           compose.go:86-90
         │    → modal re-authorization dialog per account, every 60 s
         └─ this is 1 IMAP connect + 1 HTTPS refresh per account per minute,
            indefinitely, until the network returns.

t=+8min   IDLE has exhausted its 10 attempts. Push is gone.
          Polling (60 s) is the only thing left, forever.

t=+30s   WiFi associates. Portal emits NetworkMonitor.changed(available=true)
         updateState(connected=true)                     network_linux.go:221-253
         processNetworkEvents → online branch            background.go:620-627
           → imapPool.CloseAll()
           → syncAfterWake()  (2-min cooldown: LastSync==nil ⇒ not recent ⇒ RUNS)
           → SyncAllComplete()
           → restartIDLE(): idleManager.Start + StartAccount × N
t=+31s   IDLE reconnects. Application is caught up.
```

Observations that fall out of this timeline:

- **No boot grace period.** The scheduler's first tick is at t+60 s, but IDLE starts
  connecting at t+0.5 s. There is no "wait for network before the first connect" gate,
  and no boot-time connection stagger across accounts.
- **The retry budget is spent during the offline window, not after it.** The 10-attempt
  cap at `idle.go:188-191` is exhausted by exactly the situation that needs it most, and
  nothing re-arms it. The recovery only comes from the network monitor (F-10).
- **Clock monotonicity makes the cooldown lie.** `time.Since` / `time.Ticker` use
  `CLOCK_MONOTONIC`, which **stops** during suspend on Linux. An 8-hour suspend followed
  by wake is seen by the scheduler as "seconds elapsed". Without `handleSystemWake`'s
  explicit `syncAfterWake`, the app would conclude nothing was missed. The wake path
  saves it — but only if the logind signal was actually delivered.
- **No jitter anywhere.** `MaxReconnectAttempts: 10` with a fixed 1 s→5 min backoff means
  N accounts on the same provider reconnect in lockstep, and the scheduler's 60 s ticker
  is a phase-locked hammer against one server (F-30).

---

## 5. Single-instance mechanism — `internal/platform/singleinstance_linux.go`

No D-Bus. `AGENTS.md` §3 ("Unix socket + D-Bus on Linux") and §8 (D-Bus monitors) are
partly wrong: there is a Unix socket for the lock, D-Bus for sleep/wake and network, but
no D-Bus for activation.

```
main.go:86   lock := platform.NewSingleInstanceLock()
main.go:88   locked, err := lock.TryLock(activateMsg)     activateMsg ∈ {"show", "mailto:…"}
main.go:92   if !locked { return }                        ← second process exits, code 0
main.go:96   defer lock.Unlock()
main.go:106  application.SingleInstanceLock = lock
app.go:518   a.SingleInstanceLock.SetOnShow(func(data){…}) ← registered inside Startup
```

### 5.1 TryLock control flow (`singleinstance_linux.go:35-78`)

```
socketPath = /tmp/hsx2mail-<uid>/instance.sock            :148-158 (MkdirAll 0700)
   │
   ├─ line 45  net.Listen("unix", socketPath)
   │     └─ ok  → line 48-52  first instance, go acceptLoop(), return (true, nil)
   │
   ├─ line 55  net.DialTimeout("unix", socketPath, 2s)
   │     └─ ok  → line 58  write activateMsg + "\n"; conn.Close(); return (false, nil)
   │
   │   ┌─ assumed STALE — line 65-66  os.Remove(socketPath)
   │   │
   │   └─ line 68  net.Listen again
   │         └─ ok  → return (true, nil)
   │         └─ err → return (true, error)   ← caller prints a warning and CONTINUES
   │                                              (main.go:89-91: locked stays true)
```

### 5.2 Findings

**(a) TOCTOU → two live primary instances.** `singleinstance_linux.go:64-71`.

```
   t0   P1: Listen(/…/instance.sock)   → EADDRINUSE        (P0 already owns it)
   t0   P2: Listen(/…/instance.sock)   → EADDRINUSE
   t1   P1: Dial → ECONNREFUSED        (P0's accept queue is momentarily full, or
   t1   P2: Dial → ECONNREFUSED         P0 is under heavy load and did not accept in 2 s)
   t2   P1: os.Remove(socketPath)      ← removes P0's LIVE socket
   t2   P2: os.Remove(socketPath)      ← ENOENT, ignored
   t3   P1: Listen(socketPath)         → SUCCESS  (creates a new inode)
   t3   P2: Listen(socketPath)         → EADDRINUSE
   t4   P2: Dial → connects to P1, not P0
   t4   P1: go acceptLoop()  → P1 now ALSO holds a bound socket; P0's socket is
                              unlinked but still accepting on a live inode
   ⇒  P0 and P1 both run the full app: two Wails windows, two sqlite writers on the
      same DB, two schedulers, two IDLE sets, two notification servers. P2 exits
      "successfully" having activated the wrong process.
```

Probability is low (requires the 2 s `DialTimeout` on a unix socket to elapse), but the
consequence is silent DB corruption, and the code path is unconditional. The
`EADDRINUSE`-is-always-stale assumption is the bug: a live-but-unreachable instance is
indistinguishable from a stale file.

Minimal correct fix: on `Listen` failure, `Dial` with the `SO_PEERCRED`-verified pid, and
only `Remove` if the peer is gone; or write the owner pid into a `flock`ed
`/tmp/hsx2mail-<uid>/instance.lock` and use `flock(LOCK_EX|LOCK_NB)` — the kernel
releases the lock on process death, so stale files become impossible and the TOCTOU
window closes.

**(b) Activation message is silently dropped during the Startup window.**
`singleinstance_linux.go:136-142` + `app/app.go:513-526`.

```go
l.mu.Lock()
fn := l.onShow
l.mu.Unlock()
if fn == nil {
    return                       // ← no queue, no retry, no log
}
```

`TryLock` succeeds at `main.go:88` and `acceptLoop` starts immediately. `SetOnShow` is not
registered until `Startup` reaches `app/app.go:518`, which is after `wails.Run` has
created the OS window. In that interval any `hsx2mail` / `.desktop` / `xdg-open mailto:`
invocation connects, is accepted, and is thrown away. The user's click on the launcher
during startup does nothing at all — no window raise, no mailto.

On a slow Wayland start with a cold FTS pass this window is hundreds of milliseconds to
seconds. Fix: buffer the command in a `pendingCmd` field inside the mutex and flush it
from `SetOnShow` when a handler is installed.

**(c) `ShowWindow` can be called with a nil `a.ctx`.** `app/app.go:896-897`.
`a.ctx` is assigned at `app.go:513`, before `SetOnShow` at `:518`, so the ordering is
currently safe — but only by accident of statement order, with no guard. A
`if a.ctx == nil { … }` early return (or a small `sync.Once` around context publication)
is worth 3 lines.

**(d) `shuttingDown` is an unsynchronised package-level global.**
`app/app.go:509`, read/written at `:849`, `:867`, `:918`, `:936`, `:964`.
`BeforeClose` (GTK main thread), `CloseWindow` (Wails IPC goroutine), `QuitApp`
(IPC goroutine) and `InitiateShutdown` (IPC goroutine) all touch it. `bool` reads/writes
are word-atomic on amd64 so you will not tear, but the check-then-act
(`if shuttingDown { return }` … `shuttingDown = true`) is not atomic, so two quits can
both pass the guard and both schedule a `Quit` (`app/app.go:876` and `:929`) plus two
shutdown overlays. Replace with `sync.Once` + a `chan struct{}`.

**(e) Socket in `/tmp`, not `$XDG_RUNTIME_DIR`.**
`singleinstance_linux.go:150-152` and `ipc/server_unix.go:71-73`.
`os.TempDir()` honours `$TMPDIR`, which is unset on most desktops, so both sockets land
in `/tmp/hsx2mail-<uid>/`. This directory is never removed on logout — only the
`instance.sock` file is removed, by `Unlock` (`singleinstance_linux.go:92-94`). It is a
`0700` dir in a world-writable parent, so the usual `/tmp` symlink attacks do not apply,
but the correct home for a per-session socket is `$XDG_RUNTIME_DIR` (mode `0700`,
`tmpfiles.d`-managed, wiped at logout). Practical cost: a stale 0700 directory can
outlive the user and can be owned by a *different* uid if the machine recycles uids —
`MkdirAll` succeeds, then `Listen` succeeds, and two users share one socket path.

**(f) No `sync.Once` on `Unlock`.** `singleinstance_linux.go:87-95` calls
`close(l.done)` unconditionally; a second call panics. Currently unreachable (one
`defer` at `main.go:96`), but it is a trap for any future early-return path.

**(g) Composer children are never reaped.** `app/ipc.go:243`.

```go
if err := cmd.Start(); err != nil { return nil }   // no cmd.Wait(), no Process.Release()
...
log.Info().Int("pid", cmd.Process.Pid).Msg("Composer window spawned")   // :255
```

Go does not reap children unless `Wait` is called. `grep -rn "cmd.Wait\|Process.Wait\|Process.Release" app/*.go`
returns nothing. Every detached composer window that closes leaves a **zombie
`hsx2mail --compose …` process** reaped only when the main process exits. A user who
opens and closes 20 compose windows accumulates 20 zombies. Fix (3 lines):

```go
go func() { _ = cmd.Wait() }()
```

**(h) Second instance exits 0 and reports nothing.** `main.go:92-95`. A `.desktop` launch
that merely raises an existing window is indistinguishable from a real launch in any
wrapper script or `Exec=`-retry logic. Minor.

---

## 6. Background-scheduler findings

**(i) No failure backoff in the scheduler.** `internal/sync/scheduler.go`.

`isSyncDue` returns `true` when `inbox.LastSync == nil` (`scheduler.go:200-202`), and a
failed sync never sets `LastSync` (`scheduler.go:245-252`, `:269-284`). The ticker is
1 minute (`scheduler.go:67`, `:138`). Therefore a permanently failing account is retried
**every 60 seconds, forever**, with no exponential backoff, no error counter, and no
circuit breaker. The IDLE path *does* have a correct backoff (`idle.go:162-208`) — the
polling path does not. This is the mechanism behind the boot storm in §4.

Fix: per-account `failCount`; on error, set the next attempt to
`min(syncInterval, 2^failCount * 60s)` capped at 30 min, and reset on success.

**(j) OAuth token-refresh storm and spurious re-auth modals.** `app/compose.go:69-93`.

```go
if !tokens.IsExpiringSoon(5*time.Minute) { return tokens, nil }   // :69
newTokenResp, err := ops.refreshOAuthToken(accountID, tokens)     // :79
if err != nil {
    wailsRuntime.EventsEmit(ctx, "oauth:reauth-required", …)     // :86
    return nil, fmt.Errorf(…)
}
```

With no network, every IMAP connection attempt calls `getIMAPCredentials`
(`app/compose.go:142+`) → `getValidOAuthToken` → an HTTPS refresh to the IdP token
endpoint that fails. The failure is reported to the UI as **"re-authorize your account"**,
which is a lie — the network is down. On an autostart background instance that means a
modal dialog per account every 60 s, invisible until the user opens the window and then
stacked. Fix: classify the error (`net.Error` / DNS) and do not emit `oauth:reauth-required`
for transport failures; and rate-limit the event per account.

**(k) `network.IsConnected()` optimistically `true` and models the wrong thing.**
`internal/platform/network_linux.go:35` (`connected: true, // assume connected until proven
otherwise`) and `:93` (`m.connected = available`).

`org.freedesktop.portal.NetworkMonitor` reports **metered/unmetered**, not internet
reachability — on a WiFi hotspot with a captive portal it reports `true`. And when neither
portal nor NetworkManager exists, `method = "none"` and the state stays `true` forever
(`network_linux.go:57-61`). Every "skip when offline" guard in the scheduler
(`scheduler.go:154`) and IDLE (`idle.go:178-180`) is therefore a no-op in exactly the
environment where it is most needed.

**(l) IDLE dies permanently after 10 attempts.**
`internal/imap/idle.go:188-191`: on `attempts >= MaxReconnectAttempts` the run loop
`return`s. `IdleManager.connections[accountID]` still holds a `*IdleConnection` with
`running == false`; `StartAccount` (`:539-556`) does detect that and re-creates it, but
nothing *calls* `StartAccount` except `restartIDLE` (`app/background.go:877`), which is
only reached from `handleSystemWake` and the network-online branch. A user who
unplugs the ethernet cable for 10 minutes and plugs it back into a system where the
portal emits no signal loses push for the rest of the session.

**(m) No suspend-duration threshold.** `app/background.go:681-685` calls
`handleSystemSleep`/`handleSystemWake` for *every* `PrepareForSleep` transition. A lid
close for 3 seconds tears down all IDLE connections and the whole IMAP pool
(`background.go:697-704`), then waits up to 30 s for a network signal (`:737-740`) and
re-syncs everything. On a laptop that is a steady 5–10 s of no mail whenever the user
glances at the screen. Gate on `event.Timestamp` duration (e.g. ignore sleeps < 60 s)
and skip the full sync, or just re-`SELECT`.

**(n) Sleep/wake monitor has no connection recovery.**
`internal/platform/sleep_linux.go:30-65` connects once to the system bus and
`:68-116` reads signals forever. If that connection drops (logind restart, bus
reconnect), the goroutine silently sits in `select` forever and sleep/wake handling is
gone for the session, with no error surfaced to the user. Add a liveness ping /
reconnect loop, and at minimum log loudly on `signals` channel close.

**(o) No screensaver / session-lock awareness.** No reference to
`org.gnome.ScreenSaver` or `org.freedesktop.login1` `LockSignal` anywhere in the tree.
While the screen is locked the app keeps syncing, keeps fetching bodies, and keeps
posting notifications whose body is the message subject and whose summary is the sender
(`app/background.go:498-510`). Any notification surface that renders on the lock screen
leaks sender + subject. Subscribe to `org.gnome.ScreenSaver.ActiveChanged` (or
`login1.LockSignal`) and suppress bodies while locked.

**(p) Notifications carry no inline actions.** `internal/notification/notifier_linux.go:210-217`
builds a portal vardict with only `title`, `body`, `priority`, `default-action` — no
`actions` key. `handlePortalAction` (`:349-384`) therefore only ever sees `"default"`.
The `notifyIDs` map (`:285`, `:411`) is registered and cleaned up but can never be
hit by anything other than the default action. "Archive" / "Mark read" on the
notification bubble — the whole point of a background agent — is not implemented.

---

## 7. Packaging & distribution findings

| What | Where | Note |
|---|---|---|
| `systemd` user unit | **absent** | No `.service`/`.socket`/`.timer` in repo. `main.go:155-158` sets only `WebviewGpuPolicy` + `ProgramName`. |
| `sd_notify` readiness | **absent** | `platform.NotifyStartupComplete` → `gdk_notify_startup_complete()` (`internal/platform/startup_linux.go:24`). Works with `StartupNotify=true` (`hsx2mail.desktop:41`) but cannot be used by systemd. |
| Flatpak finish-args | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:10-21` | `--share=network`, `--share=ipc`, `--socket=wayland`, `--socket=fallback-x11`, `--device=dri`, `--system-talk-name=org.freedesktop.login1`. No `--talk-name=org.freedesktop.Notifications` (needed for the `--dbus-notify` direct path, though `--share=ipc` usually covers it). No `--talk-name=org.freedesktop.portal.Desktop` (covered by `--share=ipc`). |
| `X-Flatpak` in .desktop | **absent** | Manifest installs `build/linux/hsx2mail.desktop` verbatim at `…flathub/…yml:56`. GNOME prefers `X-Flatpak` to decide activation. The dev manifest rewrites `Exec` (`…-dev.yml:30`) but the flathub one does not, and neither adds `X-Flatpak=io.github.beheoxinh.Hsx2Mail`. |
| `DBusActivatable` | **absent** | `build/linux/hsx2mail.desktop:36-44`. Every launcher click forks a Go process, opens the DB, then connects a socket and exits. Works, but ~200 ms of wasted process and one more race window (F-03). |
| Validation in CI | **absent** | `desktop-file-validate` and `appstream-util validate` appear only in `build/flatpak/README.md:85,91`. `make lint` (`Makefile:125-135`) runs `golangci-lint` + ESLint only. There is no `.github/workflows/` directory. |
| `autostart` is per-user | `internal/platform/autostart_linux.go:220-230` | Correctly reads `XDG_CONFIG_HOME` then `os.UserHomeDir()`. |

---

## 8. Findings table

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| F-01 | **P0** | `app/app.go:858`, `app/app.go:912`, `app/app.go:892-902`; no systray dep in `go.mod` | Background mode hides the window with no tray icon, no global shortcut, no in-app "show window" affordance. Only recovery = re-run the binary or click a notification. | Background mode is unusable as a *mode*. The user is stuck if they close the window and no notification arrives. | Add a `getlantern/systray` (or `fyne.io/systray`) tray icon with Open / Sync now / Check for new mail / Preferences / Quit. Wails v2 has no tray API, so it must be a separate goroutine/main-thread integration. Alternatively ship a `systemd --user` unit and treat the tray as a separate D-Bus client. |
| F-02 | **P0** | `internal/platform/autostart_linux.go:118` (and comment `:110-113`) | Flatpak autostart passes `"commandline": ["hsx2mail"]` to `org.freedesktop.portal.Background`, so the portal writes a host-side `Exec=hsx2mail` that does not resolve for Flatpak-only users. | Autostart silently does nothing on the primary distribution channel. Entry appears in GNOME Startup Applications and fails on login. | Drop the `commandline` key entirely and let the portal synthesise `flatpak run --command=… io.github.beheoxinh.Hsx2Mail`; if the escaping bug that motivated the workaround still exists, set `commandline: ["flatpak", "run", "io.github.beheoxinh.Hsx2Mail"]` explicitly. |
| F-03 | **P0** | `internal/platform/singleinstance_linux.go:64-71` | `EADDRINUSE` is treated as proof of a stale socket; a live-but-slow instance is unlinked and a second process binds a fresh socket. | Two full app instances: two windows, two SQLite writers, two schedulers, two IDLE sets. Silent DB corruption. | Use `flock(LOCK_EX\|LOCK_NB)` on a pid file, or verify liveness with `Dial` + SO_PEERCRED and only `Remove` when the peer is gone. Never `Remove` unconditionally after a failed `Dial`. |
| F-04 | **P1** | `internal/platform/singleinstance_linux.go:136-142` + `app/app.go:518` | Activation message arriving before `SetOnShow` is registered is discarded with no log, no queue, no retry. | Launcher click / `xdg-open mailto:` during startup silently does nothing. Window is the hundreds of ms between `TryLock` and `Startup:518`. | Buffer the pending command under the existing mutex and flush it from `SetOnShow`. |
| F-05 | **P1** | `internal/platform/autostart_linux.go:20`, `:232-238` | `Exec` is `os.Executable()`, unquoted. | Breaks on paths with spaces; pins the entry to a build-specific path (Nix store hash, AppImage mount point) so autostart dies on the next upgrade. | Emit `Exec=<quoted-abs-path> --start-hidden`, and re-verify the entry on startup when `os.Executable()` has changed. |
| F-06 | **P1** | `app/settings.go:222-237`; `frontend/src/lib/components/settings/GeneralTab.svelte:466-475` | The `autostart` toggle is independent of `run_background` + `start_hidden`. Enabling autostart alone writes a `.desktop` that boots a **visible** window. | The most obvious user action ("Autostart on login") produces a window at every login, which is exactly what the other two toggles exist to prevent. | Have `SetAutostart(true)` also set `run_background=true` and `start_hidden=true`; or add `--start-hidden` to the generated `Exec` and honour it in `GetStartHiddenActive`. |
| F-07 | **P1** | `app/settings.go:225-232` + `internal/platform/autostart_linux.go:157-158` | The DB flag is written *before* the OS call, and the `current == enabled` short-circuit means a failed enable is never retried. | User sees "Autostart: on" with nothing installed. Only fixable by toggling off/on, and a second failure is equally silent. | Write the DB flag only after `Enable()`/`Disable()` succeeds; roll back on error; surface the error to the frontend toast. |
| F-08 | **P1** | `internal/sync/scheduler.go:67`, `:138`, `:200-202`, `:245-252` | 1-minute ticker + `LastSync == nil ⇒ due` + no error backoff. | A failing or offline account is retried every 60 s indefinitely — 1 IMAP connect + 1 OAuth refresh per account per minute, forever. Hammer against the provider. | Per-account failure counter with exponential backoff (cap 30 min), reset on success. Do not consume the retry budget for transport errors. |
| F-09 | **P1** | `app/compose.go:69-93` | Token-refresh failure is always reported as `oauth:reauth-required` to the frontend. | Offline autostart boot spams a re-authorisation modal per account every 60 s. Users are told to re-auth when the network is simply down. | Distinguish `net.Error`/DNS/timeout from HTTP 4xx. Only emit the re-auth event for auth-class failures; rate-limit per account. |
| F-10 | **P1** | `internal/imap/idle.go:188-191`, `:539-556`; `app/background.go:877` | After 10 failed attempts the IDLE goroutine returns and nothing re-arms it except a wake or network event. | A >10 min network outage permanently disables push for the session. Mail arrives only via the 60 s poll. | Add a slow re-arm timer (e.g. every 15 min) in `IdleConnection` after the give-up path, or have the scheduler call `RestartAccount` on a successful sync. |
| F-11 | **P1** | `app/ipc.go:243` | `cmd.Start()` with no `cmd.Wait()`. | One zombie `hsx2mail --compose` process per closed composer window, reaped only when the main process exits. | `go func() { _ = cmd.Wait() }()` after `Start()`. |
| F-12 | **P1** | `app/app.go:778-788` | `app:ready` is emitted *before* `initNotifications`, `initSleepWakeMonitor`, `initThemeMonitor`. | For a start-hidden instance the frontend "boots" with no notification listener; a new-mail event in that window is lost, and a click cannot raise the window. | Emit `app:ready` after the D-Bus inits, or make the three inits fire-and-forget goroutines with their own completion events. |
| F-13 | P2 | (no code) | No screensaver / session-lock awareness. | Notification body = message subject, summary = sender (`app/background.go:498-510`). Leaks sender + subject on the lock screen. | Subscribe to `org.gnome.ScreenSaver.ActiveChanged` (and `login1.LockSignal`), suppress notification bodies while locked. |
| F-14 | P2 | (no file) | No `systemd --user` unit. `NotifyStartupComplete` is `gdk_notify_startup_complete()` (`internal/platform/startup_linux.go:24`), not `sd_notify`. | No `Type=notify`, no `Restart=on-failure`, no `WantedBy=graphical-session.target`, no supervision of the background agent. If the process dies at 3 a.m. nothing restarts it. | Ship `hsx2mail.service` with `Type=notify`, `ExecStart=/path/hsx2mail --background`, `Restart=on-failure`, `WantedBy=default.target`; add `sd_notify(READY=1)` in `NotifyStartupComplete` when `$NOTIFY_SOCKET` is set. |
| F-15 | P2 | `internal/platform/singleinstance_linux.go:150-152`; `internal/ipc/server_unix.go:71-73` | Sockets in `os.TempDir()` (`/tmp`), not `$XDG_RUNTIME_DIR`. | Directory outlives the session; uid recycling can hand the same path to two users; `$TMPDIR` divergence between the launcher and an autostarted instance breaks activation. | `os.Getenv("XDG_RUNTIME_DIR")` with a `/tmp` fallback. |
| F-16 | P2 | `internal/platform/sleep_linux.go:30-65`, `:68-116` | System-bus connection is made once; no reconnect, no liveness check. | A dropped system-bus conn silently disables all sleep/wake handling for the session. | Reconnect loop with backoff; log on signal-channel close. |
| F-17 | P2 | `app/background.go:681-685`, `:692-718` | Every `PrepareForSleep` tears down the IMAP pool regardless of duration. | A 3-second lid close costs 5–10 s of no mail plus a full re-sync. | Ignore sleeps shorter than ~60 s using `event.Timestamp`; skip the full sync for short sleeps. |
| F-18 | P2 | `internal/notification/notifier_linux.go:210-217`, `:349-384` | Portal vardict has no `actions` array; only `default` is ever handled. | No "Archive" / "Mark read" on the notification bubble — the core value of a background agent. | Add `actions: ["default","archive","mark-read"]` and handle the extra keys in `handlePortalAction`. |
| F-19 | P2 | `internal/platform/network_linux.go:35`, `:57-61`, `:93` | `connected` starts `true`; the portal reports *metered*, not reachability; `method="none"` keeps it `true` forever. | Every "skip when offline" guard (`scheduler.go:154`, `idle.go:178`) is a no-op in the environments that need it most. | Probe reachability once at startup and on each `connected=true` transition (cheap TCP dial to the IMAP host), and start pessimistic when `method=="none"`. |
| F-20 | P2 | `build/linux/hsx2mail.desktop:36-44` | No `DBusActivatable=true`, no `org.gtk.Application` bus name. | Every launcher click forks a Go process, opens SQLite, connects a socket, and exits. Slow, and one more window for the F-03 race. | Register an `org.gtk.Application` bus name (via a small `godbus` service) and set `DBusActivatable=true`; also `SingleMainWindow=true`. |
| F-21 | P2 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:56` | Flatpak .desktop installed without `X-Flatpak=app-id`; `flathub` manifest does not rewrite `Exec` (only the dev manifest does, `…-dev.yml:30`). | GNOME cannot reliably route the launcher through `flatpak run`. | `desktop-file-edit --set-key=X-Flatpak --set-value=io.github.beheoxinh.Hsx2Mail` and `--set-key=Exec --set-value=hsx2mail %U` in the flathub manifest. |
| F-22 | P2 | `Makefile:120-135`; `build/flatpak/README.md:85,91` | `desktop-file-validate` and `appstream-util validate` are documented but not wired into `make lint`; there is no CI at all (no `.github/workflows/`). | The `.desktop` and metainfo rot silently. F-02/F-21/F-23 would have been caught by the validator. | Add a `validate-desktop` target and a GitHub Actions workflow running `go test ./...`, `golangci-lint`, `npm run lint`, `desktop-file-validate`, `appstreamcli validate`. |
| F-23 | P2 | `internal/platform/autostart_linux.go:16-25` | Generated `.desktop` has no `X-GNOME-Autostart-Delay`, no `X-GNOME-Autostart-Notification=false`. | Every login fires an immediate IMAP connect per account, colliding with the whole autostart herd. A notification toast appears for no reason. | `X-GNOME-Autostart-Delay=30` and `X-GNOME-Autostart-Notification=false`; or ship `After=graphical-session.target` in the systemd unit with `systemd-analyze`-friendly ordering. |
| F-24 | P2 | `internal/platform/autostart_linux.go:59-66` | `IsEnabled()` returns a hardcoded `false` under Flatpak. | Currently unreachable (the UI reads the DB), but any future caller gets a wrong answer. A lie in the interface. | Return the DB value, or delete the method from the interface until a portal query exists. |
| F-25 | P3 | `internal/platform/power_linux.go:21` (and `_darwin`, `_windows`) | `platform.NewPowerMonitor` is never called from `app/`. | Dead code on every platform; `AGENTS.md` implies a power monitor exists. | Delete it, or wire it to throttle sync on battery (a real feature: disable body fetch on battery). |
| F-26 | P3 | `app/app.go:853-861` vs `app/app.go:909-915` | Background-hide logic duplicated, with divergent bodies (`a.windowHidden` set in only one). | Easy to drift; `IsWindowHidden` is never exposed so the divergence is currently invisible. | Extract `func (a *App) handleCloseRequest() bool` and call it from both. |
| F-27 | P3 | `app/app.go:509` + `:849,867,918,936,964` | `shuttingDown` is an unsynchronised package global; check-then-act is not atomic. | Two concurrent quits can both schedule a `Quit` and both emit `app:shutting-down`. | `sync.Once` for the shutdown, `atomic.Bool` for the flag. |
| F-28 | P3 | `AGENTS.md` §3, §8 | Claims "Unix socket + D-Bus on Linux" for single-instance and "D-Bus monitors" for the lock. There is no D-Bus in the lock path. | Misleads the next agent into searching for D-Bus activation code that does not exist. | Correct `AGENTS.md` to describe the actual Unix-socket mechanism. |
| F-29 | P3 | test files | Zero tests for the background path. Only `TestSetGetRunBackground` (`internal/settings/store_test.go:393`) and `TestDefaultIdleConfig` (`internal/imap/client_test.go:114`) touch this area. | F-03, F-08, F-24 and the autostart desktop-file contents are all untestable regressions. | Add table tests: `.desktop` template rendering (quote/spaces/flags), `isSyncDue` backoff table, IDLE give-up-then-rearm, stale-socket race via a real `net.Listen` in a test. |
| F-30 | P3 | `internal/imap/idle.go:46-48`; `internal/sync/scheduler.go:138` | Fixed backoff and a phase-locked 60 s ticker, no jitter. | N accounts on one provider reconnect in lockstep; every instance installed by the same package syncs at the same second. | Add ±20 % jitter to the backoff and stagger `StartAccount` by `accountIndex * 2s` on boot. |

---

## 9. Recommended Linux background + autostart architecture

### 9.1 The shape of the answer

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                      systemd --user  (the supervisor)                         │
│                                                                               │
│  ~/.config/systemd/user/hsx2mail.service                                      │
│      [Unit]   After=graphical-session.target                                  │
│               Wants=network-online.target                                     │
│      [Service] Type=notify                                                   │
│               ExecStart=/usr/libexec/hsx2mail/hsx2mail --background           │
│               Restart=on-failure                                              │
│               RestartSec=15                                                  │
│               Environment=GSK_RENDERER=cairo  GSK_RENDERER_NODE=wayland       │
│               Slice=background.slice           ← nice + low IO priority      │
│               IOSchedulingClass=idle          ← never competes with a build   │
│      [Install] WantedBy=default.target                                       │
│                                                                               │
│  ~/.config/systemd/user/hsx2mail-notify.target                               │
│      Wants=… wait, not needed. The *agent* owns the tray.                    │
└───────────────────────────────┬───────────────────────────────────────────────┘
                                │ sd_notify(READY=1) / WATCHDOG=1
                                ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│  hsx2mail --background                                                        │
│                                                                              │
│   StartHidden → no toplevel window is ever mapped.                           │
│   Tray (AppIndicator3 / StatusNotifierItem) — the ONLY user surface:          │
│        ●  Unread count badge (account + total)                               │
│        ⏻  Sync now                                                          │
│        ⚙  Preferences                                                        │
│        ⎋  Show & Quit                                                        │
│                                                                              │
│   Tray rule (mandatory):                                                     │
│        if tray registration fails  → fall back to a real window               │
│                                     (never leave a headless, unreachable      │
│                                      process running)                         │
└───────────────────────────────┬───────────────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────────┐
        ▼                       ▼                           ▼
┌───────────────┐      ┌──────────────────┐        ┌───────────────────┐
│ IMAP IDLE     │      │ Polling fallback │        │ systemd watchdog  │
│ per account   │◀────▶│ 60 s + jitter +  │        │ ping every 90 s;  │
│ 10 min cycle  │      │ failure backoff  │        │ no ping → restart │
└───────────────┘      └──────────────────┘        └───────────────────┘
```

### 9.2 Autostart, per distribution channel

| Channel | Mechanism | Command | Rationale |
|---|---|---|---|
| **Native (deb/rpm/AppImage)** | `systemctl --user enable --now hsx2mail.service` | `--background` | Supervision, restart, readiness, IO priority. Strictly better than a `.desktop`. |
| **Nix / Guix** | Home-manager `systemd.user.services` | `ExecStart = "${lib.getExe' …}"; Environment…` | Home-manager rewrites the store path on every rebuild; a hand-written `.desktop` with a pinned `Exec` breaks (F-05). |
| **Flatpak** | `org.freedesktop.portal.Background` with **no** `commandline` override | portal-generated `flatpak run --command=hsx2mail io.github.beheoxinh.Hsx2Mail` | The portal runs host-side and writes a correct host entry. Fixes F-02. |
| **Flatpak (bundled systemd)** | The manifest installs the unit into `~/.config/systemd/user` and the app enables it on first run | `--background` | Works on GNOME 45+, KDE 6+, where the Background portal shows a per-login dialog anyway. Gives restart + watchdog for free. |
| **Anything with a tray** | `systemd --user` unit, always | — | The tray makes the `.desktop`-only path unnecessary. |

Do **not** generate `~/.config/autostart/*.desktop` any more. Keep the code path only for
headless/heterogeneous fallbacks and deprecate it.

### 9.3 Changes required, in dependency order

**Step 1 — make background mode survivable (F-01, F-11, F-12).**
Blocking, because everything else is meaningless without it.

1. Tray icon. Wails v2 has no tray API, so this is the one piece of genuinely new
   infrastructure: `getlantern/systray` (pure Go, GTK3, no cgo beyond what Wails already
   links) or `fyne.io/systray`. Menu: unread count, Sync now, Show, Preferences, Quit.
   Register it on the GTK main thread. **If registration fails, do not start hidden** —
   fall back to showing the window. That single guard converts F-01 from P0 to P2.
2. `go func() { _ = cmd.Wait() }()` at `app/ipc.go:244`.
3. Move `EventsEmit("app:ready")` from `app/app.go:779` to after `initNotifications` /
   `initSleepWakeMonitor` / `initThemeMonitor`, and run those three in their own goroutines
   if they can block.

**Step 2 — kill the boot storm (F-08, F-09, F-19, F-30).**

```go
// internal/sync/scheduler.go
type Scheduler struct {
    // …
    failCount map[string]int // accountID → consecutive failures
}

func (s *Scheduler) nextAttempt(acc *account.Account) time.Duration {
    s.failMu.Lock()
    n := s.failCount[acc.ID]
    s.failMu.Unlock()
    if n == 0 {
        return time.Duration(acc.SyncInterval) * time.Minute
    }
    d := time.Duration(1<<min(n, 8)) * time.Minute // 2m, 4m, … 256m
    return min(d, 30*time.Minute) + jitter(20*time.Second)
}
```

Reset `failCount[acc.ID] = 0` on any successful `SyncMessages`. Seed the first tick with
jitter and delay the first IDLE connect by `accountIndex * 2s` so N accounts do not hit
one provider in the same second. Classify errors in `getValidOAuthToken` and suppress
`oauth:reauth-required` for transport failures.

**Step 3 — make autostart actually work (F-02, F-05, F-06, F-07, F-14, F-23).**

1. Add `hsx2mail.service` + a `--background` flag. `--background` forces
   `GetStartHiddenActive()` to `true` regardless of DB state — one flag, no ordering
   questions, and it makes the `.desktop` fallback correct too.
2. `platform.NotifyStartupComplete()`: if `$NOTIFY_SOCKET` is set, send
   `READY=1\nSTATUS=…\n`; also `WATCHDOG=1` on a 90 s ticker. Keep `gdk_notify_startup_complete()`
   for the non-systemd case. ~15 lines, no new dependency.
3. `SetAutostart(true)` → `Enable()` first, DB second, on error surface a toast.
4. Flatpak: drop the `commandline` override at `autostart_linux.go:118`.
5. Keep the `.desktop` generator but quote `Exec` and add
   `X-GNOME-Autostart-Delay=30` + `X-GNOME-Autostart-Notification=false`.
6. `SetAutostart(true)` implies `run_background` + `start_hidden` (F-06).

**Step 4 — harden the lock (F-03, F-04, F-15, F-27).**

`flock` is the right primitive; it makes staleness structurally impossible and closes the
TOCTOU window:

```go
// /tmp or $XDG_RUNTIME_DIR/hsx2mail-<uid>/instance.lock
f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
    // someone holds it → they are alive, by construction
    // dial the activation socket, send the command, return (false, nil)
}
f.Write(append(strconv.Itoa(os.Getpid()), '\n'))
os.Remove(activationSocket)   // safe now: the holder is us
l, _ := net.Listen("unix", activationSocket)
```

Plus: buffer the pending activation command in `SetOnShow` (F-04), and move both sockets
to `$XDG_RUNTIME_DIR` (F-15).

**Step 5 — correctness and hygiene (F-10, F-13, F-16, F-17, F-18, F-22).**

Re-arm IDLE after give-up; subscribe to screen-lock and suppress notification bodies while
locked; reconnect the logind bus; ignore sub-60 s sleeps; add notification actions; add a
CI workflow that runs `desktop-file-validate` and `appstreamcli validate`.

### 9.4 What to *not* build

- A separate headless daemon process. One binary, two modes (`--background`) is smaller
  and keeps the DB, the pool, and the tray in one process. A daemon + GUI pair doubles the
  IPC surface and the migration story for no gain.
- Dynamic notification actions that mutate mail (`"snooze"`). Body-fetch and threading
  correctness is not there yet; keep the notification read-only plus Show.
- A `systemd` **socket** unit. There is no on-demand IMAP work to do.
- Migrating the single-instance lock to D-Bus. `flock` is smaller, has no daemon
  dependency, and has no activation race. If you want zero-fork activation later, do it
  as an `org.gtk.Application` bus name (F-20) *on top of* `flock`, not instead of it.

---

## 10. Verification notes

Read-only review. No code was modified; `docs/analysis/` contained no prior documents, so
this is the first. Behavioural claims marked "would" (F-01's zombie count, F-02's portal
output, F-03's interleaving) are derived from source reading plus the documented
behaviour of `net.Listen`, `os/exec`, and `xdg-desktop-portal`; they were not reproduced
on a live session. Everything with an exact `file:line` was read directly.
