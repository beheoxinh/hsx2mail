/**
 * Frontend logger that bridges to the Go-side zerolog stream.
 *
 * Use this for any frontend diagnostic output (state transitions, unexpected
 * branches, debug instrumentation) so messages appear in the same log stream
 * as backend events. Messages are tagged with component=frontend on the Go
 * side, making them easy to filter.
 *
 * Wails has its own runtime.LogInfo/LogError but that output doesn't appear
 * in the dev terminal alongside zerolog output. This wrapper goes through a
 * Wails-bound Go method (App.LogFrontend) so all logs land in one place.
 *
 * Usage:
 *   import { logger } from '$lib/logger'
 *   logger.debug('user clicked send')
 *   logger.error(`failed to load: ${err}`)
 *
 * Note: currently routes through App.LogFrontend (main-window binding).
 * The detached composer process has its own ComposerApp.LogFrontend with
 * component=composer-frontend; composer-only code can call that directly
 * if needed.
 */

// @ts-ignore - Wails generated bindings
import { LogFrontend } from '../../wailsjs/go/app/App.js'

function emit(level: string, message: string): void {
  // Fire-and-forget — never await, never throw. Swallow errors so a logger
  // failure can't break the calling code path.
  LogFrontend(level, message).catch(() => {})
}

export const logger = {
  debug: (message: string) => emit('debug', message),
  info: (message: string) => emit('info', message),
  warn: (message: string) => emit('warn', message),
  error: (message: string) => emit('error', message),
}
