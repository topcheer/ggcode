// #5308 行为钉测:草稿跨会话污染守卫的组件级验证。
// 与 ChatViewDraftPin.test.ts(源级不变量)互补:本文件挂载真实 ChatView,
// 复现 issue 场景「S1 输入未发送 → 切 S2」,断言
// ① draft:S2 原值未被 S1 残留覆写 ② 输入框恢复 S2 草稿(非 S1 文本)
// ③ draft:S1 保留 ④ 切换后 S2 内继续输入可正常保存。
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent, act, waitFor } from '@testing-library/react'

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    value: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false }),
    writable: true, configurable: true,
  })
}
if (typeof window !== 'undefined' && !(window as any).ResizeObserver) {
  ;(window as any).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
}

vi.mock('../../wailsjs/runtime/runtime', async (importOriginal) => {
  const actual: any = await importOriginal()
  const stubbed: Record<string, any> = {}
  for (const key of Object.keys(actual)) {
    stubbed[key] = () => Promise.resolve(undefined)
  }
  stubbed.EventsOn = () => () => {}
  return stubbed
})

vi.mock('../../wailsjs/go/main/App', async (importOriginal) => {
  const actual: any = await importOriginal()
  const stubbed: Record<string, any> = {}
  for (const key of Object.keys(actual)) {
    stubbed[key] = vi.fn().mockResolvedValue(undefined)
  }
  stubbed.GetSessionHistory = vi.fn().mockResolvedValue([])
  return stubbed
})

import { ChatView } from './ChatView'

beforeEach(() => {
  sessionStorage.clear()
  cleanup()
  vi.clearAllMocks()
})

describe('#5308 draft cross-session pollution (behavioral)', () => {
  it('switch S1(unsent text) -> S2 keeps S2 draft intact, restores it, preserves S1 draft', async () => {
    sessionStorage.setItem('draft:S2', 'S2-original')

    const { rerender, container } = render(<ChatView sessionId="S1" />)
    await waitFor(() => expect(container.querySelector('textarea')).toBeTruthy())

    // 在 S1 输入未发送文本;保存 effect 正常写入 draft:S1
    const textarea = container.querySelector('textarea')!
    await act(async () => {
      fireEvent.change(textarea, { target: { value: 'abc-S1-unsent' } })
    })
    await waitFor(() => expect(sessionStorage.getItem('draft:S1')).toBe('abc-S1-unsent'))

    // 切换会话(Layout key 是 workspace 非 session,组件实例保留)
    await act(async () => {
      rerender(<ChatView sessionId="S2" />)
    })

    // S1 旧闭包 input 不得覆写 draft:S2
    expect(sessionStorage.getItem('draft:S2')).toBe('S2-original')
    // S1 草稿保留
    expect(sessionStorage.getItem('draft:S1')).toBe('abc-S1-unsent')
    // 输入框显示 S2 自己的草稿
    expect(container.querySelector('textarea')!.value).toBe('S2-original')
  })

  it('S1 unsent text must not surface in S2 input (wrong-context mis-send face)', async () => {
    sessionStorage.setItem('draft:S2', 'S2-original')
    const { rerender, container } = render(<ChatView sessionId="S1" />)
    await waitFor(() => expect(container.querySelector('textarea')).toBeTruthy())
    const textarea = container.querySelector('textarea')!
    await act(async () => {
      fireEvent.change(textarea, { target: { value: 'secret-instruction-for-S1' } })
    })
    await waitFor(() => expect(sessionStorage.getItem('draft:S1')).toBe('secret-instruction-for-S1'))
    await act(async () => {
      rerender(<ChatView sessionId="S2" />)
    })
    const after = container.querySelector('textarea')!.value
    expect(after).not.toBe('secret-instruction-for-S1')
    expect(after).toBe('S2-original')
  })

  it('guard must not permanently block saves: typing in S2 after switch persists to draft:S2', async () => {
    const { rerender, container } = render(<ChatView sessionId="S1" />)
    await waitFor(() => expect(container.querySelector('textarea')).toBeTruthy())
    await act(async () => {
      rerender(<ChatView sessionId="S2" />)
    })
    const textarea = container.querySelector('textarea')!
    await act(async () => {
      fireEvent.change(textarea, { target: { value: 'typed-in-S2' } })
    })
    await waitFor(() => expect(sessionStorage.getItem('draft:S2')).toBe('typed-in-S2'))
  })
})
