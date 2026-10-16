import { describe, expect, it } from 'vitest'
import { explainOAuthError, oauthErrorText } from './oauthErrors'

// svelte-i18n is mocked in src/test/setup.ts so `_` echoes the key back; the
// assertions below therefore check *which* message was selected, not its
// wording in any one locale.
describe('explainOAuthError', () => {
  it('maps the unregistered-app error every corporate tenant hits', () => {
    const raw =
      "token error invalid_grant: AADSTS700016: Application with identifier " +
      "'0816227c-ae3d-4470-9e1e-73c183e16b94' was not found in the directory " +
      "'FPT Information System'. — the app is not registered in tenant FPT Information System."
    expect(explainOAuthError(raw)).toBe('oauth.error.appNotRegistered')
  })

  it('maps tenant, consent, assignment and secret errors', () => {
    const cases: Array<[string, string]> = [
      ['AADSTS50059: No tenant-identifying information found', 'oauth.error.tenantUnknown'],
      ['AADSTS65001: The user or administrator has not consented', 'oauth.error.adminConsent'],
      ['AADSTS50105: The user is not assigned to the application', 'oauth.error.notAssigned'],
      ['AADSTS7000218: Request body must contain client_secret', 'oauth.error.clientSecret'],
    ]
    for (const [raw, key] of cases) {
      expect(explainOAuthError(raw), raw).toBe(key)
    }
  })

  it('finds the code even when the backend rewrote the message', () => {
    expect(explainOAuthError('token error invalid_grant: AADSTS700016: ...')).toBe(
      'oauth.error.appNotRegistered'
    )
  })

  it('returns null for codes it has no advice for', () => {
    expect(explainOAuthError('AADSTS50126: Invalid username or password')).toBeNull()
    expect(explainOAuthError('token error invalid_client')).toBeNull()
    expect(explainOAuthError(null)).toBeNull()
    expect(explainOAuthError('')).toBeNull()
  })

  it('returns null for non-Microsoft failures so Google errors pass through', () => {
    expect(explainOAuthError('invalid_scope: Bad Request')).toBeNull()
  })
})

describe('oauthErrorText', () => {
  it('localizes a recognized error', () => {
    expect(oauthErrorText('AADSTS65001: consent needed')).toBe('oauth.error.adminConsent')
  })

  it('passes an unrecognized message through untouched', () => {
    const raw = 'connection refused'
    expect(oauthErrorText(raw)).toBe(raw)
  })

  it('preserves null and empty so the UI keeps its own fallback', () => {
    expect(oauthErrorText(null)).toBeNull()
    expect(oauthErrorText(undefined)).toBeNull()
    expect(oauthErrorText('')).toBe('')
  })
})
