// Optimistic read-state overlay for the message list.
//
// MarkAsRead round-trips to IMAP before the local DB and the
// `messages:readChanged` event fire — app/actions.go does the STORE first so a
// failed STORE can never be overwritten by a later sync. That is the right
// invariant, but it means the row keeps rendering "unread" for the whole
// network round-trip, which reads as a delayed, snapping state change.
//
// This store tracks message IDs whose read-flip is in flight. The list renders
// those rows as already-read plus a spinner, so the switch is instant and
// explained. The entry is dropped when the real `messages:readChanged` event
// arrives; on a failed call the caller drops it too, and since the backend left
// the DB untouched the row reverts on its own.
//
// Only the read direction is drawn optimistically: unreadCount can be clamped
// to 0 immediately, while the reverse would have to invent a count the client
// does not know.

import { SvelteSet } from 'svelte/reactivity'

import { accountStore } from './accounts.svelte'

// Upper bound on how long a row may render as pending. The backend emits before
// returning, so this only bites if that event is lost.
const PENDING_EXPIRY_MS = 15_000

const pendingRead = new SvelteSet<string>()
const expiryTimers = new Map<string, ReturnType<typeof setTimeout>>()

/** True while `messageId`'s read-flip is in flight. Reactive. */
export function isReadPending(messageId: string): boolean {
  return pendingRead.has(messageId)
}

/** True while any of `messageIds` has a read-flip in flight. Reactive. */
export function anyReadPending(messageIds: readonly string[]): boolean {
  return messageIds.some((id) => pendingRead.has(id))
}

function armExpiry(messageId: string) {
  const existing = expiryTimers.get(messageId)
  if (existing) clearTimeout(existing)
  expiryTimers.set(
    messageId,
    setTimeout(() => {
      expiryTimers.delete(messageId)
      pendingRead.delete(messageId)
    }, PENDING_EXPIRY_MS)
  )
}

/**
 * Draw these messages as read/unread now, before the server confirms.
 *
 * `direction` is the state being moved *to*: 'read' drops the folder's unread
 * badge immediately, 'unread' raises it. The sidebar counter is updated through
 * the resolver registered by the message list, so the left rail tracks the list
 * across the IMAP round-trip instead of trailing it. The backend's later
 * `folders:countsChanged` carries the exact count and overwrites this guess, so
 * a miscounted delta self-corrects rather than sticking.
 */
export function beginReadPending(
  messageIds: readonly string[],
  direction: 'read' | 'unread' = 'read'
) {
  const delta = direction === 'read' ? -1 : 1
  const touched = new Set<string>()
  for (const id of messageIds) {
    pendingRead.add(id)
    armExpiry(id)
    const folderId = resolveFolderId?.(id)
    if (folderId && !touched.has(folderId)) {
      touched.add(folderId)
      const count = messagesInFolder(messageIds, folderId)
      accountStore.applyUnreadDelta(folderId, delta * count)
    }
  }
}

/**
 * Resolver from message id to its folder id, registered by the message list —
 * the only component that knows the id→folder mapping (the unified inbox mixes
 * folders from several accounts). Without it we cannot tell which sidebar badge
 * to move, so the optimistic sidebar step is skipped.
 */
type FolderIdResolver = (messageId: string) => string | null | undefined

let resolveFolderId: FolderIdResolver | null = null

export function setFolderIdResolver(fn: FolderIdResolver | null) {
  resolveFolderId = fn
}

function messagesInFolder(messageIds: readonly string[], folderId: string): number {
  let n = 0
  for (const id of messageIds) {
    if (resolveFolderId?.(id) === folderId) n++
  }
  return n
}

/** Drop the pending mark — the backend event landed, or the call failed. */
export function clearReadPending(messageIds: readonly string[]) {
  for (const id of messageIds) {
    const timer = expiryTimers.get(id)
    if (timer) {
      clearTimeout(timer)
      expiryTimers.delete(id)
    }
    pendingRead.delete(id)
  }
}

/**
 * How many of `messages` a `messages:readChanged` event actually flips.
 *
 * A single event can name ids from several conversations, so the raw
 * `messageIds.length` overcounts: it includes rows this conversation never held
 * and rows already in the target state. Only ids present here AND changing
 * state move this conversation's unread badge.
 */
export function countReadFlips(
  messages: readonly { id: string; isRead: boolean }[],
  messageIds: readonly string[],
  isRead: boolean
): number {
  const ids = new Set(messageIds)
  let n = 0
  for (const m of messages) {
    if (ids.has(m.id) && m.isRead !== isRead) n++
  }
  return n
}
