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

/** Draw these messages as read now, before the server confirms. */
export function beginReadPending(messageIds: readonly string[]) {
  for (const id of messageIds) {
    pendingRead.add(id)
    armExpiry(id)
  }
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
