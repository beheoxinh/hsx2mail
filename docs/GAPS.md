# GAPS — Missing Documentation

Topics the current corpus does not cover, covers only partially, or covers
incorrectly enough that a maintainer cannot rely on it. Each entry lists why
the gap matters, what the document must contain, and the code it must stay
in sync with. Companion to `docs/analysis/95-existing-docs-audit.md`.

Priority: **P0** = blocks correct maintenance now; **P1** = needed for safe

**Status key:** entries marked **STATUS: CLOSED** were closed by Phase 6 of `PLAN.md`;
the canonical owner is named inline. Unmarked entries are still open.

---

## G1 — Canonical schema reference (`docs/DATABASE.md`)

**Priority:** P0
**Why:** Every existing migration map is wrong (`architecture.md` §4,
`AGENTS.md` §4), including a non-existent `undo_commands` table. There is no
single authoritative description of the schema, so readers cannot tell
wrong from right.
**Must contain:** Table-per-migration map generated from source; every
table's columns and purpose; index inventory; foreign-key actions; the
`ErrSchemaTooNew` guard; and the runtime-added columns that migrations do
not create.
**Code:** `internal/database/migrations.go`, `internal/message/attachment_store.go`
(`ensureContentColumn`), `internal/extensions/store.go` (`ext_kv`).

**STATUS: CLOSED** — documented in `docs/DATABASE.md` (Phase 6).
## G2 — Release and versioning procedure (`docs/OPERATIONS.md`)

**Priority:** P0
**Why:** `RELEASE.md` tells the releaser to edit a `CHANGELOG.md` that does
not exist and omits at least one version location. Version strings
diverged (app 0.3.2; newest AppStream release 0.1.34).
**Must contain:** Every file carrying a version, in order, with the exact
field; the CI quality gates; tagging and artifact steps; rollback.
**Code:** `app/state.go`, `wails.json`, `frontend/package.json` and lockfile,
`build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml`.

**STATUS: CLOSED** — documented in `docs/OPERATIONS.md §4, docs/RELEASE.md` (Phase 6).
## G3 — Snapshot, backup, and recovery runbook

**Priority:** P0
**Why:** `SQL_ROLLBACK.md` covers only v39 → v30 and predates v40 and v41.
There is no procedure for a corrupt database, a WAL that will not
checkpoint, or an interrupted migration.
**Must contain:** Where the database and WAL live; how to back up safely
under WAL; how to detect and repair corruption; how to extend the rollback
scripts for v40 and v41; the schema guard behavior when opening a newer
database.
**Code:** `internal/database/database.go` (checkpoint routine),
`internal/database/migrations.go` (schema guard), `tools/db/`.

**STATUS: CLOSED** — documented in `docs/SQL_ROLLBACK.md § Backup and recovery runbook` (Phase 6).
## G4 — Background operation and window lifecycle

**Priority:** P1
**Why:** Startup behavior spans hidden start, keep-running-when-closed,
autostart, sleep/wake, and network transitions, but no document ties them
together. The tray is mentioned in `business-logic.md` as if it existed.
**Must contain:** The relationship between `start_hidden` and
`run_background`; shutdown semantics when windows are closed; how the
single-instance handoff raises an existing window; that no tray icon exists
and what "background" actually means.
**Code:** `app/app.go` (lifecycle, `shouldRunInBackground`), `app/settings.go`,
`main.go` (single-instance and mailto forwarding).

**STATUS: CLOSED** — documented in `docs/BACKGROUND.md` (Phase 6).
## G5 — Single-instance and window handoff

**Priority:** P1
**Why:** Three documents describe the transport inconsistently and two are
wrong. The differences between Unix socket (Linux/macOS) and loopback TCP
(Windows) are not documented anywhere.
**Must contain:** Lock file/socket locations per platform; the token
exchange; how a second launch forwards `mailto:` and activates the first
window; stale-lock cleanup.
**Code:** `internal/platform/singleinstance.go`, `singleinstance_linux.go`,
`singleinstance_darwin.go`, `singleinstance_windows.go`.

## G6 — IPC wire protocol reference

**Priority:** P1
**Why:** Message types are listed in `AGENTS.md` §10 but the framing,
authentication, direction, payload schema, and failure handling are only
implied across several docs.
**Must contain:** Frame encoding and length prefix; the stdin token
bootstrap; every message type with payload and direction; shutdown and
theme-broadcast semantics; versioning.
**Code:** `internal/ipc/message.go`, `internal/ipc/server.go`,
`internal/ipc/client.go`, `app/ipc.go`, `app/detached_composer.go`.

## G7 — Attachment storage and inline-content lifecycle

**Priority:** P1
**Why:** The `attachments.content` column is added at runtime, not by a
migration, and no document records this. The 5MB inline cap and download
flow are scattered.
**Must contain:** When metadata is written; when inline content is
persisted versus stored on disk; the runtime schema mutation and why it
exists; size limits; download and cleanup paths.
**Code:** `internal/message/attachment_store.go`, `internal/message/model.go`,
`internal/email/download.go`, `internal/email/attachment.go`.

## G8 — Full-text search operations

**Priority:** P1
**Why:** Search is described functionally, but index maintenance (the 5s
startup delay, `fts_index_status`, rebuild after corruption) is not
documented.
**Must contain:** Index tables; when indexing runs; how to force a rebuild;
the startup delay and why; failure modes.
**Code:** `internal/database/migrations.go` (`fts_*`), the FTS indexer used
by `app/app.go`.

## G9 — Crypto subsystem reference (PGP / S/MIME / TOFU consolidated)

**Priority:** P1
**Why:** PGP, S/MIME, and TOFU are spread across `business-logic.md` §6,
`architecture.md` §9, and `EXTENSIONS.md`, each partial, with the wrong
certificate table name in two places.
**Must contain:** Key/certificate storage tables; sign, verify, encrypt,
decrypt flows; key discovery (WKD/HKP); TOFU fingerprint prompt; the
unmaintained PKCS#7 dependency risk called out in `analysis/70`.
**Code:** `internal/pgp/`, `internal/smime/`, `internal/certificate/`,
`internal/crypto/`.

**STATUS: CLOSED** — documented in `docs/CRYPTO.md` (Phase 6).
## G10 — OAuth setup and credential provisioning

**Priority:** P1
**Why:** OAuth appears as a flow diagram but there is no operator-facing
guide for obtaining and injecting client credentials, or for the helper
binary.
**Must contain:** Required `.env` variables and how ldflags inject them;
provider registration; the credential helper binary's role; token refresh
and re-authentication behavior; the reauth event.
**Code:** `internal/oauth2/`, `cmd/hsx2mail-creds/`, `app/oauth.go`,
`app/compose.go` (`oauth:reauth-required`).

## G11 — Notifications behavior and permissions

**Priority:** P2
**Why:** Notification routing is described only as a one-line note per
platform. Click-to-focus and permission prerequisites are undocumented.
**Must contain:** When notifications fire; platform channels; click
handling and focus behavior; how notifications are suppressed while the
window is focused.
**Code:** `internal/notification/`, `app/background.go` (new-mail handler).

## G12 — Frontend architecture reference

**Priority:** P2
**Why:** `architecture.md` §10 and `AGENTS.md` §9 sketch the store and
component tree but predate the current runes-based stores and virtualized
list. `analysis/50` holds the detail but is not canonical.
**Must contain:** Store inventory and responsibilities; reactive data flow
from Go events to render; virtualization; layout modes; extension UI
registry.
**Code:** `frontend/src/lib/stores/`, `frontend/src/App.svelte`,
`frontend/src/lib/components/`.

**STATUS: CLOSED** — documented in `docs/FRONTEND.md` (Phase 6).
## G13 — Performance tuning guide

**Priority:** P2
**Why:** The analysis set contains performance findings (batch sizes,
missing composite indexes, index-defeating predicates) but no guide that
tells a maintainer which knobs exist and what their ceilings are.
**Must contain:** Sync batch sizes and why; query patterns to preserve;
known slow paths and their indexes; database pool sizing.
**Code:** `internal/sync/engine.go`, `internal/sync/fetch.go`,
`internal/message/store.go`, `internal/database/database.go`.

**STATUS: CLOSED** — documented in `docs/PERFORMANCE.md` (Phase 6).
## G14 — Testing strategy and known failures

**Priority:** P2
**Why:** `AGENTS.md` §13 gives a package list, but there is no canonical
test guide and `analysis/90` reports that `make test` currently fails on two
migration tests.
**Must contain:** How to run unit and integration tests; fixtures; currently
failing or skipped tests and their status; coverage expectations.
**Code:** `internal/database/database_test.go`, `app/app_test.go`, `Makefile`
(`test` target).

## G15 — Build and packaging reference

**Priority:** P2
**Why:** `BUILD.md` is 49 lines, names the wrong Flatpak artifact, and omits
runtime dependencies and CI. `analysis/80` is the real reference but not
promoted.
**Must contain:** Linux runtime dependencies; dev versus release Flatpak
artifact names; the Flatpak SDK/runtime versions; Windows and macOS paths;
CI gates.
**Code:** `Makefile`, `build/`, `build/flatpak/`.

**STATUS: CLOSED** — documented in `docs/OPERATIONS.md §2, §5, docs/BUILD.md` (Phase 6).
## G16 — Autostart and Linux session integration

**Priority:** P2
**Why:** Autostart exists on all platforms (XDG `.desktop` and the Flatpak
Background portal on Linux) but the only complete description is
`analysis/20`.
**Must contain:** Per-platform mechanism; the settings toggle; the Flatpak
portal path versus the XDG file; failure handling.
**Code:** `internal/platform/autostart_linux.go`, `autostart_darwin.go`,
`autostart_windows.go`, `app/settings.go`.

**STATUS: CLOSED** — documented in `docs/BACKGROUND.md §3, docs/OPERATIONS.md §6.2` (Phase 6).
## G17 — Logging, diagnostics, and log viewer

**Priority:** P2
**Why:** Logging is mentioned in `AGENTS.md` and a log viewer is bound, but
there is no document on levels, components, where logs live, or how to use
the viewer.
**Must contain:** Log locations; debug versus normal levels; component
names; the in-app viewer and its settings.
**Code:** `internal/logging/`, `app/log.go`.

## G18 — Extension storage and capability boundary

**Priority:** P2
**Why:** `EXTENSIONS.md` is strong but the runtime `ext_kv` table and the
runtime-created extension schema are not recorded, and the "not yet
implemented" list is pinned to v0.3.0.
**Must contain:** Per-extension database location; the `ext_kv` table and
lazy migration timing; the `sync.Once` initialization and its cached-error
behavior; which `coreapi` methods remain stubs.
**Code:** `internal/extensions/store.go`, `app/coreimpl.go`,
`internal/core/api/v1/`.

---

## Cross-cutting

- **G19 — Naming decision.** The repository, module, and Flatpak id use
  `Hsx2Mail`; the running product name and most docs use "Email Hub".
  Record the chosen name and the affected rename surface. **Priority:** P2.
- **G20 — Documentation drift test.** A small check, run from `make lint`,
  that compares the generated schema reference (G1) and the version list
  (G2) against source, so the P0 gaps cannot silently recur. **Priority:** P2.
