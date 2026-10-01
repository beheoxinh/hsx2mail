import { describe, expect, it } from 'vitest'
import {
  indexOfThread,
  nextIndexAfterRemoval,
  reconcileWindow,
  rowStyle,
  threadIdsInRange,
  windowChanged,
  type WindowItem,
} from './virtualWindow'

function row(index: number, key?: string, start = index * 100): WindowItem {
  return { key: key ?? `t-${index}`, index, start }
}

describe('windowChanged', () => {
  it('reports no change for two identical windows', () => {
    const applied = [row(0), row(1), row(2)]
    expect(windowChanged([row(0), row(1), row(2)], applied)).toBe(false)
  })

  it('reports no change for two empty windows', () => {
    expect(windowChanged([], [])).toBe(false)
  })

  // This is the regression that shipped once: the guard compared length and the
  // first key only, so a middle row changing went unnoticed and the list kept
  // rendering the old conversation.
  it('detects a replacement in the MIDDLE of an unchanged window', () => {
    const applied = [row(0), row(1, 't-1'), row(2)]
    const next = [row(0), row(1, 't-REPLACED'), row(2)]
    expect(next.length).toBe(applied.length)
    expect(next[0].key).toBe(applied[0].key)
    expect(windowChanged(next, applied)).toBe(true)
  })

  it('detects a row whose offset moved but identity did not', () => {
    const applied = [row(0), row(1, 't-1', 100)]
    const next = [row(0), row(1, 't-1', 140)]
    expect(windowChanged(next, applied)).toBe(true)
  })

  it('detects a different length', () => {
    expect(windowChanged([row(0), row(1)], [row(0)])).toBe(true)
    expect(windowChanged([row(0)], [row(0), row(1)])).toBe(true)
  })

  it('detects a different index at the same position', () => {
    const applied = [{ key: 'a', index: 0, start: 0 }]
    const next = [{ key: 'a', index: 7, start: 0 }]
    expect(windowChanged(next, applied)).toBe(true)
  })

  it('detects growth from empty', () => {
    expect(windowChanged([row(0)], [])).toBe(true)
    expect(windowChanged([], [row(0)])).toBe(true)
  })

  it('treats bigint keys like any other key', () => {
    const applied: WindowItem[] = [{ key: 10n, index: 0, start: 0 }]
    const next: WindowItem[] = [{ key: 11n, index: 0, start: 0 }]
    expect(windowChanged(next, applied)).toBe(true)
  })
})

describe('reconcileWindow', () => {
  it('returns the same array reference when nothing changed', () => {
    // The reference identity is load-bearing: assigning a fresh array to the
    // $state that feeds the effect makes the effect re-trigger itself.
    const applied = [row(0), row(1)]
    const result = reconcileWindow([row(0), row(1)], applied)
    expect(result.changed).toBe(false)
    expect(result.rows).toBe(applied)
  })

  it('returns a copy of the new window when it changed', () => {
    const applied = [row(0)]
    const next = [row(0), row(1)]
    const result = reconcileWindow(next, applied)
    expect(result.changed).toBe(true)
    expect(result.rows).not.toBe(next)
    expect(result.rows).toEqual(next)
  })

  it('never hands back the caller-owned array', () => {
    const next = [row(0), row(1)]
    const result = reconcileWindow(next, [])
    expect(result.rows).not.toBe(next)
  })
})

describe('rowStyle', () => {
  it('positions the row absolutely at its window offset', () => {
    const style = rowStyle(row(3, 't-3', 320))
    expect(style).toContain('translateY(320px)')
    expect(style).toContain('position: absolute')
    expect(style).toContain('width: 100%')
  })

  it('offsets rows with a transform, not a pixel top', () => {
    // transform keeps the row out of layout flow while the virtualizer
    // positions it, which is what makes scrolling cheap. `top: 0` is the
    // anchor for the transform, not an offset.
    const style = rowStyle(row(5, 't-5', 500))
    expect(style).toContain('translateY(500px)')
    expect(style).toContain('top: 0')
    expect(style).not.toMatch(/top:\s*500px/)
  })
})

describe('indexOfThread', () => {
  const list = [{ threadId: 'a' }, { threadId: 'b' }, { threadId: 'c' }]

  it('finds a present thread', () => {
    expect(indexOfThread(list, 'b')).toBe(1)
  })

  it('returns -1 for a missing thread', () => {
    expect(indexOfThread(list, 'zzz')).toBe(-1)
  })

  it('returns -1 for an empty list', () => {
    expect(indexOfThread([], 'a')).toBe(-1)
  })
})

describe('threadIdsInRange', () => {
  const list = [{ threadId: 'a' }, { threadId: 'b' }, { threadId: 'c' }, { threadId: 'd' }]

  it('returns an inclusive range', () => {
    expect(threadIdsInRange(list, 1, 2)).toEqual(['b', 'c'])
  })

  it('normalises a backwards range', () => {
    expect(threadIdsInRange(list, 2, 0)).toEqual(['a', 'b', 'c'])
  })

  it('returns a single id when both ends are equal', () => {
    expect(threadIdsInRange(list, 2, 2)).toEqual(['c'])
  })

  // A stale anchor from before a reload used to select nothing at all.
  it('clamps an anchor past the end of a shortened list', () => {
    expect(threadIdsInRange(list, 3, 99)).toEqual(['d'])
  })

  it('clamps a negative anchor', () => {
    expect(threadIdsInRange(list, -5, 1)).toEqual(['a', 'b'])
  })

  it('returns nothing for an empty list', () => {
    expect(threadIdsInRange([], 0, 5)).toEqual([])
  })
})

describe('nextIndexAfterRemoval', () => {
  it('keeps the index when rows remain after it', () => {
    expect(nextIndexAfterRemoval(3, 10)).toBe(3)
  })

  it('clamps to the last index when the tail was removed', () => {
    expect(nextIndexAfterRemoval(9, 10)).toBe(9)
    expect(nextIndexAfterRemoval(5, 3)).toBe(2)
  })

  it('never goes below zero', () => {
    expect(nextIndexAfterRemoval(0, 4)).toBe(0)
  })

  it('reports -1 when nothing is left', () => {
    expect(nextIndexAfterRemoval(0, 0)).toBe(-1)
  })
})
