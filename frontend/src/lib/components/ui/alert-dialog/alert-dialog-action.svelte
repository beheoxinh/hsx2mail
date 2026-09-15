<script lang="ts">
  import { AlertDialog as AlertDialogPrimitive } from 'bits-ui'
  import { cn } from '$lib/utils'
  import type { Snippet } from 'svelte'

  interface Props {
    class?: string
    children?: Snippet
    onclick?: (e: MouseEvent) => void
    disabled?: boolean
  }

  let { class: className, children, onclick, disabled }: Props = $props()
</script>

<AlertDialogPrimitive.Action
  class={cn(
    'inline-flex h-10 items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground ring-offset-background transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50',
    className
  )}
  {onclick}
  {disabled}
>
  {#if children}
    {@render children()}
  {/if}
</AlertDialogPrimitive.Action>
