import { get } from 'svelte/store'
import { _ } from '$lib/i18n'

/**
 * Localize a Microsoft Entra (Azure AD) sign-in failure.
 *
 * The backend already rewrites the raw AADSTS blob into a sentence that says
 * what to do next (`internal/oauth2/errors.go`), but that sentence is English
 * and the UI ships ten locales. The AADSTS code is preserved at the front of
 * the rewritten message, so we key off it here; anything we do not recognize
 * is returned untouched so the backend's wording (or a Google error) still
 * reaches the user.
 */
const ENTRA_HINT_KEYS: Record<string, string> = {
  AADSTS700016: 'oauth.error.appNotRegistered',
  AADSTS50059: 'oauth.error.tenantUnknown',
  AADSTS65001: 'oauth.error.adminConsent',
  AADSTS50105: 'oauth.error.notAssigned',
  AADSTS7000218: 'oauth.error.clientSecret',
}

// AADSTS codes are not a fixed width — 50059, 700016 and 7000218 all occur in
// the sign-in path — so the match is bounded rather than exact.
const AADSTS_RE = /AADSTS\d{5,8}/

/**
 * Returns a localized, actionable message when the error carries a known
 * AADSTS code, or null when nothing matches.
 */
export function explainOAuthError(message: string | null | undefined): string | null {
  if (!message) return null
  const match = AADSTS_RE.exec(message)
  if (!match) return null
  const key = ENTRA_HINT_KEYS[match[0]]
  if (!key) return null
  return get(_)(key)
}

/**
 * Localize when possible, otherwise pass the backend message through. This is
 * what the UI should render.
 */
export function oauthErrorText(message: string | null | undefined): string | null {
  if (!message) return message ?? null
  return explainOAuthError(message) ?? message
}
