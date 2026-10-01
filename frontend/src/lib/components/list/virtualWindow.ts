// Pure helpers behind the virtualized conversation list.
//
// The component keeps its window in `$state` because @tanstack/svelte-virtual
// can notify its store synchronously from inside getVirtualItems() and
// measureElement(), and writing state during a block effect is a Svelte error.
// That indirection is exactly where a regression is invisible: the original
// guard compared only `length` and the first key, so a batch that replaced rows
// in the MIDDLE of the window (a flag update, a folder rename) left the same
// length and first key and the list silently rendered stale rows.
//
// These functions are pure so that failure mode is testable without a browser,
// a Wails bridge, or a component instance.

/**
 * The shape these helpers need from a virtual item. It is a structural subset
 * of @tanstack/virtual-core's VirtualItem (same key and start types), so real
 * VirtualItems pass without casting while tests can build plain objects.
 */
export interface WindowItem {
  /** Matches @tanstack/virtual-core's Key, which includes bigint. */
  key: string | number | bigint
  index: number
  start: number
}

/**
 * Reports whether a newly computed window differs from the one currently
 * applied to the DOM.
 *
 * Every row is compared, not just the length and the first key: rows are keyed
 * by thread id, so replacing rows in the middle of an otherwise unchanged
 * window must count as a change. `start` is compared because a row whose offset
 * moved needs re-positioning, and `index` because the row body reads
 * `conversations[row.index]` — after a delete or a reorder the same thread can
 * keep its key and offset while sitting at a different index, and rendering the
 * stale index would show a different conversation entirely.
 *
 * An empty window is only "unchanged" when the previous window was also empty.
 */
export function windowChanged(
  next: readonly WindowItem[],
  applied: readonly WindowItem[],
): boolean {
  if (next.length !== applied.length) return true
  for (let i = 0; i < next.length; i++) {
    if (next[i].key !== applied[i].key) return true
    if (next[i].start !== applied[i].start) return true
    if (next[i].index !== applied[i].index) return true
  }
  return false
}

/**
 * Returns the window to render, or the previously applied one when nothing
 * changed.
 *
 * Returning the SAME array reference when unchanged is what keeps the effect
 * from re-triggering itself: assigning a fresh array unconditionally would
 * invalidate `virtualRows` on every scroll tick even when the rendered rows are
 * identical.
 */
export function reconcileWindow<T extends WindowItem>(
  next: readonly T[],
  applied: readonly T[],
): { rows: readonly T[]; changed: boolean } {
  if (!windowChanged(next, applied)) {
    return { rows: applied, changed: false }
  }
  return { rows: next.slice(), changed: true }
}

/**
 * Absolute positioning for a row inside the total-size spacer.
 *
 * `transform: translateY()` keeps the row out of layout flow while letting the
 * browser composite the scroll, and `height: auto` lets the measured height win
 * over the estimate once measureElement() has seen the node.
 */
export function rowStyle(item: WindowItem): string {
  return `transform: translateY(${item.start}px); position: absolute; top: 0; left: 0; width: 100%;`
}

/**
 * Index of a thread in a list, or -1.
 *
 * Shift-select and "select the next message after a delete" work off indices
 * into the FULL list, never the mounted window — the window holds a fraction of
 * the rows, so an index from the DOM cannot be trusted. Routing those lookups
 * through one function keeps that invariant explicit.
 */
export function indexOfThread<T extends { threadId: string }>(
  list: readonly T[],
  threadId: string,
): number {
  return list.findIndex((item) => item.threadId === threadId)
}

/**
 * Thread ids for an inclusive index range, clamped to the list bounds.
 *
 * Used by shift-click range selection. Out-of-range indices are clamped rather
 * than skipped: a selection anchored before a reload must still cover every row
 * that now exists at that position, otherwise clicking silently selects nothing.
 */
export function threadIdsInRange<T extends { threadId: string }>(
  list: readonly T[],
  from: number,
  to: number,
): string[] {
  if (list.length === 0) return []
  const start = Math.max(0, Math.min(from, to))
  const end = Math.min(list.length - 1, Math.max(from, to))
  if (start > end) return []

  const ids: string[] = []
  for (let i = start; i <= end; i++) {
    ids.push(list[i].threadId)
  }
  return ids
}

/**
 * The index to select after rows are removed from `totalBefore`, given the
 * index that was removed.
 *
 * Deleting the last visible row must land on the row that slid into its place,
 * not past the end of the shortened list.
 */
export function nextIndexAfterRemoval(
  removedIndex: number,
  totalAfter: number,
): number {
  if (totalAfter <= 0) return -1
  return Math.max(0, Math.min(removedIndex, totalAfter - 1))
}
