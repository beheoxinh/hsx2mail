import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  anyReadPending,
  beginReadPending,
  clearReadPending,
  isReadPending,
} from './readStatus.svelte'

// Unit test — pure in-process state, no bridge, no socket. The store is what
// lets a row render "read" before the IMAP STORE round-trip returns, so the
// cases that matter are: it starts pending, it stops on the real event, and a
// lost event cannot strand a row as pending forever.

const EXPIRY_MS = 15_000

describe('readStatus pending overlay', () => {
  beforeEach(() => {
    vi.useRealTimers()
    clearReadPending(['a', 'b', 'c'])
  })

  it('reports a message pending only after beginReadPending', () => {
    expect(isReadPending('a')).toBe(false)
    beginReadPending(['a'])
    expect(isReadPending('a')).toBe(true)
    expect(isReadPending('b')).toBe(false)
  })

  it('anyReadPending is true when any listed id is in flight', () => {
    beginReadPending(['b'])
    expect(anyReadPending(['a', 'b'])).toBe(true)
    expect(anyReadPending(['a', 'c'])).toBe(false)
    expect(anyReadPending([])).toBe(false)
  })

  it('clearReadPending drops the mark — the messages:readChanged path', () => {
    beginReadPending(['a', 'b'])
    clearReadPending(['a'])
    expect(isReadPending('a')).toBe(false)
    expect(isReadPending('b')).toBe(true)
  })

  it('expires a pending mark that never gets its event', () => {
    vi.useFakeTimers()
    beginReadPending(['a'])
    expect(isReadPending('a')).toBe(true)
    vi.advanceTimersByTime(EXPIRY_MS - 1)
    expect(isReadPending('a')).toBe(true)
    vi.advanceTimersByTime(1)
    expect(isReadPending('a')).toBe(false)
    vi.useRealTimers()
  })

  it('does not expire early after a re-mark clears the old timer', () => {
    vi.useFakeTimers()
    beginReadPending(['a'])
    vi.advanceTimersByTime(EXPIRY_MS - 1)
    // A second mark (row re-entered) must restart the window, not inherit the
    // nearly-expired one — otherwise the spinner would blink off immediately.
    beginReadPending(['a'])
    vi.advanceTimersByTime(2)
    expect(isReadPending('a')).toBe(true)
    vi.advanceTimersByTime(EXPIRY_MS)
    expect(isReadPending('a')).toBe(false)
    vi.useRealTimers()
  })

  it('clearReadPending cancels the expiry timer', () => {
    vi.useFakeTimers()
    beginReadPending(['a'])
    clearReadPending(['a'])
    // Timer must not fire later against a re-marked id.
    beginReadPending(['a'])
    vi.advanceTimersByTime(EXPIRY_MS - 1)
    expect(isReadPending('a')).toBe(true)
    clearReadPending(['a'])
    vi.advanceTimersByTime(EXPIRY_MS * 2)
    expect(isReadPending('a')).toBe(false)
    vi.useRealTimers()
  })
})
