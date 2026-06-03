import React from 'react'

export function ContextPanel({ onClose }: { onClose: () => void }) {
  const usagePercent = 6
  const ctxTotal = 204.8
  const ctxUsed = 12.4

  const tokens = [
    { label: 'Input', value: '8,247', color: 'var(--color-info)' },
    { label: 'Output', value: '4,128', color: 'var(--text-primary)' },
    { label: 'Cache Read', value: '2,041', color: 'var(--color-success)' },
    { label: 'Cache Write', value: '986', color: 'var(--color-warning)' },
    { label: 'Cache Hit', value: '78%', color: 'var(--color-success)' },
  ]

  const modes = ['Yolo', 'Auto', 'Plan', 'Default']
  const activeMode = 'Auto'

  const files = [
    'internal/middleware/auth.go',
    'internal/config/config.go',
    'cmd/server/main.go',
  ]

  return (
    <div style={{
      width: 340, height: '100%',
      background: 'var(--color-card)',
      borderLeft: '1px solid var(--color-border)',
      padding: 'var(--spacing-lg)',
      display: 'flex', flexDirection: 'column', gap: 'var(--spacing-lg)',
      overflowY: 'auto',
    }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{ fontWeight: 600, fontSize: 14 }}>Context</span>
        <div style={{ flex: 1 }} />
        <button onClick={onClose} style={{
          background: 'none', border: 'none',
          color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 14,
        }}>✕</button>
      </div>

      {/* Model card */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 4,
      }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 13, fontWeight: 600, color: 'var(--color-info)' }}>
          glm-5.1
        </span>
        <span style={{ fontSize: 11, color: 'var(--text-secondary)' }}>
          Z.ai • {ctxTotal}K context
        </span>
      </div>

      {/* Usage */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 8,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>Context Usage</span>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <div style={{
            flex: 1, height: 6, borderRadius: 3, background: 'var(--color-surface)',
            overflow: 'hidden',
          }}>
            <div style={{ width: `${usagePercent}%`, height: '100%', borderRadius: 3, background: 'var(--color-success)' }} />
          </div>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
            {ctxUsed}K / {ctxTotal}K
          </span>
        </div>
        {tokens.map(t => (
          <div key={t.label} style={{ display: 'flex', alignItems: 'center' }}>
            <span style={{ fontSize: 12, color: 'var(--text-secondary)', width: 90 }}>{t.label}</span>
            <div style={{ flex: 1 }} />
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: t.color }}>{t.value}</span>
          </div>
        ))}
      </div>

      {/* Permission mode */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 8,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>Permission Mode</span>
        <div style={{ display: 'flex', gap: 6 }}>
          {modes.map(m => (
            <button key={m} style={{
              padding: '4px 10px', borderRadius: 'var(--radius-sm)',
              background: m === activeMode ? 'var(--color-primary)' : 'var(--color-surface)',
              color: m === activeMode ? '#fff' : 'var(--text-secondary)',
              border: 'none', cursor: 'pointer', fontSize: 11,
              fontWeight: m === activeMode ? 500 : 400,
            }}>{m}</button>
          ))}
        </div>
      </div>

      {/* Files */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>Files in Context</span>
        {files.map(f => (
          <div key={f} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            <span style={{ fontSize: 12 }}>📄</span>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
              {f}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
