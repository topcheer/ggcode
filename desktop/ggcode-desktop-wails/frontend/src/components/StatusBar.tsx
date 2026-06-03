import React from 'react'

export function StatusBar() {
  return (
    <div style={{
      height: 'var(--statusbar-height)',
      display: 'flex', alignItems: 'center',
      padding: '0 var(--spacing-lg)', gap: 'var(--spacing-md)',
      background: 'var(--color-nav)',
      borderTop: '1px solid var(--color-border)',
      fontSize: 10, flexShrink: 0,
      fontFamily: 'var(--font-mono)',
    }}>
      <span style={{
        padding: '1px 6px', borderRadius: 3,
        background: 'var(--color-primary)', color: '#fff',
      }}>
        zai/glm-5.1
      </span>
      <span style={{ color: 'var(--text-secondary)' }}>ctx 12.4K</span>
      <span style={{ color: 'var(--text-secondary)' }}>in 8.2K</span>
      <span style={{ color: 'var(--text-secondary)' }}>out 4.1K</span>
      <span style={{ color: 'var(--color-success)' }}>cache 78%</span>
      <div style={{ flex: 1 }} />
      <span style={{ color: 'var(--color-success)' }}>● Ready</span>
    </div>
  )
}
