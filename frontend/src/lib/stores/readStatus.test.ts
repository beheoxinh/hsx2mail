import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  anyReadPending,
  beginReadPending,
  clearReadPending,
  countReadFlips,
  isReadPending,
  setFolderIdResolver,
} from './readStatus.svelte'
import { accountStore } from './accounts.svelte'

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

  it('applies optimistic unread delta for read direction', () => {
    const spy = vi.spyOn(accountStore, 'applyUnreadDelta')
    setFolderIdResolver((id) => (id === 'm1' ? 'f1' : null))
    beginReadPending(['m1'], 'read')
    expect(spy).toHaveBeenCalledWith('f1', -1)
    spy.mockRestore()
    setFolderIdResolver(null)
  })

  it('applies optimistic unread delta for unread direction', () => {
    const spy = vi.spyOn(accountStore, 'applyUnreadDelta')
    setFolderIdResolver((id) => (id === 'm2' ? 'f2' : null))
    beginReadPending(['m2'], 'unread')
    expect(spy).toHaveBeenCalledWith('f2', 1)
    spy.mockRestore()
    setFolderIdResolver(null)
  })

  it('sums affected messages across mixed folders', () => {
    const spy = vi.spyOn(accountStore, 'applyUnreadDelta')
    setFolderIdResolver((id) => (id === 'm1' ? 'f1' : id === 'm2' ? 'f1' : null))
    beginReadPending(['m1', 'm2'], 'read')
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith('f1', -2)
    spy.mockRestore()
    setFolderIdResolver(null)
  })

  it('skips when resolver cannot map a message', () => {
    const spy = vi.spyOn(accountStore, 'applyUnreadDelta')
    setFolderIdResolver(() => null)
    beginReadPending(['m1'], 'read')
    expect(spy).not.toHaveBeenCalled()
    spy.mockRestore()
    setFolderIdResolver(null)
  })
})

describe('countReadFlips', () => {
  // The bug this guards: a messages:readChanged event can name ids from other
  // conversations, and a naive data.messageIds.length would count them against
  // this conversation's unread badge.
  it('counts only messages present in this conversation', () => {
    const msgs = [
      { id: 'a', isRead: false },
      { id: 'b', isRead: false },
    ]
    expect(countReadFlips(msgs, ['a', 'b', 'zzz-from-other-conv'], true)).toBe(2)
  })

  it('skips messages already in the target state', () => {
    const msgs = [
      { id: 'a', isRead: true },
      { id: 'b', isRead: false },
    ]
    expect(countReadFlips(msgs, ['a', 'b'], true)).toBe(1)
  })

  it('counts unread flips in the other direction', () => {
    const msgs = [
      { id: 'a', isRead: true },
      { id: 'b', isRead: true },
    ]
    expect(countReadFlips(msgs, ['a', 'b'], false)).toBe(2)
  })

  it('returns zero when no id matches', () => {
    expect(countReadFlips([{ id: 'a', isRead: false }], ['x', 'y'], true)).toBe(0)
  })
})
