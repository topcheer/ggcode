import React, { useEffect, useRef, useState, useCallback } from 'react'
import { Terminal, Search, Trash2, Play, Pause } from 'lucide-react'
import { useTranslation } from '../i18n'

interface LogEntry {
  seq: number
  category: string
  message: string
  time: string
}

const MAX_LINES = 5000
const POLL_INTERVAL = 300

// Module-level state — survives component unmount when navigating away
let persistentLines: LogEntry[] = []
let persistentEnabled = false

// Category colors
const catColors: Record<string, string> = {
  agent: '#22c55e',
  tool: '#3b82f6',
  context: '#06b6d4',
  openai: '#10a37f',
  anthropic: '#d97706',
  gemini: '#4285f4',
  provider: '#8b5cf6',
  probe: '#6366f1',
  relay: '#a855f7',
  tunnel: '#f59e0b',
  broker: '#ec4899',
  config: '#6b7280',
  session: '#14b8a6',
  swarm: '#f97316',
  desktop: '#8b5cf6',
  im: '#06b6d4',
  qq: '#12b7f5',
  tg: '#0088cc',
  discord: '#5865f2',
  dingtalk: '#0089ff',
  whatsapp: '#25d366',
  wechat: '#07c160',
  signal: '#3a76f0',
  mattermost: '#0058cc',
  pc: '#9333ea',
  daemon: '#ef4444',
  mcp: '#f472b6',
  harness: '#fb923c',
  tui: '#34d399',
  permission: '#fbbf24',
}

export default function DebugConsole() {
  const { t } = useTranslation()
  const [lines, setLines] = useState<LogEntry[]>(() => persistentLines)
  const [enabled, setEnabled] = useState(() => persistentEnabled)
  const [filter, setFilter] = useState('')
  const [catFilter, setCatFilter] = useState<string>('')
  const [autoScroll, setAutoScroll] = useState(true)
  const containerRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const enabledRef = useRef(persistentEnabled)

  // Sync lines to persistent store
  const updateLines = useCallback((updater: (prev: LogEntry[]) => LogEntry[]) => {
    setLines(prev => {
      const next = updater(prev)
      persistentLines = next
      return next
    })
  }, [])

  // Toggle log capture
  const toggle = useCallback(async () => {
    const next = !enabledRef.current
    enabledRef.current = next
    persistentEnabled = next
    setEnabled(next)
    try {
      // @ts-ignore — Wails binding
      await window.go?.main?.App?.ToggleLogStream?.(next)
    } catch { /* ignore */ }
  }, [])

  // Clear local buffer
  const clear = useCallback(() => updateLines(() => []), [])

  // On mount: check backend state and restore
  useEffect(() => {
    if (!persistentEnabled) return
    // Drain any logs that accumulated while we were away
    const drain = async () => {
      try {
        // @ts-ignore — Wails binding
        const raw = await window.go?.main?.App?.DrainLogStream?.()
        if (raw && raw !== '[]') {
          const entries: LogEntry[] = JSON.parse(raw)
          if (entries.length > 0) {
            updateLines(prev => {
              const next = [...prev, ...entries]
              return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next
            })
          }
        }
      } catch { /* ignore */ }
    }
    drain()
  }, [updateLines])

  // Poll for new log entries
  useEffect(() => {
    const poll = async () => {
      if (!enabledRef.current) return
      try {
        // @ts-ignore — Wails binding
        const raw = await window.go?.main?.App?.DrainLogStream?.()
        if (!raw || raw === '[]') return
        const entries: LogEntry[] = JSON.parse(raw)
        if (entries.length === 0) return
        updateLines(prev => {
          const next = [...prev, ...entries]
          return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next
        })
      } catch { /* ignore */ }
    }
    const id = setInterval(poll, POLL_INTERVAL)
    return () => clearInterval(id)
  }, [updateLines])

  // Auto-scroll
  useEffect(() => {
    if (autoScroll && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [lines, autoScroll])

  // Detect manual scroll
  const handleScroll = () => {
    if (!containerRef.current) return
    const { scrollTop, scrollHeight, clientHeight } = containerRef.current
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 50)
  }

  // Filter
  const filtered = lines.filter(l => {
    if (catFilter && l.category !== catFilter) return false
    if (filter) {
      const f = filter.toLowerCase()
      return l.message.toLowerCase().includes(f) || l.category.toLowerCase().includes(f)
    }
    return true
  })

  // Unique categories
  const categories = [...new Set(lines.map(l => l.category))].sort()

  return (
    <div className="dbg-console" style={{ display: 'flex', flexDirection: 'column', height: '100%', fontFamily: 'var(--font-mono)', fontSize: 12 }}>
      {/* Toolbar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderBottom: '1px solid var(--color-border)', flexShrink: 0 }}>
        <Terminal size={16} style={{ color: '#58a6ff' }} />
        <span style={{ fontWeight: 600, color: '#58a6ff', fontSize: 13 }}>{t('debug.title')}</span>

        <button onClick={toggle} title={enabled ? t('debug.stop') : t('debug.start')} aria-label={enabled ? t('debug.stop') : t('debug.start')} style={{
          marginLeft: 8, padding: '4px 10px', borderRadius: 4, border: 'none', cursor: 'pointer',
          background: enabled ? '#da3633' : '#238636', color: '#fff', fontSize: 11, fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 4,
        }}>
          {enabled ? <><Pause size={12} /> {t('debug.stop')}</> : <><Play size={12} /> {t('debug.start')}</>}
        </button>

        <button onClick={clear} title={t('debug.clear')} aria-label={t('debug.clear')} style={{
          padding: '4px 8px', borderRadius: 4, border: '1px solid var(--color-border)', cursor: 'pointer',
          background: 'transparent', color: 'var(--text-secondary)', fontSize: 11,
          display: 'flex', alignItems: 'center', gap: 4,
        }}>
          <Trash2 size={12} /> {t('debug.clear')}
        </button>

        <span style={{ marginLeft: 8, color: 'var(--text-secondary)', fontSize: 11 }}>
          {filtered.length} / {lines.length} {t('debug.lines')}
          {enabled && <span style={{ color: '#3fb950', marginLeft: 6 }}>{t('debug.live')}</span>}
        </span>

        <div style={{ flex: 1 }} />

        {/* Category filter */}
        <select value={catFilter} onChange={e => setCatFilter(e.target.value)} aria-label={t('common.filterByCategory')} title={t('common.filterByCategory')} style={{
          padding: '3px 6px', borderRadius: 4, border: '1px solid var(--color-border)',
          background: 'var(--color-surface)', color: 'var(--text-primary)', fontSize: 11,
        }}>
          <option value="">{t('debug.allCategories')}</option>
          {categories.map(c => <option key={c} value={c}>{c}</option>)}
        </select>

        {/* Search */}
        <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
          <Search size={12} style={{ position: 'absolute', left: 6, color: 'var(--text-tertiary)' }} />
          <input value={filter} onChange={e => setFilter(e.target.value)} placeholder={t('debug.filterPlaceholder')} aria-label="Filter debug logs" title="Filter debug logs" style={{
            padding: '3px 6px 3px 22px', borderRadius: 4, border: '1px solid var(--color-border)',
            background: 'var(--color-surface)', color: 'var(--text-primary)', fontSize: 11, width: 140,
            outline: 'none',
          }} />
        </div>
      </div>

      {/* Log lines */}
      <div ref={containerRef} onScroll={handleScroll} style={{ flex: 1, overflow: 'auto', padding: '4px 0' }}>
        {filtered.map(l => (
          <div key={l.seq} style={{ padding: '1px 12px', display: 'flex', gap: 8, lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            <span style={{ color: 'var(--text-tertiary)', flexShrink: 0 }}>{l.time}</span>
            <span style={{ color: catColors[l.category] || 'var(--text-secondary)', flexShrink: 0, minWidth: 60 }}>{l.category || '—'}</span>
            <span style={{ color: 'var(--text-primary)' }}>{l.message}</span>
          </div>
        ))}
        {!enabled && lines.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-tertiary)' }}>
            {t('debug.empty')}
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
