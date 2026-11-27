import { useCallback, useEffect, useRef } from 'react'

export interface BufferedChunk {
  type: 'text' | 'reasoning'
  content: string
  messageID?: string
}

export interface StreamBufferAPI {
  buffer: (chunk: BufferedChunk) => void
  flush: () => void
  hasPending: () => boolean
}

/** Minimum interval between two flushes. ~30ms caps the render rate at ~33fps
 *  regardless of display refresh (120Hz screens no longer double the work),
 *  which is well above the perception threshold for streaming text. */
export const STREAM_FLUSH_INTERVAL_MS = 30

/**
 * Coalesces streaming text/reasoning chunks so React re-renders at most once
 * per interval instead of once per network delta.
 *
 * Scheduling is demand-driven: a timer is only armed while the buffer is
 * non-empty, so an idle session runs zero timers (the previous rAF loop ran
 * continuously). rAF is deliberately not used: it pauses when the document is
 * hidden, which would strand the tail of a reply until the user returns.
 */
export function useStreamBuffer(
  onFlush: (chunks: BufferedChunk[]) => void,
  enabled: boolean,
  intervalMS: number = STREAM_FLUSH_INTERVAL_MS,
): StreamBufferAPI {
  const bufferRef = useRef<BufferedChunk[]>([])
  const onFlushRef = useRef(onFlush)
  const enabledRef = useRef(enabled)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    onFlushRef.current = onFlush
  }, [onFlush])

  useEffect(() => {
    enabledRef.current = enabled
  }, [enabled])

  const doFlush = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
    if (bufferRef.current.length === 0) return
    const chunks = bufferRef.current
    bufferRef.current = []
    onFlushRef.current(chunks)
  }, [])

  // Passthrough mode drains anything left over from batched mode.
  useEffect(() => {
    if (!enabled) doFlush()
  }, [enabled, doFlush])

  // Never lose the tail of a reply when the tab goes hidden mid-stream.
  useEffect(() => {
    const onHidden = () => {
      if (document.hidden) doFlush()
    }
    document.addEventListener('visibilitychange', onHidden)
    return () => document.removeEventListener('visibilitychange', onHidden)
  }, [doFlush])

  useEffect(() => () => doFlush(), [doFlush])

  const buffer = useCallback((chunk: BufferedChunk) => {
    if (!enabledRef.current) {
      onFlushRef.current([chunk])
      return
    }
    bufferRef.current.push(chunk)
    if (timerRef.current === null) {
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        const chunks = bufferRef.current
        bufferRef.current = []
        if (chunks.length > 0) onFlushRef.current(chunks)
      }, intervalMS)
    }
  }, [intervalMS])

  const flush = useCallback(() => {
    doFlush()
  }, [doFlush])

  const hasPending = useCallback(() => {
    return bufferRef.current.length > 0
  }, [])

  return { buffer, flush, hasPending }
}
