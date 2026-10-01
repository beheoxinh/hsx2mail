# 80 — Build & Packaging Analysis

Read-only analysis of the Hsx2Mail build system, Flatpak packaging, Linux desktop
integration, version-string consistency, CI, and dependency hygiene.

- Repo: `/mnt/WannaBeTheGuy/WwW/MyOwn/hsx2mail`
- Branch: `main`, no git remote configured, 0 git tags
- Toolchain observed: Go 1.25, golangci-lint **not installed**, Node via mise, WebKit2GTK 4.1 headers present
- No source file was modified by this analysis.

---

## 1. Build target inventory

### 1.1 Makefile targets

`Makefile:11-13` declares the `.PHONY` set. Actual targets and their artifacts:

| Target | Line | Command | Artifact produced |
|---|---|---|---|
| `all` | `Makefile:66` | delegates to `build` | — |
| `build` | `Makefile:71` | `wails build -ldflags "$(LDFLAGS)" -tags webkit2_41` (+ ad-hoc `codesign` on Darwin, `Makefile:78-81`) | `build/bin/hsx2mail` (45 MB, no strip) or `build/bin/Hsx2Mail.app` on macOS |
| `build-linux` | `Makefile:84` | `wails build -ldflags "$(LDFLAGS)" -tags webkit2_41,linux,production` | same, explicitly Linux/production |
| `flatpak` | `Makefile:89` | `./build/flatpak/build-local.sh` | `build/bin/Hsx2Mail-${VERSION}.flatpak` — **currently broken**, see §2.3 |
| `flatpak-dev` | `Makefile:94` | `./build/flatpak/build-flatpak.sh` | `build/bin/Hsx2Mail-dev.flatpak` (prebuilt host binary) |
| `dev` | `Makefile:99` | `wails dev -ldflags "$(LDFLAGS)" -tags webkit2_41` | dev server + window |
| `dev-race` | `Makefile:102` | `wails dev -ldflags "$(LDFLAGS)" -tags webkit2_41 -race` | dev server with race detector |
| `generate` | `Makefile:105` | `wails generate module` | `frontend/wailsjs/**` |
| `test` | `Makefile:108` | `go test ./...` | console only |
| `lint` | `Makefile:125` | `lint-go` + `lint-frontend` | console only |
| `lint-go` | `Makefile:127` | `golangci-lint run` | console only |
| `lint-frontend` | `Makefile:132` | `cd frontend && npm run lint` (`eslint .`) | console only |
| `fmt` | `Makefile:137` | `go fmt ./...` | rewrites sources |
| `clean` | `Makefile:144` | `rm -rf build/bin frontend/dist AppDir; rm -f hsx2mail` | — |
| `tools-clean` | `Makefile:152` | echo only (AppImage removed) | — |
| `frontend-deps` | `Makefile:157` | `cd frontend && npm install` | `frontend/node_modules` |
| `frontend-update` | `Makefile:163` | `cd frontend && npm update` | updated lockfile |
| `install` | `Makefile:169` | `$(MAKE) install-linux` \| `install-darwin` | — |
| `uninstall` | `Makefile:180` | `$(MAKE) uninstall-linux` \| `uninstall-darwin` | — |
| `install-linux` | `Makefile:193` | `install -Dm755/-Dm644` into `$(DESTDIR)$(PREFIX)` | `/usr/local/bin/hsx2mail`, `/usr/local/share/icons/hicolor/256x256/apps/io.github.beheoxinh.Hsx2Mail.png`, `/usr/local/share/applications/io.github.beheoxinh.Hsx2Mail.desktop` |
| `uninstall-linux` | `Makefile:208` | `rm -f` the same three paths | — |
| `install-darwin` | `Makefile:221` | `cp -R` + re-`codesign` | `/Applications/Hsx2Mail.app` |
| `uninstall-darwin` | `Makefile:235` | `rm -rf /Applications/Hsx2Mail.app` | — |
| `build-windows-installer` | `Makefile:243` | `wails build -ldflags ... -nsis` | `build/bin/hsx2mail-amd64-installer.exe` |
| `help` | `Makefile:252` | echo only | — |

**Not declared but present on disk:** `build/flatpak/build-flatpak-docker.sh` and
`build/flatpak/test-build.sh` have no Makefile target.

### 1.2 Shell scripts

| Script | Line count | Purpose | Notes |
|---|---|---|---|
| `build.sh` | 15 | `set -euo pipefail`, `cd $(dirname $0)`, `make build`, prints artifact path | correct |
| `build-frontend.sh` | 5 | `set -euo pipefail`, `cd frontend`, `npm run build` | correct |
| `install.sh` (root) | 34 | builds if binary missing, then `sudo install -Dm{755,644}` | hardcodes `PREFIX=${PREFIX:-/usr/local}` (`install.sh:14`), user-local unsupported |
| `run.sh` | — | checks wails CLI pinned `@v2.12.0`, `make dev` | — |
| `run-debug.sh` | — | debug launcher | — |
| `scripts/dev-run.sh` | 5 | `npm run build` then `go run -tags webkit2_41,linux,production . --debug` | **no `-ldflags`**, so no OAuth creds in dev builds |
| `build/linux/install.sh` | 6.3 KB | full-colour installer with **system and user** modes (`build/linux/install.sh:78-89`) | the only installer that supports `~/.local`; not wired to any Makefile target |
| `build/linux/uninstall.sh` | 5.6 KB | counterpart | — |
| `build/flatpak/build-flatpak.sh` | 76 | dev bundle: `make build-linux` + dev manifest | — |
| `build/flatpak/build-local.sh` | 92 | flathub manifest + bundle | `make build-linux` at line 62 is wasted work, see §2.3 |
| `build/flatpak/build-flatpak-docker.sh` | 83 | containerised variant | Dockerfile incompatible, see F-04 |
| `build/flatpak/test-build.sh` | — | container smoke test | runs **no** lint/test/validate |
| `build/flatpak/flathub/release.sh` | ~60 | copies manifest + node sources to a flathub repo | hardcoded old app-id, see F-03 |
| `build/flatpak/flathub/calculate-hashes.sh` | — | rewrites release URLs + sha256 in the manifest | hardcoded old app-id, see F-03 |
| `build/flatpak/flathub/gen-node-sources.py` | — | flatpak-node-generator wrapper | — |

### 1.3 Non-target build inputs

- `wails.json` — `"name": "hsx2mail"`, `"outputfilename": "hsx2mail"`,
  `frontend:install: npm install`, `frontend:build: npm run build`,
  `frontend:dev:watcher: npm run dev`, `frontend:dev:serverUrl: auto`,
  `info.productVersion: 0.3.2`.
- `.golangci.yml` — 55 lines, `version: "2"`; disables `unused`; narrows
  `staticcheck` to `SA*`/`S*`; excludes `internal/carddav/` from
  `errcheck`/`ineffassign`/`staticcheck`; excludes `frontend/node_modules`.
- `frontend/vite.config.ts` — 78 lines, 8 rollup inputs, aliases pinned to
  absolute `node_modules` paths for `@iconify/svelte`, `svelte-i18n`,
  `date-fns-tz`; `build.target: 'esnext'`; `build.sourcemap: false`;
  `server.strictPort: true`.
- `frontend/svelte.config.js` — 5 lines, `vitePreprocess()` only.
- `frontend/tsconfig.json` — `strict: true`, `checkJs: true`, `noEmit`.
- `frontend/eslint.config.js` — 83 lines, flat config.
- `frontend/knip.json` — entry `src/composerMain.ts`, `index.html`, `composer.html`.

---

## 2. Flatpak manifest review

Two manifests exist, both `app-id: io.github.beheoxinh.Hsx2Mail`:

- `build/flatpak/io.github.beheoxinh.Hsx2Mail-dev.yml` (39 lines) — packages a
  **host-built** binary; no `sdk-extensions`; no sources beyond `type: dir`.
- `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml` (88 lines) — the real
  Flathub from-source manifest.

### 2.1 Runtime / SDK / finish-args

Both manifests: `runtime: org.gnome.Platform`, `runtime-version: '50'`,
`sdk: org.gnome.Sdk`. Only the flathub manifest adds
`sdk-extensions: org.freedesktop.Sdk.Extension.golang` (`:5`) and
`org.freedesktop.Sdk.Extension.node24` (`:6`).

`runtime-version: '50'` is bleeding-edge — GNOME 50 is not yet on Flathub at
the time of writing, so `flatpak install-deps-from=flathub` will fail for
anyone without a pre-seeded runtime. This is a release blocker, not a nit.

`finish-args` (identical in both, flathub manifest lines 10-21):

| Flag | Present | Assessment |
|---|---|---|
| `--share=network` | yes | required for IMAP/SMTP |
| `--share=ipc` | yes | required for X11/Wayland |
| `--socket=wayland` | yes | good |
| `--socket=fallback-x11` | yes | **insufficient** — X11-only sessions (Xorg sessions launched without Wayland) are not covered. Should be `--socket=x11` as well |
| `--socket=x11` | **MISSING** | see above |
| `--device=dri` | yes | required for GPU accel; `WebviewGpuPolicyOnDemand` (`main.go:156`) makes it necessary |
| `--talk-name=org.freedesktop.login1` | yes | sleep/wake (`internal/platform/sleep_linux.go:45`) |
| `--talk-name=org.freedesktop.portal.Desktop` | **MISSING** | **P0** — the app's default notification path is the portal (`internal/notification/notifier_linux.go:20`), plus `Background`, `FileChooser`, `NetworkMonitor`, `OpenURI`, `Settings` portals. All blocked by the Flatpak D-Bus proxy |
| `--talk-name=org.freedesktop.Notifications` | **MISSING** | the `--dbus-notify` opt-in path (`main.go:35`, `notifier_linux.go:23-25`) cannot work |
| `--talk-name=org.freedesktop.NetworkManager` | **MISSING** | network-monitor fallback (`internal/platform/network_linux.go:16`) |
| `--talk-name=org.freedesktop.secrets` | **MISSING** | keyring → silently falls back to AES-GCM file store |
| `--talk-name=org.freedesktop.appearance` | **MISSING** | theme monitor |
| `--filesystem=*` | none | acceptable (Flatpak auto-grants `~/.var/app/<id>`), but no `xdg-download`/`xdg-documents` if the attachment downloader writes there |

### 2.2 Build steps

`build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:36-65`:

1. `:39` `cd frontend && npm install --offline --legacy-peer-deps`
2. `:42` `cd frontend && npm run build` — **the frontend build does run**
3. `:47` `go build -mod=vendor -tags desktop,webkit2_41,production -ldflags "-w -s" -o hsx2mail` — **broken, see F-01**
4. `:50` install binary to `/app/bin/hsx2mail`
5. `:53` install prebuilt `hsx2mail-creds` shim to `/app/lib/hsx2mail/hsx2mail-creds`
6. `:56` desktop file, `:58` icon, `:61` metainfo, `:64` `update-desktop-database`

**No lint, no `go test`, no `svelte-check`, no `desktop-file-validate`, no
`appstreamcli validate` run anywhere in any of the four flatpak scripts.**

### 2.3 Offline node_modules

`build/flatpak/flathub/node-sources.json` — 1024 entries, 450 unique packages,
587 KB. Verified against `frontend/package-lock.json`: **all 450 locked packages
are present** in the offline cache. The offline strategy is sound in principle.

Two weaknesses:

- `npm install --offline` is used instead of `npm ci`. `npm ci` is the correct
  offline-install command — it wipes `node_modules`, honours the lockfile
  exactly, and never re-resolves ranges. `--legacy-peer-deps` additionally
  disables peer-dependency resolution, making the tree non-deterministic.
- The cache is generated by `gen-node-sources.py` from the lockfile, so
  **any dependency bump requires regenerating `node-sources.json` and the
  manifest sha256s**. Nothing enforces this.

### 2.4 Prebuilt-binary download

`:76-88` downloads `hsx2mail-creds` from
`https://github.com/beheoxinh/hsx2mail/releases/download/v0.3.2/flathub-build-env-v0.3.2-linux-{x86_64,aarch64}`
with pinned sha256. The source exists in-tree at `cmd/hsx2mail-creds/main.go`
but is **not built by the manifest** — it is fetched pre-compiled. This means:

- The version string `v0.3.2` is hardcoded twice in the manifest (`:77`, `:84`)
  and must be bumped manually with every release.
- If the release tag does not exist, the Flatpak build fails at fetch time.
- Flathub's "build from source" policy is bent; a reviewer will flag it.
- `build/flatpak/flathub/go.mod.yml` (60 module archives) exists as an
  alternative vendoring strategy but is **not referenced by the manifest**
  (0 occurrences) — dead file.

### 2.5 Dev manifest

`build/flatpak/io.github.beheoxinh.Hsx2Mail-dev.yml:22-38` installs
`build/bin/hsx2mail` directly and uses `desktop-file-edit` to rewrite `Icon`
and `Exec` to `hsx2mail %U`. It has **no trailing newline** on its last line
and **no `update-desktop-database`**. Fine for a dev-only artifact.

---

## 3. Desktop integration inventory

### 3.1 Files found

| Path | Lines | Role |
|---|---|---|
| `build/linux/hsx2mail.desktop` | 57 | the only shipped desktop entry |
| `.agent-work/stage/usr/share/applications/io.github.hkdb.Aerion.desktop` | — | stale staging artifact from the pre-rename era |
| `build/linux/hsx2mail.png` | — | 256×256, byte-identical to `build/appicon.png` |
| `build/appicon.png` | — | 256×256, installed as `io.github.beheoxinh.Hsx2Mail.png` |
| `brand/icon.png` | — | 256×256, different from `appicon.png` |
| `brand/icon-beautyline.png` | — | variant |
| `build/windows/icon.ico` | 107 KB | Windows |
| `build/darwin/Info.plist`, `Info.dev.plist` | — | macOS bundles |

No `.service` unit, no `.policy` (polkit), no DBus `.service`, no
`XDG autostart` `.desktop` shipped in-repo, no AppStream `.appdata.xml`
(uses `.metainfo.xml` instead), no mime XML (`shared-mime-info`).

### 3.2 `desktop-file-validate`

Both desktop files validate clean. Verbatim output:

```
$ desktop-file-validate build/linux/hsx2mail.desktop
exit=0

$ desktop-file-validate .agent-work/stage/usr/share/applications/io.github.hkdb.Aerion.desktop
exit=0
```

No warnings, no errors from `desktop-file-validate` on either file.

### 3.3 `appstreamcli validate`

```
$ appstreamcli validate --no-net build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml
I: io.github.beheoxinh.Hsx2Mail:454: description-first-word-not-capitalized
W: io.github.beheoxinh.Hsx2Mail:646: description-has-plaintext-url p

✘ Validation failed: warnings: 1, infos: 1, pedantic: 1
```

With `--pedantic` it also reports:

```
P: io.github.beheoxinh.Hsx2Mail:3: cid-contains-uppercase-letter io.github.beheoxinh.Hsx2Mail
```

The uppercase `S` in `Hsx2Mail` is intentional (matches the D-Bus/flatpak id
convention already in use) but it is a pedantic warning Flathub will surface.

`description-first-word-not-capitalized` at `:454` is the release `0.1.39`
description starting with a lowercase token. `description-has-plaintext-url` at
`:646` is the `0.1.3x` description containing a bare URL.

### 3.4 Desktop entry key audit — `build/linux/hsx2mail.desktop`

| Key | Line | Value | Verdict |
|---|---|---|---|
| `Version` | 2 | `1.5` | OK |
| `Type` | 3 | `Application` | OK |
| `Name` | 4 | `Email Hub` | app is called "Hsx2Mail" everywhere else; cosmetic divergence |
| `GenericName` | 5 | + 9 localized (`cs de fr it nb vi zh_CN zh_HK zh_TW`) | OK, underscore locale codes are correct |
| `Comment` | 15 | + 9 localized | OK |
| `Keywords` | 25 | + 9 localized | OK |
| `Icon` | 35 | `io.github.beheoxinh.Hsx2Mail` | resolves to the 256×256 installed PNG; **no scalable SVG, no 48/64/128/512 sizes** |
| `TryExec` | 36 | `hsx2mail` | matches the installed binary name |
| `Exec` | 37 | `hsx2mail %U` | OK — `%U` is required for `mailto:` URIs |
| `Terminal` | 38 | `false` | OK |
| `Categories` | 39 | `Network;Email;` | OK |
| `MimeType` | 40 | `x-scheme-handler/mailto;` | correct for a `mailto:` handler |
| `StartupNotify` | 41 | `true` | set, but see F-09 — no `StartupWMClass`-verified startup-complete signal exists |
| `StartupWMClass` | 42 | `io.github.beheoxinh.Hsx2Mail` | **correct** — matches `linux.Options.ProgramName` at `main.go:157` |
| `X-GNOME-UsesNotifications` | 43 | `true` | advisory only; the app uses the portal, and the portal D-Bus name is not granted |
| `Actions` | 44 | `compose;` | OK |
| `[Desktop Action compose]` `Exec` | 57 | `hsx2mail mailto:` | valid — separate group, no duplicate key |
| `DBusActivatable` | — | **absent** | no D-Bus activation, see §8 |

---

## 4. Version-string divergence

Every location a version string appears. All values currently agree at
`0.3.2`; the problem is that there is **no single source of truth and no
verification**, and `docs/RELEASE.md` does not enumerate all of them.

| # | File:line | Kind | Value | In `docs/RELEASE.md`? |
|---|---|---|---|---|
| 1 | `app/state.go:30` | `const Version` (About dialog, `--version`) | `0.3.2` | yes (item 1) |
| 2 | `wails.json:12` | `info.productVersion` (macOS bundle, Windows NSIS) | `0.3.2` | yes (item 6) |
| 3 | `frontend/package.json:4` | npm version | `0.3.2` | yes (item 2) |
| 4 | `frontend/package-lock.json:3` | lockfile root version | `0.3.2` | yes (item 3) |
| 5 | `frontend/package-lock.json:9` | lockfile `packages[""]` version | `0.3.2` | yes (item 3) |
| 6 | `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml:288` | `<release version="0.3.2" date="2026-07-16">` | `0.3.2` | yes (item 4) |
| 7 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:77` | x86_64 release URL, `v0.3.2` twice in one line | `v0.3.2` | **no** |
| 8 | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:84` | aarch64 release URL, `v0.3.2` twice | `v0.3.2` | **no** |
| 9 | `AGENTS.md:6` | `- **Version:** v0.3.2` | `v0.3.2` | **no** |
| 10 | `build/flatpak/build-local.sh:78` | `git describe --tags \|\| echo "dev"` | dynamic | **no** — repo has **0 tags**, so `make flatpak` always produces `Hsx2Mail-dev.flatpak` |
| 11 | `build/flatpak/build-flatpak-docker.sh` | same `git describe` pattern | dynamic | **no** |
| — | `Makefile` | **no `VERSION` variable exists at all** | — | — |
| — | `build/linux/hsx2mail.desktop` | no version key (correct) | — | — |
| — | `build/darwin/Info.plist:12,16` | `{{.Info.ProductVersion}}` templated | from #2 | — |
| — | `build/windows/info.json:7` | `{{.Info.ProductVersion}}` templated | from #2 | — |
| — | `build/windows/installer/project.nsi:25,38-39` | `VIProductVersion "${INFO_PRODUCTVERSION}.0"` | from #2 | — |

Additional divergence found in `docs/RELEASE.md:5`: it instructs
`CHANGELOG.md - add new release entry`, but **`CHANGELOG.md` does not exist**
in the repo. The documented release process is broken at step 5.

Also divergent (identity, not version):
`build/flatpak/flathub/README.md` still documents `io.github.hkdb.Aerion.yml`
and `aerion-v0.1.13-linux-x86_64.tar.gz`; `.gitignore:20` ignores
`build/flatpak/flathub/io.github.hkdb.Aerion.yml.backup`.

---

## 5. CI and quality-gate gaps

### 5.1 Does CI exist?

**No.** Verified absent: `.github/`, `.gitlab-ci.yml`, `Jenkinsfile`,
`.circleci/`, `.woodpecker.yml`, `azure-pipelines.yml`, `.pre-commit-config.yaml`.
The only CI-adjacent file in the tree is `.golangci.yml`. There is no
`.github/workflows/`, no `dependabot.yml`, no `renovate.json`.

### 5.2 What `make test` and `make lint` actually run

| Target | Command | Gap |
|---|---|---|
| `make test` (`Makefile:108`) | `go test ./...` | no `-race`, no `-count=1`, no `-timeout`, no `-cover`, no frontend test step |
| `make lint-go` (`Makefile:127`) | `golangci-lint run` | not a Make dependency check; tool absent on this host |
| `make lint-frontend` (`Makefile:132`) | `cd frontend && npm run lint` → `eslint .` | **no `svelte-check`, no `knip`, no `tsc --noEmit`** |
| `make lint` (`Makefile:125`) | `lint-go` + `lint-frontend` | inherits both gaps |

There is **no `test-race` target**, no `test-frontend` target, no `vet` target,
no `check` target, and no `check-all` aggregate.

### 5.3 Measured state on this host

```
go build ./...                       -> exit 0
go vet ./...                         -> exit 0   (only cgo GDK deprecation noise)
go mod tidy -diff                    -> exit 0   (go.mod/go.sum are tidy)
go test ./...                        -> exit 1   *** FAILS ***
```

Go package/test inventory:

| Metric | Count |
|---|---|
| Packages in module (`go list ./...`) | 41 |
| Packages with tests, passing | 35 |
| Packages with no test files | 5 |
| Packages failing | 1 |
| `Test*`/`Benchmark*`/`Example*` functions | 491 |

Failing package: `internal/database` — 2 tests fail:

```
--- FAIL: TestMigrationV32_LocalRecordIDsRewrittenToUUIDs (0.05s)
    database_test.go:202: re-migrate: failed to apply migration 41:
        migration SQL failed: SQL logic error: duplicate column name: secondary_sync_interval (1)
--- FAIL: TestMigrationV33_CleansExistingOrphans (0.05s)
    database_test.go:379: re-migrate: failed to apply migration 41:
        migration SQL failed: SQL logic error: duplicate column name: secondary_sync_interval (1)
FAIL	github.com/beheoxinh/hsx2mail/internal/database	0.431s
```

Root cause: `internal/database/database_test.go:169` does
`DELETE FROM migrations WHERE version >= 32` and then drops the v34/v35
columns listed at `:177`, but never drops `secondary_sync_interval`, which
migration 41 adds (`internal/database/migrations.go:1327`). The test was not
updated when v41 landed.

Frontend gates run manually on this host:

```
cd frontend && npm run lint    -> exit 0  (eslint clean)
cd frontend && npm run check   -> exit 0  (svelte-check: 0 errors, 0 warnings)
cd frontend && npm run knip    -> exit 1  (9 unused deps, 3 unused devDeps,
                                           135 unused exports, 1 unused file)
```

### 5.4 Commands that belong in CI

Exactly these, in this order, as the quality gate:

```bash
# --- Go ---
go mod tidy -diff                 # catches go.mod/go.sum drift
go build ./...                    # compile gate
go vet ./...
golangci-lint run                # config is already v2, 55 lines
go test -race -count=1 -timeout 10m ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# --- Frontend ---
cd frontend
npm ci                           # NOT npm install — the lockfile is committed
npm run lint                     # eslint
npm run check                    # svelte-check --tsconfig ./tsconfig.json
npm run knip || true             # advisory: 135 unused exports are pre-existing
npm run build                    # vite build must succeed (go:embed depends on it)

# --- Linux desktop integration ---
desktop-file-validate build/linux/hsx2mail.desktop
appstreamcli validate --pedantic build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml
flatpak-builder --user --install-deps-from=flathub \
    build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml
```

Two extra gates worth adding: `shellcheck -x` over the 12 shell scripts (they
currently use bare `set -e` with no `-u`/`pipefail`), and a `wails doctor` /
`wails build -platform linux/amd64` smoke job.

---

## 6. Dependency hygiene

### 6.1 Go modules

`go.mod` declares 22 direct requirements, `go 1.25.0`. `go mod tidy -diff` is
clean — **no declared-but-unused direct dependencies**. Every direct import was
verified present in `internal/`, `app/`, `cmd/`, or `extensions/`.

| Module | Version | Concern |
|---|---|---|
| `github.com/emersion/go-imap/v2` | `v2.0.0-beta.7` | **beta** in production. API can break on any `beta.N` bump. AGENTS.md §11 acknowledges this |
| `github.com/ProtonMail/go-crypto` | `v1.4.1` | v1 line is the legacy branch; the maintained line is `v2`. `internal/pgp/*.go` imports `go-crypto/openpgp` (v1 API) — 11 files would need a migration |
| `github.com/teamwork/tnef` | `v0.0.0-20200108124832` | pseudo-version pinned to a **2020-01-08** commit. Effectively unmaintained. Used for `winmail.dat` extraction |
| `git.sr.ht/~jackmordaunt/go-toast/v2` | `v2.0.3` | pulls in a large transitive chain for a Windows-only feature: `labstack/echo/v4`, `labstack/gommon`, `leaanthony/go-ansi-parser`, `leaanthony/gosod`, `leaanthony/u`, `mattn/go-colorable`, `mattn/go-isatty`. `go-toast` only needs `wintoast`; the echo chain is dead weight |
| `github.com/zalando/go-keyring` | `v0.2.6` | last release 2022. API is stable, so low risk, but effectively unmaintained |
| `go.mozilla.org/pkcs7` | `v0.9.0` | maintained fork of the retired `mozilla/pkcs7`; fine |
| `github.com/wailsapp/wails/v2` | `v2.12.0` | current. `run.sh` pins the CLI to `@v2.12.0` (good); `build/flatpak/Dockerfile:26` uses `@latest` (bad) |
| `golang.org/x/{crypto,image,net,sys}` | 0.46 / 0.41 / 0.48 / 0.39 | current |
| `modernc.org/{libc,mathutil,memory}` | indirect | the pure-Go SQLite backend; `libc` is large but required for the no-CGO goal |

Note: the module path is `github.com/beheoxinh/hsx2mail` but the repo has **no
git remote configured**, so the build cannot verify provenance or be pushed.

### 6.2 Frontend packages

`frontend/package.json`: 32 runtime deps, 26 devDeps. `frontend/package-lock.json`
is committed (247 KB) — good, this is what makes the Flatpak offline build
possible. `node_modules` is 635 MB.

Knip-reported, manually verified:

| Package | Type | Verdict |
|---|---|---|
| `@tanstack/svelte-virtual` | dep | **genuinely unused** — 0 source references. AGENTS.md §9 claims it powers virtual scroll; it does not |
| `@iconify/json` | devDep | **genuinely unused** — 390 MB of the 635 MB `node_modules`. The app uses the per-icon `@iconify-json/*` packages via `frontend/src/lib/iconify-offline.ts:5-9` |
| `unplugin-icons` | devDep | **genuinely unused** — not in `vite.config.ts` plugins, no source references |
| `tailwind-variants` | dep | **genuinely unused** — 0 source references |
| `tslib` | devDep | knip-reported; not directly imported, but pulled in by Svelte/Tiptap tooling. Leave it |
| `date-fns-tz` | dep | **knip false positive** — aliased to an absolute path at `frontend/vite.config.ts:48`, so knip cannot resolve it. 4 real source files use it |
| `dictionary-{cs,de,en,fr,it,nb}` | dep | **knip false positives** — consumed via a template literal at `frontend/scripts/copy-dictionaries.mjs:23` |
| `@iconify/svelte`, `svelte-i18n` | dep | aliased at `vite.config.ts:43-44`; knip false positives |

Also stale: `@eslint/js ^10.0.1` and `eslint ^10.3.0` are ESLint 10 — bleeding
edge, and `eslint-plugin-svelte ^3.17.1` + `svelte-eslint-parser ^1.6.1` are
written for the ESLint 8/9 plugin API. `bits-ui ^1.0.0-next.74` is a
**pre-release** dependency in production code. `tailwindcss ^3.4.17` is the v3
line while the Vite 6 / PostCSS 8 toolchain would support v4.

**Zero frontend tests.** `package.json` has no `test` script; there is no
Vitest/Jest/Playwright config and 0 `*.test.ts` / `*.spec.ts` files. The
`svelte-check` and `knip` scripts exist but are wired into nothing.

### 6.3 Version pinning across toolchains — divergence

| Component | Pinned to | Source |
|---|---|---|
| Go | `1.25.0` | `go.mod:3` |
| Go in Flatpak sandbox | `org.freedesktop.Sdk.Extension.golang` (whatever GNOME 50 ships) | manifest `:5` |
| Go in `build/flatpak/Dockerfile` | **1.23.0** | `Dockerfile:18` — **cannot build a `go 1.25.0` module** |
| Node in Flatpak sandbox | `org.freedesktop.Sdk.Extension.node24` | manifest `:6` |
| Node in `build/flatpak/Dockerfile` | **20.11.0** | `Dockerfile:22` |
| GNOME runtime in manifests | **`50`** | manifest `:3-4` |
| GNOME runtime in `Dockerfile` | **`47`** | `Dockerfile:37-38` |
| WebKit | `webkit2_41` only | `Makefile:52` — no `webkit2_40` fallback tag exists anywhere in the tree |

---

## 7. Findings

| ID | Sev | File:line | Problem | Impact | Fix |
|---|---|---|---|---|---|
| F-01 | **P0** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:47` | `go build -mod=vendor` but the repo has **no `vendor/` directory**. Verified locally: `go: inconsistent vendoring in …: git.sr.ht/~jackmordaunt/go-toast/v2@v2.0.3: is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt` | `make flatpak` and every Flathub build **fails outright** | Either commit `vendor/` (`go mod vendor`), or drop `-mod=vendor` and use the already-generated `build/flatpak/flathub/go.mod.yml` (60 module archives) as manifest sources |
| F-02 | **P0** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:10-21` and `build/flatpak/io.github.beheoxinh.Hsx2Mail-dev.yml:7-18` | `--talk-name=org.freedesktop.portal.Desktop` absent from `finish-args` | **All notifications silently fail in Flatpak.** Default path is the portal (`internal/notification/notifier_linux.go:20`). Also breaks Background/FileChooser/NetworkMonitor/OpenURI/Settings portals | Add `--talk-name=org.freedesktop.portal.Desktop`; add `--talk-name=org.freedesktop.Notifications` for the `--dbus-notify` path; add `--talk-name=org.freedesktop.appearance`; add `--talk-name=org.freedesktop.secrets` if keyring matters |
| F-03 | **P0** | `build/flatpak/flathub/release.sh:39-41`, `build/flatpak/flathub/calculate-hashes.sh:14,25,90,93`, `build/flatpak/flathub/README.md:15,31` | Flathub release tooling hardcoded to the pre-rename identity: `io.github.hkdb.Aerion.yml`, `REPO="https://github.com/hkdb/aerion"`, `aerion-v0.1.13-linux-*.tar.gz` | **Flathub submission is broken.** `release.sh` `cp`s a file that does not exist; `calculate-hashes.sh` `sed`s a manifest that does not exist | Rename every `hkdb`/`Aerion`/`aerion` occurrence to `beheoxinh`/`Hsx2Mail`; add the 2 missing version locations from §4 to `docs/RELEASE.md` |
| F-04 | **P0** | `internal/database/database_test.go:169-178` vs `internal/database/migrations.go:1327` | Re-migrate helper deletes `migrations WHERE version >= 32` but does not drop `secondary_sync_interval` (added by v41) | **`make test` exits 1.** `TestMigrationV32_LocalRecordIDsRewrittenToUUIDs` and `TestMigrationV33_CleansExistingOrphans` both fail. No merge can be gated on `make test` today | Add `"secondary_sync_interval"` to the drop-column list at `database_test.go:177`; better: derive the list from `migrations` automatically so future migrations cannot break it |
| F-05 | **P0** | `build/flatpak/Dockerfile:18,22,37-38` vs `go.mod:3` and manifest `:3-6` | Dockerfile installs **Go 1.23.0**, **Node 20.11.0**, and **GNOME Platform/SDK 47**; the module needs Go ≥1.25, the manifests need Node 24 and GNOME 50 | `build/flatpak/build-flatpak-docker.sh` and `test-build.sh` **cannot build the project** | Bump the Dockerfile to Go 1.25.x, Node 24.x, and GNOME 50; pin `wails@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)` instead of `@latest` (`Dockerfile:26`) |
| F-06 | **P0** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:3-4`; `build/flatpak/build-flatpak.sh:33`, `build-local.sh:33` | `runtime-version: '50'` for `org.gnome.Platform` | GNOME 50 is not on Flathub; `--install-deps-from=flathub` fails for every clean machine | Pin to the newest runtime actually on Flathub (48 or 49) and move to 50 when it lands |
| F-07 | **P1** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:17`; dev manifest `:14` | Only `--socket=fallback-x11`; no `--socket=x11` | X11-only sessions (GDM Xorg, `XDG_SESSION_TYPE=x11`, nested/remote X) cannot start the app | Add `--socket=x11` alongside `--socket=wayland` |
| F-08 | **P1** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:39` | `npm install --offline --legacy-peer-deps` instead of `npm ci` | Non-deterministic tree; `--legacy-peer-deps` silently skips peer resolution; `npm install` re-resolves ranges and can reach for the network inside the sandbox | Use `npm ci --offline`. Keep `--legacy-peer-deps` only if peer conflicts are real, and say so in a comment |
| F-09 | **P1** | `build/linux/hsx2mail.desktop:41` vs `main.go:142` | `StartupNotify=true` but the app never calls `gtk_window_set_visual`/`gtk_widget_realize` with a startup-complete handshake; Wails shows the window only after the frontend emits `app:ready` | Cursor shows as busy indefinitely (or not at all) on a cold WebKit2GTK start. The real signal is the Wails `WindowShow` event | Either implement the GIO startup-notification protocol, or drop `StartupNotify=true` to avoid promising a signal that never arrives |
| F-10 | **P1** | `build/linux/hsx2mail.desktop` (whole file) | No `DBusActivatable=true` and no companion `<app-id>.service` D-Bus activation file | Every launcher click and every `mailto:` open forks a Go process, opens SQLite, connects a socket, then exits. ~200 ms wasted and an extra TOCTOU window on the single-instance lock | Register an `org.gtk.Application`-style bus name via the existing `godbus` dependency, ship `io.github.beheoxinh.Hsx2Mail.service`, and set `DBusActivatable=true` + `SingleMainWindow=true` |
| F-11 | **P1** | whole tree — no `.github/`, no `.gitlab-ci.yml`, `Jenkinsfile`, `.circleci/` | **No CI whatsoever.** 491 Go tests, an eslint config, a svelte-check config, a knip config, a golangci-lint v2 config, and a desktop-file all run only by hand | Nothing blocks a broken commit. F-01 and F-04 would both have been caught by a 3-minute CI run | Add the command set in §5.4 |
| F-12 | **P1** | `Makefile:125-134` | `make lint` runs `golangci-lint run` + `eslint .` only. `svelte-check` and `knip` are configured but wired into nothing | Type errors in Svelte components and dead dependencies ship silently | Add `lint-types: cd frontend && npm run check` and make `lint` depend on it |
| F-13 | **P1** | `frontend/package.json` | No `test` script, no test runner, 0 `*.test.ts`/`*.spec.ts` files | 0 % frontend test coverage on a codebase with a Tiptap editor, i18n, and a threading-heavy message list | Add Vitest + a few store/parser tests, or explicitly accept zero frontend coverage in `docs/` |
| F-14 | **P1** | `docs/RELEASE.md:5` | Step 5 says `CHANGELOG.md - add new release entry`; **`CHANGELOG.md` does not exist** | The documented release process breaks at step 5 | Create `CHANGELOG.md` or drop the step |
| F-15 | **P1** | `docs/RELEASE.md:1-6` | Release checklist omits `AGENTS.md:6` and the two hardcoded `v0.3.2` release URLs in `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:77,84` | A version bump that follows the checklist leaves the Flatpak fetching stale credentials shims | Add all three to the checklist; better, inject the version from `wails.json` into the manifest at build time |
| F-16 | **P1** | `build/flatpak/build-flatpak-docker.sh:32,52-54` | The script reads `$GOOGLE_CLIENT_ID`/`$MICROSOFT_CLIENT_ID` and forwards them with `docker run -e`, but never sources `.env`/`.env.local`. It is not reachable from any Makefile target, so nothing exports them | The container build **silently ships a binary with empty OAuth credentials**; the only signal is a warning message that also never fires | `source .env 2>/dev/null; source .env.local 2>/dev/null` before the check — or add a Makefile target that inherits the already-`export`ed vars (`Makefile:18-20`) |
| F-17 | **P1** | `build/flatpak/build-local.sh:62` | Runs `make build-linux` (host, full Wails build) and then packages with the **flathub manifest**, which builds its own binary from source inside the sandbox (`manifest:47`) | 3-5 minutes of wasted host build on every `make flatpak`, and a confusing two-binary story | Delete line 62 — the flathub manifest is self-contained. `build-flatpak.sh` legitimately needs its host build for the *dev* manifest |
| F-18 | **P1** | `go.mod:6` vs `go.mod:8`; `Makefile:52` | `go-imap/v2 v2.0.0-beta.7` in production, and `Makefile:52` pins `BUILD_TAGS := webkit2_41` with **no `webkit2_40` fallback tag** in the tree | A beta dependency can break the build on any patch bump; distros shipping only WebKit2GTK **4.0** (Debian 10, Ubuntu 20.04, Fedora <33) cannot build at all | Pin to a known-good beta; add a `webkit2_40` build tag file mirroring the 4.1 path, or document 4.1 as a hard prerequisite |
| F-19 | **P1** | `build/flatpak/build-flatpak.sh:5`, `build-local.sh:5`, `build-flatpak-docker.sh:4` | All three use bare `set -e` — no `-u`, no `-o pipefail`. Unquoted expansions, no shellcheck, no `readonly` | `flatpak list --runtime \| grep -q …` masks upstream failures; unset vars expand to empty silently | `set -euo pipefail` in all three; add `shellcheck -x` to CI |
| F-20 | **P1** | `build/linux/hsx2mail.desktop:16-42`; `build/appicon.png`; `brand/icon.png` | Single 256×256 PNG, no scalable SVG, no 48/64/128/512 sizes. `build/appicon.png` and `build/linux/hsx2mail.png` are byte-identical duplicates (md5 `1f01c5be…`) | Blurry in launchers, taskbars, and notifications; no HiDPI crispness | Ship a 512×512 or SVG at `hicolor/scalable/apps/io.github.beheoxinh.Hsx2Mail.svg`; de-duplicate the PNGs |
| F-21 | **P1** | `build/flatpak/flathub/io.github.beheoxinh.Hsx2Mail.yml:76-88`; `cmd/hsx2mail-creds/main.go` | The manifest downloads a **prebuilt** `hsx2mail-creds` from a GitHub release instead of building the in-tree source. The release tag `v0.3.2` is hardcoded twice (`:77`, `:84`) | Flatpak build breaks if the release tag is missing; bends Flathub's from-source policy; a reviewer will reject it. Also means **no Makefile target builds the shim at all** — `make install-linux` installs only the main binary, so native installs have no runtime-credential path | Add a `build-creds` target: `go build -o build/bin/hsx2mail-creds ./cmd/hsx2mail-creds`, install it next to the main binary in `install-linux` (`internal/oauth2/config.go:148` already looks in `filepath.Dir(exe)`), and drop both downloads from the manifest |
| F-22 | **P1** | `Makefile:193-205`; `install.sh:14-29`; `build/linux/install.sh:78-89` | `make install-linux` and the root `install.sh` both hardcode `/usr/local` + `sudo`. Only the unreferenced `build/linux/install.sh` supports a `--user` mode into `~/.local` | The unprivileged install path — the recommended one for most desktops — is not reachable from `make`. Packagers cannot build a `DESTDIR` staging tree without overriding `PREFIX` manually | Add `install-user` / `PREFIX=~/.local` support to the Makefile, and make `build/linux/install.sh` the single implementation invoked by both `make` targets |
| F-23 | **P1** | `internal/platform/network_linux.go:16,25` vs manifest `finish-args` | Network monitor tries the portal, then NetworkManager, then gives up (`method = "none"`). Neither bus name is granted | Online/offline pause-resume of sync never fires in Flatpak | Covered by the F-02 fix; add a polling fallback in `network_linux.go` so a blocked bus is not fatal |
| F-24 | **P1** | `frontend/src/lib/iconify-offline.ts:5-9` vs `frontend/package.json:58` | `@iconify/json` (390 MB installed) is a devDependency but unused — only the 5 `@iconify-json/*` per-icon packages are imported | 390 MB of dead `node_modules` on every dev machine and in the Flatpak build sandbox; slower `npm ci` in CI | `npm rm -D @iconify/json unplugin-icons` |
| F-25 | **P2** | `frontend/package.json:19` | `@tanstack/svelte-virtual` declared but has 0 source references. AGENTS.md §9 claims it powers message-list virtual scroll | Dead dependency; the claim in the architecture doc is wrong | Remove the dep or wire it into the conversation list; correct AGENTS.md either way |
| F-26 | **P2** | `go.mod:19` | `github.com/teamwork/tnef` pinned to a 2020-01-08 commit | `winmail.dat` parsing has an unmaintained dependency and no security patches | Fork, vendor, or drop TNEF support behind a build tag |
| F-27 | **P2** | `go.mod:6` | `go-toast/v2` drags in `labstack/echo/v4` + `gommon` + `leaanthony/*` + `mattn/go-isatty` for a Windows-only toast feature | ~7 unnecessary modules in the dependency graph, larger vendor tree | Import `go-toast/v2/wintoast` behind a `//go:build windows` file so the echo chain drops out of Linux builds |
| F-28 | **P2** | `go.mod:8` | `ProtonMail/go-crypto v1.4.1` — the v1 branch is legacy; v2 is the maintained line | Missed security fixes; 11 files in `internal/pgp/` pin the v1 API | Plan a v2 migration; track it, don't block on it |
| F-29 | **P2** | `Makefile:71,84` | No `-trimpath`, no `-s -w`, no `-buildvcs=false`. `build/bin/hsx2mail` is 45,317,120 bytes | 45 MB binary for an email client; build paths leak into the binary; non-reproducible output | Add `-trimpath -ldflags "-w -s"` to the native build targets (the Flatpak manifest `:47` already does `-w -s`) |
| F-30 | **P2** | `frontend/vite.config.ts:60` | `build.target: 'esnext'` | Emits syntax that WebKit2GTK 4.1 on older distros (Safari 15.4-class JSC) cannot parse. The Flatpak pins GNOME 50 so it is safe there, but native builds against Ubuntu 22.04 / Fedora 37 can break | Set `target: 'es2022'` (or `['chrome110','safari16']`) to match the oldest WebKit2GTK 4.1 you claim to support |
| F-31 | **P2** | `internal/platform/autostart_linux.go:16-25` | The runtime-generated autostart entry is bare `Exec=%s` with no hidden/start-minimized argument, and no `Hidden=` key | Autostart pops the window into the user's face on every login | Add a `--hidden` flag and emit `Exec=%s --hidden`; pair with `X-GNOME-Autostart-Delay=3` |
| F-32 | **P2** | `internal/platform/autostart_linux.go:59-63` | `IsEnabled()` returns hardcoded `false` for the Flatpak path ("the Background portal doesn't provide a query method") | The settings toggle never reflects true state under Flatpak | Persist the requested state in the settings store and read it back, as the code comment already suggests |
| F-33 | **P2** | `build/flatpak/flathub/go.mod.yml` | 60 module archives, referenced **0 times** by any manifest | 30 KB of dead vendoring data that will drift | Delete it, or adopt it as the F-01 fix |
| F-34 | **P2** | `build/flatpak/io.github.beheoxinh.Hsx2Mail-dev.yml:40` | No trailing newline; no `update-desktop-database` step (the flathub manifest has one at `:64`) | Cosmetic | Add the newline and the desktop-database step |
| F-35 | **P2** | `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml:454,646,3` | `appstreamcli validate --pedantic` reports `description-first-word-not-capitalized`, `description-has-plaintext-url`, `cid-contains-uppercase-letter` | Flathub reviewer noise; the last one is unavoidable given the chosen app-id | Capitalize the `0.1.39` description's first word; replace the bare URL at `:646` with an AppStream link |
| F-36 | **P2** | `wails.json:9-14`; `build/linux/hsx2mail.desktop:4`; `build/darwin/Info.plist`; `build/windows/installer/project.nsi:25-28` | Product name is **"Email Hub"** everywhere, but the app/module/binary is `Hsx2Mail` / `hsx2mail` | User-visible name and internal name diverge; complicates bug reports and support | Pick one. `Hsx2Mail` in the desktop entry at least, so the launcher matches the docs |
| F-37 | **P2** | `wails.json:13` | `productName`/`companyName` = `"Email Hub"`; `Info.dev.plist:6` sets `LSMinimumSystemVersion` to `10.14.0` | macOS 10.14 predates the WebKit2GTK-era requirements Wails v2 needs; the installer will ship an app that cannot run on its declared minimum | Raise to macOS 11 or 12 |
| F-38 | **P2** | `scripts/dev-run.sh:5` | `go run -tags webkit2_41,linux,production . --debug` with no `-ldflags` | Dev builds have empty OAuth client IDs; Gmail/Outlook login fails with no obvious cause | Add the same `LDFLAGS` the Makefile uses, or document that dev OAuth needs `make dev` |
| F-39 | **P2** | `build/Email_Hub__debug_` (55 MB), `build/go_build_github_com_hkdb_aerion` (55 MB), `build/go_build_github_com_hkdb_aerion_cmd_aerion_creds` (2.8 MB), `build/bin/aerion` (45 MB) | 158 MB of stale pre-rename build artifacts left in `build/` | Wasted disk; confusing; risk of packaging the wrong binary | `rm` them; they are untracked so they will not reach a release |
| F-40 | **P2** | `.agent-work/stage/usr/share/applications/io.github.hkdb.Aerion.desktop` | Pre-rename staged desktop entry still on disk (validates clean, but wrong app-id) | Stale state that could be packaged by accident | Remove |
| F-41 | **P2** | `Makefile:10-13` | `flatpak` and `flatpak-dev` names are inverted relative to their scripts: `make flatpak` → `build-local.sh` (the real flathub manifest), `make flatpak-dev` → `build-flatpak.sh` (the host-prebuilt dev manifest) | A new maintainer will run the wrong one and get a dev-quality bundle | Rename the scripts to `build-flathub.sh` and `build-dev-flatpak.sh` to match the targets |
| F-42 | **P2** | `.gitignore:10,11,20` + `/home/alienware/.gitignore:98` | The **global** gitignore contains `/build/`, so on this machine `git ls-files build/` returns **0** — every Flatpak manifest, the desktop file, the icons, and the NSIS installer are untracked. The repo's own `.gitignore` only excludes `build/bin/` and `build/linux/AppDir` | On this checkout the committed repo has **no build metadata at all**. Flathub builds from a git tag would fail with an empty `build/` | Add an explicit `!build/**` negation or drop `/build/` from the global ignore; verify with `git ls-files build/` before any release |
| F-43 | **P2** | repo root | No `git remote` configured and **0 git tags** | `build/flatpak/build-local.sh:78` (`git describe --tags \|\| echo "dev"`) can only ever produce `Hsx2mail-dev.flatpak`; Flathub requires tagged releases | Add the upstream remote; tag releases `vX.Y.Z` |
| F-44 | **P2** | `README.md` (35 lines) | No build prerequisites documented anywhere: GTK 3, WebKit2GTK **4.1** (not 4.0), `gnome-keyring`/`libsecret` for the keyring backend, `wails` CLI `@v2.12.0`, Node 24 | Every new contributor hits the same four discover-by-failure problems | Add a Prerequisites section; mirror it in `build/README.md` |
| F-45 | **P2** | `go.mod:8`; `frontend/package.json:38-43` | Pinned to `go-imap/v2` **beta** and `bits-ui ^1.0.0-next` (pre-release) in shipping code | Both can break the build on a routine dependency bump | Pin exact versions with a documented upgrade procedure |

---

## 8. Proposed Linux-native packaging + autostart plan

The Flatpak path is the right primary channel and is nearly there. What is
missing is a **distro-native path** (RPM/DEB or an AUR/coverage-packaging
submission) and a **proper session-integration story**. Concrete plan:

### Phase 1 — unblock the current path (1 day)

1. Fix F-01: commit `vendor/` or switch the manifest to `go.mod.yml` sources.
2. Fix F-02: add the five missing `--talk-name` flags to both manifests, plus
   `--socket=x11`.
3. Fix F-04: add `secondary_sync_interval` to the drop list, or derive it.
4. Fix F-06: drop `runtime-version` to a Flathub-available release.
5. Fix F-05: bump the Dockerfile to Go 1.25 / Node 24 / GNOME 50.
6. Fix F-03: rename `hkdb`/`Aerion` → `beheoxinh`/`Hsx2Mail` across
   `build/flatpak/flathub/`.
7. Fix F-21: add a `build-creds` Makefile target, install the shim in
   `install-linux`, drop both downloads from the manifest.

### Phase 2 — CI (2 days)

Add `.github/workflows/ci.yml` running exactly the §5.4 command set on
`ubuntu-latest` (Go 1.25 + Node 24 + `desktop-file-utils` + `appstream-glib` +
`flatpak-builder`), plus a `flatpak.yml` job gated on `main`. A
`desktop-file-validate` failure or a `go test` failure must block the merge.
That alone would have caught F-01, F-04, F-08, and F-11.

### Phase 3 — distro-native packaging (3-5 days)

No spec/deb rules exist. Add both, generated from a single `VERSION` variable:

- **`build/linux/packaging/hsx2mail.spec`** (Fedora/RHEL/Nobara — the natural
  first target given the 4.1 tag): `BuildRequires: golang >= 1.25,
  gtk3-devel, webkit2gtk4.1-devel, libsecret-devel, pkgconfig`; `%build` runs
  `make build build-creds`; `%install` places the binary in
  `/usr/bin/hsx2mail`, the shim in `/usr/libexec/hsx2mail/hsx2mail-creds`
  (add that path to `internal/oauth2/config.go:143-148`), the desktop file in
  `/usr/share/applications/`, and **split the 256×256 PNG into
  22/24/32/48/64/128/256/512 plus a scalable SVG**. `%check` runs
  `desktop-file-validate` and `appstreamcli validate`.
- **`build/linux/packaging/debian/`** (`debian/control`, `rules`, `changelog`
  with `0.3.2-1`) for Ubuntu/Debian, same layout, plus
  `Depends: libwebkit2gtk-4.1-0, gnome-keyring | libsecret-1-0`.

### Phase 4 — session integration (2 days)

- **`share/io.github.beheoxinh.Hsx2Mail.service`** + `DBusActivatable=true` +
  `SingleMainWindow=true` in the desktop entry (F-10). A ~120-line `godbus`
  service in `internal/singleinstance` that owns the bus name, opens the DB
  once, and forwards `Activate` to the existing `SetOnShow` path. The
  `godbus/v5` dependency is already present.
- **`share/io.github.beheoxinh.Hsx2Mail.autostart.desktop`**, installed to
  `/etc/xdg/autostart/` (system) and offered per-user. Reuse the template at
  `internal/platform/autostart_linux.go:16-25` and add the `--hidden` flag from
  F-31. This removes the need for the app to write into `~/.config/autostart`
  at runtime and makes autostart admin-manageable.
- **D-Bus single-instance as the primary lock**, keeping the Unix socket
  (`internal/platform/singleinstance_linux.go:17`) as the non-D-Bus fallback so
  the app still works on a bare TTY/ssh session.

### Phase 5 — runtime dependency declaration

- Flatpak: `--talk-name` set from F-02, plus `--filesystem=xdg-download` if the
  attachment downloader writes there.
- Native: `gnome-keyring`/`libsecret` declared as a hard `Requires` (or the
  AES-GCM fallback documented as a deliberate, visible downgrade), WebKit2GTK
  4.1 pinned via `libwebkit2gtk-4.1` — never 4.0, which does not build this
  tree (F-18). Document all of it in `README.md` (F-44).

### Items explicitly out of scope

- AUR / Nix / Homebrew packaging — community-maintained once the spec and deb
  rules exist.
- `bits-ui` and `go-imap/v2` major upgrades (F-45) — track, do not block.
- polkit: not needed. The app has no privileged operation. `gnome-keyring`
  handles the Secret Service prompt.
