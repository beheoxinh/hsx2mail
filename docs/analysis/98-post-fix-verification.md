# 98 — Post-fix verification (second adversarial pass)

Scope: verification of the 11 fixes listed against the current working tree,
plus a hunt for defects the fixes introduced.

Method: `codegraph_explore`, direct reads of every touched function, a
programmatic resolution check of every `iconify` icon name in the frontend
against the pinned `@iconify-json/*` packages, `go build ./...`,
`go vet` over all touched packages, `go test` over `app`, `internal/draft`,
`internal/message`, `internal/sync`, `internal/undo`, `internal/tray`, and
`go test -race` over `app` and `internal/undo`.

All build/vet/test gates are green. Every finding below is behavioural.

---

## 1. Per-fix verdict table

| Fix | Verdict | Evidence |
|-----|---------|----------|
| **F-01** iconify collections | **CORRECT (incomplete)** | `frontend/src/lib/iconify-offline.ts:19-26` imports and registers `mdi`, `lucide`, `logos`, `simple-icons`. Prefix census over `frontend/src` + extension frontends: 459 `mdi`, 9 `lucide`, 5 `logos`, 5 `simple-icons` — all four covered. But two *names* still dangle (N6, N7). |
| **F-02** empty-bodies branch | **CORRECT** | `internal/sync/fetch.go:509` declares `emptyRespAttempts := make(map[string]int)` at **function scope**, outside the `for {` at `:558`, so it survives across batches *and* across loop iterations. `:790-794` increments per ID and only inserts into `emptySizes` when `> maxMessageRetries`. `:802` passes the non-nil map. Converges: the loop re-queries candidates at `:624` each iteration, so unflagged IDs return and the counter climbs to 4, at which point they are flagged and drop out. |
| **F-03** `GetDeletedUIDInfo` tx | **CORRECT** | `internal/message/store.go:1107-1122` — chunked loop over `s.db.Query`, no `Begin()`. `querySpecialFolderTypes` now takes `*database.DB` (`:1185`) and is called with `s.db` at `:1164`. Comment at `:1100-1105` records the rationale. |
| **F-04** undo plumbing | **CORRECT** | `suppressUndoPush` has zero references repo-wide. `Push` occurs once, at `app/actions.go:564`, guarded by `if pushUndo`. All 8 call sites correct: `actions.go:382-383` (public wrapper → `true`), `892/928/987/1130/1163` (user actions → `true` via wrapper), `actions.go:1424` (pass-through), `undo.go:176` (`false`). `app/undo.go:22 Peek → :27 Undo() → :31 Discard(cmd)` closes the old `Store(false)`/`Pop()` gap. `internal/undo/undo.go:111-121` `Discard` locks, scans top-down, removes the target, leaves newer entries intact. `NewMoveCommand` returns `&MoveCommand{}` (`commands.go:143`), so the interface comparison at `undo.go:116` is pointer identity — no panic on non-comparable dynamic types. Dead parameter noted at N9. |
| **F-05** virtualizer guard | **CORRECT** | `MessageList.svelte:1288` — `let lastWindow: VirtualItem[] = []` is a **plain `let`, not `$state`**, so the effect reads no reactive dependency from it and cannot self-trigger. `:1331` compares `key` **and** `start` for every row; `:1338` `lastWindow = next.slice()` (defensive copy, kept in lockstep with `virtualRows` so it cannot drift); `:1339` assigns. The effect never *reads* `virtualRows`, so the write does not feed back. No loop, no staleness. |
| **F-06** staging sweep | **REGRESSED** | The sweep itself works (`staging.go:156-191`, wired at `app/app.go:629`) but introduces three new data-loss paths — N1, N2, N3. |
| **F-07** inline cache | **CORRECT** | `inlineAttachmentCache.ts:51-54` subtracts `previous.size` before the add. The invariant `currentBytes == Σ entry sizes` now holds (`evictEntries:71` subtracts `freed`; `setCache` subtracts then adds). `EmailBody.svelte:32` `onDestroy(() => clearCache())`. Ordering nit at N-see F-07 row below. |
| **F-08** tray | **INCOMPLETE** | `app/tray.go:34-40` `syncTray()` is written correctly and does call `tray.Stop()` — but **all three call sites only reach it on the *enable* path**, so `Stop()` is dead code in production and the F-08 symptom is unchanged. |
| **F-09** `sessionLock` mutex | **CORRECT** | `app/app.go:339-340` (`sessionLockMu goSync.RWMutex`, `sessionLock platform.SessionLockMonitor`); write under `Lock` at `background.go:690-692`; reads under `RLock` at `background.go:919-921` (`isSessionLocked`) and `app.go:1068-1070` (shutdown). `isSessionLocked` fails open on a nil monitor (`:922-924`). |
| **F-10** `UpdateContent` guard | **CORRECT w/ regression** | `internal/draft/store.go:140-149` — Go-side `time.Time` comparison, `errors.Is(err, sql.ErrNoRows)` → `ErrDraftConflict`. All three new tests pass and are non-vacuous. But the atomic SQL predicate was dropped from the `UPDATE` (`:159 WHERE id = ?`) — see N5. |
| **F-13** `counters.failed` | **CORRECT** | `internal/sync/fetch.go:806` — `counters.failed.Add(int64(exhausted))`, where `exhausted` counts only IDs past `maxMessageRetries` (`:793`). |

### Detail on the two verdicts that are not clean

**F-08 — `tray.Stop()` is unreachable.** All three toggles gate on `enabled`:

```
app/settings.go:193    if !enabled {
app/settings.go:194        if err := a.settingsStore.SetStartHidden(false); err != nil {
app/settings.go:197        return nil          // <-- early return, syncTray never reached
app/settings.go:198    }
app/settings.go:201    a.syncTray()           // only reachable when enabled == true

app/settings.go:221    if enabled { a.syncTray() }
app/settings.go:285    if enabled { a.syncTray() }
```

The reported defect was "turning **off** both `run_background` and `autostart`
leaves the tray". Turning off is precisely the path that returns early or skips
the call. Correct version: drop the `if enabled` guards and call
`a.syncTray()` unconditionally on both edges —
`SetRunBackground`: move `a.syncTray()` after the `if !enabled { … }` block (and
remove the early `return nil`, or duplicate the call inside it);
`SetStartHidden`/`SetAutostart`: replace `if enabled { a.syncTray() }` with a bare
`a.syncTray()`.

**F-07 — minor ordering nit.** `inlineAttachmentCache.ts:44` runs the eviction
check *before* `:53` subtracts the replaced entry's size, so replacing a large
entry with a small one can evict an unrelated LRU entry that would not have
needed to go. `currentBytes` still stays consistent, so this is cache
efficiency only, not correctness. P3.

---

## 2. New problems

| ID | Sev | File:line | Problem | Mechanism | Confidence |
|----|-----|-----------|---------|-----------|------------|
| **N1** | **P1** | `app/draft.go:378` → `internal/draft/staging.go:52`, `:131-146` | `Remove` deletes a content-addressed blob that a *different* live draft still references | `StagingID(content) = sha256(content)` (`staging.go:52-55`) is a global namespace with no draft scoping, and `Put` dedups on it (`:65-70`). Two drafts attaching identical bytes share one file. `deleteDraftCore` calls `ops.staging.Remove(id)` for every referenced id; `Remove` does `delete(s.files, id)` + `os.Remove(...)` unconditionally — no refcount, no "does any other draft reference this?" check. Deleting draft A destroys draft B's attachment bytes. | HIGH |
| **N2** | **P1** | `internal/draft/staging.go:68-70`, `:182-188` | `SweepOlderThan` deletes blobs that live drafts still reference | `Put` short-circuits for already-staged content (`return id, nil`) and **never calls `os.Chtimes`**, so a blob's mtime is frozen at first staging. `SweepOlderThan(StagingRetention = 7d, staging.go:38)` deletes any file with `ModTime().Before(now-7d)` (`:182-188`). Draft rows live in SQLite indefinitely. Sequence: stage an attachment on day 0 → do not reopen the draft → start the app on day 8 → `app.go:629` sweeps the blob → the still-live draft row's `StagingID` dangles. | HIGH |
| **N3** | **P1** | `internal/smtp/message.go:70` (via `app/draft.go:131-135`) | A missing staged blob yields a **silently zero-byte attachment** at send time | The documented recovery story is false for the current producer. `ComposerAttachment.Data` is legacy, "no longer produced by `PickAttachmentFiles`/`ReadFileAsAttachment`" (`compose.go:35-37`), which set only `StagingID` (`compose.go:576`, `:646`). So `ResolveContent` falls through to `return nil, nil` — **nil error**. `draft.go:131-135` checks only `err != nil`, assigns `resolved[i].Content = nil`, and the send proceeds with an empty attachment. The `SweepOlderThan` doc comment (`staging.go:154-155`) and `draft.go:105-106` both promise an inline fallback that does not exist. Correct version: `ResolveContent` should error when bytes were expected but none resolved — e.g. `if a.Size > 0 && len(a.Content) == 0 { return nil, fmt.Errorf("attachment %q: %d staged bytes missing", a.Filename, a.Size) }` — and `draft.go:131` will then surface it. | HIGH |
| **N4** | **P2** | `internal/tray/tray.go:81-82` (+ `app/tray.go:39,46`) | `Stop()` clears `started` before `systray.Run` unwinds → two concurrent `systray.Run` calls | `Stop` does `systray.Quit(); started.Store(false)`; `Quit` only signals the loop, `Run` returns later on its own goroutine. If `syncTray()` runs again inside that window with `trayWanted()` true, `startTray` passes the `tray.Running()` check (`tray.go:46`) and `Start` wins the CAS (`:39`), launching a second `systray.Run` against the same package-global D-Bus/menu state. Separately, `Stop`'s doc comment "Only used by tests and the shutdown path" (`:76`) is now false. | MEDIUM (needs a fast off→on toggle inside the teardown window) |
| **N5** | **P2** | `internal/draft/store.go:141-149`, `:159` | Moving the guard out of SQL reintroduced a read-then-write race | `SELECT updated_at` (`:141`) and `UPDATE … WHERE id = ?` (`:159`) are separate statements with **no transaction**. Two savers — the main window and a detached composer are separate processes (AGENTS.md §1) — that both run the SELECT before either commits both observe `currentUpdatedAt == previousUpdatedAt`, both pass `!currentUpdatedAt.After(previousUpdatedAt)`, and the second silently overwrites the first. The original SQL predicate made this atomic. Correct version: keep the comparison in Go but bind it — `UPDATE drafts SET …, updated_at = ? WHERE id = ? AND updated_at = ?` with `previousUpdatedAt` as a `time.Time` param, then treat `RowsAffected == 0` as `ErrDraftConflict` (the existing `:177-178` already does). Binding a `time.Time` does not use SQLite date functions, so the NULL/affinity problem that motivated the move does not apply. **Sub-second saves are fine** (nanosecond precision round-trips — `TestUpdateContent_ConsecutiveSavesSucceed` proves it); concurrent interleaving is what regressed. | HIGH on mechanism, MEDIUM on practical impact (autosave is 30 s apart) |
| **N6** | **P2** | `frontend/src/lib/config/providers.ts:110` | `simple-icons:fastmail` does not exist in the pinned collection | Verified programmatically against the installed package: `@iconify-json/simple-icons@1.2.68` has no `fastmail` icon and no alias (`sendinblue` exists instead; no key contains `fastmail`). `addCollection` registers the collection, so `@iconify/svelte` finds no match and renders nothing — and offline there is no network fallback either. The Fastmail provider logo is blank in account setup: the exact F-01 defect at the icon-name level, left unfixed. | HIGH |
| **N7** | **P3** | `frontend/src/lib/components/viewer/ConversationViewer.svelte:1734` | `mdi:key-check` does not exist in `@iconify-json/mdi` | Verified programmatically. Closest names are `key-variant` and `chart-sankey-variant`. Renders blank. 166 of the 167 distinct `mdi:`/`lucide:` names in the tree resolve, so this is the sole outlier. | HIGH |
| **N8** | **P3** | `app/app.go:626-627` vs `app/draft.go:377` | Startup comment now contradicts the code | "Staged attachment blobs are **only ever removed by this sweep**: the same draft can be reopened and re-sent, so **nothing may delete them eagerly**" — but `deleteDraftCore` deletes eagerly. Whichever policy is intended, the comment is wrong; if the eager delete stays (it should, for bounded growth), the comment must change and N1 must be fixed. | HIGH |
| **N9** | **P3** | `app/actions.go:583` | `pushUndo` is a parameter of `moveMessagesToIMAP` but is never read | The signature is `moveMessagesToIMAP(messages, sourceFolderID, destFolder, pushUndo bool)`; the body (`:584-689`) contains **no** reference to `pushUndo`. The push happens in the caller at `:563-565`. Dead parameter, and it makes the fix description ("threaded through `MoveToFolder → moveToFolder → moveMessagesToIMAP`") misleading. | HIGH |
| **N10** | **P3** | `app/actions.go:1418-1424` | One undo entry per source account in a cross-account move | `partitionByAccount` then one `moveToFolder` call per partition, each pushing its own `MoveCommand`. A 3-account move yields 3 undo entries; one `Undo()` reverts one account. **Pre-existing and unchanged by these fixes**, but F-04 touched this function. | HIGH |
| **N11** | **P3** | `internal/undo/`, `internal/draft/staging.go`, `app/tray.go` | No test exercises `Stack.Discard`, `SweepOlderThan`, or `syncTray`/`trayWanted` | Three new primitives landed with zero coverage. The one F-08 defect that survived sits exactly inside the untested `syncTray`. Separately, `app/notify_lock_test.go:92` writes `a.sessionLock` without `sessionLockMu` — harmless today (single-goroutine; `-race` verified clean) but it teaches the wrong pattern next to the code the fix just guarded. | HIGH |

---

## 3. What I verified as correct

**Direct answers to the six hunt questions.**

1. **Did removing `suppressUndoPush` leave a path that pushes an unwanted undo?**
   **No.** `Push` is reachable from exactly one line — `app/actions.go:564`,
   under `if pushUndo`. The only caller passing `false` is `app/undo.go:176`,
   the undo path itself. No IPC or composer file references `undoStack`
   (`app/detached_composer.go`, `app/ipc.go`, `app/draft.go`: zero hits), and
   `CopyToFolder` does IMAP COPY directly without touching the stack. The
   cross-account fan-out passes `pushUndo` through unchanged. The fix is also a
   strict improvement: the parameter cannot leak across goroutines the way a
   process-global `atomic.Bool` did.

2. **Is `lastWindow` safe with Svelte 5 runes? Stale? Looping?**
   Safe, not stale, no loop. It is a plain `let`, so reading it inside `$effect`
   registers no dependency — that is exactly what makes the guard loop-free.
   `lastWindow` and `virtualRows` are written together at `:1338-1339`, so the
   mirror cannot drift from what is rendered. The effect reads `virtualSize`
   (`:1341`) and writes it in the same run; Svelte 5 schedules one extra run,
   then `nextSize === virtualSize` and it settles — at most one redundant run,
   no cycle. Both lines are from the original uncommitted set, not from F-05.
   Residual (P3): `size` is not compared, so a row re-measured *in place*
   (same key, same `start`, new `size`) does not replace `virtualRows`. The old
   guard missed strictly more, so this is not a regression.

3. **Can `SweepOlderThan` delete a staging file a live draft still needs?**
   **Yes — two independent ways.** N2 (frozen mtime; `Put` never refreshes it)
   and N1 (a shared content-addressed blob removed by another draft's delete).
   And the promised escape hatch does not exist for current producers, so both
   escalate to N3: a silent zero-byte attachment rather than a visible failure.

4. **Can `tray.Stop()` tear down a systray another goroutine is using?**
   The `started.Load()` guard (`tray.go:78`) makes a spurious `Stop` a no-op,
   so no live tray is torn down. The real hazard is the opposite direction — see
   N4: `started` is cleared before `systray.Run` returns, allowing a second
   concurrent `Run`.

5. **Is the read-then-write in `UpdateContent` correct when two saves land in
   the same second?**
   Same-*instant sequential* saves: yes, correct — `time.Now()` keeps nanosecond
   precision, the value round-trips through the DATETIME column, and
   `TestUpdateContent_ConsecutiveSavesSucceed` proves four back-to-back saves all
   pass. *Concurrent* saves: no — N5, a genuine TOCTOU introduced by taking the
   predicate out of the `UPDATE`.

6. **Any test that cannot fail, or that asserts the wrong thing?**
   The three new `TestUpdateContent_*` tests are sound. Test 2 writes a strictly
   newer timestamp via raw SQL and would fail if the guard were removed; test 3
   writes the *same instant* with a `+14:00` offset and would fail under a text
   comparison, so it genuinely discriminates the design the fix claims. The gap
   is coverage, not validity — see N11.

**Build, vet, and test gates (all green).**

- `go build ./...` — clean.
- `go vet` over `app`, `internal/sync`, `internal/draft`, `internal/message`,
  `internal/undo`, `internal/tray` — clean.
- `go test` over `app`, `internal/draft`, `internal/message`, `internal/sync`,
  `internal/undo`, `internal/tray` — all pass.
- `go test -race` over `app` and `internal/undo` — pass.

**Specific things confirmed right.**

- F-02's original regression is genuinely gone: with the synthesized non-nil
  `emptySizes` (`fetch.go:802`) plus the `!returned → continue` guard
  (`:389-396`), a one-off empty FETCH no longer loses a body permanently —
  non-exhausted IDs simply stay absent from the map and remain candidates for
  the next cycle.
- `emptyRespAttempts` is function-scoped (`:509`), not per-batch — the single
  thing that would have made the whole fix a no-op. It is not reset inside the
  `for {` at `:558`, and the loop re-queries candidates at `:624`, so the bound
  is reached and the loop terminates.
- `markUnresolvedAsFailed`'s accounting is consistent with the synthesized map:
  only IDs present in `emptySizes` are evaluated, and `fetchedSize{}` yields
  `received == reported == 0`, which `shouldChargeFailure` treats as a genuine
  failure — the intended semantics.
- F-03: `querySpecialFolderTypes` genuinely takes `*database.DB`, so no hidden
  transaction remains; reads are chunk-scoped and self-consistent by design.
- F-04: the `Store(false)` → `Pop()` window is genuinely closed. `Peek` → `Undo`
  → `Discard` never removes the command before success, and `Discard` survives a
  command pushed concurrently during the undo's network round-trip — which is
  precisely the interleaving the old global flag got wrong. Pointer-identity
  comparison is safe because both command constructors return pointers.
- F-07: `currentBytes == Σ entry.size` now holds as an invariant, so the budget
  keeps evicting.
- F-09: every read and the single write of `sessionLock` are under the RWMutex,
  and the nil-monitor case fails open (notifications keep full content), which
  matches the documented non-fatal intent.
- F-13: `counters.failed` now agrees with what is actually persisted — progress
  telemetry no longer reports failures the database does not reflect.
- Icon census: all four icon prefixes used anywhere in the tree (including both
  extension frontends) are registered; all 4 `logos:` and 4 of 5 `simple-icons:`
  names resolve (N6 is the exception).
