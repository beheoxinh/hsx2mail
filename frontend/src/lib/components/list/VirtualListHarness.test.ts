import { cleanup, render, screen } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import VirtualListHarness from './VirtualListHarness.svelte'

function makeRows(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    threadId: `t-${i}`,
    label: `conversation ${i}`,
  }))
}

function mountedThreads(): string[] {
  return screen.getAllByTestId('row').map((el) => el.getAttribute('data-thread') ?? '')
}

beforeEach(() => {
  // The virtualizer measures from the DOM; jsdom reports zero for every
  // element, so a real clientHeight is stubbed to make the window deterministic.
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      return this.dataset.testid === 'scroller' ? 200 : 40
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      return 40
    },
  })
})

afterEach(cleanup)

describe('virtualized conversation list', () => {
  it('mounts only a window of rows for a long list', async () => {
    const rows = makeRows(1000)
    render(VirtualListHarness, { props: { rows, estimateSize: 40 } })

    // 200px viewport / 40px rows = 5 visible, plus overscan 2 on each side.
    await Promise.resolve()
    const mounted = screen.queryAllByTestId('row')
    expect(mounted.length).toBeGreaterThan(0)
    expect(mounted.length).toBeLessThan(50)
  })

  it('resizes the spacer to the full scroll height', async () => {
    const rows = makeRows(200)
    render(VirtualListHarness, { props: { rows, estimateSize: 40 } })
    await Promise.resolve()

    const total = Number(screen.getByTestId('total-size').textContent ?? '0')
    expect(total).toBe(200 * 40)
  })

  it('resolves each row body through its own index', async () => {
    const rows = makeRows(300)
    render(VirtualListHarness, { props: { rows, estimateSize: 40 } })
    await Promise.resolve()

    // The rendered label must match the thread at that index, which is what
    // breaks if the window reconciliation keeps a stale index.
    for (const el of screen.queryAllByTestId('row')) {
      const index = Number(el.getAttribute('data-index'))
      const thread = el.getAttribute('data-thread')
      expect(thread).toBe(`t-${index}`)
      expect(el.textContent?.trim()).toBe(rows[index].label)
    }
  })

  it('mounts everything for a list that fits the viewport', async () => {
    const rows = makeRows(3)
    render(VirtualListHarness, { props: { rows, estimateSize: 40 } })
    await Promise.resolve()

    expect(mountedThreads()).toHaveLength(3)
  })
})
