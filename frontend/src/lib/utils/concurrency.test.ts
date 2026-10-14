import { describe, expect, it } from 'vitest'
import { mapWithConcurrency } from './concurrency'

// Unit test — pure in-process promises, no bridge, no socket. The helper exists
// to bound in-flight body fetches; the cases that matter are: results stay in
// input order, the limit is actually respected, and one failure does not sink
// the batch.
describe('mapWithConcurrency', () => {
  it('preserves input order even when completion order differs', async () => {
    const delays = [30, 5, 15]
    const out = await mapWithConcurrency(delays, 3, async (ms, i) => {
      await new Promise((r) => setTimeout(r, ms))
      return i
    })
    expect(out).toEqual([0, 1, 2])
  })

  it('never exceeds the concurrency limit', async () => {
    let inFlight = 0
    let peak = 0
    const items = Array.from({ length: 12 }, (_, i) => i)
    await mapWithConcurrency(items, 3, async () => {
      inFlight++
      peak = Math.max(peak, inFlight)
      await new Promise((r) => setTimeout(r, 5))
      inFlight--
      return null
    })
    expect(peak).toBeLessThanOrEqual(3)
  })

  it('actually runs in parallel up to the limit', async () => {
    let inFlight = 0
    let peak = 0
    const items = Array.from({ length: 6 }, (_, i) => i)
    await mapWithConcurrency(items, 4, async () => {
      inFlight++
      peak = Math.max(peak, inFlight)
      await new Promise((r) => setTimeout(r, 5))
      inFlight--
      return null
    })
    expect(peak).toBe(4)
  })

  it('captures a per-item rejection as undefined without failing the batch', async () => {
    const out = await mapWithConcurrency([1, 2, 3], 2, async (n) => {
      if (n === 2) throw new Error('nope')
      return n * 10
    })
    expect(out).toEqual([10, undefined, 30])
  })

  it('returns an empty array for empty input', async () => {
    const out = await mapWithConcurrency([], 4, async () => 1)
    expect(out).toEqual([])
  })

  it('clamps a limit larger than the item count', async () => {
    const out = await mapWithConcurrency([1, 2], 10, async (n) => n + 1)
    expect(out).toEqual([2, 3])
  })

  it('clamps non-positive and NaN limits to 1', async () => {
    const fn = async (n: number) => n
    expect(await mapWithConcurrency([1, 2, 3], 0, fn)).toEqual([1, 2, 3])
    expect(await mapWithConcurrency([1, 2, 3], -5, fn)).toEqual([1, 2, 3])
    expect(await mapWithConcurrency([1, 2, 3], NaN, fn)).toEqual([1, 2, 3])
  })

  it('passes the item index to the callback', async () => {
    const out = await mapWithConcurrency(['a', 'b', 'c'], 2, async (_item, i) => i)
    expect(out).toEqual([0, 1, 2])
  })
})
