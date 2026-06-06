import React, { useState, useEffect } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'
import type { StatusBarData } from '../types'
import { useTranslation } from '../i18n'

interface StatusBarProps {
  onContextToggle?: () => void
  data?: StatusBarData
}

export function StatusBar({ onContextToggle, data }: StatusBarProps) {
  const { t } = useTranslation()
  const [info, setInfo] = useState<StatusBarData>(data ?? {
    vendor: '...',
    model: '...',
    mode: 'auto',
    contextUsed: 0,
    contextTotal: 0,
    usagePercent: 0,
    remainingPercent: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cacheHit: 0,
    status: t('status.ready'),
  })

  // Merge external data if provided
  useEffect(() => {
    if (data) setInfo(data)
  }, [data])

  const formatTokens = (n: number) => {
    if (n >= 1000000 && n % 1000000 === 0) return `${n / 1000000}m`
    if (n >= 1000 && n % 1000 === 0) return `${n / 1000}k`
    return String(n)
  }

  const modelLabel = info.vendor && info.model
    ? `${info.vendor}/${info.model}`
    : '...'

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
        {modelLabel}
      </span>
      <span style={{ color: 'var(--text-secondary)' }}>ctx {formatTokens(info.contextUsed)}</span>
      <span style={{ color: 'var(--text-secondary)' }}>in {formatTokens(info.inputTokens)}</span>
      <span style={{ color: 'var(--text-secondary)' }}>out {formatTokens(info.outputTokens)}</span>
      {info.cacheHit > 0 && (
        <span style={{ color: 'var(--color-success)' }}>cache {info.cacheHit}%</span>
      )}
      <div style={{ flex: 1 }} />
      <button onClick={onContextToggle} style={{
        background: 'none', border: 'none',
        color: 'var(--text-secondary)', cursor: 'pointer',
        fontSize: 10, fontFamily: 'var(--font-mono)',
      }}>⌘.</button>
      <span style={{
        color: info.status === t('status.ready') ? 'var(--color-success)' : 'var(--color-warning)',
      }}>● {info.status}</span>
    </div>
  )
}
