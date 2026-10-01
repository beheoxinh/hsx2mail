# FRONTEND — Svelte 5 architecture

**Status: canonical.** This file owns the store graph, the component tree, the
virtualized list, the event plumbing and the layout modes. It replaces the frontend
sections of `architecture.md` and `business-logic.md`, which describe an older shape.

---

## 1. Stack

| Concern | Choice | Version |
|---|---|---|
| Framework | Svelte 5 with runes (`$state`, `$derived`, `$effect`) — **not** the legacy `svelte/store` `writable` pattern for app state | `^5.16.0` |
| Bundler | Vite | `^6.0.6` |
| Styling | Tailwind CSS 3 + `clsx` / `tailwind-merge` for class composition | `^3.4.17`, `clsx ^2.1.1`, `tailwind-merge ^2.6.0` |
| Headless primitives | `bits-ui` (`dialog`, `dropdown-menu`, `context-menu`, `select`, `switch`, `tabs`, `toast`, `alert-dialog`, `button`, `input`, `label`, `color-picker`) | `^1.0.0-next.74` |
| Icons | `@iconify/svelte` + `unplugin-icons`, with **offline** JSON bundles enforced by `make check-offline-icons` | `@iconify/svelte ^4.2.0` |
| i18n | `svelte-i18n`, 10 locales | `^4.0.1` |
| Rich text | Tiptap v2 (ProseMirror) with table, image, link, colour, placeholder extensions | `^2.11` / `^2.27` |
| Virtual scroll | `@tanstack/svelte-virtual` | `^3.13.0` |
| Spellcheck | `nspell` + `hspell`-derived dictionaries for 6 languages (`dictionary-cs/de/en/fr/it/nb`) | `nspell ^2.1.5` |
| Compression | `fflate` (draft MIME) | `^0.8.3` |
| Dates | `date-fns` + `date-fns-tz` | `date-fns ^4.1.0` |

`frontend/wailsjs/` holds generated Wails bindings. **Do not edit it**; regenerate with
`make generate`.

---

## 2. Entry points

| File | Role |
|---|---|
| `frontend/index.html` | Shell. Contains the **anti-flash theme script** (below). |
| `frontend/src/main.ts` | Main-window bootstrap. |
| `frontend/src/App.svelte` | Main window root, 1712 lines. |
| `frontend/src/composerMain.ts` | Detached-composer bootstrap. |
| `frontend/src/ComposerApp.svelte` | Composer root, 293 lines. |
| `frontend/src/lib/composerApi.ts` | The API surface the composer uses. |

There is **no `stores/composer.svelte.ts`**. Composer state lives in
`ComposerApp.svelte` and `composerApi.ts`. Older docs that list a composer store are
wrong.

### Anti-flash theme

`frontend/index.html:74-88` runs a synchronous inline script *before* first paint:

```js
var theme = localStorage.getItem('hsx2mail-theme')
if (theme === 'dark') document.documentElement.classList.add('dark')
else if (theme === 'light') document.documentElement.classList.remove('dark')
```

Wails is configured with `StartHidden=true`, so this script — not a CSS transition —
is what prevents a light flash on a dark-theme boot. If you remove it, dark-mode users
see a white flash on every launch. The Svelte side then reconciles via `initTheme(stored,
GetSystemTheme)` (`App.svelte:390`); it does not own the first paint.

---

## 3. Stores

`frontend/src/lib/stores/` — 16 modules. Class-based stores with `$state` in class
fields, exported as singletons.

| Store | Owns |
|---|---|
| `accounts.svelte.ts` | Account list, folder tree, sync state, selected folder, unread counts. The largest and most central. |
| `settings.svelte.ts` | Application settings (key/value), mirroring the `settings` table. |
| `theme.svelte.ts` | Dark/light/system mode, system-preference events. |
| `layout.svelte.ts` | Responsive layout mode and pane visibility. |
| `uiState.svelte.ts` | Persisted UI state: pane widths, active extension, selected folder. |
| `keyboard.svelte.ts` | Pane-navigation state for keyboard shortcuts. |
| `toast.ts` | Toast notification queue. |
| `oauth.svelte.ts` | OAuth flow state machine. |
| `contactPhotos.svelte.ts` | Contact photo cache. |
| `contactSources.svelte.ts` | Contact source configuration. |
| `imageAllowlist.svelte.ts` | Remote-image allowlist. |
| `dialogGuard.svelte.ts` | Tracks open-dialog state so shortcuts do not fire behind a modal. |
| `extensionRegistry.svelte.ts` | Extension UI registry: rail tabs, hooks. |
| `extensionDeepLink.svelte.ts` | Routes `extension:open` events to the right extension. |
| `extensionShortcuts.svelte.ts` | Extension keyboard shortcuts. |
| `inlineAttachmentCache.ts` | Byte-budgeted LRU cache for inline attachment bytes. |

`accounts.svelte.ts` guards its event-listener registration behind an init flag and
deliberately **does not** call `EventsOff`. `EventsOff(name)` removes *every* listener for
that event name, so unsubscribing there would kill other components' subscriptions. Any
new component that subscribes must use the unsubscribe function returned by `EventsOn`
(what `ConversationViewer.svelte` does), never `EventsOff`.

---

## 4. Component tree

```
App.svelte
├── TitleBar                          (common/TitleBar.svelte — frameless, custom)
├── Sidebar                           (sidebar/Sidebar.svelte)
│   ├── UnifiedInboxSection
│   └── AccountSection × N
│       └── FolderTreeItem            (recursive)
├── MessageList                       (list/MessageList.svelte — virtualized)
│   └── ConversationRow
├── ConversationViewer                (viewer/ConversationViewer.svelte)
│   ├── EmailBody
│   └── AttachmentList
├── ExtensionRail                     (rail/ExtensionRail.svelte + RailButton)
│   ├── ContactsPane                  (from the contacts extension)
│   └── CalendarPane                  (from the calendar extension)
├── Composer                          (composer/Composer.svelte — overlay or separate window)
├── SettingsDialog                    (settings/*Tab.svelte)
│   └── General, Accounts, Composer, Contacts, Extensions, Images, About
├── Dialogs                           (CertificateDialog, OAuthMissingDialog, TermsDialog,
│                                       WhatsNewDialog, ContactSourceDialog, …)
├── SpellSuggestionMenu
└── ToastContainer                    (ui/toast)
```

Shared primitives live in two places:

- `components/ui/` — thin wrappers over `bits-ui`, one directory per primitive.
- `components/kit/` — composite widgets that are not primitives: `Avatar`,
  `ColorPicker`, `ConfirmDialog`, `DetailOverlay`, `DetailPane`, `ListHeader`, …

Composer-specific helpers are plain modules, not components:
`composer/composerEditor.ts`, `composer/composerSignature.ts`,
`composer/composerUtils.ts`.

---

## 5. The virtualized message list

`MessageList.svelte:1262-1300`. Rows are **variable height** — subject and snippet wrap —
so the virtualizer is configured with a per-density estimate that every mounted row then
corrects through a Svelte action handing the real DOM node to the virtualizer; a
`ResizeObserver` keeps it current.

```ts
const ROW_HEIGHT_ESTIMATE: Record<string, number> = {
  micro: 64, compact: 80, standard: 96, large: 112,
}

const rowVirtualizer = createVirtualizer<HTMLDivElement, HTMLElement>({
  getScrollElement: () => listContainerRef,
  count: 0,
  estimateSize: () => ROW_HEIGHT_ESTIMATE[getMessageListDensity()],
  getItemKey: (index) => convRowKey(conversations[index]),
  overscan: 8,
})
```

Three things matter when touching this:

1. **`getItemKey` returns a stable per-conversation identity** matching the keyed-`each`
   key. Without it, Svelte cannot reuse row components across a re-sort and the
   virtualizer loses track of scroll position.
2. **A virtualizer signature is recomputed** whenever the input that shapes rows changes
   (density, sort, grouping); the virtualizer is reconfigured rather than mutated
   in place.
3. **The virtualizer only owns the conversation branch.** Search results and other
   non-conversation branches render normally — they are paginated server-side and are
   small.

`overscan: 8` rows is the deliberate balance; raising it smooths fast scrolling at the
cost of re-creating rows you cannot see.

---

## 6. Inline attachment cache

`frontend/src/lib/stores/inlineAttachmentCache.ts`. The backend already stores inline
attachment content in SQLite (`attachments.content`, see `DATABASE.md` §8), so this cache
is about avoiding redundant Wails round trips when switching between messages in a
conversation.

It is an **LRU with a byte budget**, not a count-based cache:

```ts
const MAX_CACHE_BYTES = 50 * 1024 * 1024
```

A new entry that would exceed the budget evicts least-recently-used entries until it
fits. A count-based limit would be wrong here because attachment sizes vary by three
orders of magnitude. 50 MB is the ceiling; raise it only with a reason.

---

## 7. Backend events

Wails `EventsOn` subscriptions in `App.svelte`:

| Event | Effect |
|---|---|
| `app:ready` | Backend finished `Startup`. Frontend mounts. |
| `notification:clicked` | Raise window, open the conversation (or `extension:open`). |
| `extension:open` | Route to an extension via `extensionDeepLink`. |
| `mail:new` | New unread message arrived. |
| `folder:synced` | A folder finished syncing; refresh counts. |
| `theme:system-preference` | OS switched light/dark. |
| `composer:messageSent` | A detached composer sent a message. |
| `network:online` / `network:offline` | Connectivity change (see `BACKGROUND.md` §6). |
| `oauth:reauth-required` | Token refresh failed; show the re-auth dialog. |
| `app:shutting-down` | Render the shutdown overlay before the process exits. |

**Rule: use the function `EventsOn` returns, not `EventsOff`.** See §3.

---

## 8. Layout modes

`stores/layout.svelte.ts` defines three modes, not the four some older docs list:

| Mode | Width | Behaviour |
|---|---|---|
| `full` | `> 1024px` | Sidebar + list + split viewer. Composer overlays. |
| `medium` | `768–1024px` | Sidebar hidden behind a toggle; list + viewer. |
| `narrow` | `< 768px` | Single pane at a time. Selecting a message replaces the list with the viewer (`responsiveView = 'viewer'`). |

Switching is `matchMedia`-driven. The `mediaQuery` change listener in `App.svelte` must
be stored and removed on teardown, or it leaks per remount.

---

## 9. i18n

10 locales in `frontend/src/lib/i18n/locales/`: `cs`, `de`, `en`, `fr`, `it`, `nb`,
`vi`, `zh-CN`, `zh-HK`, `zh-TW`. `dateFnsLocale.ts` maps the active locale to a
`date-fns` locale so dates format correctly too.

Translation workflow: `LANGUAGE.md`. Adding a locale means adding a JSON file **and**
registering it in `locales/index.ts`.

---

## 10. Quality gates

```bash
cd frontend
npm run lint        # ESLint (eslint-plugin-svelte, typescript-eslint)
npm run check       # svelte-check: Svelte + TypeScript
```

From the repo root, `make lint` runs `lint-frontend`, `check-frontend`,
`check-offline-icons` and `knip` in addition to the Go linters. `make check` is the full
gate.

`check-offline-icons` is not optional: it fails if any `<Icon icon="…">` reference has no
local JSON bundle, which would otherwise be a runtime blank icon in an offline install.

---

## 11. Related documents

| Topic | Owner |
|---|---|
| Store graph, components, virtualization, events, layout | this file |
| Background/foreground behaviour, tray, autostart | `BACKGROUND.md` |
| Go-side sync, drafts, attachments | `architecture.md`, `analysis/30-compose-send-draft.md` |
| Performance tuning | `PERFORMANCE.md` |
| Build and lint commands | `OPERATIONS.md` |
| Translation workflow | `LANGUAGE.md` |
