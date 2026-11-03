import { useState, useEffect, useCallback } from 'react'

const STORAGE_KEY = 'ggcode-zoom'
const MIN_ZOOM = 0.7
const MAX_ZOOM = 1.8
const STEP = 0.1

function clampZoom(v: number): number {
  return Math.round(Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v)) * 100) / 100
}

export function useZoom() {
  const [zoom, setZoom] = useState(() => {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved) {
      const v = parseFloat(saved)
      if (!isNaN(v)) return clampZoom(v)
    }
    return 1
  })

  useEffect(() => {
    document.documentElement.style.setProperty('--app-zoom', String(zoom))
    document.documentElement.style.fontSize = `${zoom * 100}%`
    localStorage.setItem(STORAGE_KEY, String(zoom))
  }, [zoom])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return
      const target = e.target as HTMLElement
      const inEditable = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)

      if (e.key === '=' || e.key === '+') {
        e.preventDefault()
        setZoom(prev => clampZoom(prev + STEP))
      } else if (e.key === '-') {
        e.preventDefault()
        setZoom(prev => clampZoom(prev - STEP))
      } else if (e.key === '0') {
        e.preventDefault()
        setZoom(1)
      } else {
        return
      }
      if (inEditable) {
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  const zoomIn = useCallback(() => setZoom(prev => clampZoom(prev + STEP)), [])
  const zoomOut = useCallback(() => setZoom(prev => clampZoom(prev - STEP)), [])
  const resetZoom = useCallback(() => setZoom(1), [])

  return { zoom, zoomIn, zoomOut, resetZoom }
}
