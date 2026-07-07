/**
 * useStreamBuffer — Token batching hook for smooth streaming UX
 *
 * Research basis: ChatGPT and other production AI chat UIs don't re-render
 * on every token. Instead they buffer tokens in a ref and flush to state
 * at regular intervals (~30-50ms), reducing React re-renders from
 * hundreds per second to ~20 per second.
 *
 * Source: akashbuilds.com/blog/chatgpt-stream-text-react (2025)
 * Source: thefrontkit.com/blogs/ai-chat-ui-best-practices (2026)
 *
 * Usage:
 *   const { buffer, flush } = useStreamBuffer()
 *
 *   // In stream event handler:
 *   case 'text':
 *     buffer(p.content)  // non-blocking, just appends to ref
 *     break
 *
 *   // The hook auto-flushes via requestAnimationFrame:
 *   // - Text/reasoning tokens batched and flushed every animation frame
 *   // - Non-stream events (tool_call, done, error) trigger immediate flush
 *
 * Integration plan (requires ChatView.tsx changes — coordinate with team):
 *   1. Replace direct setMessages in 'text' case with buffer()
 *   2. Replace direct setMessages in 'reasoning' case with buffer()
 *   3. Add flush() call before tool_call_done, done, error events
 *   4. The flush callback updates setMessages with all buffered content at once
 */

import { useRef, useCallback, useEffect } from 'react'

export interface BufferedChunk {
  type: 'text' | 'reasoning'
  content: string
  messageID?: string
}

export interface StreamBufferAPI {
  /** Add a chunk to the buffer. Non-blocking — just appends to a ref. */
  buffer: (chunk: BufferedChunk) => void
  /** Immediately flush all buffered chunks. Returns true if any were flushed. */
  flush: () => void
  /** Whether there are pending buffered chunks. */
  hasPending: () => boolean
}

/**
 * Token batching hook that collects streaming text/reasoning chunks
 * in a ref and flushes them on requestAnimationFrame boundaries.
 *
 * @param onFlush Called with all buffered chunks when they're flushed.
 *   The callback should update React state (setMessages).
 * @param enabled When false, flushes immediately on every buffer() call
 *   (passthrough mode). Set to true only during active streaming.
 */
export function useStreamBuffer(
  onFlush: (chunks: BufferedChunk[]) => void,
  enabled: boolean,
): StreamBufferAPI {
  const bufferRef = useRef<BufferedChunk[]>([])
  const onFlushRef = useRef(onFlush)
  const enabledRef = useRef(enabled)
  const rafRef = useRef<number | null>(null)

  // Keep refs in sync without re-creating the flush loop
  useEffect(() => {
    onFlushRef.current = onFlush
  }, [onFlush])

  useEffect(() => {
    enabledRef.current = enabled
  }, [enabled])

  const doFlush = useCallback(() => {
    if (bufferRef.current.length === 0) return
    const chunks = bufferRef.current
    bufferRef.current = []
    onFlushRef.current(chunks)
  }, [])

  // rAF loop — only runs while enabled (during streaming)
  useEffect(() => {
    if (!enabled) {
      // When disabled, flush any remaining and stop the loop
      doFlush()
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
      return
    }

    const tick = () => {
      doFlush()
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)

    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
    }
  }, [enabled, doFlush])

  const buffer = useCallback((chunk: BufferedChunk) => {
    if (!enabledRef.current) {
      // Passthrough mode — flush immediately
      onFlushRef.current([chunk])
      return
    }
    bufferRef.current.push(chunk)
  }, [])

  const flush = useCallback(() => {
    doFlush()
  }, [doFlush])

  const hasPending = useCallback(() => {
    return bufferRef.current.length > 0
  }, [])

  return { buffer, flush, hasPending }
}
