

import { useEffect, useRef, useCallback } from 'react'

export interface ShortcutConfig {

  key: string

  ctrl?: boolean

  shift?: boolean

  alt?: boolean

  meta?: boolean
}

export interface ShortcutOptions {

  global?: boolean

  targetRef?: React.RefObject<HTMLElement | null>

  capture?: boolean

  preventDefault?: boolean

  stopPropagation?: boolean

  enabled?: boolean
}

export function matchesShortcut(e: KeyboardEvent, config: ShortcutConfig): boolean {
  if (e.key.toLowerCase() !== config.key.toLowerCase()) return false

  const wantCtrl = config.ctrl ?? false
  const wantShift = config.shift ?? false
  const wantAlt = config.alt ?? false
  const wantMeta = config.meta ?? false

  if (wantCtrl && !wantMeta) {
    if (!e.ctrlKey && !e.metaKey) return false
  } else if (wantCtrl) {
    if (!e.ctrlKey) return false
  }

  if (wantMeta && !e.metaKey) return false
  if (wantShift && !e.shiftKey) return false
  if (wantAlt && !e.altKey) return false

  if (!wantShift && e.shiftKey && config.key !== 'Shift') return false
  if (!wantAlt && e.altKey && config.key !== 'Alt') return false

  return true
}

export function useKeyboardShortcut(
  config: ShortcutConfig,
  handler: (e: KeyboardEvent) => void,
  options: ShortcutOptions = {},
): void {
  const {
    global = true,
    targetRef,
    capture = true,
    preventDefault = true,
    stopPropagation = false,
    enabled = true,
  } = options

  const handlerRef = useRef(handler)
  handlerRef.current = handler

  const callback = useCallback((e: Event) => {
    const ke = e as KeyboardEvent
    if (!matchesShortcut(ke, config)) return
    if (preventDefault) ke.preventDefault()
    if (stopPropagation) ke.stopPropagation()
    handlerRef.current(ke)
  }, [config.key, config.ctrl, config.shift, config.alt, config.meta, preventDefault, stopPropagation])

  useEffect(() => {
    if (!enabled) return

    const target: EventTarget = global
      ? window
      : (targetRef?.current ?? window)

    target.addEventListener('keydown', callback, capture)
    return () => target.removeEventListener('keydown', callback, capture)
  }, [callback, global, targetRef, capture, enabled])
}

export function useKeyboardShortcuts(
  shortcuts: Array<{ config: ShortcutConfig; handler: (e: KeyboardEvent) => void }>,
  options: ShortcutOptions = {},
): void {
  for (const { config, handler } of shortcuts) {
    // eslint-disable-next-line react-hooks/rules-of-hooks
    useKeyboardShortcut(config, handler, options)
  }
}
