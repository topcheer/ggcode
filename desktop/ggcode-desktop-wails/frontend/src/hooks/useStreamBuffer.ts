

import { useRef, useCallback, useEffect } from 'react'

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

export function useStreamBuffer(
  onFlush: (chunks: BufferedChunk[]) => void,
  enabled: boolean,
): StreamBufferAPI {
  const bufferRef = useRef<BufferedChunk[]>([])
  const onFlushRef = useRef(onFlush)
  const enabledRef = useRef(enabled)
  const rafRef = useRef<number | null>(null)

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

  useEffect(() => {
    if (!enabled) {
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
