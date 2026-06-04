import React, { useEffect, useState } from 'react'
import * as App from '../../wailsjs/go/main/App'

export function AboutDialog({ onClose }: { onClose: () => void }) {
  const [version, setVersion] = useState('dev')

  useEffect(() => {
    App.GetVersion().then(v => setVersion(v || 'dev')).catch(() => {})
  }, [])

  return (
    <div style={{
      position: 'absolute', top: '20%', left: '50%', transform: 'translateX(-50%)',
      width: 400, background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
      border: '1px solid var(--color-border)', padding: '32px 32px 24px 32px',
      display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      alignItems: 'center', boxShadow: '0 16px 48px rgba(0,0,0,0.5)', zIndex: 100,
    }}>
      <div style={{
        width: 56, height: 56, borderRadius: 14,
        background: 'var(--color-primary)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: '#fff', fontWeight: 700, fontSize: 20,
      }}>G</div>
      <span style={{ fontSize: 18, fontWeight: 600 }}>GGCode Desktop</span>
      <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)' }}>
        {version !== 'dev' ? `v${version}` : 'dev'}
      </span>
      <span style={{ fontSize: 12, color: 'var(--text-secondary)', textAlign: 'center' }}>
        AI coding agent with terminal and desktop workflows
      </span>

      {/* Links */}
      <div style={{ display: 'flex', gap: 24 }}>
        <a href="https://github.com/topcheer/ggcode" target="_blank" rel="noopener noreferrer"
          style={{ fontSize: 12, color: 'var(--color-info)', textDecoration: 'none' }}>GitHub</a>
        <a href="https://github.com/topcheer/ggcode/releases" target="_blank" rel="noopener noreferrer"
          style={{ fontSize: 12, color: 'var(--color-info)', textDecoration: 'none' }}>Releases</a>
        <a href="https://github.com/topcheer/ggcode/issues" target="_blank" rel="noopener noreferrer"
          style={{ fontSize: 12, color: 'var(--color-info)', textDecoration: 'none' }}>Issues</a>
        <a href="https://discord.gg/F2v4mJmfG" target="_blank" rel="noopener noreferrer"
          style={{ fontSize: 12, color: 'var(--color-info)', textDecoration: 'none' }}>Discord</a>
      </div>

      <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
        MIT License &middot; &copy; 2025 GG AI Studio
      </span>
    </div>
  )
}

export function UpdateNotification({ onClose }: { onClose: () => void }) {
  // This is a placeholder — real update checking is done by the Go backend.
  // The backend will push an event to show this dialog when an update is found.
  return null
}
