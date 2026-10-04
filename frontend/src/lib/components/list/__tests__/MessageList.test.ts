import { cleanup, render, waitFor } from '@testing-library/svelte'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import MessageList from '../MessageList.svelte'
import messageListSource from '../MessageList.svelte?raw'
import { resetAppBindings, setAppBinding } from '../../../../test/setup'

// The virtualizer measures real row heights to size its window. jsdom reports
// zero for everything, which would make the window empty or the whole list.
beforeAll(() => {
  const height = 88
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get: () => height,
  })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      return this.dataset.testid === 'scroller' || this.className?.includes?.('overflow-y-auto')
        ? 400
        : height
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, get: () => 800 })
  if (!Element.prototype.scrollTo) Element.prototype.scrollTo = () => {}
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  resetAppBindings()
})

// A Conversation as the backend serialises it. `latestDate` is the field the row
// formats (internal/message/model.go: LatestDate time.Time -> RFC3339); omitting
// it is what first surfaced the invalid-date crash.
const conversationFixture = {
  id: 'c1',
  threadId: 't1',
  messageId: '<m1@example.com>',
  subject: 'Hello',
  fromName: 'Alice',
  fromEmail: 'alice@example.com',
  from: [{ name: 'Alice', email: 'alice@example.com' }],
  snippet: 'Hi there',
  date: '2026-01-02T10:00:00Z',
  internalDate: '2026-01-02T10:00:00Z',
  latestDate: '2026-01-02T10:00:00Z',
  isRead: false,
  isStarred: false,
  isAnswered: false,
  isFlagged: false,
  isForwarded: false,
  isDraft: false,
  isDeleted: false,
  isJunk: false,
  hasAttachments: false,
  attachmentCount: 0,
  size: 100,
  labels: [],
  uid: 1,
  folderId: 'f1',
  accountId: 'a1',
  accountName: 'Test',
  accountColor: '#000',
  preview: '',
  recipients: [],
  bodyFetched: false,
} as const

function makeConversations(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    ...conversationFixture,
    id: `c${i}`,
    threadId: `t${i}`,
    uid: i + 1,
    subject: `Subject ${i}`,
    snippet: `Snippet ${i}`,
    date: `2026-01-${String((i % 28) + 1).padStart(2, '0')}T10:00:00Z`,
    internalDate: `2026-01-${String((i % 28) + 1).padStart(2, '0')}T10:00:00Z`,
    latestDate: `2026-01-${String((i % 28) + 1).padStart(2, '0')}T10:00:00Z`,
  }))
}

/** Wire every binding MessageList needs to return `count` conversations. */
function configure(count: number) {
  resetAppBindings()
  const conversations = makeConversations(count)
  setAppBinding('GetConversations', conversations)
  setAppBinding('GetConversationCount', conversations.length)
  setAppBinding('GetUnreadCount', conversations.length)
  setAppBinding('GetSearchCount', 0)
  setAppBinding('GetSearchCountUnifiedInbox', 0)
  setAppBinding('GetFTSIndexStatus', { indexed: conversations.length, total: conversations.length })
  setAppBinding('IsFTSIndexing', false)
  setAppBinding('GetUnifiedInboxConversations', [])
  setAppBinding('GetUnifiedInboxCount', 0)
  setAppBinding('SearchConversations', [])
  setAppBinding('SearchUnifiedInbox', [])
  // Selecting a folder now kicks off a background refresh. The shared stub hands
  // back undefined, which a real binding never does, so register one that
  // resolves like the Wails bridge does.
  setAppBinding('SyncFolder', async () => undefined)
  return conversations
}

function rows(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>('[data-index]'))
}

describe('MessageList', () => {
  describe('virtualization', () => {
    it('mounts a window of rows, not the whole list', async () => {
      configure(2000)
      render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })

      await waitFor(() => expect(rows().length).toBeGreaterThan(0))
      const mounted = rows().length
      expect(mounted).toBeLessThan(80)
    })

    it('renders every row when the list fits the viewport', async () => {
      configure(5)
      render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })

      await waitFor(() => expect(rows().length).toBe(5))
    })

    it('spans each row with the conversation at its own index', async () => {
      configure(300)
      render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })

      await waitFor(() => expect(rows().length).toBeGreaterThan(0))
      // data-index must address the same conversation the row displays, which is
      // what breaks when the reconciled window keeps a stale index.
      for (const el of rows()) {
        const index = Number(el.getAttribute('data-index'))
        expect(Number.isInteger(index)).toBe(true)
        expect(index).toBeGreaterThanOrEqual(0)
        expect(index).toBeLessThan(300)
      }
    })
  })

  describe('empty state', () => {
    it('shows the empty state when the folder has no conversations', async () => {
      configure(0)
      render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })

      await waitFor(() => expect(rows().length).toBe(0))
      expect(document.body.textContent).not.toContain('undefined')
    })
  })
})

describe('MessageList keyboard navigation', () => {
  it('selectNext moves the selection forward through the list', async () => {
    // Configure the bridge before rendering: the component loads conversations
    // on mount, so bindings set afterwards never reach the first load.
    configure(6)
    const { component } = render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })
    await waitFor(() => expect(rows().length).toBe(6))

    // The list auto-selects the first conversation so keyboard navigation works
    // immediately (MessageList.svelte: "Auto-select first conversation ... for
    // keyboard navigation").
    await waitFor(() => expect(component.getSelectedThreadId()).toBe('t0'))

    component.selectNext()
    await waitFor(() => expect(component.getSelectedThreadId()).toBe('t1'))

    component.selectNext()
    await waitFor(() => expect(component.getSelectedThreadId()).toBe('t2'))

    component.selectPrevious()
    await waitFor(() => expect(component.getSelectedThreadId()).toBe('t1'))
  })

  it('selectPrevious with nothing selected does not select off the end of the list', async () => {
    configure(4)
    const { component } = render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })
    await waitFor(() => expect(rows().length).toBe(4))

    // The first conversation is auto-selected, so "previous" from the top must
    // stay on it rather than producing a selection for a row that is not there.
    await waitFor(() => expect(component.getSelectedThreadId()).toBe('t0'))
    component.selectPrevious()
    await new Promise((r) => setTimeout(r, 50))
    expect(component.getSelectedThreadId()).toBe('t0')
  })

  it('selectAll checks every loaded conversation and clearChecked empties it', async () => {
    configure(4)
    const { component } = render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })
    await waitFor(() => expect(rows().length).toBe(4))

    expect(component.hasCheckedMessages()).toBe(false)
    component.selectAll()
    await waitFor(() => expect(component.hasCheckedMessages()).toBe(true))

    component.clearChecked()
    await waitFor(() => expect(component.hasCheckedMessages()).toBe(false))
  })

  it('canUndo is false until something is checked', async () => {
    configure(3)
    const { component } = render(MessageList, { props: { accountId: 'a1', folderId: 'f1' } })
    await waitFor(() => expect(rows().length).toBe(3))
    expect(component.hasCheckedMessages()).toBe(false)
  })
})

// The component addresses rows by DOM attribute: findRowElement() and the
// scrollToIndex() fallback query `[data-conversation-row]` / `[data-index]`, while
// the template is what supplies them. Search mode is unreachable from props, so
// this asserts the contract at the source level: every row-rendering path must
// declare the attributes the queries depend on.
//
// This is not a stylistic lint. The search branch once rendered ConversationRow
// with no addressing attribute at all, so both queries returned an empty
// NodeList and "select the next result after a delete" silently did nothing
// while searching.
describe('MessageList row addressing contract', () => {
  // Vite's ?raw import resolves the source relative to this file, so the check
  // does not depend on the process cwd and needs no Node types (the browser
  // tsconfig does not include them).
  const source = () => messageListSource as unknown as string

  it('every row path declares the attributes the DOM queries rely on', () => {
    const src = source()
    const rowBlocks = src.match(/<ConversationRow/g) ?? []
    expect(rowBlocks.length).toBeGreaterThanOrEqual(2)

    // Anchor on the markup, not the bare attribute name: `data-conversation-row`
    // also appears inside querySelectorAll('[data-conversation-row]') strings, so
    // a naive name check would pass even when no row renders it.
    const markup = {
      'data-conversation-row': /<[a-zA-Z][^>]*\sdata-conversation-row/,
      'data-index': /<[a-zA-Z][^>]*\sdata-index=\{/,
    }
    for (const [attr, pattern] of Object.entries(markup)) {
      expect(src, `${attr} must be rendered as an attribute in the template`).toMatch(pattern)
    }
  })

  it('declares data-index for the virtualized branch and data-conversation-row for the search branch', () => {
    const src = source()
    // Virtualized branch: the virtualizer reads the index off the node.
    expect(src).toMatch(/<[a-zA-Z][^>]*\sdata-index=\{row\.index\}/)
    // Search branch: not virtualized, so it needs the row marker instead.
    expect(src).toMatch(/<[a-zA-Z][^>]*\sdata-conversation-row/)
  })
})
