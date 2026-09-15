<script lang="ts">
  import { ContextMenu as ContextMenuPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import Icon from '@iconify/svelte'
  import type { Snippet } from 'svelte'

  interface Props {
    class?: string
    inset?: boolean
    children?: Snippet
  }

  let { class: className, inset = false, children }: Props = $props()
</script>

<ContextMenuPrimitive.SubTrigger
  class={cn(
    'flex cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none',
    'focus:bg-accent focus:text-accent-foreground',
    'data-[state=open]:bg-accent data-[state=open]:text-accent-foreground',
    inset && 'pl-8',
    className
  )}
>
  {#if children}
    {@render children()}
  {/if}
  <Icon icon="mdi:chevron-right" class="ml-auto h-4 w-4" />
</ContextMenuPrimitive.SubTrigger>
