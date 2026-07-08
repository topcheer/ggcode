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
    if (!n || n === 0) return '0'
    if (n >= 1000000) return `${(n / 1000000).toFixed(1)}m`
    if (n >= 10000) return `${Math.round(n / 1000)}k`
    if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
    return String(n)
  }
  const ctxTotal = formatTokens(statusBarData?.contextTotal)
  const ctxUsed = formatTokens(statusBarData?.contextUsed)

  // Estimate session cost based on token usage and model pricing
  const estimateCost = () => {
    const input = statusBarData?.inputTokens ?? 0
    const output = statusBarData?.outputTokens ?? 0
    const cacheRead = statusBarData?.cacheRead ?? 0
    const cacheWrite = statusBarData?.cacheWrite ?? 0
    if (input === 0 && output === 0) return null

    // Rough pricing per 1M tokens (USD) — conservative estimates
    const model = (statusBarData?.model ?? '').toLowerCase()
    let inPrice = 3, outPrice = 15  // default (Sonnet-tier)
    let cacheReadPrice = 0.3
    let cacheWritePrice = 3.75

    if (model.includes('opus')) {
      inPrice = 15; outPrice = 75; cacheReadPrice = 1.5; cacheWritePrice = 18.75
    } else if (model.includes('haiku')) {
      inPrice = 0.8; outPrice = 4; cacheReadPrice = 0.08; cacheWritePrice = 1
    } else if (model.includes('gpt-4o-mini') || model.includes('mini')) {
      inPrice = 0.15; outPrice = 0.6; cacheReadPrice = 0.075; cacheWritePrice = 0.15
    } else if (model.includes('gpt-4o') || model.includes('gpt4o')) {
      inPrice = 5; outPrice = 15; cacheReadPrice = 2.5; cacheWritePrice = 5
    } else if (model.includes('deepseek')) {
      inPrice = 0.27; outPrice = 1.1; cacheReadPrice = 0.07; cacheWritePrice = 0.27
    }

    const cost = (input / 1e6 * inPrice) + (output / 1e6 * outPrice)
      + (cacheRead / 1e6 * cacheReadPrice) + (cacheWrite / 1e6 * cacheWritePrice)
    return cost
  }
  const estCost = estimateCost()
  const formatCost = (c: number) => c < 0.01 ? `$${c.toFixed(4)}` : c < 1 ? `$${c.toFixed(3)}` : `$${c.toFixed(2)}`

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
            <div style={{ width: `${usagePercent}%`, height: '100%', borderRadius: 3, background: usagePercent > 80 ? 'var(--color-error)' : usagePercent > 50 ? 'var(--color-warning)' : 'var(--color-success)' }} />
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

      {/* Estimated cost */}
      {estCost !== null && (
        <div style={{
          padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
          background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 6,
        }}>
          <div style={{ display: 'flex', alignItems: 'center' }}>
            <span style={{ fontSize: 12, fontWeight: 500 }}>{t('context.estimatedCost')}</span>
            <div style={{ flex: 1 }} />
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 600, color: 'var(--color-warning)' }}>
              {formatCost(estCost)}
            </span>
          </div>
          <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
            {t('context.costDisclaimer')}
          </span>
        </div>
      )}

      {/* IM adapters */}
      <div style={{
        padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
        background: 'var(--color-bg)', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <span style={{ fontSize: 12, fontWeight: 500 }}>IM</span>
        {(() => {
          const bound = imAdapters.filter(a => a.isCurrent)
          return bound.length === 0 ? (
            <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
              No bound IM adapters
            </span>
          ) : bound.map(adapter => (
            <div key={adapter.name} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <span style={{ fontSize: 12 }}>
                {adapter.enabled && !adapter.muted ? '🟢' : adapter.muted ? '🔇' : '⚪'}
              </span>
              <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)' }}>
                  {adapter.name}
                </span>
                <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                  {adapter.platform}{adapter.muted ? ' · muted' : ''}
                </span>
              </div>
            </div>
          ))
        })()}
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
