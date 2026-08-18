// @vitest-environment jsdom
// Issue #646: the session:changed handler was registered in a useEffect
// with an empty dependency array, so reading the `activeSessionId` state
// inside it was a stale closure — it always saw the first-render undefined,
// the `if (activeSessionId)` guard was permanently falsy, and the empty-sid
// reset branch from #630 was dead code.
//
// Discriminating assertion: fire session:changed with a real sid, then with
// ''. The sessionId passed down to ChatView must flip to ''. With the stale
// closure the reset never executed, so ChatView kept receiving the deleted
// session's id and kept rendering its transcript.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, act } from '@testing-library/react'

// jsdom lacks localStorage/matchMedia in this vitest config (same shim the
// useTheme tests install).
if (typeof globalThis.localStorage === 'undefined' || !globalThis.localStorage) {
  const store: Record<string, string> = {}
  const lsMock = {
    getItem: (k: string) => store[k] ?? null,
    setItem: (k: string, v: string) => { store[k] = v },
    removeItem: (k: string) => { delete store[k] },
    clear: () => { for (const k of Object.keys(store)) delete store[k] },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: lsMock, writable: true, configurable: true })
  if (typeof window !== 'undefined') {
    Object.defineProperty(window, 'localStorage', { value: lsMock, writable: true, configurable: true })
  }
}
if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    value: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false }),
    writable: true, configurable: true,
  })
}

const { sessionChangedHandlers, chatViewSessionIdRef } = vi.hoisted(() => ({
  sessionChangedHandlers: [] as Array<(data: any) => void>,
  chatViewSessionIdRef: { current: undefined as string | undefined },
}))

// Layout pulls a wide surface from the wails runtime/App modules. Partial
// mocks keep every real export name defined (vi errors on missing ones);
// App functions are stubbed to resolved promises, EventsOn is captured.
vi.mock('../../wailsjs/runtime/runtime', async (importOriginal) => {
  const actual: any = await importOriginal()
  const stubbed: Record<string, any> = {}
  for (const key of Object.keys(actual)) {
    // Real wails bindings dereference window.runtime (undefined in jsdom) —
    // stub every export with a resolved promise; only EventsOn needs real
    // capture.
    stubbed[key] = () => Promise.resolve(undefined)
  }
  stubbed.EventsOn = (name: string, cb: (data: any) => void) => {
    if (name === 'session:changed') sessionChangedHandlers.push(cb)
    return () => {}
  }
  return stubbed
})

vi.mock('../../wailsjs/go/main/App', async (importOriginal) => {
  const actual: any = await importOriginal()
  const stubbed: Record<string, any> = {}
  for (const key of Object.keys(actual)) {
    stubbed[key] = vi.fn().mockResolvedValue(undefined)
  }
  return stubbed
})

// ChatView's own dependency graph is irrelevant to Layout's state logic —
// capture the sessionId prop it receives instead of rendering the real one.
vi.mock('./ChatView', () => ({
  ChatView: (props: any) => {
    chatViewSessionIdRef.current = props.sessionId
    return null
  },
}))

import { Layout } from './Layout'

const fireSessionChanged = (sessionId: string) => {
  for (const cb of [...sessionChangedHandlers]) cb({ sessionId })
}

afterEach(cleanup)

describe('#646 session:changed stale closure', () => {
  beforeEach(() => {
    sessionChangedHandlers.length = 0
    chatViewSessionIdRef.current = undefined
  })

  it('empty sid after a real session resets the sessionId passed to ChatView', async () => {
    render(<Layout />)

    // Backend loads a session — the ref-mirrored state must track it.
    act(() => fireSessionChanged('sid-123'))
    expect(chatViewSessionIdRef.current).toBe('sid-123')

    // Backend then clears it (active session deleted).
    act(() => fireSessionChanged(''))

    // With the stale closure, the reset branch never executed and ChatView
    // kept receiving the deleted session's id.
    expect(chatViewSessionIdRef.current).toBe('')
  })

  it('empty sid with no prior session does not manufacture a reset', async () => {
    render(<Layout />)
    act(() => fireSessionChanged(''))
    expect(chatViewSessionIdRef.current).toBeUndefined()
  })
})
