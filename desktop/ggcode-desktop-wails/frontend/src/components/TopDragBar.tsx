import React, { useEffect, useState } from 'react'
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised } from '../../wailsjs/runtime/runtime'

// TopDragBar provides a draggable spacer at the top of each page.
// On Windows (frameless mode), it shows minimize/maximize/close buttons.
// On macOS, traffic lights are provided natively by the system.

const isWindows = navigator.platform?.startsWith('Win') || navigator.userAgent?.includes('Windows')

export function TopDragBar() {
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
      justifyContent: 'flex-end',
      '--wails-draggable': 'drag',
    } as React.CSSProperties}>
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
  )
}
