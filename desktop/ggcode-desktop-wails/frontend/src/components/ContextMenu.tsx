import React, { useEffect, useRef, useCallback } from 'react'

export interface ContextMenuItem {
  label: string
  icon?: React.ReactNode
  onClick: () => void
  danger?: boolean
  disabled?: boolean
}

interface ContextMenuProps {
  x: number
  y: number
  items: ContextMenuItem[]
  onClose: () => void
}

const MENU_ITEM_HEIGHT = 36
const MENU_PADDING = 8
const MENU_MIN_WIDTH = 180

/** Clamp a menu position so it stays on-screen. Pure function for testing. */
export function clampPosition(x: number, y: number, itemCount: number, viewportW: number, viewportH: number): { left: number; top: number } {
  const menuHeight = itemCount * MENU_ITEM_HEIGHT + MENU_PADDING
  return {
    left: Math.min(x, viewportW - MENU_MIN_WIDTH - MENU_PADDING),
    top: Math.min(y, viewportH - menuHeight - MENU_PADDING),
  }
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null)

  // Close on outside click or escape
  const handleClickOutside = useCallback((e: MouseEvent) => {
    if (ref.current && !ref.current.contains(e.target as Node)) {
      onClose()
    }
  }, [onClose])

  const handleEsc = useCallback((e: KeyboardEvent) => {
    if (e.key === 'Escape') onClose()
  }, [onClose])

  useEffect(() => {
    document.addEventListener('mousedown', handleClickOutside)
    document.addEventListener('keydown', handleEsc)
    window.addEventListener('scroll', onClose, true) // close on any scroll (capture phase)
    window.addEventListener('resize', onClose)
    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
      document.removeEventListener('keydown', handleEsc)
      window.removeEventListener('scroll', onClose, true)
      window.removeEventListener('resize', onClose)
    }
  }, [handleClickOutside, handleEsc, onClose])

  // Clamp position so the menu doesn't go off-screen
  const { left: clampedX, top: clampedY } = clampPosition(x, y, items.length, window.innerWidth, window.innerHeight)

  return (
    <>
      {/* Invisible overlay to catch right-click events that might propagate */}
      <div style={{ position: 'fixed', inset: 0, zIndex: 9998 }} onContextMenu={e => { e.preventDefault(); onClose() }} />
      <div
        ref={ref}
        onContextMenu={e => { e.preventDefault(); e.stopPropagation() }}
        style={{
          position: 'fixed',
          left: clampedX,
          top: clampedY,
          zIndex: 9999,
          minWidth: MENU_MIN_WIDTH,
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius-md)',
          boxShadow: '0 8px 24px rgba(0,0,0,0.35)',
          padding: '4px 0',
          overflow: 'hidden',
        }}
      >
        {items.map((item, idx) => (
          <button
            key={idx}
            disabled={item.disabled}
            onClick={() => { item.onClick(); onClose() }}
            style={{
              display: 'flex', alignItems: 'center', gap: 8,
              width: '100%',
              padding: '6px 12px',
              background: 'transparent',
              border: 'none',
              color: item.danger ? '#f87171' : 'var(--text-secondary)',
              fontSize: 13,
              textAlign: 'left',
              cursor: item.disabled ? 'default' : 'pointer',
              opacity: item.disabled ? 0.4 : 1,
              transition: 'background 0.1s',
            }}
            onMouseEnter={e => {
              if (!item.disabled) {
                e.currentTarget.style.background = item.danger ? 'rgba(220,38,38,0.12)' : 'rgba(128,128,128,0.12)'
                e.currentTarget.style.color = item.danger ? '#f87171' : 'var(--text-primary)'
              }
            }}
            onMouseLeave={e => {
              e.currentTarget.style.background = 'transparent'
              e.currentTarget.style.color = item.danger ? '#f87171' : 'var(--text-secondary)'
            }}
          >
            {item.icon && <span style={{ display: 'flex', flexShrink: 0 }}>{item.icon}</span>}
            <span>{item.label}</span>
          </button>
        ))}
      </div>
    </>
  )
}
