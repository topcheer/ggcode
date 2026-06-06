import React, { useState, useEffect } from 'react'
import * as App from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import type { StatusBarData } from '../types'
import { useTranslation } from '../i18n'

interface IMAdapterInfo {
  name: string
  enabled: boolean
  muted: boolean
  platform: string
  workspace: string
  isCurrent: boolean
}

interface ContextPanelProps {
  onClose: () => void
  statusBarData?: StatusBarData
}

export function ContextPanel({ onClose, statusBarData }: ContextPanelProps) {
  const { t } = useTranslation()
  const [mcpServers, setMcpServers] = useState<any[]>([])
  const [imAdapters, setImAdapters] = useState<IMAdapterInfo[]>([])

  const usagePercent = statusBarData?.contextTotal
    ? statusBarData.usagePercent
    : 0
  const formatTokens = (n?: number) => {
    if (!n) return '0'
    if (n >= 1000000 && n % 1000000 === 0) return `${n / 1000000}m`
    if (n >= 1000 && n % 1000 === 0) return `${n / 1000}k`
    return String(n)
  }
  const ctxTotal = formatTokens(statusBarData?.contextTotal)
  const ctxUsed = formatTokens(statusBarData?.contextUsed)

  const tokens = [
    { label: t('context.input'), value: formatTokens(statusBarData?.inputTokens), color: 'var(--color-info)' },
    { label: t('context.output'), value: formatTokens(statusBarData?.outputTokens), color: 'var(--text-primary)' },
    { label: t('context.cacheRead'), value: formatTokens(statusBarData?.cacheRead), color: 'var(--color-success)' },
    { label: t('context.cacheWrite'), value: formatTokens(statusBarData?.cacheWrite), color: 'var(--color-warning)' },
    { label: t('context.cacheHit'), value: `${statusBarData?.cacheHit ?? 0}%`, color: 'var(--color-success)' },
  ]

  // Load IM adapters with real-time updates
  useEffect(() => {
    let cancelled = false
    const loadIM = async () => {
      try {
        const list = await App.ListIMAdapters() as IMAdapterInfo[]
        if (!cancelled) setImAdapters(list || [])
      } catch {}
    }
    void loadIM()
    EventsOn('im:status', () => { void loadIM() })
    return () => { cancelled = true; EventsOff('im:status') }
  }, [])

  const modelLabel = statusBarData?.model ?? '...'
  const vendorLabel = statusBarData?.vendor ?? '...'

  useEffect(() => {
    let cancelled = false
    const loadMCP = async () => {
      try {
        const list = await App.ListMCPServers()
        if (!cancelled) setMcpServers(list || [])
      } catch {}
    }
    void loadMCP()
    // Poll every 5s as fallback
    const id = window.setInterval(() => { void loadMCP() }, 5000)
    // Real-time updates: backend pushes mcp:status when server connects/disconnects/fails
    EventsOn('mcp:status', () => { void loadMCP() })
    return () => {
      cancelled = true
      window.clearInterval(id)
      EventsOff('mcp:status')
    }
  }, [])

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
        <span style={{ fontWeight: 600, fontSize: 14 }}>{t('context.title')}</span>
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
          {vendorLabel} • {t('context.contextLabel', { n: ctxTotal })}
        </span>
      </div>

      {/* Usage */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 8,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>{t('context.usage')}</span>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <div style={{
            flex: 1, height: 6, borderRadius: 3, background: 'var(--color-surface)',
            overflow: 'hidden',
          }}>
            <div style={{ width: `${usagePercent}%`, height: '100%', borderRadius: 3, background: 'var(--color-success)' }} />
          </div>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
            {ctxUsed} / {ctxTotal}
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

      {/* IM adapters */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>IM</span>
        {imAdapters.length === 0 ? (
          <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
            No IM adapters
          </span>
        ) : imAdapters.map(adapter => (
          <div key={adapter.name} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            <span style={{ fontSize: 12 }}>
              {adapter.enabled && !adapter.muted ? '🟢' : adapter.muted ? '🔇' : '⚪'}
            </span>
            <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
                {adapter.name}
              </span>
              <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                {adapter.platform}{adapter.isCurrent ? ' · active' : adapter.workspace ? ` · ${adapter.workspace.split('/').pop()}` : ''}{adapter.muted ? ' · muted' : ''}
              </span>
            </div>
          </div>
        ))}
      </div>

      {/* MCP servers */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>MCP</span>
        {mcpServers.length === 0 ? (
          <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
            No MCP servers
          </span>
        ) : mcpServers.map(server => (
          <div key={server.name} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            <span style={{ fontSize: 12 }}>{server.connected ? '🟢' : server.disabled ? '⚪' : server.status === 'failed' ? '🔴' : '🟡'}</span>
            <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
                {server.name}
              </span>
              <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                {server.disabled ? 'disabled' : server.status || 'unknown'}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
