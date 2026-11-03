// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, act } from '@testing-library/react'

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

vi.mock('../../wailsjs/runtime/runtime', async (importOriginal) => {
  const actual: any = await importOriginal()
  const stubbed: Record<string, any> = {}
  for (const key of Object.keys(actual)) {
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

    act(() => fireSessionChanged('sid-123'))
    expect(chatViewSessionIdRef.current).toBe('sid-123')

    act(() => fireSessionChanged(''))

    expect(chatViewSessionIdRef.current).toBe('')
  })

  it('empty sid with no prior session does not manufacture a reset', async () => {
    render(<Layout />)
    act(() => fireSessionChanged(''))
    expect(chatViewSessionIdRef.current).toBeUndefined()
  })
})
