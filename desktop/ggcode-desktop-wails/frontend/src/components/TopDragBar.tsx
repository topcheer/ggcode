import React, { useEffect, useState } from 'react'
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit } from '../../wailsjs/runtime/runtime'

// TopDragBar — fully custom-drawn title bar for ALL platforms.
// macOS: traffic-light circles on the left (red/yellow/green).
// Windows/Linux: flat square buttons on the right (−/□/×).
// No native title bar is used anywhere (Frameless: true + HideTitleBar: true).

const isMac = navigator.platform?.toLowerCase().includes('mac') || navigator.userAgent?.includes('Mac OS')

interface TopDragBarProps {
  title?: string
  subtitle?: string
}

export function TopDragBar({ title = 'GGCode Desktop', subtitle }: TopDragBarProps) {
  const [isMaximized, setIsMaximized] = useState(false)
  const [lightsHover, setLightsHover] = useState(false)

  useEffect(() => {
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
    try {
      Quit()
    } catch {
      window.close()
    }
  }

  // Shared styles ----------------------------------------------------------

  const barStyle: React.CSSProperties = {
    height: 36,
    flexShrink: 0,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingLeft: isMac ? 12 : 12,
    paddingRight: isMac ? 12 : 0,
    borderBottom: '1px solid rgba(255, 255, 255, 0.06)',
    background: 'linear-gradient(180deg, rgba(17, 24, 39, 0.92), rgba(13, 17, 23, 0.86))',
    '--wails-draggable': 'drag',
  } as React.CSSProperties

  const leftSection: React.CSSProperties = {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    minWidth: 0,
  }

  // macOS traffic-light button
  const lightBtn = (color: string): React.CSSProperties => ({
    width: 12,
    height: 12,
    borderRadius: '50%',
    border: 'none',
    background: lightsHover ? color : 'rgba(255,255,255,0.18)',
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 9,
    fontWeight: 700,
    lineHeight: 1,
    padding: 0,
    color: 'rgba(0,0,0,0.55)',
    transition: 'background 0.12s',
  })

  // Windows/Linux flat button base
  const flatBtn = (isClose = false): React.CSSProperties => ({
    width: 46,
    height: '100%',
    border: 'none',
    background: 'transparent',
    color: '#a0a0a0',
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    lineHeight: 1,
  })

  // ------------------------------------------------------------------------
  // macOS traffic lights (left side)
  // ------------------------------------------------------------------------
  const macLights = isMac ? (
    <div
      style={{ display: 'flex', gap: 8, alignItems: 'center' }}
      onMouseEnter={() => setLightsHover(true)}
      onMouseLeave={() => setLightsHover(false)}
      onClick={(e) => e.stopPropagation()}
    >
      <button onClick={handleClose} style={lightBtn('#ff5f57')} title="Close" aria-label="Close window">
        {lightsHover ? '\u2715' : ''}
      </button>
      <button onClick={handleMinimize} style={lightBtn('#febc2e')} title="Minimize" aria-label="Minimize window">
        {lightsHover ? '\u2212' : ''}
      </button>
      <button onClick={handleMaximize} style={lightBtn('#28c840')} title="Maximize" aria-label={isMaximized ? "Restore window" : "Maximize window"}>
        {lightsHover ? (isMaximized ? '\u2752' : '\u25B3') : ''}
      </button>
    </div>
  ) : null

  // ------------------------------------------------------------------------
  // Windows/Linux flat buttons (right side)
  // ------------------------------------------------------------------------
  const winButtons = !isMac ? (
    <div style={{ display: 'flex', height: '100%' }} onClick={(e) => e.stopPropagation()}>
      <button
        onClick={handleMinimize}
        style={{ ...flatBtn(), fontSize: 16 }}
        onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(255,255,255,0.1)')}
        onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
      >
        {'\u2500'}
      </button>
      <button
        onClick={handleMaximize}
        style={{ ...flatBtn(), fontSize: 14 }}
        onMouseEnter={(e) => (e.currentTarget.style.background = 'rgba(255,255,255,0.1)')}
        onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
      >
        {isMaximized ? '\u2752' : '\u25A1'}
      </button>
      <button
        onClick={handleClose}
        style={{ ...flatBtn(true), fontSize: 16 }}
        onMouseEnter={(e) => { e.currentTarget.style.background = '#e81123'; e.currentTarget.style.color = '#fff' }}
        onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = '#a0a0a0' }}
      >
        {'\u2715'}
      </button>
    </div>
  ) : null

  return (
    <div style={barStyle}>
      {/* Left: platform buttons + logo + title */}
      <div style={leftSection}>
        {macLights}
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
          flexShrink: 0,
          pointerEvents: 'none',
        }}>G</span>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, minWidth: 0, pointerEvents: 'none' }}>
          <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--text-primary)', whiteSpace: 'nowrap' }}>{title}</span>
          {subtitle && (
            <span style={{ fontSize: 11, color: 'var(--text-tertiary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 420 }}>
              {subtitle}
            </span>
          )}
        </div>
      </div>

      {/* Right: Windows/Linux buttons only */}
      {winButtons}
    </div>
  )
}
