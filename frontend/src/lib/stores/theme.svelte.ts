// Theme store - centralizes all theme application and system theme detection logic
//
// Used by both App.svelte (main window) and ComposerApp.svelte (detached composer).
// The OS theme probe is injected by the caller because each Wails process binds a
// different Go struct (App vs ComposerApp), and importing the wrong binding at the
// module level silently fails at runtime.

import { getThemeMode, type ThemeMode } from './settings.svelte'

export type { ThemeMode }

// Internal state for portal-based system theme (XDG Settings Portal on Linux)
let portalThemeAvailable = false
let portalTheme: 'light' | 'dark' = 'light'

// Reactive flag mirroring the `.dark` class on <html>. Consumers (e.g., the
// email-content dark-filter toggle) need a Svelte-reactive way to observe it.
let isDarkActive = $state<boolean>(false)

export function getIsDarkActive(): boolean {
  return isDarkActive
}

/** Apply a resolved theme to the document element. The dark/light classification
 *  is read from the CSS-declared `color-scheme` property on the matching
 *  [data-theme="..."] block, so each theme owns its own scheme — no JS list to
 *  maintain. We mirror it as the `.dark` class so Tailwind `dark:` variants and
 *  any `.dark mark`-style selectors keep working. */
export function applyTheme(themeName: ThemeMode) {
  document.documentElement.setAttribute('data-theme', themeName)
  const scheme = getComputedStyle(document.documentElement).colorScheme.trim()
  const dark = scheme === 'dark'
  document.documentElement.classList.toggle('dark', dark)
  isDarkActive = dark
  // Save to localStorage for next session
  try {
    localStorage.setItem('hsx2mail-theme', dark ? 'dark' : 'light')
  } catch {
    // Storage can be unavailable (private mode, sandboxed storage); the
    // in-memory value still applies for this session.
  }
}

/** Resolve a ThemeMode (which may be 'system') to a concrete theme and apply it. */
export function applyThemeFromMode(mode: ThemeMode) {
  if (mode !== 'system') {
    applyTheme(mode)
    return
  }

  // System mode: use portal-based theme if available, otherwise fall back to matchMedia
  if (portalThemeAvailable) {
    applyTheme(portalTheme)
    return
  }

  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  applyTheme(mediaQuery.matches ? 'dark' : 'light')
}

/**
 * Apply a theme pushed from the main window over IPC.
 *
 * The payload crosses the bridge as a plain string, so it is validated here
 * rather than trusted: anything other than the three known modes is ignored,
 * because applyTheme() writes the value into a `data-theme` attribute and an
 * unexpected string would leave the document themed inconsistently.
 */
export function handleThemeChanged(newTheme: string) {
  if (newTheme === 'light' || newTheme === 'dark' || newTheme === 'system') {
    applyThemeFromMode(newTheme as ThemeMode)
  } else {
    console.warn('[theme] ignoring unknown theme from IPC:', newTheme)
  }
}

/**
 * Initialize the theme on mount.
 * Probes the XDG Settings Portal for system theme via the caller-supplied binding,
 * then applies the stored mode.
 */
export async function initTheme(
  storedMode: ThemeMode,
  getSystemTheme: () => Promise<string>,
) {
  try {
    const sysTheme = await getSystemTheme()
    if (sysTheme === 'light' || sysTheme === 'dark') {
      portalThemeAvailable = true
      portalTheme = sysTheme
    }
  } catch {
    // Portal not available, will use matchMedia fallback
  }

  applyThemeFromMode(storedMode)
}

/** Handle backend 'theme:system-preference' events (XDG Settings Portal changes). */
export function handleSystemThemeEvent(newTheme: string) {
  if (newTheme !== 'light' && newTheme !== 'dark') return

  portalThemeAvailable = true
  portalTheme = newTheme
  if (getThemeMode() === 'system') {
    applyTheme(portalTheme)
  }
}

/** Handle matchMedia 'change' events (fallback when portal is unavailable). */
export function handleMediaQueryChange(matches: boolean) {
  if (getThemeMode() !== 'system' || portalThemeAvailable) return
  applyTheme(matches ? 'dark' : 'light')
}
