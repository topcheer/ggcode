import React from 'react'

export function ShareDialog({ onClose }: { onClose: () => void }) {
  return (
    <div style={{
      position: 'absolute', top: '20%', left: '50%', transform: 'translateX(-50%)',
      width: 480, background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
      border: '1px solid var(--color-border)', padding: 'var(--spacing-xl)',
      display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      boxShadow: '0 16px 48px rgba(0,0,0,0.5)', zIndex: 100,
    }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center' }}>
        <span style={{ fontSize: 16, fontWeight: 600 }}>Share Session</span>
        <div style={{ flex: 1 }} />
        <button onClick={onClose} style={{
          background: 'none', border: 'none', color: 'var(--text-secondary)',
          cursor: 'pointer', fontSize: 14,
        }}>✕</button>
      </div>

      {/* Tunnel status */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)',
      }}>
        <div style={{ width: 10, height: 10, borderRadius: 5, background: 'var(--color-success)' }} />
        <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--color-success)' }}>Tunnel Connected</span>
        <div style={{ flex: 1 }} />
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>42ms</span>
      </div>

      {/* Share link */}
      <span style={{ fontSize: 13, fontWeight: 500 }}>Share Link</span>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <div style={{
          flex: 1, height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-bg)', border: '1px solid var(--color-border)',
          display: 'flex', alignItems: 'center',
        }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--color-info)' }}>
            https://gg.ai/s/abc123def
          </span>
        </div>
        <button style={{
          padding: '6px 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-surface)', border: 'none',
          color: 'var(--text-primary)', cursor: 'pointer', fontSize: 12, fontWeight: 500,
        }}>Copy</button>
      </div>

      {/* QR code */}
      <span style={{ fontSize: 13, color: 'var(--text-secondary)' }}>Or scan QR code</span>
      <div style={{
        width: 120, height: 120, borderRadius: 'var(--radius-lg)',
        background: '#FFFFFF', display: 'flex', alignItems: 'center', justifyContent: 'center',
        alignSelf: 'center',
      }}>
        <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-bg)' }}>QR</span>
      </div>

      <span style={{ fontSize: 11, color: 'var(--text-tertiary)', textAlign: 'center' }}>
        Link expires in 24 hours
      </span>
    </div>
  )
}

export function AboutDialog({ onClose }: { onClose: () => void }) {
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
        v1.3.60 (2026060304)
      </span>
      <span style={{ fontSize: 12, color: 'var(--text-secondary)', textAlign: 'center' }}>
        AI coding agent with terminal and desktop workflows
      </span>

      {/* Update */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%' }}>
        <span style={{ fontSize: 12, color: 'var(--color-success)' }}>Up to date</span>
        <div style={{ flex: 1 }} />
        <button style={{
          padding: '4px 12px', borderRadius: 'var(--radius-sm)',
          border: '1px solid var(--color-border)', background: 'transparent',
          color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 11,
        }}>Check for Updates</button>
      </div>

      {/* Links */}
      <div style={{ display: 'flex', gap: 24 }}>
        <a href="#">GitHub</a>
        <a href="#">Docs</a>
        <a href="#">Discord</a>
        <a href="#">Feedback</a>
      </div>

      <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
        MIT License · © 2025 GG AI Studio
      </span>
    </div>
  )
}

export function UpdateNotification({ onClose }: { onClose: () => void }) {
  return (
    <div style={{
      position: 'absolute', top: 16, right: 16,
      width: 400, background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
      border: '1px solid var(--color-primary)', padding: '20px 24px',
      display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)',
      boxShadow: '0 8px 32px rgba(0,0,0,0.4)', zIndex: 100,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ fontSize: 16, color: 'var(--color-primary)' }}>⬆</span>
        <span style={{ fontSize: 15, fontWeight: 600 }}>Update Available</span>
        <div style={{ flex: 1 }} />
        <button onClick={onClose} style={{
          background: 'none', border: 'none', color: 'var(--text-secondary)',
          cursor: 'pointer', fontSize: 14,
        }}>✕</button>
      </div>
      <span style={{ fontSize: 13, color: 'var(--text-secondary)' }}>
        GGCode Desktop v1.3.61 is now available.
      </span>
      <span style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>Current: v1.3.60</span>
      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
        <button style={{
          padding: '6px 14px', borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border)', background: 'transparent',
          color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 12,
        }}>Skip</button>
        <button style={{
          padding: '6px 14px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', border: 'none',
          color: '#fff', cursor: 'pointer', fontSize: 12, fontWeight: 500,
        }}>Download</button>
      </div>
    </div>
  )
}
