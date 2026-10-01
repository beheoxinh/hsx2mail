# 95 — Audit of Pre-Existing Documentation

Read-only audit of the documentation that predates the 8-part
`docs/analysis/` set. No source or pre-existing doc was modified; the only
files written are this report and `docs/GAPS.md`.

## 1. Scope and method

Documents audited:

| Document | Lines |
|----------|-------|
| `docs/architecture.md` | 677 |
| `docs/business-logic.md` | 1251 |
| `docs/EXTENSIONS.md` | 2353 |
| `docs/EXT_RULES.md` | 209 |
| `docs/KEYBOARD_SHORTCUTS.md` | 305 |
| `docs/BUILD.md` | 49 |
| `docs/PRIVACY.md` | policy prose |
| `docs/LANGUAGE.md` | translation guide |
| `docs/CASAT2.md` | 503 |
| `docs/SQL_ROLLBACK.md` | rollback runbook |
| `docs/RELEASE.md` | 8 |
| `docs/TERMS.md` | legal prose |
| `AGENTS.md` (repo root) | 782 |

Method. Each document was read for structure (heading extraction), then
specific factual claims (paths, symbols, version numbers, migration counts,
constants, IPC identifiers, event names, table names) were extracted with
targeted search and each was checked against the real source. At least
twenty concrete claims were verified by direct inspection. Legal and policy
prose (`PRIVACY.md`, `TERMS.md`) plus the translation workflow
(`LANGUAGE.md`) were assessed for consistency only, since they assert no
verifiable code facts.

## 2. Per-document verdict

| Document | Verdict | Notes |
|----------|---------|-------|
| `docs/architecture.md` | **Stale (high priority)** | Migration map and count wrong (`v1 → v39`, undo table at v20); single-instance transport wrong; keyring library misnamed. Overall shape still useful. |
| `docs/business-logic.md` | **Good, minor caveat** | Sync, compose, undo (in-memory, 50/30s), OAuth, extension flows match source. One "minimize to tray" phrasing where no tray exists. |
| `docs/EXTENSIONS.md` | **Good** | The authoritative extension reference. Current to v0.3.0; some sections still label v0.3.0 as the active release and describe planned work. |
| `docs/EXT_RULES.md` | **Good, minor caveat** | Rules match the extension store implementation. Rule R32 expects a `rollback-v<latest>-to-v30.sql`; only the v39 file exists. |
| `docs/KEYBOARD_SHORTCUTS.md` | **Good** | Predicates in `frontend/src/lib/keyboard/shortcuts.ts` back the documented bindings (j/k, Shift+J/K, arrows). |
| `docs/BUILD.md` | **Thin / partly stale** | Covers the `.env` + Makefile happy path only. Names the wrong Flatpak artifact and omits CI, version bumps, and runtime deps. |
| `docs/PRIVACY.md` | **OK (policy)** | No code facts to verify. Dated, but content is standalone legal text. |
| `docs/LANGUAGE.md` | **OK (workflow)** | Translation contribution guide; consistent with the i18n tree. |
| `docs/CASAT2.md` | **Good, one error** | Accurate security questionnaire. Windows single-instance is described as a named mutex; the code uses a fixed loopback TCP port plus a lock file. |
| `docs/SQL_ROLLBACK.md` | **Stale scope** | Correct and detailed for v39 → v30 (0.3.0 → 0.2.5). No rollback path exists for the newer v40 or v41 migrations. |
| `docs/RELEASE.md` | **Stale / broken** | Instructs the releaser to edit `CHANGELOG.md`, which does not exist in the repository. |
| `docs/TERMS.md` | **OK (legal)** | No code facts to verify. |
| `AGENTS.md` | **Stale (high priority)** | Several structural claims are wrong: migration count, the whole §4 table map, a non-existent `undo_commands` table, single-instance D-Bus, and a wrong certificate table name. |

## 3. Stale-claim detail

Format: `doc:line` → claimed fact → correct fact → `source:line`.

### Migration count

| Claim location | Claimed | Correct | Source |
|----------------|---------|---------|--------|
| `docs/architecture.md:69` | `db.Migrate() — v1..v39` | latest is v41 | `internal/database/migrations.go:1319` |
| `docs/architecture.md:170` | `migrations (v1-v39)` | v1..v41 | same |
| `docs/architecture.md:199` | heading `Database Migrations (v1 → v39)` | v1 → v41 | same |
| `docs/architecture.md:219` | `v21-v39` refinements | v21-v41 | same |
| `AGENTS.md:118` | `migrations (v1..v39)` | v1..v41 | same |
| `AGENTS.md:175` | `db.Migrate (v1..v39, ...)` | v1..v41 | same |
| `AGENTS.md:706` | `Migrations (v1..v39)` | v1..v41 | same |

### Schema / table map

| Claim location | Claimed | Correct | Source |
|----------------|---------|---------|--------|
| `docs/architecture.md:205` | v1 = accounts, identities, folders | v1 also creates messages, attachments, drafts | `internal/database/migrations.go:89,145,159` |
| `docs/architecture.md:206`, `AGENTS.md:254` | `messages` added v2 | created v1 | `internal/database/migrations.go:89` |
| `docs/architecture.md:207`, `AGENTS.md:255` | `attachments` added v3, "inline content" | created v1; content column added at runtime by `ensureContentColumn` | `internal/database/migrations.go:145`; `internal/message/attachment_store.go:20,24-33` |
| `docs/architecture.md:208`, `AGENTS.md:256` | `contacts` added v4 | no `contacts` table until v27; unified `contact_records` | `internal/database/migrations.go:817,827` |
| `docs/architecture.md:210`, `AGENTS.md:258` | `app_state` added v6 | added v13 | `internal/database/migrations.go:380,384` |
| `docs/architecture.md:211`, `AGENTS.md:259` | `image_allowlist` added v7 | added v16 | `internal/database/migrations.go:466,471` |
| `docs/architecture.md:212`, `AGENTS.md:260` | `certificates` (TOFU) added v8 | table is `trusted_certificates`, added v19 | `internal/database/migrations.go:509,513` |
| `docs/architecture.md:214`, `AGENTS.md:261` | `contact_sources` added v15 | added v6 | `internal/database/migrations.go:257,260` |
| `docs/architecture.md:216` | `contact_records` at v18 | v27 | `internal/database/migrations.go:701,827` |
| `AGENTS.md:270` | `contact_records (v18+)` | v27 | same |
| `docs/architecture.md:218`, `AGENTS.md:263` | v20 = `undo_commands` table | v20 = S/MIME raw body + encrypted draft body; no undo table anywhere | `internal/database/migrations.go:577-589`; `internal/undo/undo.go:42-71` |
| `AGENTS.md:513` | fingerprints in `certificates` table | `trusted_certificates` | `internal/database/migrations.go:513` |

### Platform / infrastructure

| Claim location | Claimed | Correct | Source |
|----------------|---------|---------|--------|
| `docs/architecture.md:34`, `AGENTS.md:167` | single-instance uses Unix socket **or D-Bus** on Linux | Unix domain socket only; no D-Bus in the lock path | `internal/platform/singleinstance_linux.go:68` |
| `docs/CASAT2.md:192` | Windows uses a named mutex | fixed loopback TCP port + lock file | `internal/platform/singleinstance_windows.go:16-20` |
| `docs/architecture.md:536` | Linux keyring "via golang-keyring" | `github.com/zalando/go-keyring` | `internal/keyring/keyring.go:7` |
| `docs/business-logic.md:815` | `run_background` = "minimize to tray" | keep-running-when-window-closed flag; no tray exists | `app/app.go:952-958`; `app/app.go:934` ("future tray menu") |

### Build / release

| Claim location | Claimed | Correct | Source |
|----------------|---------|---------|--------|
| `docs/BUILD.md:24` | install `build/bin/Hsx2Mail.flatpak` | dev script emits `Hsx2Mail-dev.flatpak`; release variant is `Hsx2Mail-<version>.flatpak` | `build/flatpak/build-flatpak.sh:65,71` |
| `docs/RELEASE.md:7` | edit `CHANGELOG.md` | no `CHANGELOG.md` exists; release notes live inside the Flatpak metainfo | repository file listing; `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml:548` |
| `docs/EXT_RULES.md:149` | rollback SQL is `rollback-v<latest>-to-v30.sql` | only `rollback-v39-to-v30.sql` exists; latest schema is v41 | `tools/db/` listing |
| `build/flatpak/...metainfo.xml:548` | newest AppStream release is 0.1.34 | app version is 0.3.2 | `app/state.go:30` |

### Verified-correct claims (selected)

| Claim | Source |
|-------|--------|
| Version 0.3.2 | `app/state.go:30`; `wails.json:12`; `frontend/package.json:4` |
| IPC types `message_sent`, `draft_saved`, `draft_deleted`, `composer_ready`, `composer_closed`, `theme_changed`, `shutdown` | `internal/ipc/message.go:12-33` |
| Events `app:ready`, `folder:synced`, `composer:messageSent`, `oauth:reauth-required` | `app/app.go:779`; `app/sync.go:146`; `app/ipc.go:112`; `app/compose.go:86` |
| Header batch 50; body batch 512KB; MIME part 10MB; raw 50MB; inline 5MB | `internal/sync/engine.go:32,37,45,46,47` |
| SyncFolder debounce 500ms | `app/sync.go:23` |
| FTS indexing starts after 5s | `app/app.go:813` |
| WAL checkpoint interval 5min | `internal/database/database.go:37` |
| Undo stack 50 commands / 30s, in-memory | `app/app.go:711`; `internal/undo/undo.go:50-71` |
| D-Bus sleep/wake, network, theme monitors | `internal/platform/sleep_linux.go:49`; `network_linux.go:11`; `theme_linux.go:14-21` |
| `StartHidden` + preflight before `wails.Run` | `main.go:119,142`; `preflight.go:12-22` |

## 4. Coverage matrix

Depth legend: **Full** = a reader can implement from it; **Partial** = main
path only; **Missing** = absent; **Wrong** = present but materially
incorrect.

| Topic | Documented where | Depth |
|-------|------------------|-------|
| Startup sequence | `architecture.md` §2; `AGENTS.md` §3; `analysis/20`,`analysis/70` | Partial (wrong migration count in both old docs) |
| Background sync + IDLE | `architecture.md` §5; `business-logic.md` §2; `analysis/10`; `analysis/70` | Full |
| Linux autostart | `analysis/20`; `AGENTS.md` §3 bullet | Partial (only analysis 20 is complete) |
| Single-instance | `architecture.md` §1; `AGENTS.md` §3; `CASAT2.md:192` | Wrong (transport described incorrectly in architecture/AGENTS/CASAT2) |
| Schema + migrations | `architecture.md` §4; `AGENTS.md` §4; `SQL_ROLLBACK.md`; `analysis/60` | Wrong (old map); full in `analysis/60` |
| Threading | `architecture.md` §5; `business-logic.md` §2; `analysis/10`,`analysis/40` | Full |
| FTS search | `business-logic.md` §10; `analysis/40` | Full |
| Compose / draft / send | `architecture.md` §6; `business-logic.md` §4-5; `analysis/30` | Full |
| Undo | `business-logic.md` §11; `analysis/30` | Partial (correct in business-logic; wrong in architecture's migration table) |
| Attachments | `architecture.md` §5; `business-logic.md`; `analysis/30`,`analysis/40` | Partial |
| OAuth2 | `architecture.md` §7; `business-logic.md` §13; `analysis/70` | Full |
| Keyring fallback | `architecture.md` §9; `CASAT2.md`; `analysis/70` | Partial (library name wrong in architecture) |
| TOFU certificates | `architecture.md` §9; `business-logic.md`; `analysis/70` | Partial (table name wrong) |
| PGP | `business-logic.md` §6; `analysis/70` | Partial |
| S/MIME | `business-logic.md` §6; `analysis/70` | Partial |
| Extension API | `EXTENSIONS.md`; `EXT_RULES.md`; `AGENTS.md` §7; `analysis/70` | Full |
| IPC protocol | `architecture.md` §1; `AGENTS.md` §10; `business-logic.md` §14; `analysis/70` | Full |
| Notifications | `business-logic.md` §12; `AGENTS.md`; `analysis/70` | Partial |
| Frontend stores | `architecture.md` §10; `AGENTS.md` §9; `analysis/50` | Full |
| Keyboard shortcuts | `KEYBOARD_SHORTCUTS.md`; `analysis/50` | Full |
| Build + Flatpak | `BUILD.md`; `AGENTS.md` §12; `analysis/80` | Partial (old docs thin; wrong artifact name) |
| Ops runbook | `RELEASE.md`; `SQL_ROLLBACK.md`; `CASAT2.md` §14-15; `analysis/80` | Partial (release steps broken; rollback scoped to v39) |
| Testing strategy | `AGENTS.md` §13; `analysis/90` | Partial (no canonical test doc; two migration tests currently fail per `analysis/90`) |
| Performance tuning | `analysis/10`,`analysis/40`,`analysis/50`,`analysis/60` | Partial (findings only; no tuning guide) |

## 5. Duplication and contradiction

| Item | Where it appears | Conflict |
|------|------------------|----------|
| Migration count | `architecture.md:69,170,199,219`; `AGENTS.md:118,175,706` say v39. `analysis/60` and `analysis/90` say 41. | Old docs wrong; analysis correct (`migrations.go:1319`). |
| Undo persistence | `architecture.md:218` and `AGENTS.md:263` claim a persisted `undo_commands` table. `business-logic.md` §11 describes an in-memory stack. | business-logic correct; the other two false. No undo table exists. |
| Single-instance transport | `architecture.md:34` and `AGENTS.md:167` say "Unix socket + D-Bus on Linux". `CASAT2.md:192` says Unix socket on Linux/macOS. Code uses a Unix socket only. | CASAT2 correct for Linux; architecture/AGENTS wrong. CASAT2 then errs on Windows (mutex vs TCP port). |
| Certificate table name | `architecture.md:212` and `AGENTS.md:260,513` use `certificates`. Code uses `trusted_certificates`. | Old docs wrong. |
| Attachments version and storage | `AGENTS.md:255` says v3 with inline content. Code creates the table at v1 and adds the `content` column at runtime. | Wrong version; runtime column is undocumented in every pre-existing doc. |
| Sync polling default | `AGENTS.md` §5 diagram says "30-60 min default". Account migration default is 30 minutes; 60 is a separate legacy column default. | Imprecise. |
| Product naming | Repo, module, Flatpak id, `main.go` `ProgramName` use `Hsx2Mail`. `wails.json:11`, `app/state.go:44`, `main.go:136`, and virtually every doc use "Email Hub". | Not a pure doc bug: the runtime product name is still "Email Hub", so docs match the app while `AGENTS.md` matches the repo. Project-wide rename is incomplete. |
| Startup-sequence detail | `AGENTS.md` §3 and `architecture.md` §2 duplicate the same sequence; both carry the stale migration count. | Duplication that guarantees future drift. |
| Extension rules | `EXT_RULES.md` restates rules already in `EXTENSIONS.md`. | Low-risk duplication; R32 naming is the stale copy. |
| Rollback guidance | `SQL_ROLLBACK.md` and `EXT_RULES.md:149` describe rollback. | Both assume v39 is latest. |

### False `AGENTS.md` structural claims (verified)

1. Migration count `v1..v39` — actually v41 (three locations, lines 118, 175, 706).
2. §4 table maps `messages` to v2, `attachments` to v3, `contacts` to v4, `app_state` to v6, `image_allowlist` to v7, `certificates` to v8, `contact_sources` to v15 — all wrong (lines 254-261).
3. `undo_commands` table added v20 — no such table; v20 is S/MIME and encrypted-draft columns (line 263).
4. Single-instance "Unix socket + D-Bus on Linux" — Unix socket only (line 167).
5. "certificates table" for TOFU fingerprints — `trusted_certificates` (line 513).
6. `contact_records (v18+)` — v27 (line 270).
7. `attachments` "inline content" attributed to v3 — table is v1, content column added at runtime (line 255).

## 6. Recommended restructuring

The corpus suffers from three problems: duplicated architecture prose
(`architecture.md`, `AGENTS.md`, `business-logic.md`), version-pinned facts
embedded in narrative that nobody bumps on release, and the new
`analysis/` material not yet merged back into the canonical docs. A
concrete plan:

1. **Single source of truth for schema.** Create `docs/DATABASE.md` owning
   the table-to-migration map. Generate the table with a script from
   `internal/database/migrations.go` so it cannot drift. Delete the
   migration tables in `architecture.md` §4 and `AGENTS.md` §4 and link to
   the new file.

2. **Single source of truth for lifecycle.** Merge `architecture.md` §2 and
   `AGENTS.md` §3 into one `docs/ARCHITECTURE.md` startup section and
   replace the other with a link. Correct the single-instance transport and
   the keyring library.

3. **Fold the analysis set into canonical docs.** Promote `analysis/10`,
   `30`, `40`, `50`, `60`, `70` into topic chapters of `business-logic.md`
   or new files (`SYNC.md`, `COMPOSE.md`, `FRONTEND.md`, `PLATFORM.md`).
   Keep `analysis/` as the raw investigation record, referenced by stable
   anchors.

4. **Extract a version-agnostic operations doc.** Rework `RELEASE.md` into
   `docs/OPERATIONS.md` covering version bumps across all four locations
   (`app/state.go`, `frontend/package.json` and lock, metainfo, `wails.json`),
   the CI gates, and rollback. Fix or remove the `CHANGELOG.md` step and
   extend `SQL_ROLLBACK.md` to v41.

5. **Add a build reference.** Expand `BUILD.md` with runtime dependencies,
   the dev vs release Flatpak artifact names, and a pointer to
   `analysis/80`.

6. **Reconcile naming.** Decide whether the product is "Email Hub" or
   "Hsx2Mail" and apply it consistently across `wails.json`, `app/state.go`,
   `main.go`, the Flatpak id and name, and every doc.

7. **Add a documentation test.** A small script that asserts the migration
   count and table map in the generated `DATABASE.md` match
   `migrations.go`, wired into `make lint`, prevents recurrence.

Missing topics are enumerated in `docs/GAPS.md`.
