import { describe, expect, it } from 'vitest'
import { formatRelativeDate } from './date'

// A malformed timestamp must degrade to an empty string, never throw.
//
// `latestDate` arrives over the Wails bridge from a server, so a missing or
// unparsable value is reachable. date-fns' format() throws a RangeError on an
// Invalid Date, and these functions run during render — so before the guard, a
// single bad timestamp in a folder unmounted the entire conversation list.

describe('date formatters reject invalid dates', () => {
  const invalid = [
    new Date('nonsense'),
    new Date(NaN),
    new Date(''),
    new Date(undefined as unknown as string),
  ]

  it('formatRelativeDate returns an empty string', () => {
    for (const d of invalid) {
      expect(formatRelativeDate(d)).toBe('')
    }
  })

  it('rejects non-Date input as well', () => {
    const notADate = [undefined, null, 0, '', '2026-01-01'] as unknown as Date[]
    for (const d of notADate) {
      expect(() => formatRelativeDate(d)).not.toThrow()
    }
  })
})
