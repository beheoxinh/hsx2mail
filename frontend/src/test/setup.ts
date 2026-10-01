// Shared test setup for component tests.
//
// Two things every component in this tree needs before it can mount under
// jsdom, and neither is the component's own responsibility:
//
//  1. The Wails bridge. The generated bindings are literal property lookups —
//     `window['go']['app']['App'][Name](...)` in frontend/wailsjs/go/app/App.js
//     and `window.runtime.*` in frontend/wailsjs/runtime/runtime.js. jsdom has
//     neither object, so a missing one surfaces as
//     "Cannot read properties of undefined" from inside the component.
//  2. svelte-i18n. `$lib/i18n` runs `init()` against the detected system locale
//     at import time; outside a browser that value is not a locale string and
//     svelte-i18n throws `refLocale.split is not a function` during mount.
//     Separately, `$_` is consumed as a store (`{$_('some.key')}`), so a mocked
//     `_` must expose `subscribe` or Svelte throws `store_invalid_shape`.
//
// Mocking `$lib/i18n` rather than calling init() keeps the app's real i18n
// bootstrap — which tests should not reproduce — out of the test path.

import { readable } from 'svelte/store'
import { vi } from 'vitest'

vi.mock('../lib/i18n', () => ({
  app: readable('en'),
  _: readable((key: string) => key),
  addMessages: () => {},
  getLocaleFromNavigator: () => 'en',
  detectSystemLocale: () => 'en',
  isLocaleLoaded: () => true,
  waitLocale: async () => {},
}))

const noop = () => undefined

;(globalThis as unknown as { runtime?: unknown }).runtime = {
  EventsOn: noop,
  EventsOnMultiple: noop,
  EventsOnce: noop,
  EventsOff: noop,
  EventsEmit: noop,
  ClipboardSetText: noop,
  BrowserOpenURL: noop,
  BrowserOpenFile: noop,
}

type Binding = (...args: unknown[]) => unknown

const bindingCalls = new Map<string, unknown[][]>()
const bindings = new Map<string, Binding>()

/**
 * Make an App binding answer with `value`, and record every call.
 *
 * Any binding a test does not configure resolves to a function returning
 * undefined, which matches how a real call to an unimplemented path behaves.
 */
export function setAppBinding(name: string, value: unknown) {
  bindingCalls.set(name, [])
  bindings.set(name, async (...args: unknown[]) => {
    bindingCalls.get(name)?.push(args)
    return value
  })
}

/** Make an App binding run `impl`, recording its arguments. */
export function trackAppBinding(name: string, impl: (...args: unknown[]) => unknown) {
  bindingCalls.set(name, [])
  bindings.set(name, async (...args: unknown[]) => {
    bindingCalls.get(name)?.push(args)
    return impl(...args)
  })
}

/** Arguments of every recorded call to `name`. */
export function appBindingCalls(name: string): unknown[][] {
  return bindingCalls.get(name) ?? []
}

/** Forget all configured bindings; call between tests. */
export function resetAppBindings() {
  bindingCalls.clear()
  bindings.clear()
}

// Resolve lazily so a test can re-configure a binding after the first mount
// without the proxy having captured a stale function.
const appBridge = new Proxy(
  {},
  {
    get: (_target, prop: string) => {
      if (typeof prop !== 'string') return undefined
      const configured = bindings.get(prop)
      if (configured) return configured
      return (...args: unknown[]) => {
        bindingCalls.get(prop)?.push(args)
        return undefined
      }
    },
  },
)

;(globalThis as unknown as { go?: unknown }).go = { app: { App: appBridge } }
