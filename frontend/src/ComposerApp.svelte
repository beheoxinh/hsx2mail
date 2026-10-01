<script lang="ts">
  // Load offline icon data before anything else
  import './lib/iconify-offline'

  import { onMount, onDestroy } from 'svelte'

  // Component-scope so onDestroy can reach them; declared inside onMount they were
  // out of scope there and the unsubscribes never ran.
  let unsubscribeThemeChanged: (() => void) | null = null
  let unsubscribeAppShutdown: (() => void) | null = null
  import Icon from '@iconify/svelte'
  import Composer from './lib/components/composer/Composer.svelte'
  import ToastContainer from './lib/components/ui/toast/ToastContainer.svelte'
  import SpellSuggestionMenu from './lib/spellcheck/SpellSuggestionMenu.svelte'
  import { addToast } from '$lib/stores/toast'
  import { _ } from '$lib/i18n'
  import { createComposerWindowApi } from '$lib/composerApi'
  import { getShowTitleBar, getNativeTitleBar, setShowTitleBar, setNativeTitleBar, setDarkComposerBody } from '$lib/stores/settings.svelte'
  import { initTheme, handleThemeChanged, type ThemeMode } from '$lib/stores/theme.svelte'
  // @ts-ignore - wailsjs imports
  import { GetComposeMode, PrepareReply, GetDraft, CloseWindow, GetThemeMode, GetSystemTheme, GetShowTitleBar, GetNativeTitleBar, GetDarkComposerBody, RefreshWindowConstraints, NotifyStartupComplete } from '../wailsjs/go/app/ComposerApp.js'
  // @ts-ignore - wailsjs imports
  import { smtp, app } from '../wailsjs/go/models'
  // @ts-ignore - wailsjs runtime
  import { WindowMinimise, WindowToggleMaximise, WindowShow, WindowSetTitle, EventsOn } from '../wailsjs/runtime/runtime'

  // Compose mode info from backend
  let composeMode = $state<app.ComposeMode | null>(null)
  let initialMessage = $state<smtp.ComposeMessage | null>(null)
  let loading = $state(true)
  let error = $state<string | null>(null)

  // Window state
  let isMaximized = $state(false)

  // Close request state - triggers Composer's close dialog
  let closeRequested = $state(false)

  // Dynamic title parts from Composer
  let titleTo = $state('')
  let titleSubject = $state('')

  // Window title based on mode + dynamic recipient/subject
  let windowTitle = $derived(() => {
    if (!composeMode) return $_('sidebar.compose')
    let base: string
    switch (composeMode.mode) {
      case 'reply': base = $_('composer.reply'); break
      case 'reply-all': base = $_('composer.replyAll'); break
      case 'forward': base = $_('composer.forward'); break
      default: base = composeMode.draftId ? $_('composer.editDraft') : $_('composer.newMessage')
    }
    if (!titleTo && !titleSubject) return base
    const detail = titleTo && titleSubject
      ? `${titleTo} | ${titleSubject}`
      : titleTo || titleSubject
    return `${base} — ${detail}`
  })

  function handleTitleChange(to: string, subject: string) {
    titleTo = to
    titleSubject = subject
    WindowSetTitle(windowTitle())
  }

  onMount(async () => {
    // Load title bar settings so the composer respects the user's preference
    try {
      const [stb, ntb, dcb] = await Promise.all([GetShowTitleBar(), GetNativeTitleBar(), GetDarkComposerBody()])
      setShowTitleBar(stb ?? true)
      setNativeTitleBar(ntb ?? false)
      setDarkComposerBody(dcb ?? false)
    } catch (err) {
      console.error('Failed to load title bar settings:', err)
    }

    // Load saved theme mode from backend and apply (probes XDG portal)
    try {
      const savedThemeMode = await GetThemeMode() as ThemeMode
      await initTheme(savedThemeMode, GetSystemTheme)
    } catch (err) {
      console.error('Failed to load theme mode:', err)
      await initTheme('system', GetSystemTheme)
    }

    // Show window after theme is applied (prevents white flash on startup)
    WindowShow()

    // Clear the desktop-environment startup indicator. Called after WindowShow()
    // so KDE/Plasma sees the placeholder → real window handoff cleanly (#154).
    NotifyStartupComplete()

    // Remove GTK max size constraints that Wails v2 sets at startup
    RefreshWindowConstraints()

    // Listen for theme changes from main window via IPC
    unsubscribeThemeChanged = EventsOn('theme:changed', (newTheme: string) => {
      handleThemeChanged(newTheme)
    })

    // Listen for shutdown request from main window
    unsubscribeAppShutdown = EventsOn('app:shutdown', (_reason: string) => {
      addToast({
        type: 'info',
        message: $_('toast.mainWindowClosing'),
      })
      // Give user a moment to see the toast, then close
      setTimeout(() => {
        CloseWindow()
      }, 1000)
    })

    // Load compose mode and initial data
    try {
      composeMode = await GetComposeMode()

      // If editing a draft, load it
      if (composeMode?.draftId) {
        const draft = await GetDraft()
        if (draft) {
          initialMessage = draft
        }
      }
      // If replying/forwarding, prepare the message
      else if (composeMode?.mode !== 'new' && composeMode?.messageId) {
        const prepared = await PrepareReply()
        if (prepared) {
          initialMessage = prepared
        }
      }
      // For new message, PrepareReply returns a message with just the From address
      else {
        const prepared = await PrepareReply()
        if (prepared) {
          initialMessage = prepared
        }
      }
    } catch (err) {
      console.error('Failed to initialize composer:', err)
      error = String(err)
    } finally {
      loading = false
    }
  })

  onDestroy(() => {
    unsubscribeThemeChanged?.()
    unsubscribeAppShutdown?.()
  })

  // Window control functions
  async function minimize() {
    await WindowMinimise()
  }

  async function toggleMaximize() {
    await WindowToggleMaximise()
    isMaximized = !isMaximized
  }

  // Request close - triggers Composer's close confirmation dialog
  function requestClose() {
    closeRequested = true
  }

  // Called when Composer has handled the close request (user made a choice)
  function handleCloseHandled() {
    closeRequested = false
  }

  // Handle composer close (after send or discard confirmation)
  function handleComposerClose() {
    CloseWindow()
  }

  // Handle message sent
  function handleMessageSent() {
    // The Composer component shows its own toast
    // Close the window after a brief delay
    setTimeout(() => {
      CloseWindow()
    }, 500)
  }
</script>

<div class="h-screen flex flex-col bg-background text-foreground">
  <!-- Custom Title Bar for frameless window -->
  {#if getShowTitleBar() && !getNativeTitleBar()}
    <header class="h-10 flex items-center justify-between bg-muted/50 border-b border-border select-none shrink-0">
      <!-- Drag region - left side with title -->
      <div class="flex-1 flex items-center gap-2 px-3 h-full" style="--wails-draggable: drag">
        <Icon icon="mdi:email-edit-outline" class="w-5 h-5 text-primary" />
        <span class="text-sm font-medium text-foreground">{windowTitle()}</span>
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
          onclick={requestClose}
          title={$_('window.close')}
          aria-label={$_('aria.closeWindow')}
        >
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" stroke="currentColor" stroke-width="1">
            <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" />
          </svg>
        </button>
      </div>
    </header>
  {/if}

  <!-- Main content -->
  <main class="flex-1 min-h-0 overflow-hidden">
    {#if loading}
      <div class="h-full flex items-center justify-center">
        <div class="flex flex-col items-center gap-3">
          <Icon icon="mdi:loading" class="w-8 h-8 animate-spin text-primary" />
          <span class="text-sm text-muted-foreground">{$_('common.loading')}</span>
        </div>
      </div>
    {:else if error}
      <div class="h-full flex items-center justify-center">
        <div class="flex flex-col items-center gap-3 text-center px-4">
          <Icon icon="mdi:alert-circle" class="w-12 h-12 text-destructive" />
          <p class="text-sm text-destructive">{error}</p>
          <button
            onclick={() => CloseWindow()}
            class="px-4 py-2 text-sm bg-muted hover:bg-muted/80 rounded-md transition-colors"
          >
            {$_('window.closeWindow')}
          </button>
        </div>
      </div>
    {:else if composeMode}
      <Composer
        accountId={composeMode.accountId}
        initialMessage={initialMessage}
        draftId={composeMode.draftId || null}
        messageId={composeMode.messageId || null}
        onClose={handleComposerClose}
        onSent={handleMessageSent}
        api={createComposerWindowApi(composeMode.accountId)}
        isDetached={true}
        closeRequested={closeRequested}
        onCloseHandled={handleCloseHandled}
        onTitleChange={handleTitleChange}
      />
    {/if}
  </main>
</div>

<!-- Toast notifications -->
<ToastContainer />

<!-- Spellcheck right-click suggestion menu -->
<SpellSuggestionMenu />
