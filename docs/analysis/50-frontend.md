# 50 — Frontend Analysis (Svelte 5 / Wails IPC)

Repo: `/mnt/WannaBeTheGuy/WwW/MyOwn/hsx2mail` · Scope: `frontend/src/**`, `frontend/*.config.*`, extension frontends
Method: `codegraph_explore` + targeted grep/read + measured build artifacts. No code changed.

---

## 0. Build / toolchain facts (measured)

| Check | Result |
|---|---|
| `npx svelte-check --tsconfig ./tsconfig.json` | **0 errors, 0 warnings** |
| `npx eslint .` | **exit 0, clean** |
| `npx knip` | 1 unused file, **9 unused deps**, 3 unused devDeps |
| Frontend unit/integration tests | **NONE** — no `*.test.ts`, `*.spec.ts`, no `vitest.config.*`, no `jest.config.*`, no `playwright.config.*`, no `__tests__/` anywhere in `frontend/` |
| CI workflows | **NONE** — `.github/workflows/` does not exist, so lint/svelte-check never gate a merge |
| `dist/` total | 22,128,735 B (21.1 MiB) across 64 files |

### 1.1 Bundle — measured sizes

| Artifact | Raw | gzip | In startup graph? |
|---|---|---|---|
| `dist/assets/SpellSuggestionMenu-D5JkHpZv.js` | **16,928,008 B** | **5,784,783 B** | **YES — `<link rel=modulepreload>` in `dist/index.html`** |
| `dist/assets/main-CQfHsDM1.js` (entry) | 844,070 B | 222,586 B | YES (module script) |
| `dist/assets/SpellSuggestionMenu-ByuIXJM6.css` | 73,891 B | 13,381 B | YES (stylesheet) |
| `dist/assets/isSameWeek-*.js` / `buildMatchPatternFn-*.js` | small | — | YES (modulepreload) |
| `dist/spellcheck/nb.dic.gz` | 1,388,987 B | — | lazy (correct) |
| `dist/spellcheck/cs.dic.gz` | 899,835 B | — | lazy (correct) |
| locale chunks (`vi`, `it`, `de`, `cs`, `en`, `zh-*`) | 12–48 KB each | — | lazy (correct) |

**Root cause of the 16.9 MB chunk** — measured `node_modules/@iconify-json/*/icons.json`:

```
7,289,685  @iconify-json/logos/icons.json
4,722,788  @iconify-json/simple-icons/icons.json
~1,530,000 @iconify-json/mdi/icons.json
  629,678  @iconify-json/heroicons/icons.json
  531,760  @iconify-json/lucide/icons.json
----------
16,267,087  total
```

The chunk contains **16,356 occurrences of `"body":`** (icon SVG path data) — i.e. the entire offline Iconify catalogue is inlined into the eager graph. `main-*.js` statically imports it (`from"./SpellSuggestionMenu-D5JkHpZv.js"`), and `dist/index.html` emits it as `modulepreload`.

Chain: `App.svelte:3` → `lib/iconify-offline.ts:5-9` → `addCollection(...)` ×5 at `:12-16`. Same in `ComposerApp.svelte:3`.

**Every cold start fetches ~5.78 MB compressed before the first interaction.**

### 1.2 `knip` unused-dependency list (verbatim)

```
Unused dependencies (9)
  @tanstack/svelte-virtual   package.json:19:6   <-- virtualization library, never imported
  date-fns-tz                package.json:37:6
  dictionary-cs/de/en/fr/it/nb
  tailwind-variants          package.json:48:6
Unused devDependencies (3)
  @iconify/json, tsconfig/…, …
Unused files (1)
  src/lib/components/kit/SidebarAddItem.svelte
```

---

## 2. Component / store map

### 2.1 Global (module-singleton) stores — `frontend/src/lib/stores/`

| File | Lines | Kind | Backed by |
|---|---|---|---|
| `accounts.svelte.ts` | ~330 | `$state` class, `initEvents()` guarded by `eventsInitialized` | `GetAccounts`, `GetFolderTree`, 5 Wails events |
| `settings.svelte.ts` | ~300 | module `$state` runes + getter/setter fns | `Get*`/`Set*` settings bindings |
| `theme.svelte.ts` | ~90 | module `$state` (`isDarkActive`), `applyTheme()` writes `documentElement[data-theme]` + `.dark` | `GetSystemTheme` (XDG portal) |
| `uiState.svelte.ts` | ~140 | module `$state` + debounced `saveUIState()` | `SetAppState` |
| `layout.svelte.ts` | 72 | module `$state`, two `matchMedia` listeners (`:36-37`) | — |
| `keyboard.svelte.ts` | — | module `$state` (focused pane, flash timer `:57`) | — |
| `toast.ts` | 70 | plain class queue (not rune-based) | — |
| `dialogGuard.ts` | 15 | plain module flag | — |
| `contactPhotos.svelte.ts` | 87 | `$state` map + `inflight` dedupe set; `subscribeOnce()` at `:32` | `contacts:changed` |
| `contactSources.svelte.ts` | — | `$state` | — |
| `imageAllowlist.svelte.ts` | 46 | `$state` + `isImageAllowedSync()` sync read path | `GetImageAllowlist` |
| `inlineAttachmentCache.ts` | 47 | plain `Map` (see §4.4) | — |
| `oauth.svelte.ts` | — | `$state` flow machine | — |
| `extensionRegistry.svelte.ts` | 86 | `$state` rail tabs / settings tabs / context menus | host bridge |
| `extensionDeepLink.svelte.ts` | 37 | `$state` | — |
| `extensionShortcuts.svelte.ts` | 88 | `$state` | — |

### 2.2 Per-component state (not global)

- `App.svelte:70-168` — 20+ `let … = $state(...)`: `selectedAccountId`, `selectedFolderId`, `selectedThreadId`, `focusMode`, `showComposer`, `showCertDialog`, `pendingCertificate`, …
- `MessageList.svelte:50-130` — `conversations`, `totalCount`, `loading`, `offset`, `searchResults`, `checkedThreadIds` (`Set`), `loadGeneration`
- `ConversationViewer.svelte` — `conversation`, `expandedMessages`, `markAsReadTimer`, `refreshTimer`, `cleanupFunctions[]`
- `Composer.svelte` (~2218 lines) — draft state, `saveTimeoutId` (`:296`), `certCheckTimeout` (`:215`)
- `RecipientInput.svelte:37-42` — `inputValue`, `suggestions`, `showSuggestions`, `selectedIndex`, `debounceTimer` (`:43`)
- `ConversationRow.svelte:212-242` — 9 `$derived` values recomputed per row instance
- `EmailBody.svelte:28-43` — `imagesBlocked`, `iframeElement`, `inlineAttachments`, tooltip state

### 2.3 Component tree (from `App.svelte:1490-1665`)

```
App.svelte
├── TitleBar                       {#if getShowTitleBar() && !getNativeTitleBar()}  :1490
├── ExtensionRail                  :1510
├── <div style:display=contents|none>  :1511   <-- mail tree ALWAYS mounted
│   ├── Sidebar (accounts.svelte.ts + FolderTree)
│   ├── <MessageList>              :1570
│   ├── resize handle              :1594
│   └── <ConversationViewer>       :1605
├── Composer                       {#if showComposer && composerAccountId}  :1645
└── SpellSuggestionMenu            :1639  (ALWAYS mounted, pulls 16.9 MB chunk)
```

Extension panes (`ContactsPane`, `CalendarPane`) are `{#if}`-mounted at `:1498` / `:1503`.

---

## 3. Reactive data flow

```
                  ┌──────────── Wails EventsOn (JS bridge → Go Emit) ────────────┐
folder:synced ─────► MessageList:173        ConversationViewer:271   accounts:109
messages:updated ──► MessageList:181        ConversationViewer:263
messages:readCh. ──► MessageList:190        ConversationViewer:172
fts:progress/… ────► MessageList:214-231
theme:system-pref ► App:479
contacts:changed ► contactPhotos:36   ContactsPane:40
                  └──────────────────────────────────────────────────────────────┘
                                        │
                     EventsOff(name) ────┘   ← DESTRUCTIVE, see §4.1
                                        ▼
   ┌─────────────────── module-singleton $state stores ───────────────────┐
   │ accounts.svelte.ts  · settings · theme · uiState · layout · keyboard │
   └────────────────────────────────┬──────────────────────────────────────┘
                                    ▼   getter functions (getThemeMode(), getFocusedPane(), …)
   ┌───────────────────── App.svelte local $state (70-168) ──────────────┐
   │  $effect :64  themeMode → applyThemeFromMode                        │
   │  $effect :91  setComposerOpen(showComposer)                         │
   │  $effect :219/:226  dialog guards                                   │
   │  $derived :…  viewerIsOverlay, layoutMode, responsive               │
   └────────────────────────────────┬──────────────────────────────────────┘
                                    ▼  props (one-way)
              MessageList :1570  ──► ConversationRow ×N  ({#each} :1681 / :1706)
                                    ConversationViewer :1605 ──► EmailBody ──► <iframe srcdoc>
```

### 3.1 Full-array reassignments (Svelte-5 perf killers)

| File:line | Code | Cost |
|---|---|---|
| `MessageList.svelte:395` | `conversations = [...conversations, ...(convList \|\| [])]` | new array + re-key check on every `{#each (conv.threadId + '-' + …)}` (`:1681`) |
| `MessageList.svelte:399` | `conversations = convList \|\| []` | full replace on every folder:synced reload |
| `MessageList.svelte:203-204` | `conversations = conversations` | **no-op** — same `$state` proxy, Svelte 5 `set` short-circuits on `===`; relies entirely on the `c.unreadCount = …` proxy mutation at `:198` to invalidate. Dead code that reads as an invalidation hack. |
| `MessageList.svelte:~420-430` | per-`c` loop mutating `c.unreadCount` | O(N) mutation across the whole loaded array on every `messages:readChanged` event |
| `uiState.svelte.ts:112` | `currentState = { ...currentState, ...updates }` | whole-object spread per save call (debounced) |
| `settings.svelte.ts:150, 264, 278, 289` | whole-object reload on `loadSettings()` | one-shot, acceptable |

`getMessageListDensity` / `getMessageListSortOrder` are plain getter functions over `$state` — reading them inside a template creates a fine-grained dependency, which is correct here.

---

## 4. Findings table

Severity: **P0** blocks/shows wrong state or costs multi-MB at startup · **P1** real bug or major perf · **P2** real but bounded · **P3** smell.

| ID | Sev | File:line | Problem | Mechanism | Fix |
|---|---|---|---|---|---|
| **F-01** | **P0** | `frontend/src/lib/components/list/MessageList.svelte:258-263` | `EventsOff('folder:synced')` etc. wipe **every** listener for those events, not just this component's | Wails `removeListener()` = `delete eventListeners[eventName]` (`wails/v2@v2.12.0/internal/frontend/runtime/desktop/events.js:64-69`) — no per-handler removal. `accounts.svelte.ts:59-60` guards `initEvents()` with `eventsInitialized`, so it **never re-registers** | Use the unsubscribe function `EventsOn` already returns (as `ConversationViewer.svelte:270,284,341,367` correctly does), or namespace the event per component |
| **F-02** | **P0** | `frontend/src/lib/iconify-offline.ts:5-9` | 16.3 MB of Iconify JSON eagerly imported into the entry graph; `dist/index.html` `modulepreload`s the 5.78 MB-gzip chunk on every cold start | 5 × `import … from '@iconify-json/*/icons.json'` + `addCollection()` at `:12-16`, reached from `App.svelte:3` and `ComposerApp.svelte:3` | Generate a subset of only-referenced icons at build time (`unplugin-icons` or a codegen step) and drop `@iconify-json/*` runtime imports. Interim: `import()` the collections lazily inside `addCollection` on first icon render |
| **F-03** | **P0** | whole message list — no `createVirtualizer` anywhere | Message list is **not virtualized**. `@tanstack/svelte-virtual` is a declared dep that is **never imported** (knip confirms) | `MessageList.svelte:1681` / `:1706` `{#each conversations as conv, index (…)}` renders every loaded row; `:1715` `offset += PAGE_SIZE` grows the array without bound. `PAGE_SIZE = 50` (`:77`). No `measureElement`, no `overscan`, no row recycling anywhere in `frontend/src` | Add `createVirtualizer` with a `getScrollElement` on the scroll container, `estimateSize` matching the density-driven row height, and `measureElement` on the row ref. Concurrently: cap the retained window (drop rows far above/below the viewport) |
| **F-04** | **P0** | `App.svelte:389-390` | **Theme flash** on every launch for dark-mode users | Theme is applied only after `await loadSettings()` → `await initTheme(...)`, i.e. after an IPC round trip that happens in `onMount` (post-first-paint). `[data-theme="light"]` lives in `frontend/src/themes/built-in.css:6`, so before `initTheme` runs the document has **no** `data-theme` at all → `--background` undefined → `hsl(var(--background))` in `app.css` is an invalid declaration → white body | Inline a synchronous blocking `<script>` in `index.html` that reads the persisted theme (localStorage mirror written by `applyTheme`, or a value Go injects into the page) and sets `data-theme` + `.dark` before first paint. `index.html:38` already has a `prefers-color-scheme` block for the splash — extend it |
| **F-05** | **P1** | `frontend/src/lib/stores/inlineAttachmentCache.ts:12-13`, written at `EmailBody.svelte:632` | **Unbounded memory leak** — every inline image ever viewed is retained as a base64 data URL for the whole session. `clearCache()` (`:37`) is exported and has **zero call sites** | `Map<messageId, Record<contentId, dataUrl>>`, no eviction (the file comment at `:7` admits "Cache is unlimited (no eviction)"). Backend caps inline images at 5 MB each; 50 e-mails × 1 MB of inline art ≈ 50 MB+ of base64 (≈66 MB as JS UTF-16) pinned in the WebView heap | LRU with a byte budget (e.g. 32 MB) using `Blob`/object URLs instead of base64 strings; call `clearCache()` on folder switch |
| **F-06** | **P1** | `MessageList.svelte:195-199` | Every `messages:readChanged` event mutates **all** loaded conversations' `unreadCount` | `for (const c of conversations) { … c.unreadCount = … }` — O(N) proxy writes where N = total rows loaded (unbounded, see F-03). A bulk "mark all read" on 2,000 loaded rows = 2,000 proxy writes in one task | Maintain a `Map<messageId, conversationIndex>` (or a `Set<conversationId>` per thread) and touch only affected rows; or debounce/coalesce the event into one reload |
| **F-07** | **P1** | `MessageList.svelte:203-204` | `conversations = conversations` is a **no-op** self-assignment | Svelte 5's `$state` setter short-circuits when the new value is `===` the old (same deep proxy). It only appears to "work" because the `c.unreadCount` write at `:198` goes through the proxy. If anyone later "optimises" the proxy writes away, this line silently stops invalidating | Delete the line. If an explicit invalidation is wanted, use a `$state` counter bumped in the handler and read it in a `$derived` |
| **F-08** | **P1** | `App.svelte:1511` `style:display={getActiveExtension() === 'mail' ? 'contents' : 'none'}` | The `display:none` workaround **masks** F-01 rather than fixing it — the comment at `:1505-1509` documents a prior "zombie listener" incident from the same root cause | Because the mail tree is never unmounted, `MessageList`'s `onDestroy` (`:257-268`) never runs today. The instant anyone reintroduces conditional mounting (responsive layouts, a future tab UI, the composer window) F-01 becomes live: sidebar sync spinners stick forever because `accounts.svelte.ts:109` is dead | Fix F-01 properly, then the `display:none` workaround can go |
| **F-09** | **P1** | `MessageList.svelte:236-239` `setInterval(..., 500)` | Dialog-guard poll runs every 500 ms for the component's whole lifetime | `dialogGuardInterval` created in `onMount` (`:236`), cleared in `onDestroy` (`:267`) — correct cleanup, but a permanent 2 Hz timer per list instance | Convert to a one-shot: on dialog close, if `pendingReload` is set, reload. Or drive it from `dialogGuard`'s own state change |
| **F-10** | **P1** | `EmailBody.svelte:88-108` (regex remote-image blocker) | `checkForRemoteImages` (`:69-77`) and the blocking passes **miss** protocol-relative URLs (`//host/pixel.gif`) and `srcset` entirely (`grep -c srcset EmailBody.svelte` → **0**) | Three hand-rolled regexes, all requiring a literal `https?://` prefix. `CSS_REMOTE_URL_PATTERN` (`:67`) likewise. | **Not a privacy leak** — the iframe CSP at `:362` (`img-src 'self' data:` when blocked, `:172`) is the real backstop and it does cover both cases. It *is* a UX bug: the "images blocked" banner won't show for srcset/protocol-relative-only mails, so the user sees missing images with no explanation. Add `//` and `srcset` to the detector, or better: drive the banner off the CSP by having the injected script `postMessage` on a blocked load |
| **F-11** | **P1** | `Composer.svelte:1226-1229` | `handleClose()` cancels the pending debounced draft save **before** showing the confirm dialog | `clearTimeout(saveTimeoutId)` runs, then `// Always show confirmation dialog`. If the user picks **Cancel** in that dialog, the debounce chain is dead — the next keystroke re-schedules, but if they instead clicked a non-input control the unflushed delta stays unsaved. The `hasUnsavedChanges` test compares against `lastContent`, which is only updated *after* the save fires | Flush `saveDraft()` on the Cancel path, or capture and restore the timer |
| **F-12** | **P1** | `RecipientInput.svelte:43, 67` | `debounceTimer` has no `onDestroy` | `setTimeout` at `:67`; `grep -n "onDestroy" RecipientInput.svelte` → no match. Destroy the composer within the debounce window and the timer still fires `api.searchContacts()` against a torn-down component | Add `onDestroy(() => { if (debounceTimer) clearTimeout(debounceTimer) })` |
| **F-13** | **P1** | `App.svelte:483-486` | `mediaQuery.addEventListener('change', …)` with an inline handler, never removed. **App.svelte has no `onDestroy` at all** | 10 `EventsOn` subscriptions (`:294, 337, 344, 349, 354, 364, 369, 374, 479`), one `window.addEventListener('escape-iframe-focus')` (`:386`), one `matchMedia` listener — none cleaned | Low practical impact (root component), but it is the only file that violates the convention. Store the handler refs and add an `onDestroy` |
| **F-14** | **P1** | no `*.test.*` / no `vitest.config` / no CI | **Zero frontend tests, zero CI** | `svelte-check` and `eslint` both pass locally but nothing runs them on merge; `.github/workflows/` absent | Add `vitest` + `@testing-library/svelte` for the stores (they are pure functions over `$state`, trivial to test) and a GitHub Actions job running `npm run lint && npm run check` |
| **F-15** | **P2** | `layout.svelte.ts:36-37` | Two `matchMedia` listeners registered at module scope, never removed | Module singletons live for the app lifetime → safe today | none needed; note it |
| **F-16** | **P2** | `keyboard.svelte.ts:57` `flashTimeoutId` | Module-scope timer for pane flash, never cleared on "reset" | if a new flash starts while one is pending the old id is overwritten → the old timer still fires and clears the new flash early | `clearTimeout(flashTimeoutId)` before reassigning (the file already does this pattern at `:15`) |
| **F-17** | **P2** | `uiState.svelte.ts:108-112` | Whole-object merge + spread on every `saveUIState()` | `currentState = { ...currentState, ...updates }` — every `set`-style call invalidates every `$derived` reading any field | Field-level `$state` per key, or one `$state` object mutated in place (Svelte 5 deep proxies make the spread unnecessary) |
| **F-18** | **P2** | `ConversationRow.svelte:216-242` | 9 `$derived` per row × N unvirtualized rows | `ownMessageIds` (`:216-218`) does `conversation.messages?.map(m => m.id)` when `messageIds` is absent — an array allocation per row per invalidation | Memoize at the list level and pass down as props; N is unbounded per F-03 |
| **F-19** | **P2** | `ConversationViewer.svelte:738-740` | `iframe.contentWindow.postMessage({ type: 'select…' }, '*')` | Target origin `'*'`. The iframe is `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"` (no `allow-same-origin`) so it is an opaque origin and `*` is the only option — but the payload should still be inert data, not commands | Low risk; keep, document why `'*'` is required |
| **F-20** | **P2** | `App.svelte:871-880` | While the composer overlay is open, `handleGlobalKeyDown` returns early for **all** keys and `preventDefault()`s `Ctrl/Cmd+R` | `if (showComposer) { if ((e.ctrlKey\|\|e.metaKey) && ['r','f'].includes(…)) { e.preventDefault(); return } return }` | Intentional (protect the draft) but silently removes the user's only escape hatch. Show a modal "discard draft and reload?" instead of swallowing the key |
| **F-21** | **P2** | `App.svelte:316` `setTimeout(() => messageListRef?.selectThread(…), 100)` | Magic 100 ms race on notification-click deep link | The list may not have loaded the thread yet at t+100 ms | Retry/poll until the thread appears, or have the backend emit the thread id after `folder:synced` completes |
| **F-22** | **P3** | `EmailBody.svelte:412` | `console.log('[EmailBody] Opening URL:', url)` in production | Logs every clicked link (URLs may carry tokens) | Guard behind `import.meta.env.DEV` |
| **F-23** | **P3** | `ui/components/dialog/dialog-content.svelte:45` | `<span class="sr-only">Close</span>` — hardcoded English | Not routed through `$_()`; screen-reader users in the other 9 locales hear "Close" | `{$_('aria.close')}` |
| **F-24** | **P3** | `package.json:19` | `@tanstack/svelte-virtual` declared but unused (F-03) | knip: "Unused dependencies (9)" | Use it (F-03) or drop it |
| **F-25** | **P3** | `package.json:37,48` | `date-fns-tz`, `tailwind-variants` unused | knip | Drop or wire up |

---

## 5. Virtualization verdict (task 2)

**Not implemented.**

- `grep -rn "createVirtualizer|svelte-virtual"` over `frontend/src`, `extensions/*/frontend/src`, `frontend/*.ts`, `frontend/*.js` → **0 hits**.
- `knip` independently reports `@tanstack/svelte-virtual package.json:19:6` as an unused dependency.
- `MessageList.svelte` has `scrollToIndex()` at `:981`, `:1134`, `:1262` — but those drive `rowRefs`/`element.scrollIntoView()` on an already-rendered DOM, i.e. manual "keep the selected row visible", **not** a virtualizer.
- Row height: `ConversationRow.svelte` is a fixed-height row whose height is driven by `getMessageListDensity()` (`settings.svelte.ts:12`, `MessageList.svelte:17`). Because height is **density-determined and not content-determined**, `measureElement` would in fact be unnecessary — a plain `estimateSize` per density would be exact. The absence of virtualization is therefore purely a missing-feature, not a misconfiguration.
- `overscan`: N/A. No virtualizer exists.
- Row recycling / re-render storms: **yes, and worse than recycling** — with no recycling, every "load more" appends 50 brand-new `ConversationRow` component instances, each with 9 `$derived` and its own `$state`, and the parent `{#each}` key is `conv.threadId + '-' + (conv.accountId || accountId)` (`:1681`), so the whole keyed list is diffed on every `conversations = [...]` (`:395`).

---

## 6. Logic bugs & gaps (task 4)

### 6.1 XSS / injection — audited, mostly clean

| Surface | Verdict |
|---|---|
| `{@html highlightedFromName}` `ConversationRow.svelte:338`; `{@html highlightedSubject}` `:390`; `{@html highlightedSnippet}` `:404` | **SAFE.** These come from the Go backend search result (`MessageList.svelte:1630-1632`). `internal/message/store.go:2502-2508` `highlightMatches()` runs `html.EscapeString(text)` **before** wrapping matches in `<mark>` (`:2533-2534`). |
| Message body | Rendered in `<iframe srcdoc>` (`EmailBody.svelte:362, 785`) with `sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"` — **no `allow-same-origin`**, so scripts run in an opaque origin and cannot reach `window.go.*` (the Wails RPC bridge), cookies, or localStorage. CSP at `:362` = `default-src 'self' data:; img-src ${imgSrc}; style-src 'unsafe-inline'; script-src 'unsafe-inline';` — `connect-src` falls back to `default-src`, so no exfiltration channel. `script-src 'unsafe-inline'` is the weak spot, neutralised by the opaque origin. Residual risk: `allow-popups-to-escape-sandbox` lets a hostile mail open an arbitrary `https://` URL in the real browser on one click — standard for mail clients, accepted. |
| `postMessage` from iframe | **Properly validated** — `EmailBody.svelte:439` `if (event.source !== iframeElement?.contentWindow) return`. |
| Link opening | `EmailBody.svelte:411-435 safeOpenURL()` → backend `App.OpenURL` (`app/app.go:1136`) → `isAllowedProtocol()` prefix allowlist `["http://", "https://", "mailto:"]` (`app/app.go:1200-1202`). `javascript:` is rejected. |
| `innerHTML` writes | `Composer.svelte:384`, `composerUtils.ts:49`, `AccountIdentityTab.svelte:198`, `IdentityEditor.svelte:142` — all `DOMParser`/`textContent`-style measurement on the **user's own** signature/draft, not remote content. |
| i18n `{@html}` | none found |

### 6.2 Memory / timer leaks

| What | Where | Status |
|---|---|---|
| Inline-attachment base64 cache | `inlineAttachmentCache.ts:12-13`, `EmailBody.svelte:632` | **LEAK** — no eviction, `clearCache()` never called (F-05) |
| `EventsOn` unsubscribes | `MessageList.svelte:258-263` | **LEAK + collateral damage** (F-01) |
| `App.svelte` listeners | `:294-479`, `:386`, `:483` | no `onDestroy` (F-13) |
| `MessageList` dialog-guard interval | `:236` / cleared `:267` | OK |
| `ConversationViewer` timers | cleared `:355-365` | OK |
| `Composer` save/cert timers | cleared `:587-588`, `:215`, `:1238` | OK (see F-11 for the logic bug) |
| `RecipientInput` debounce | `:67`, no cleanup | **LEAK** (F-12) |
| `layout.svelte.ts` matchMedia | `:36-37` | module scope, OK |
| `keyboard.svelte.ts` flash timer | `:57` | minor (F-16) |

### 6.3 Stale state / optimistic-update races

- `MessageList.svelte:190-210` — the `messages:readChanged` handler applies an **optimistic** `unreadCount` delta (`:198`) and buffers into `pendingFlagChanges` (`:208`) only when `loading` is true. But `loadConversations()` can start **after** the buffer check and **before** the array lands, and the `pendingFlagChanges` drain at `:404-412` runs *before* `lastLoadedFolderId` is updated — a `flagsChanged` event arriving in the window between the `await` and the drain is silently lost. `loadGeneration` (`:56`) only guards folder switches, not this.
- `MessageList.svelte:203` — the no-op self-assignment (F-07).
- `accounts.svelte.ts:59-60` + F-01 — after any `MessageList` unmount, `folders:countsChanged` / `folder:synced` never reach the store again → **sidebar unread counts and sync spinners go permanently stale**. Because of the `display:none` workaround (F-08) this is latent, not live.

### 6.4 Responsive / layout

- `App.svelte:1511` — the whole mail tree is `display: contents` / `display: none`. `display: contents` on the wrapper removes its own box, so the flex children become direct flex items of `:1495`. Any child using `gap` on the wrapper, or a CSS child-combinator that expects the wrapper to exist, breaks. It works today only because the wrapper has no visual styles.
- `layout.svelte.ts:36-37` — `narrow`/`medium` `matchMedia` drive `getLayoutMode()`; the two listeners are added at module scope, so the store is correct but untestable.
- `Viewer` overlay: `App.svelte:100` `viewerIsOverlay = $derived(isResponsive() || focusMode !== 'off')` — in narrow mode the list and viewer both claim `flex-1`; correctness depends entirely on the CSS at `:1601-1604` (`.responsive-viewer-overlay`). Fragile but not demonstrably broken.

### 6.5 i18n — exact key counts (task 4)

**`frontend/src/lib/i18n/locales/` (main app, 10 locales)**

```
en baseline (leaf keys)      920
cs 913  MISSING 7      de 913  MISSING 7
en 920  OK             fr 913  MISSING 7
it 913  MISSING 7      nb 913  MISSING 7
vi 913  MISSING 7      zh-CN 913  MISSING 7
zh-HK 913 MISSING 7    zh-TW 913  MISSING 7
EXTRA keys in non-en locales: none
Type mismatches vs en: none
```

**The same 7 keys are missing from all 9 non-en locales** (net −63 keys):

| # | Missing key | Used at |
|---|---|---|
| 1 | `account.every10Min` | `lib/config/providers.ts:280` (secondary sync-interval dropdown option label) |
| 2 | `account.secondaryAuto` | `lib/config/providers.ts:278` |
| 3 | `account.secondarySyncInterval` | `components/settings/account/AccountServerTab.svelte:613` |
| 4 | `account.secondarySyncIntervalHelp` | `components/settings/account/AccountServerTab.svelte:630` |
| 5 | `account.secondarySyncOptions` | (group heading in the same tab) |
| 6 | `settingsGeneral.showMessageListProfilePics` | `components/settings/SettingsDialog.svelte` (General tab) |
| 7 | `settingsGeneral.showMessageListProfilePicsHelp` | same |

→ In cs/de/fr/it/nb/vi/zh-CN/zh-HK/zh-TW the **Secondary Sync Interval** dropdown renders raw keys or empty labels, and the **Show contact photos in message list** toggle renders untranslated. Confirmed live, not dead keys.

**`extensions/calendar/frontend/i18n/locales/`** — en 312, all 10 locales 312, **0 missing, 0 extra**.
**`extensions/contacts/frontend/i18n/locales/`** — en 139, all 10 locales 139, **0 missing, 0 extra**.

Plus one hardcoded English screen-reader string: `ui/dialog/dialog-content.svelte:45`.

---

## 7. UX / accessibility inventory (task 5)

### 7.1 Present and working

| Capability | Evidence |
|---|---|
| Virtualized list | **ABSENT** (F-03) |
| Multi-select / bulk | `MessageList.svelte:73-74` `checkedThreadIds` + `lastClickedIndex` (shift-range); row checkboxes; bulk mark-read/star/archive/trash |
| Drag & drop | `ConversationRow.svelte:246+` `dataTransfer` stashes `messageIds` + `sourceAccountId`; folder nodes are drop targets → `MoveToFolder()` |
| Right-click context menus | `MessageList.svelte` + `components/common/MessageContextMenu.svelte`; viewer-aware (`ConversationRow.svelte:240-242`) |
| Search-as-you-type | Server-side FTS with 50-per-page paging (`SERVER_SEARCH_LIMIT = 200`, `MessageList.svelte:130`), FTS index progress bar (`:214-231`) |
| Dark mode | 13 theme files in `frontend/src/themes/` + `prefers-color-scheme` fallback; system-theme via XDG Settings Portal (`App.svelte:479-486`) — but see F-04 |
| Contact photos in list | `showMessageListProfilePics` setting + `contactPhotos` store with in-flight dedupe |
| Focus pane navigation | `stores/keyboard.svelte.ts` + `<svelte:window onkeydown>` (`App.svelte:1486`), Alt+arrow/hjkl, `setFocusedPane`/`focusPreviousPane`/`focusNextPane` also wired **inside the iframe** (`EmailBody.svelte:447-473` re-dispatches synthetic `KeyboardEvent`s to `window`) |
| Focus trap in dialogs | bits-ui `Dialog` primitive (`lib/components/ui/dialog/dialog-content.svelte`) — trap is built in; `onCloseAutoFocus` overridden at `:31` |
| Dialog/shortcut guard | `stores/dialogGuard.ts` + `App.svelte:867-871`; `MessageList.svelte:236` defers reloads while a dialog is open |
| Spellcheck | 6 dictionaries, `.dic.gz`/`.aff.gz` **lazily** fetched from `dist/spellcheck/` (correct) |
| Detached composer | separate window, IPC, standalone `ComposerApp.svelte` |

### 7.2 Missing / weak — concrete list

1. **Virtualized message list** (F-03) — the single biggest UX/scroll defect.
2. **No `svelte-window` / `aria-live` announcement** for sync completion, bulk-action results, or "N conversations loaded" — 124 aria attributes exist across the app but there is no live region; screen-reader users get no feedback for background events.
3. **`dialog-content.svelte:45` hardcoded "Close"** (F-23) — untranslated in 9 locales.
4. **No reduced-motion handling** — `app.css:16` sets `transition: background-color 0.2s ease, color 0.2s ease` globally on `*` with no `@media (prefers-reduced-motion: reduce)` guard anywhere in `frontend/src`.
5. **No `:focus-visible` ring audit** — keyboard navigation exists but a visible focus indicator is not guaranteed for the custom rows/rows in `ConversationRow.svelte`.
6. **Keyboard shortcut coverage** — `docs/KEYBOARD_SHORTCUTS.md` documents **94 shortcut rows**; the implementation contains only **30 distinct `key ===` literals** across all of `frontend/src` + both extensions. A heuristic cross-check (normalising Cmd→Ctrl, ⌥→Alt, ↑→ArrowUp, and multi-key cells) leaves the following documented bindings with **no matching key literal** in code — each needs manual confirmation:
   - `Ctrl+U` "Mark as read" vs `Ctrl+U` "Underline" (composer) — two different bindings for the same chord in two contexts; the composer path is shadowed if the global handler does not bail first.
   - `Alt+Enter` / `Space` "Expand/collapse account" (sidebar) — no `altKey`+`Enter` literal found.
   - `Alt(L)+Alt(R)` and `Alt(R)` context-menu chords — implemented as `leftAltHeld` state (`App.svelte:857`) + a right-click path, so the doc's phrasing is accurate but there is **no `AltRight` literal**; the chord relies on `e.code === 'AltLeft'` only.
   - `Up`/`K`, `Down`/`J` in three different panes (folder tree / message list / viewer) — only a subset of the letters appears as a literal.
   - `Backspace`/`Delete` "Move to trash" — `delete` is not in the code's key-literal set.
7. **No swipe gestures** on touch/narrow mode (kinto-style) — arguably out of scope for a desktop client, but `App.svelte` has a `narrow` layout with no touch affordances beyond tap.
8. **No `aria-current` / `aria-selected` on list rows** — `ConversationRow.svelte` has no `role="option"`/`aria-selected`, so the message list is not exposed as a listbox/grid to AT.
9. **Composer is not a real modal** — `App.svelte:1645` `{#if showComposer …}` renders an overlay without `role="dialog"`/`aria-modal`; no focus trap, and `handleGlobalKeyDown` bails entirely (F-20).
10. **No undo toast affordance in the list** for bulk actions beyond the 30 s queue (`internal/undo`), and no per-row "undo" button.

---

## 8. Build config notes (task 6)

- `frontend/vite.config.ts` — no `build.rollupOptions.output.manualChunks` at all; no `build.chunkSizeWarningLimit` override; no `reportCompressedSize` tuning. The 16.9 MB chunk is the *default* Rollup shared-chunk assignment given the eager Iconify import. Aliases `$lib`, `$`, `$extensions`, `$wailsjs` + per-extension npm dep aliasing (documented at `:14-20`).
- `frontend/tailwind.config.js` — content globs include `extensions/*/frontend/src` (needed for the extension panes). `dist/assets/SpellSuggestionMenu-ByuIXJM6.css` (73.9 KB raw / 13.4 KB gz) is the full compiled Tailwind output; it is emitted as a separate stylesheet but is in the eager graph.
- `frontend/eslint.config.js` — clean, includes `@stylistic` + svelte rules. Passes with 0 findings, which is itself a finding (no test runner, no CI).
- `frontend/knip.json` — configured; running it is what surfaced F-24/F-25.
- No `sourcemap` config; `dist` has no `.map` files → production stack traces from the 5.78 MB chunk will be unreadable.
- Iconify offline mode: `@iconify/svelte` v4 is used with `addCollection` (offline) rather than the HTTP API. That is the right call for an air-gapped mail client — it just needs to be **subsetted at build time**, not shipped whole.
- `dictionary-{cs,de,en,fr,it,nb}` are npm packages consumed only by `frontend/scripts/copy-dictionaries.mjs` (hence knip's "unused").

---

## 9. Test gaps (task 7)

| Item | Status |
|---|---|
| Unit tests (stores, utils) | **0** |
| Component tests | **0** |
| E2E (Playwright/Cypress) | **0** |
| Test runner configured | **none** (`vitest.config.*`, `jest.config.*`, `playwright.config.*` all absent) |
| `npm test` script | **absent** from `package.json` scripts (`predev`, `dev`, `prebuild`, `build`, `preview`, `check`, `lint`, `lint:fix`, `knip` only) |
| `svelte-check` | present, passes 0/0 — but never run in CI |
| `eslint` | present, passes — never run in CI |
| `knip` | present, **not in any npm script that gates** and reports 13 issues |
| CI | **no `.github/workflows/`** |

**Highest-value tests to add first** (pure, framework-free, no DOM needed):
1. `i18n` locale parity — the script in this analysis, as a unit test. Would have caught the 7 missing keys (F-14).
2. `highlightMatches` parity: a Go test asserting every `ConversationSearchResult.highlighted*` is the `html.EscapeString` of its source with only `<mark>` added — locks down the `{@html}` sites in `ConversationRow.svelte`.
3. `accounts.svelte.ts` event lifecycle: mount/unmount a `MessageList` twice and assert `EventsOn('folder:synced')` still resolves to the store's handler (F-01).
4. `isAllowedProtocol` table test for `javascript:`, `data:`, `file:`, `//host` (already exists in Go — verify it covers the prefix form used by the frontend).

---

## 10. Fix order

1. **F-02** (Iconify subsetting) — one build-config change removes 5.78 MB from every cold start. Highest ratio of bytes saved to risk.
2. **F-01** (per-handler `EventsOff`) — small diff, removes a live landmine and a permanent-staleness class of bug.
3. **F-04** (blocking theme script in `index.html`) — ~10 lines, removes the dark-mode flash.
4. **F-03** (virtualization) — the only genuinely large piece of work; do it after 1-3 so profiling is on a sane bundle.
5. **F-05** (LRU on the inline-attachment cache) — bounds a session-long leak.
6. **F-14** (vitest + CI) — otherwise 1-5 can regress silently.
7. i18n 7 keys, F-11, F-12, F-07, F-06, then P2/P3.
