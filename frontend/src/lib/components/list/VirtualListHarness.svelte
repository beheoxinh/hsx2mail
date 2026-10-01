// A deliberately small component that wires @tanstack/svelte-virtual exactly the
// way MessageList.svelte does — same scroll element, same absolute-positioned
// spacer, same data-index + reconcileWindow path, same $virtualizer store
// access — but without the 1800-line list's Wails bridge, i18n and folder state.
//
// It exists so the virtualization wiring itself is covered by a test: that only
// a window of rows is mounted, that row bodies resolve through row.index, and
// that a middle-of-window replacement is not missed. A pure-unit test of
// virtualWindow.ts cannot see any of that.
<script lang="ts">
  import { createVirtualizer } from '@tanstack/svelte-virtual'
  import type { VirtualItem } from '@tanstack/virtual-core'
  import { reconcileWindow } from './virtualWindow'

  interface Row {
    threadId: string
    label: string
  }

  let { rows = [], estimateSize = 40 }: { rows?: Row[]; estimateSize?: number } = $props()

  let scrollEl = $state<HTMLDivElement | null>(null)
  let virtualRows = $state<readonly VirtualItem[]>([])
  let lastWindow: readonly VirtualItem[] = []

  function rowKey(index: number): string {
    return rows[index]?.threadId ?? 'missing-row'
  }

  const virtualizer = createVirtualizer<HTMLDivElement, HTMLElement>({
    getScrollElement: () => scrollEl,
    // count starts at 0 and is kept in sync by the effect below: reading
    // rows.length here would capture the initial value only. Same as
    // MessageList.svelte.
    count: 0,
    estimateSize: () => estimateSize,
    getItemKey: rowKey,
    overscan: 2,
  })

  $effect.pre(() => {
    $virtualizer.setOptions({ count: rows.length })
  })

  $effect(() => {
    void scrollEl
    const next = $virtualizer.getVirtualItems()
    const reconciled = reconcileWindow(next, lastWindow)
    if (reconciled.changed) {
      lastWindow = reconciled.rows
      virtualRows = reconciled.rows
    }
  })

  function measureRow(node: HTMLElement) {
    queueMicrotask(() => $virtualizer.measureElement(node))
    return {
      destroy() {
        queueMicrotask(() => $virtualizer.measureElement(null))
      },
    }
  }
</script>

<div
  bind:this={scrollEl}
  data-testid="scroller"
  style="height: 200px; overflow-y: auto"
>
  <div
    data-testid="spacer"
    style="height: {$virtualizer.getTotalSize()}px; position: relative; width: 100%"
  >
    {#each virtualRows as row (row.key)}
      {@const item = rows[row.index]}
      <div
        data-index={row.index}
        data-testid="row"
        data-thread={item?.threadId}
        use:measureRow
        style="transform: translateY({row.start}px); position: absolute; top: 0; left: 0; width: 100%; height: {estimateSize}px"
      >
        {item?.label}
      </div>
    {/each}
  </div>
</div>

<div data-testid="total-size">{$virtualizer.getTotalSize()}</div>
