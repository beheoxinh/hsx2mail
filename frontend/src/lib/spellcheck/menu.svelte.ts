// Shared state for the spellcheck suggestion menu. The ProseMirror plugin
// populates it (with position, word, suggestions, and action closures bound to
// its editor view) on right-click of a misspelled word; a single
// <SpellSuggestionMenu /> mounted at app root renders it.

interface SpellMenuState {
  open: boolean
  keyboard: boolean // opened via F7 → enable arrow/Enter navigation + auto-focus
  x: number
  y: number
  word: string
  suggestions: string[]
  onReplace: (replacement: string) => void
  onAdd: () => void
  onIgnore: () => void
}

const noop = () => {}

let state = $state<SpellMenuState>({
  open: false,
  keyboard: false,
  x: 0,
  y: 0,
  word: '',
  suggestions: [],
  onReplace: noop,
  onIgnore: noop,
  onAdd: noop,
})

export const spellMenu = {
  get state() {
    return state
  },
  open(next: Omit<SpellMenuState, 'open'>) {
    state = { ...next, open: true }
  },
  close() {
    if (state.open) state = { ...state, open: false }
  },
}
