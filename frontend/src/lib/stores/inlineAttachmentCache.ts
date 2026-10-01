/**
 * In-memory cache for inline attachments within a session.
 *
 * This cache avoids redundant API calls when switching between messages.
 * The backend already stores inline attachment content in SQLite for offline access,
 * but this frontend cache prevents unnecessary round-trips.
 *
 * **LRU cache with byte budget** - prevents unbounded memory growth when viewing
 * many image-heavy emails. Cache is automatically cleared when the viewer changes.
 */

interface CacheEntry {
  data: Record<string, string>
  size: number
  lastAccess: number
}

// Byte budget: 50MB max cache size
const MAX_CACHE_BYTES = 50 * 1024 * 1024

const cache = new Map<string, CacheEntry>()
let currentBytes = 0

/**
 * Get cached inline attachments for a message
 */
export function getCached(id: string): Record<string, string> | undefined {
  const entry = cache.get(id)
  if (entry) {
    entry.lastAccess = Date.now()
    return entry.data
  }
  return undefined
}

/**
 * Store inline attachments in cache with LRU eviction
 */
export function setCache(id: string, data: Record<string, string>): void {
  const size = JSON.stringify(data).length
  const key = id

  // Evict entries if adding this would exceed budget
  if (currentBytes + size > MAX_CACHE_BYTES) {
    evictEntries(size)
  }

  // Replacing an existing entry must subtract its old size first, otherwise
  // currentBytes inflates on every re-set of the same key and the budget stops
  // evicting anything.
  const previous = cache.get(key)
  if (previous) {
    currentBytes -= previous.size
  }
  cache.set(key, { data, size, lastAccess: Date.now() })
  currentBytes += size
}

/**
 * Evict LRU entries to make room for new data
 */
function evictEntries(newSize: number): void {
  const entries = Array.from(cache.entries()).sort((a, b) => a[1].lastAccess - b[1].lastAccess)
  let freed = 0

  for (const [key, entry] of entries) {
    if (currentBytes + newSize - freed <= MAX_CACHE_BYTES) break
    cache.delete(key)
    freed += entry.size
  }
  currentBytes -= freed
}

/**
 * Clear all cached inline attachments
 * Called when the message viewer changes
 */
export function clearCache(): void {
  cache.clear()
  currentBytes = 0
}
