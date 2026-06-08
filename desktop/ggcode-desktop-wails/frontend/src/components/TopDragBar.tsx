import React, { useEffect, useState } from 'react'
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised } from '../../wailsjs/runtime/runtime'

// TopDragBar provides a draggable spacer at the top of each page.
// On Windows (frameless mode), it shows minimize/maximize/close buttons.
// On macOS, traffic lights are provided natively by the system.

const isWindows = navigator.platform?.startsWith('Win') || navigator.userAgent?.includes('Windows')
const isMac = navigator.platform?.toLowerCase().includes('mac') || navigator.userAgent?.includes('Mac OS')

interface TopDragBarProps {
  title?: string
  subtitle?: string
}

export function TopDragBar({ title = 'GGCode Desktop', subtitle }: TopDragBarProps) {
  const [isMaximized, setIsMaximized] = useState(false)

  useEffect(() => {
    if (!isWindows) return
    WindowIsMaximised().then((maximized: boolean) => setIsMaximized(maximized))
  }, [])

  const handleMinimize = (e: React.MouseEvent) => {
    e.stopPropagation()
    WindowMinimise()
  }

  const handleMaximize = (e: React.MouseEvent) => {
    e.stopPropagation()
    WindowToggleMaximise()
    WindowIsMaximised().then((maximized: boolean) => setIsMaximized(maximized))
  }

  const handleClose = (e: React.MouseEvent) => {
    e.stopPropagation()
    window.close()
  }



  return (
    <div style={{
      height: 36,
      flexShrink: 0,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      paddingLeft: isMac ? 82 : 12,
      borderBottom: '1px solid rgba(255, 255, 255, 0.06)',
      background: 'linear-gradient(180deg, rgba(17, 24, 39, 0.92), rgba(13, 17, 23, 0.86))',
      '--wails-draggable': 'drag',
    } as React.CSSProperties}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        minWidth: 0,
        pointerEvents: 'none',
      }}>
        <span style={{
          width: 18,
          height: 18,
          borderRadius: 6,
          background: 'linear-gradient(135deg, var(--color-primary), #8b5cf6)',
          color: '#fff',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontSize: 11,
          fontWeight: 800,
          letterSpacing: -0.5,
          boxShadow: '0 4px 14px rgba(59, 130, 246, 0.28)',
        }}>G</span>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, minWidth: 0 }}>
          <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--text-primary)', whiteSpace: 'nowrap' }}>{title}</span>
          {subtitle && (
            <span style={{ fontSize: 11, color: 'var(--text-tertiary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 420 }}>
              {subtitle}
            </span>
          )}
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', height: '100%' }}>
        {isWindows && (
          <div style={{ display: 'flex', height: '100%' }} onClick={(e) => e.stopPropagation()}>
          <button
            onClick={handleMinimize}
            style={{
              width: 46, height: '100%', border: 'none', background: 'transparent',
              color: '#a0a0a0', fontSize: 16, cursor: 'pointer', display: 'flex',
              alignItems: 'center', justifyContent: 'center', lineHeight: 1,
            }}
            onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(255,255,255,0.1)')}
            onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
          >
            &#x2500;
          </button>
          <button
            onClick={handleMaximize}
            style={{
              width: 46, height: '100%', border: 'none', background: 'transparent',
              color: '#a0a0a0', fontSize: 14, cursor: 'pointer', display: 'flex',
              alignItems: 'center', justifyContent: 'center', lineHeight: 1,
            }}
            onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(255,255,255,0.1)')}
            onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
          >
            {isMaximized ? '\u2752' : '\u25A1'}
          </button>
          <button
            onClick={handleClose}
            style={{
              width: 46, height: '100%', border: 'none', background: 'transparent',
              color: '#a0a0a0', fontSize: 16, cursor: 'pointer', display: 'flex',
              alignItems: 'center', justifyContent: 'center', lineHeight: 1,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.background = '#e81123'; e.currentTarget.style.color = '#fff' }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = '#a0a0a0' }}
          >
            &#x2715;
          </button>
          </div>
        )}
      </div>
    </div>
  )
}
