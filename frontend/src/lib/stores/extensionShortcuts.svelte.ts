// Extension-shortcut registry.
//
// Extensions register their pane-local keyboard shortcuts here at component
// mount; the host's global key handler (App.svelte's handleGlobalKeyDown)
// dispatches via dispatchExtensionShortcut whenever the active rail pane is
// NOT mail. This mirrors how mail dispatches its own switch-case shortcuts
// from the same global handler — extensions get a symmetric path.
//
// The handler always returns true/false from dispatch so the global key
// handler knows whether to continue down its mail-side branch. Predicates
// live in the extension's own
// `extensions/<name>/frontend/keyboard/shortcuts.ts` file (with shared
// helpers imported from `$lib/keyboard/shortcuts`).
//
// Lifetime: callers register at onMount and call the returned Unregister at
// onDestroy. The registry is window-global (single module-level map) — fine
// for now since only one extension can be the active rail pane at a time.

import { getActiveExtension } from './uiState.svelte'

export type ShortcutPredicate = (e: KeyboardEvent) => boolean
export type ShortcutHandler = (e: KeyboardEvent) => void
export type Unregister = () => void

interface Registration {
  predicate: ShortcutPredicate
  handler: ShortcutHandler
}

// Indexed by extensionId → ordered list of registrations.
const registry = new Map<string, Registration[]>()

/**
 * Register a keyboard shortcut on behalf of an extension.
 *
 * Registrations are scoped by extension id and only fire while that extension
 * is the active pane, so an extension's shortcut cannot shadow a mail shortcut or
 * fire while the user is looking at another tab. The returned function removes
 * the registration and must be called on teardown, otherwise a closed extension
 * keeps handling keys.
 */
export function registerExtensionShortcut(
  extensionId: string,
  predicate: ShortcutPredicate,
  handler: ShortcutHandler,
): Unregister {
  const list = registry.get(extensionId) ?? []
  const registration: Registration = { predicate, handler }
  list.push(registration)
  registry.set(extensionId, list)

  return () => {
    const current = registry.get(extensionId)
    if (!current) return
    const at = current.indexOf(registration)
    if (at >= 0) current.splice(at, 1)
  }
}

/**
 * Dispatch a keyboard event to the currently-active extension's registered
 * shortcuts. Called from App.svelte's global key handler.
 *
 * Returns true when a handler ran (caller should treat the event as handled —
 * don't run mail's downstream dispatch). Returns false when nothing matched
 * (caller continues to its own logic).
 *
 * Callers gate on isMailActive() / isDialogGuardActive() / inInput BEFORE
 * invoking this — those are host-level concerns the dispatcher doesn't repeat.
 */
export function dispatchExtensionShortcut(e: KeyboardEvent): boolean {
  const ext = getActiveExtension()
  if (!ext || ext === 'mail') return false
  const list = registry.get(ext)
  if (!list) return false
  for (const reg of list) {
    if (reg.predicate(e)) {
      reg.handler(e)
      return true
    }
  }
  return false
}
