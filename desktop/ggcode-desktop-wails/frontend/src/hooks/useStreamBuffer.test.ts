// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStreamBuffer } from './useStreamBuffer'

describe('useStreamBuffer', () => {
  it('flushes immediately in passthrough mode (enabled=false)', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, false))

    act(() => {
      result.current.buffer({ type: 'text', content: 'hello' })
    })

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith([{ type: 'text', content: 'hello' }])
  })

  it('accumulates buffer when enabled', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'a' })
      result.current.buffer({ type: 'text', content: 'b' })
      result.current.buffer({ type: 'reasoning', content: 'thinking' })
    })

    // Should not have been flushed yet by rAF (we haven't waited)
    expect(result.current.hasPending()).toBe(true)
  })

  it('flush() drains all pending chunks', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'a' })
      result.current.buffer({ type: 'text', content: 'b' })
    })

    expect(result.current.hasPending()).toBe(true)

    act(() => {
      result.current.flush()
    })

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith([
      { type: 'text', content: 'a' },
      { type: 'text', content: 'b' },
    ])
    expect(result.current.hasPending()).toBe(false)
  })

  it('hasPending() returns false when buffer is empty', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, false))
    expect(result.current.hasPending()).toBe(false)
  })

  it('handles messageID in buffered chunks', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'hello', messageID: 'msg-1' })
      result.current.flush()
    })

    expect(onFlush).toHaveBeenCalledWith([
      { type: 'text', content: 'hello', messageID: 'msg-1' },
    ])
  })
})
