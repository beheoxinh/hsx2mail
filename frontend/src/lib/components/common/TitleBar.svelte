<script lang="ts">
  import Icon from '@iconify/svelte'
  import { onMount } from 'svelte'
  import { _ } from '$lib/i18n'
  import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit } from '../../../../wailsjs/runtime/runtime'

  interface Props {
    onClose?: () => void
  }

  let { onClose }: Props = $props()

  let isMaximized = $state(false)

  // Sync with the real WM state on mount — the window may start maximized
  // (and the user can maximize via double-click on the drag region or the WM).
  onMount(async () => {
    try {
      isMaximized = await WindowIsMaximised()
    } catch {
      // Runtime not available (e.g. plain browser dev server) — keep false.
    }
  })

  async function minimize() {
    await WindowMinimise()
  }

  async function toggleMaximize() {
    await WindowToggleMaximise()
    isMaximized = !isMaximized
  }

  function close() {
    if (onClose) {
      onClose()
    } else {
      // For windows without custom onClose (e.g., composer), just quit directly
      Quit()
    }
  }
</script>

<header class="h-10 flex items-center justify-between bg-muted/50 border-b border-border select-none shrink-0">
  <!-- Drag region - left side with app title -->
  <div class="flex-1 flex items-center gap-2 px-3 h-full" style="--wails-draggable: drag">
    <Icon icon="mdi:email-fast-outline" class="w-5 h-5 text-primary" />
    <span class="text-sm font-medium text-foreground">Email Hub</span>
  </div>

  <!-- Windows-style window controls (square, full-height, icon-only) -->
  <div
    class="flex items-stretch h-full"
    role="group"
    aria-label={$_('aria.windowControls')}
  >
    <!-- Minimize -->
    <button
      class="w-12 h-full flex items-center justify-center text-foreground/80 hover:bg-foreground/10 active:bg-foreground/20 transition-colors"
      onclick={minimize}
      title={$_('window.minimize')}
      aria-label={$_('aria.minimizeWindow')}
    >
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <rect x="0" y="4.5" width="10" height="1" fill="currentColor" />
      </svg>
    </button>

    <!-- Maximize / Restore -->
    <button
      class="w-12 h-full flex items-center justify-center text-foreground/80 hover:bg-foreground/10 active:bg-foreground/20 transition-colors"
      onclick={toggleMaximize}
      title={isMaximized ? $_('window.restore') : $_('window.maximize')}
      aria-label={isMaximized ? $_('aria.restoreWindow') : $_('aria.maximizeWindow')}
    >
      {#if isMaximized}
        <!-- Restore: two overlapping squares -->
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1">
          <rect x="0.5" y="2.5" width="7" height="7" />
          <path d="M2.5 2.5V0.5h7v7h-2" />
        </svg>
      {:else}
        <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1">
          <rect x="0.5" y="0.5" width="9" height="9" />
        </svg>
      {/if}
    </button>

    <!-- Close -->
    <button
      class="w-12 h-full flex items-center justify-center text-foreground/80 hover:bg-[#c42b1c] hover:text-white active:bg-[#b02718] transition-colors"
      onclick={close}
      title={$_('window.close')}
      aria-label={$_('aria.closeWindow')}
    >
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" stroke="currentColor" stroke-width="1">
        <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" />
      </svg>
    </button>
  </div>
</header>
