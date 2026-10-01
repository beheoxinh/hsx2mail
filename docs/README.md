# Hsx2Mail — Documentation Index (Single Source of Truth)

Hsx2Mail is a modern, cross-platform desktop email client built on Wails v2, with a Go
backend and a Svelte 5 frontend. It speaks IMAP/SMTP, CalDAV/CardDAV, supports PGP and
S/MIME, and runs a background sync engine (IDLE push plus a polling scheduler) that can
auto-start with the desktop session. This file is the entry point for every other
document in the repository: if two documents disagree, the one marked **canonical** here
wins, and the analysis set under `docs/analysis/` is the evidence of record for defects.

- **Product name:** Hsx2Mail (runtime window title is still the legacy "Email Hub").
- **Version:** 0.3.2 (`app/state.go:30`, `wails.json:12`, `frontend/package.json:4`).
- **Tech stack:** Wails v2.12 · Go 1.25 (`go.mod:3`) · Svelte 5 (runes) · TypeScript · Vite 6 ·
  Tailwind 3 · bits-ui · Tiptap v2 · modernc.org/sqlite (pure Go, no CGO) · go-imap v2 beta.
- **Primary distribution:** Flatpak; also native Linux binary, macOS `.app`, Windows NSIS.

## Repository layout

```
main.go, preflight.go      Entry point, CLI flags, single-instance, preflight before Wails
app/                       Wails-bound application layer (accounts, sync trigger, compose,
                           draft, IPC, extensions bridges, background scheduler glue)
internal/                  Core business logic, no Wails imports
  account/ folder/ message/ draft/ database/ credentials/ oauth2/ settings/ appstate/
  imap/ smtp/ sync/ email/ pgp/ smime/ crypto/ carddav/ contact/ certificate/
  ipc/ platform/ notification/ keyring/ logging/ undo/ extensions/ core/api/v1/
extensions/                First-party extensions: calendar/, contacts/ (backend + frontend)
frontend/                  Svelte 5 UI: src/lib/stores, src/lib/components, wailsjs/ bindings
cmd/hsx2mail-creds/        OAuth credential helper binary
build/                     Flatpak, Linux, macOS, Windows packaging scripts and manifests
tools/db/                  Database rollback SQL (`rollback-v39-to-v30.sql`)
tools/db/schemadump/      Applies all migrations to a throwaway DB and prints the
                          real schema (generator input for docs/DATABASE.md)
tools/db/gen-database-doc.py  Splices that schema into docs/DATABASE.md between
                          the <!--GEN:*--> markers
brand/                     Application icons
archive/                   Discontinued approaches (AppImage, old backups)
docs/                      This documentation tree (see map below)
docs/analysis/             Adversarial analysis of the v0.3.2 tree (evidence, not canon)
```

## Documentation map

Status legend: **Canonical** = trust as-is for its topic and do not duplicate.
**Current** = accurate but not the owner. **Register** = an open-gap ledger.
**Evidence** = immutable historical record; do not edit, do not cite as current.

### Canonical owners

| Document | Owns | Notes |
|---|---|---|
| `docs/README.md` (this file) | Index, reading order, maintenance rules | Start here. |
| `docs/architecture.md` | **Structure**: repository layout, module graph, startup order, data flow | Summarises other topics and links out; does not re-document them. |
| `docs/DATABASE.md` | **Schema**: migrations v1..v42, every table and column, all 42 indexes, DSN + PRAGMAs, foreign keys, transaction boundaries, retention | Migration, column and index sections are **generated** — see below. |
| `docs/OPERATIONS.md` | **Build, test, release, install, troubleshoot** | `BUILD.md` and `RELEASE.md` are short forms of this. |
| `docs/SQL_ROLLBACK.md` | **Backup, restore, corruption repair, forward-only rationale, per-transition rollback** | Includes the v40/v41/v42 notes. |
| `docs/BACKGROUND.md` | **Startup, close semantics, autostart, tray, single instance, sleep/wake, network, scheduling** | |
| `docs/FRONTEND.md` | **Store graph, component tree, virtualized list, events, layout modes, i18n** | |
| `docs/CRYPTO.md` | **PGP, S/MIME, TOFU, credential storage, HTML sanitizing, security invariants** | |
| `docs/PERFORMANCE.md` | **Size limits, timeouts, batch sizes, cache budgets, index rationale** | The catalogue of *why* each number is what it is. |
| `docs/EXTENSIONS.md` | Extension API surface and lifecycle | |
| `docs/EXT_RULES.md` | Extension submission and review rules (R1..R33) | |
| `docs/KEYBOARD_SHORTCUTS.md` | Keyboard shortcut reference | |
| `docs/LANGUAGE.md` | Translation contribution workflow | |
| `docs/TERMS.md`, `docs/PRIVACY.md`, `docs/CASAT2.md` | Legal and security-questionnaire prose | Policy, not engineering. |

### Registers and plans

| Document | Role |
|---|---|
| `docs/PLAN.md` | Phased remediation roadmap, with task IDs and `file:line` |
| `docs/GAPS.md` | Open documentation gaps (G1..G18) |

### Current, not canonical

| Document | Role |
|---|---|
| `docs/business-logic.md` | End-to-end narrative walkthrough. Overlaps the canonical owners; where it conflicts, they win. |
| `docs/BUILD.md` | Short build form → `OPERATIONS.md` |
| `docs/RELEASE.md` | Release checklist → `OPERATIONS.md` §4 |

### Evidence (immutable)

`docs/analysis/` is the evidence of record. It describes the tree as it was when each
pass ran and is **not** updated as fixes land.

| Document | Subject |
|---|---|
| `analysis/10-sync-imap-idle.md` | Sync engine, IMAP pool, scheduler, IDLE |
| `analysis/20-background-autostart-linux.md` | Background mode, autostart, Linux session |
| `analysis/30-compose-send-draft.md` | Compose, send, draft, undo |
| `analysis/40-message-store-search.md` | Message store, FTS, search |
| `analysis/50-frontend.md` | Frontend architecture as found |
| `analysis/60-database.md` | Database as found |
| `analysis/70-platform-services.md` | Platform services, notifications, crypto |
| `analysis/80-build-packaging.md` | Build and packaging |
| `analysis/90-verification.md` | **The authoritative defect verdict** |
| `analysis/95-existing-docs-audit.md` | Audit of the pre-existing docs |
| `analysis/97-post-implementation-review.md` | First post-implementation adversarial review |
| `analysis/98-post-fix-verification.md` | Second pass, after the fixes |

`AGENTS.md` is the agent architecture guide. Its structural claims have been corrected
against the source as part of this consolidation, but it is a map, not an owner — verify
any specific claim before relying on it.

---

## Where do I find X

| Question | Answer here |
|---|---|
| What table holds X? Which migration added it? | `DATABASE.md` §4 |
| What are the PRAGMAs and why? | `DATABASE.md` §2 |
| How do I take a backup / restore? | `SQL_ROLLBACK.md` § Backup and recovery runbook |
| Can I roll back a migration? | `SQL_ROLLBACK.md` § Why migrations are forward-only — then the per-transition section |
| How do I build? | `OPERATIONS.md` §2, or `BUILD.md` |
| How do I run the tests and linters? | `OPERATIONS.md` §3 |
| How do I cut a release? Which files carry the version? | `OPERATIONS.md` §4, `RELEASE.md` |
| How do I install the Flatpak? What does it request? | `OPERATIONS.md` §5 |
| Notifications do not appear | `OPERATIONS.md` §6.1 |
| Autostart does not fire | `OPERATIONS.md` §6.2 |
| The keyring is unavailable | `OPERATIONS.md` §6.3, `CRYPTO.md` §4 |
| Composer windows misbehave | `OPERATIONS.md` §6.4 |
| What happens when I close the window? | `BACKGROUND.md` §2 |
| Is there a tray icon? (and "minimize to tray" corrections) | `BACKGROUND.md` §4 |
| How do startup, autostart and the tray interact? | `BACKGROUND.md` §1, §3 |
| How does sleep/wake and network recovery drive sync? | `BACKGROUND.md` §6 |
| How do boot storm and IDLE/polling interact? | `BACKGROUND.md` §7 |
| What are the stores and the component tree? | `FRONTEND.md` §3, §4 |
| How is the message list virtualized? | `FRONTEND.md` §5, `PERFORMANCE.md` §6 |
| How does PGP key discovery work? | `CRYPTO.md` §2 |
| How does TOFU certificate trust work? | `CRYPTO.md` §3 |
| Where are secrets stored? | `CRYPTO.md` §4 |
| What HTML sanitizing is applied to incoming vs outgoing mail? | `CRYPTO.md` §5 |
| What is every timeout / batch size / cache budget? | `PERFORMANCE.md` |
| Why does that index exist? | `PERFORMANCE.md` §5, `DATABASE.md` §11 |
| How are extensions written and reviewed? | `EXT_RULES.md`, `EXTENSIONS.md` |
| What keyboard shortcuts exist? | `KEYBOARD_SHORTCUTS.md` |
| How do I add a locale? | `LANGUAGE.md` |

---

## Reading order

**New engineer:** this file → `architecture.md` (the system map) → `DATABASE.md` and
`BACKGROUND.md` (the two you will touch first) → `FRONTEND.md` if you work on UI →
then the analysis document for your area (10 sync, 30 compose, 40 store, 50 frontend,
60 database, 70 platform).

**Agent starting a task:** `PLAN.md` (task IDs and `file:line`) → the canonical owner for
the area the task touches → the source file the task cites. Use
`analysis/90-verification.md` only to check whether a finding was REFUTED; do not plan
against a refuted finding. Verify any `AGENTS.md` structural claim against the source
before relying on it.

---

## Getting started

```bash
git clone <repo> && cd hsx2mail
./prepare.sh     # toolchain + dependencies, then verifies the tree
./build.sh       # release binaries into build/bin/
./install.sh     # build, then install over any existing install
```

| Script | What it does |
|---|---|
| `prepare.sh` | Installs the system libraries, the Go and Node toolchains, the Wails CLI and the frontend dependencies, then verifies the tree compiles and type-checks. Idempotent; `--check` reports without changing anything. |
| `build.sh` | Builds the app **and** the `hsx2mail-creds` OAuth helper, running the static checks and the Go tests first. Runs `prepare.sh` automatically when the environment is not ready. `--debug`, `--race`, `--clean`, `--skip-tests`. |
| `install.sh` | Installs both binaries, the icon and the desktop entry, refreshing the desktop/icon caches. Replaces an existing install in place and never touches your mail or settings. `--user`, `--prefix DIR`, `--autostart`, `--uninstall`. |
| `run.sh` | Runs the built app without installing. |
| `run-debug.sh` | Runs with verbose logging. |

See [docs/PLAN.md](PLAN.md) for the toolchain rules these scripts enforce and
why they exist.

## Quality gates

Run `make check` for the whole set; CI runs the same commands.

| Gate | Command | What it protects |
|---|---|---|
| Go build / vet / tests | `go build ./...`, `go vet ./...`, `go test ./...` | general correctness |
| Go race | `go test -race ./...` | concurrent sync, IDLE and undo paths |
| Read-only transactions | `make check-tx` | the DSN sets `_txlock=immediate`, so a read-only transaction takes SQLite's global write lock and stalls every writer |
| Frontend tests | `make test-frontend` | virtualized message list: window reconciliation, row-index resolution, selection ranges |
| Frontend types / lint | `cd frontend && npm run check`, `npx eslint .` | Svelte 5 + TS |
| Offline icons | `node frontend/scripts/check-offline-icons.mjs` | an unregistered icon name silently fetches from `api.iconify.design` |
| Desktop entries | `desktop-file-validate` | packaging correctness |
| Schema docs | `go run ./tools/db/schemadump` + `python3 tools/db/gen-database-doc.py` | `docs/DATABASE.md` cannot drift from the code |

## Documentation maintenance rules

1. **One canonical owner per topic.** The map above names it. Link to the owner from other
   documents; do not restate. Duplicated prose is the root cause of the stale facts
   `analysis/95` found — the schema map was wrong in three different files simultaneously.
2. **Refresh-on-change.** If your change touches a fact a document states, that document
   changes in the same commit. This applies to flags, defaults, timeouts, settings keys,
   manifest `finish-args`, schema, and version strings.
3. **`docs/analysis/` is immutable evidence.** It records what was true at the time of the
   pass. Do not edit analysis files. When a finding is fixed, the fix belongs in the
   canonical owner doc and, if the finding was refuted, in the next verification pass.
4. **Plans cite `file:line`.** Any task list points at real source, never at prose.
   Prefer `file:symbol` when a line number is uncertain.
5. **Version strings live in five places** — `app/state.go`, `wails.json`,
   `frontend/package.json` (+ `package-lock.json`), the git tag, and the AppStream
   metainfo. Bump them together. There is no `CHANGELOG.md`; release notes go in the
   metainfo `<releases>` entries. See `OPERATIONS.md` §4.1.
6. **Generated documentation is generated, not written.** `docs/DATABASE.md` §3, §4
   (columns) and §11 (indexes) live between `<!--GEN:NAME-->` markers. After any
   migration change run:
   ```bash
   go run ./tools/db/schemadump > /tmp/schema_raw.txt
   python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt
   git diff docs/DATABASE.md     # the diff IS the schema change, in doc form
   ```
   An empty diff means the schema did not change. Never hand-edit inside a marker block —
   the next regeneration discards it. The hand-written sections around them still need
   your judgement; `DATABASE.md` §10 lists which.
7. **When two documents disagree, the canonical owner wins, and the loser is fixed in the
   same change.** Do not leave a known-wrong duplicate for a follow-up.
8. **A limit without a stated reason is a limit nobody dares raise.** Any new timeout,
   batch size, cache budget or concurrency cap ships with a `// Why` comment and a row in
   `PERFORMANCE.md`.
9. **A security invariant change needs a threat model in the pull request.** The list is
   `CRYPTO.md` §8.

---

## Gap status

The open list is `docs/GAPS.md` (G1..G18). Phase 6 of `PLAN.md` closed the ones below;
the register itself records what is still open.

| Gap | Topic | Now documented in |
|---|---|---|
| G1 | Canonical schema reference | `DATABASE.md` (generated) |
| G2 | Release and versioning procedure | `OPERATIONS.md` §4, `RELEASE.md` |
| G3 | Snapshot, backup and recovery runbook | `SQL_ROLLBACK.md` § Backup and recovery runbook |
| G4 | Background operation and window lifecycle | `BACKGROUND.md` |
| G9 | Crypto subsystem reference | `CRYPTO.md` |
| G12 | Frontend architecture reference | `FRONTEND.md` |
| G13 | Performance tuning guide | `PERFORMANCE.md` |
| G15 | Build and packaging reference | `OPERATIONS.md` §2, §5; `BUILD.md` |
| G16 | Autostart and Linux session integration | `BACKGROUND.md` §3; `OPERATIONS.md` §6.2 |

Still open and tracked in `GAPS.md`: G5 (single-instance/window handoff depth), G6 (IPC
wire protocol), G7 (attachment storage lifecycle), G8 (full-text search operations), G10
(OAuth setup and credential provisioning), G11 (notifications permissions), G14 (testing
strategy and known failures), G17 (logging and diagnostics), G18 (extension storage
boundary).

