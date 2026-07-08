import React, { useState, useEffect } from 'react'
import { Radio, Smartphone } from 'lucide-react'
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

  // IM adapter active count + mobile tunnel connection status
  const [imCount, setImCount] = useState(0)
  const [mobileConnected, setMobileConnected] = useState(false)

  // Elapsed timer: tracks how long the agent has been working
  const [workStartTime, setWorkStartTime] = useState<number | null>(null)
  const [, setTick] = useState(0)

  // Detect status transitions to start/stop the timer
  useEffect(() => {
    const isWorking = info.status !== t('status.ready') &&
                      info.status !== 'Ready' &&
                      info.status !== ''
    if (isWorking && workStartTime === null) {
      setWorkStartTime(Date.now())
    } else if (!isWorking && workStartTime !== null) {
      setWorkStartTime(null)
    }
  }, [info.status, t, workStartTime])

  // 1-second tick to update elapsed display while working
  useEffect(() => {
    if (workStartTime === null) return
    const id = window.setInterval(() => setTick(t => t + 1), 1000)
    return () => window.clearInterval(id)
  }, [workStartTime])

  const elapsedLabel = (() => {
    if (workStartTime === null) return null
    const secs = Math.floor((Date.now() - workStartTime) / 1000)
    if (secs < 60) return `${secs}s`
    const m = Math.floor(secs / 60)
    const s = secs % 60
    return `${m}m${String(s).padStart(2, '0')}s`
  })()

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const [adapters, sharing] = await Promise.all([
          App.ListIMAdapters(),
          App.IsSharing(),
        ])
        if (cancelled) return
        const active = (adapters as any[])?.filter(a =>
          (a.Enabled ?? a.enabled) && !(a.Muted ?? a.muted)
        ).length ?? 0
        setImCount(active)
        setMobileConnected(!!sharing)
      } catch {}
    }
    void poll()
    const id = window.setInterval(poll, 3000)
    return () => { cancelled = true; window.clearInterval(id) }
  }, [])

  // Merge external data if provided
  useEffect(() => {
    if (data) setInfo(data)
  }, [data])

  const formatTokens = (n: number) => {
    if (n >= 1000000) return `${(n / 1000000).toFixed(1)}m`
    if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
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
      {/* Context usage progress bar */}
      {(() => {
        const pct = info.usagePercent || (info.contextTotal > 0 ? (info.contextUsed / info.contextTotal) * 100 : 0)
        const color = pct >= 85 ? '#ef4444' : pct >= 65 ? '#f59e0b' : '#22c55e'
        const bgColor = pct >= 85 ? 'rgba(239,68,68,0.15)' : pct >= 65 ? 'rgba(245,158,11,0.15)' : 'rgba(34,197,94,0.12)'
        return (
          <div
            title={`Context: ${formatTokens(info.contextUsed)} / ${formatTokens(info.contextTotal)} (${Math.round(pct)}%)`}
            style={{
              display: 'flex', alignItems: 'center', gap: 4,
              cursor: 'default',
            }}
          >
            <span style={{ color: 'var(--text-secondary)', fontSize: 10 }}>ctx</span>
            <div style={{
              width: 60, height: 6, borderRadius: 3,
              background: bgColor, overflow: 'hidden',
              border: `1px solid ${color}30`,
            }}>
              <div style={{
                width: `${Math.min(100, pct)}%`, height: '100%',
                background: color, borderRadius: 3,
                transition: 'width 0.3s ease, background 0.3s ease',
              }} />
            </div>
            <span style={{ color, fontSize: 10, minWidth: 28, fontVariantNumeric: 'tabular-nums' }}>
              {Math.round(pct)}%
            </span>
          </div>
        )
      })()}
      {/* Permission mode indicator */}
      {(() => {
        const modeColors: Record<string, string> = {
          auto: 'var(--color-success)',
          supervised: 'var(--color-warning)',
          bypass: '#ef4444',
          plan: 'var(--text-secondary)',
          autopilot: '#8b5cf6',
        }
        const modeLabels: Record<string, string> = {
          auto: 'auto',
          supervised: 'manual',
          bypass: 'bypass',
          plan: 'plan',
          autopilot: 'pilot',
        }
        const mc = modeColors[info.mode] || 'var(--text-secondary)'
        const ml = modeLabels[info.mode] || info.mode
        return (
          <span title={`Permission mode: ${info.mode}`} style={{
            color: mc, cursor: 'default',
            border: `1px solid ${mc}40`, borderRadius: 3,
            padding: '0 4px', fontSize: 10,
          }}>{ml}</span>
        )
      })()}
      <span title={`Input: ${info.inputTokens.toLocaleString()} tokens`} style={{ color: 'var(--text-secondary)', fontVariantNumeric: 'tabular-nums' }}>in {formatTokens(info.inputTokens)}</span>
      <span title={`Output: ${info.outputTokens.toLocaleString()} tokens`} style={{ color: 'var(--text-secondary)', fontVariantNumeric: 'tabular-nums' }}>out {formatTokens(info.outputTokens)}</span>
      {(() => {
        const total = info.inputTokens + info.outputTokens
        if (total === 0) return null
        return (
          <span title={`Total session tokens: ${total.toLocaleString()}`} style={{
            color: 'var(--text-primary)', fontWeight: 600, fontVariantNumeric: 'tabular-nums',
          }}>Σ {formatTokens(total)}</span>
        )
      })()}
      {info.cacheHit > 0 && (
        <span style={{ color: 'var(--color-success)', fontVariantNumeric: 'tabular-nums' }}>cache {info.cacheHit}%</span>
      )}
      <div style={{ flex: 1 }} />
      {/* IM adapter count */}
      {imCount > 0 && (
        <span title={`${imCount} IM adapter(s) active`} style={{
          display: 'flex', alignItems: 'center', gap: 3,
          color: 'var(--text-secondary)', fontSize: 10,
        }}>
          <Radio size={11} style={{ color: 'var(--color-success)' }} />
          {imCount}
        </span>
      )}
      {/* Mobile tunnel connected */}
      {mobileConnected && (
        <span title="Mobile connected" style={{
          display: 'flex', alignItems: 'center',
        }}>
          <Smartphone size={11} style={{ color: 'var(--color-primary)' }} />
        </span>
      )}
      <button onClick={onContextToggle} style={{
        background: 'none', border: 'none',
        color: 'var(--text-secondary)', cursor: 'pointer',
        fontSize: 10, fontFamily: 'var(--font-mono)',
      }}>⌘.</button>
      <span style={{
        display: 'flex', alignItems: 'center', gap: 4,
        color: info.status === t('status.ready') ? 'var(--color-success)' : 'var(--color-warning)',
      }}>
        <span className={info.status === t('status.ready') ? '' : 'agent-status-dot'} style={{
          width: 6, height: 6, borderRadius: '50%',
          background: info.status === t('status.ready') ? 'var(--color-success)' : 'var(--color-warning)',
          display: 'inline-block',
        }} />
        {info.status}
        {elapsedLabel && (
          <span style={{
            color: 'var(--text-tertiary)',
            fontVariantNumeric: 'tabular-nums',
            marginLeft: 2,
          }}>· {elapsedLabel}</span>
        )}
      </span>
    </div>
  )
}
