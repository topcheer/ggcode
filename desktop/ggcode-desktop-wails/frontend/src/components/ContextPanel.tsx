import React, { useState, useEffect } from 'react'
import * as App from '../../wailsjs/go/main/App'
import type { StatusBarData } from '../types'

interface ContextPanelProps {
  onClose: () => void
  statusBarData?: StatusBarData
}

export function ContextPanel({ onClose, statusBarData }: ContextPanelProps) {
  const [activeMode, setActiveMode] = useState('auto')
  const [files, setFiles] = useState<string[]>([
    'internal/middleware/auth.go',
    'internal/config/config.go',
    'cmd/server/main.go',
  ])

  const usagePercent = statusBarData?.contextTotal
    ? Math.round((statusBarData.contextUsed / statusBarData.contextTotal) * 100)
    : 6
  const ctxTotal = statusBarData?.contextTotal
    ? (statusBarData.contextTotal / 1000).toFixed(1)
    : '204.8'
  const ctxUsed = statusBarData?.contextUsed
    ? (statusBarData.contextUsed / 1000).toFixed(1)
    : '12.4'

  const tokens = [
    { label: 'Input', value: statusBarData?.inputTokens?.toLocaleString() ?? '0', color: 'var(--color-info)' },
    { label: 'Output', value: statusBarData?.outputTokens?.toLocaleString() ?? '0', color: 'var(--text-primary)' },
    { label: 'Cache Read', value: '—', color: 'var(--color-success)' },
    { label: 'Cache Write', value: '—', color: 'var(--color-warning)' },
    { label: 'Cache Hit', value: statusBarData?.cacheHit ? `${statusBarData.cacheHit}%` : '—', color: 'var(--color-success)' },
  ]

  const modes = ['Yolo', 'Auto', 'Plan', 'Default']

  // Load config for permission mode
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const cfg = await App.GetConfig()
        if (cancelled) return
        if (cfg.defaultMode) setActiveMode(cfg.defaultMode)
      } catch {
        // fallback to default
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  const modelLabel = statusBarData?.model ?? '...'
  const vendorLabel = statusBarData?.vendor ?? '...'

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
          {modelLabel}
        </span>
        <span style={{ fontSize: 11, color: 'var(--text-secondary)' }}>
          {vendorLabel} • {ctxTotal}K context
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
            <button key={m} onClick={() => setActiveMode(m.toLowerCase())} style={{
              padding: '4px 10px', borderRadius: 'var(--radius-sm)',
              background: m.toLowerCase() === activeMode ? 'var(--color-primary)' : 'var(--color-surface)',
              color: m.toLowerCase() === activeMode ? '#fff' : 'var(--text-secondary)',
              border: 'none', cursor: 'pointer', fontSize: 11,
              fontWeight: m.toLowerCase() === activeMode ? 500 : 400,
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
