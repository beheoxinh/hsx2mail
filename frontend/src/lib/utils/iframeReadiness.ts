/**
 * Readiness handshake for the sandboxed srcdoc iframe that renders a mail body.
 *
 * The iframe owns the truth about when its document is painted, and reports it
 * with an `iframe-ready` postMessage. Two things make that signal unreliable
 * enough to need a guard:
 *
 * - Re-assigning `srcdoc` tears the document down and reloads it. A message
 *   that arrives for the *previous* document must not mark the new one ready.
 * - The postMessage can be dropped when a reload swaps the document mid-flight,
 *   which would leave the loading skeleton covering the message forever.
 *
 * This module keeps that bookkeeping out of the component so the rules can be
 * exercised directly: identical markup must not trigger a reload, and readiness
 * must always be reachable.
 */

export interface IframeReadiness {
  /** True once the current document has reported (or been assumed) painted. */
  readonly ready: boolean
  /** Last srcdoc written; '' before the first write. */
  readonly srcdoc: string
  /**
   * Record a new srcdoc. Returns true when it differs from the last one and the
   * caller must therefore write it and wait for a fresh readiness signal.
   */
  write(next: string): boolean
  /** Apply the iframe's own readiness signal. */
  markReady(): void
  /** Apply the watchdog signal used when the postMessage never arrives. */
  markReadyByTimeout(): void
}

export function createIframeReadiness(): IframeReadiness {
  let ready = false
  let srcdoc = ''

  return {
    get ready() {
      return ready
    },
    get srcdoc() {
      return srcdoc
    },
    write(next: string): boolean {
      if (next === srcdoc) return false
      srcdoc = next
      ready = false
      return true
    },
    markReady(): void {
      ready = true
    },
    markReadyByTimeout(): void {
      ready = true
    },
  }
}
