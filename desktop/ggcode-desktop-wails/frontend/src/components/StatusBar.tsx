import React, { useState, useEffect } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'
import type { StatusBarData } from '../types'

interface StatusBarProps {
  onContextToggle?: () => void
  data?: StatusBarData
}

export function StatusBar({ onContextToggle, data }: StatusBarProps) {
  const [info, setInfo] = useState<StatusBarData>(data ?? {
    vendor: '...',
    model: '...',
    contextUsed: 0,
    contextTotal: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheHit: 0,
    status: 'Ready',
  })

  // Load config on mount
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const cfg = await App.GetConfig()
        if (cancelled) return
        setInfo(prev => ({
          ...prev,
          vendor: cfg.vendor || prev.vendor,
          model: cfg.model || prev.model,
        }))
      } catch {
        // GetConfig not available yet, keep defaults
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  // Listen for chat:stream events to update token usage
  useEffect(() => {
    const off = EventsOn('chat:stream', (data: any) => {
      if (!data) return
      if (data.type === 'done') {
        setInfo(prev => ({
          ...prev,
          inputTokens: data.inputTokens ?? prev.inputTokens,
          outputTokens: data.outputTokens ?? prev.outputTokens,
          contextUsed: data.contextUsed ?? prev.contextUsed,
          contextTotal: data.contextTotal ?? prev.contextTotal,
          cacheHit: data.cacheHit ?? prev.cacheHit,
          status: 'Ready',
        }))
      } else if (data.type === 'start') {
        setInfo(prev => ({ ...prev, status: 'Thinking...' }))
      } else if (data.type === 'stream') {
        setInfo(prev => ({ ...prev, status: 'Streaming' }))
      }
    })
    return () => { if (typeof off === 'function') off() }
  }, [])

  // Merge external data if provided
  useEffect(() => {
    if (data) setInfo(data)
  }, [data])

  const formatTokens = (n: number) => {
    if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
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
        color: info.status === 'Ready' ? 'var(--color-success)' : 'var(--color-warning)',
      }}>● {info.status}</span>
    </div>
  )
}
