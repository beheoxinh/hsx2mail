/// <reference lib="webworker" />
// Spellcheck worker: runs nspell + hunspell dictionaries off the main thread.
// Dictionaries are fetched lazily as static assets (/spellcheck/<locale>.{aff,dic})
// only when a language is enabled. A word is considered correct if it is valid
// in ANY enabled dictionary (multi-language model), so mixing languages in one
// message doesn't produce false positives.
import nspell from 'nspell'
import { gunzipSync } from 'fflate'

type Spell = ReturnType<typeof nspell>

const spells = new Map<string, Spell>()
const loading = new Map<string, Promise<void>>()
const customWords = new Set<string>()
let enabled: string[] = []

// Fetches a dictionary asset and returns UTF-8 text. The asset is gzipped on
// disk, but a dev server may serve it with Content-Encoding: gzip (the webview
// then hands us already-decompressed bytes), while the production asset server
// returns the raw gzip. Detect via the gzip magic number (1f 8b) and only
// decompress when it's actually gzipped. fflate (pure JS) is used rather than
// the native DecompressionStream so it works on webkit2gtk < 2.42 too.
async function fetchDictText(url: string): Promise<string> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`fetch ${url}: ${res.status}`)
  const buf = new Uint8Array(await res.arrayBuffer())
  const gzipped = buf.length > 1 && buf[0] === 0x1f && buf[1] === 0x8b
  return new TextDecoder('utf-8').decode(gzipped ? gunzipSync(buf) : buf)
}

async function load(locale: string): Promise<void> {
  if (spells.has(locale)) return
  let p = loading.get(locale)
  if (!p) {
    p = (async () => {
      const [aff, dic] = await Promise.all([
        fetchDictText(`/spellcheck/${locale}.aff.gz`),
        fetchDictText(`/spellcheck/${locale}.dic.gz`),
      ])
      const spell = nspell(aff, dic)
      for (const w of customWords) spell.add(w)
      spells.set(locale, spell)
    })()
    loading.set(locale, p)
  }
  await p
}

function isMisspelled(word: string): boolean {
  let anyLoaded = false
  for (const locale of enabled) {
    const spell = spells.get(locale)
    if (!spell) continue
    anyLoaded = true
    if (spell.correct(word)) return false
  }
  // Don't flag anything until at least one enabled dictionary has loaded.
  return anyLoaded
}

function correctInAny(word: string): boolean {
  for (const locale of enabled) {
    const spell = spells.get(locale)
    if (spell && spell.correct(word)) return true
  }
  return false
}

// a-z plus the accented letters used by the shipped European dictionaries, so
// substitution/insertion candidates can reach accented corrections.
const SUGGEST_ALPHABET = 'abcdefghijklmnopqrstuvwxyzàáâäãåæçèéêëìíîïñòóôöõøœùúûüýÿ'

// All edit-distance-1 variants of a word, ordered by how common the typo is:
// adjacent transposition, substitution, insertion, then deletion.
function editCandidates(w: string): string[] {
  const out: string[] = []
  for (let i = 0; i < w.length - 1; i++) {
    out.push(w.slice(0, i) + w[i + 1] + w[i] + w.slice(i + 2))
  }
  for (const c of SUGGEST_ALPHABET) {
    for (let i = 0; i < w.length; i++) out.push(w.slice(0, i) + c + w.slice(i + 1))
  }
  for (const c of SUGGEST_ALPHABET) {
    for (let i = 0; i <= w.length; i++) out.push(w.slice(0, i) + c + w.slice(i))
  }
  for (let i = 0; i < w.length; i++) out.push(w.slice(0, i) + w.slice(i + 1))
  return out
}

// Suggestions: our edit-distance-1 corrections first (nspell's own suggester
// misses obvious transpositions like teh→the), then nspell's suggestions.
function suggestFor(word: string): string[] {
  const seen = new Set<string>([word])
  const result: string[] = []
  for (const cand of editCandidates(word)) {
    if (seen.has(cand) || !correctInAny(cand)) continue
    seen.add(cand)
    result.push(cand)
  }
  for (const locale of enabled) {
    const spell = spells.get(locale)
    if (!spell) continue
    for (const sug of spell.suggest(word)) {
      if (seen.has(sug)) continue
      seen.add(sug)
      result.push(sug)
    }
  }
  return result.slice(0, 8)
}

function post(message: unknown): void {
  ;(self as unknown as DedicatedWorkerGlobalScope).postMessage(message)
}

self.onmessage = async (e: MessageEvent) => {
  const msg = e.data
  switch (msg.type) {
    case 'setLanguages': {
      enabled = msg.locales as string[]
      try {
        await Promise.all(enabled.map(load))
      } catch (err) {
        post({ type: 'error', message: `dict load failed: ${(err as Error)?.message ?? err}` })
        return
      }
      // selfTest: with a dictionary loaded, "teh" must read as misspelled.
      post({ type: 'ready', locales: enabled, loaded: [...spells.keys()], selfTest: isMisspelled('teh') })
      return
    }
    case 'check': {
      const misspelled = (msg.words as string[]).filter(isMisspelled)
      post({ type: 'checked', id: msg.id, misspelled })
      return
    }
    case 'suggest': {
      post({ type: 'suggested', id: msg.id, suggestions: suggestFor(msg.word) })
      return
    }
    case 'addWord': {
      customWords.add(msg.word)
      for (const spell of spells.values()) spell.add(msg.word)
      return
    }
    case 'removeWord': {
      customWords.delete(msg.word)
      for (const spell of spells.values()) spell.remove(msg.word)
      return
    }
  }
}
