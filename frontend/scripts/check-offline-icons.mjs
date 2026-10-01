#!/usr/bin/env node
// Verifies that every icon name referenced in the app or in an extension
// resolves to one of the collections bundled for offline use.
//
// An unregistered or misspelled name makes @iconify/svelte fall back to
// https://api.iconify.design at render time, which both breaks offline use and
// leaks the user's mail-provider choices to a third party. This is a plain
// script rather than a test framework: the repo has no frontend test runner and
// one assertion does not justify adding one.
//
// Usage: node scripts/check-offline-icons.mjs
// Exits non-zero and lists the offending names on failure.

import { readFileSync, readdirSync } from 'node:fs'
import { dirname, extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

// Must stay in sync with src/lib/iconify-offline.ts.
const COLLECTION_NAMES = ['mdi', 'lucide', 'logos', 'simple-icons']

const scriptDir = dirname(fileURLToPath(import.meta.url))
const frontendRoot = join(scriptDir, '..')
const repoRoot = join(frontendRoot, '..')
const scanRoots = [join(frontendRoot, 'src'), join(repoRoot, 'extensions')]

const ICON_RE = /["'`]([a-z0-9-]+):([a-z0-9-]+)["'`]/g

function sourceFiles(dir) {
  const out = []
  let entries
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return out
  }
  for (const entry of entries) {
    if (['node_modules', 'dist', 'build', '.git'].includes(entry.name)) continue
    const path = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...sourceFiles(path))
    else if (['.svelte', '.ts'].includes(extname(entry.name))) out.push(path)
  }
  return out
}

const icons = {}
for (const name of COLLECTION_NAMES) {
  const mod = await import(`@iconify-json/${name}/icons.json`, { with: { type: 'json' } })
  icons[name] = new Set(Object.keys(mod.default?.icons ?? mod.icons ?? {}))
}

const missing = []
for (const root of scanRoots) {
  for (const file of sourceFiles(root)) {
    const src = readFileSync(file, 'utf8')
    for (const match of src.matchAll(ICON_RE)) {
      const set = icons[match[1]]
      if (!set) continue // collection not bundled at all: a separate check
      if (!set.has(match[2])) {
        missing.push(`${match[1]}:${match[2]}  (${relative(repoRoot, file)})`)
      }
    }
  }
}

// Also assert the registration file still covers every collection we rely on.
const offlineSrc = readFileSync(join(frontendRoot, 'src', 'lib', 'iconify-offline.ts'), 'utf8')
for (const name of COLLECTION_NAMES) {
  if (!offlineSrc.includes(`@iconify-json/${name}/icons.json`)) {
    missing.push(`iconify-offline.ts does not import @iconify-json/${name}/icons.json`)
  }
}

if (missing.length > 0) {
  console.error('Unresolvable offline icon names:\n  ' + missing.join('\n  '))
  process.exit(1)
}
console.log(`All offline icon names resolve (${COLLECTION_NAMES.length} collections).`)
