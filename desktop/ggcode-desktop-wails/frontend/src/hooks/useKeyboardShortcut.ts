/**
 * useKeyboardShortcut — reusable keyboard shortcut registration hook.
 *
 * Centralizes the keyboard shortcut logic currently scattered across
 * components (ChatView, ApprovalDialog, CommandPalette, etc.).
 *
 * Features:
 * - Modifier key support (ctrl, shift, alt, meta/cmd)
 * - Global (window) or scoped (element) listening
 * - Automatic cleanup on unmount
 * - Debounce-free, fires on keydown
 *
 * Usage:
 *   // Global shortcut: Cmd+K
 *   useKeyboardShortcut({ key: 'k', meta: true }, () => openPalette(), { global: true })
 *
 *   // Scoped shortcut: Esc on a dialog
 *   useKeyboardShortcut({ key: 'Escape' }, () => onClose(), { global: false })
 *
 *   // Multi-key: Ctrl+Shift+P
 *   useKeyboardShortcut({ key: 'p', ctrl: true, shift: true }, () => ...)
 */

import { useEffect, useRef, useCallback } from 'react'

export interface ShortcutConfig {
  /** The KeyboardEvent.key to match (e.g. 'k', 'Escape', 'Enter', 'ArrowDown') */
  key: string
  /** Require Ctrl/Cmd (meta on macOS). Defaults to false */
  ctrl?: boolean
  /** Require Shift. Defaults to false */
  shift?: boolean
  /** Require Alt/Option. Defaults to false */
  alt?: boolean
  /** Require Meta/Command (macOS Cmd). If ctrl is also true, matches either. */
  meta?: boolean
}

export interface ShortcutOptions {
  /** Listen on window (true, default) or on a specific element ref (false) */
  global?: boolean
  /** If global=false, attach to this ref's element */
  targetRef?: React.RefObject<HTMLElement | null>
  /** Use capture phase (default true, to intercept before component handlers) */
  capture?: boolean
  /** Prevent default behavior on match (default true) */
  preventDefault?: boolean
  /** Stop propagation on match (default false) */
  stopPropagation?: boolean
  /** If false, the shortcut is disabled. Defaults to true */
  enabled?: boolean
}

/**
 * Check if a KeyboardEvent matches the shortcut config.
 * On macOS, Ctrl is often replaced by Cmd (metaKey). If ctrl=true and meta is not set,
 * we accept either ctrlKey or metaKey for cross-platform convenience.
 */
function matchesShortcut(e: KeyboardEvent, config: ShortcutConfig): boolean {
  // Key match (case-insensitive for letters)
  if (e.key.toLowerCase() !== config.key.toLowerCase()) return false

  const wantCtrl = config.ctrl ?? false
  const wantShift = config.shift ?? false
  const wantAlt = config.alt ?? false
  const wantMeta = config.meta ?? false

  // If ctrl is wanted but meta is not explicitly set, accept either (macOS Cmd / Windows Ctrl)
  if (wantCtrl && !wantMeta) {
    if (!e.ctrlKey && !e.metaKey) return false
  } else if (wantCtrl) {
    if (!e.ctrlKey) return false
  }

  if (wantMeta && !e.metaKey) return false
  if (wantShift && !e.shiftKey) return false
  if (wantAlt && !e.altKey) return false

  // Negative checks: if modifier not wanted, it should NOT be pressed
  // (except for the ctrl/meta cross-platform exception above)
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

  // Keep handler ref fresh without re-registering the listener
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

/**
 * Register multiple shortcuts at once.
 * Useful for components with several keyboard shortcuts.
 *
 * Usage:
 *   useKeyboardShortcuts([
 *     { config: { key: 'Escape' }, handler: onClose },
 *     { config: { key: 'Enter' }, handler: onConfirm },
 *     { config: { key: 'Enter', shift: true }, handler: onAlwaysAllow },
 *   ], { global: true })
 */
export function useKeyboardShortcuts(
  shortcuts: Array<{ config: ShortcutConfig; handler: (e: KeyboardEvent) => void }>,
  options: ShortcutOptions = {},
): void {
  for (const { config, handler } of shortcuts) {
    // eslint-disable-next-line react-hooks/rules-of-hooks
    useKeyboardShortcut(config, handler, options)
  }
}
