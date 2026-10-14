import { describe, expect, it } from 'vitest'
import { createIframeReadiness } from './iframeReadiness'

describe('createIframeReadiness', () => {
  it('starts unready with no document written', () => {
    const r = createIframeReadiness()
    expect(r.ready).toBe(false)
    expect(r.srcdoc).toBe('')
  })

  it('reports a first write as needing a reload', () => {
    const r = createIframeReadiness()
    expect(r.write('<p>a</p>')).toBe(true)
    expect(r.srcdoc).toBe('<p>a</p>')
    expect(r.ready).toBe(false)
  })

  it('does not reload when the markup is unchanged', () => {
    const r = createIframeReadiness()
    r.write('<p>a</p>')
    r.markReady()
    expect(r.write('<p>a</p>')).toBe(false)
    expect(r.ready).toBe(true)
  })

  it('drops readiness when the markup actually changes', () => {
    const r = createIframeReadiness()
    r.write('<p>a</p>')
    r.markReady()
    expect(r.write('<p>b</p>')).toBe(true)
    expect(r.ready).toBe(false)
  })

  it('becomes ready on the iframe signal', () => {
    const r = createIframeReadiness()
    r.write('<p>a</p>')
    r.markReady()
    expect(r.ready).toBe(true)
  })

  it('becomes ready on the watchdog signal so the skeleton cannot stick', () => {
    const r = createIframeReadiness()
    r.write('<p>a</p>')
    r.markReadyByTimeout()
    expect(r.ready).toBe(true)
  })

  it('treats a late signal for a superseded document as ready for the new one', () => {
    const r = createIframeReadiness()
    r.write('<p>a</p>')
    r.write('<p>b</p>')
    r.markReady()
    expect(r.ready).toBe(true)
    expect(r.srcdoc).toBe('<p>b</p>')
  })

  it('handles an empty body without leaving readiness stuck', () => {
    const r = createIframeReadiness()
    expect(r.write('')).toBe(false)
    r.markReadyByTimeout()
    expect(r.ready).toBe(true)
  })
})
