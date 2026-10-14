/**
 * Run `fn` over `items` with at most `limit` calls in flight, preserving result
 * order.
 *
 * The message list can hold several messages with no local body yet; fetching
 * them one after another makes opening a thread pay one IPC round-trip per
 * message, serially. A bounded number in flight cuts that wall-clock time
 * without flooding the Wails bridge (and the IMAP connection behind it).
 *
 * A rejection is captured per item as `undefined` rather than failing the whole
 * batch: one message whose body cannot be fetched must not deny the rest.
 */
export async function mapWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>
): Promise<(R | undefined)[]> {
  const results = new Array<R | undefined>(items.length)
  // Floor and clamp into [1, items.length]. `NaN || 1` and `0 || 1` both give 1,
  // and a negative limit is floored to at least 1 rather than dropping work.
  const size = Math.max(1, Math.min(Math.floor(limit) || 1, items.length))
  let next = 0

  const worker = async (): Promise<void> => {
    while (true) {
      const index = next++
      if (index >= items.length) return
      try {
        results[index] = await fn(items[index], index)
      } catch {
        results[index] = undefined
      }
    }
  }

  await Promise.all(Array.from({ length: size }, worker))
  return results
}
