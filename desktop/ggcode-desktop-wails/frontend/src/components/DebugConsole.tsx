import React, { useEffect, useRef, useState, useCallback } from 'react'
import { Terminal, Search, Trash2, Play, Pause } from 'lucide-react'

interface LogEntry {
  seq: number
  category: string
  message: string
  time: string
}

const MAX_LINES = 5000
const POLL_INTERVAL = 300

// Category colors
const catColors: Record<string, string> = {
  agent: '#22c55e',
  tool: '#3b82f6',
  relay: '#a855f7',
  tunnel: '#f59e0b',
  broker: '#ec4899',
  config: '#6b7280',
  session: '#14b8a6',
  swarm: '#f97316',
  desktop: '#8b5cf6',
  im: '#06b6d4',
}

export default function DebugConsole() {
  const [lines, setLines] = useState<LogEntry[]>([])
  const [enabled, setEnabled] = useState(false)
  const [filter, setFilter] = useState('')
  const [catFilter, setCatFilter] = useState<string>('')
  const [autoScroll, setAutoScroll] = useState(true)
  const containerRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const enabledRef = useRef(false)

  // Toggle log capture
  const toggle = useCallback(async () => {
    const next = !enabledRef.current
    enabledRef.current = next
    setEnabled(next)
    try {
      // @ts-ignore — Wails binding
      await window.go?.main?.App?.ToggleLogStream?.(next)
    } catch { /* ignore */ }
  }, [])

  // Clear local buffer
  const clear = useCallback(() => setLines([]), [])

  // On mount: check backend state and start polling if active
  useEffect(() => {
    // Check if backend is already capturing (e.g. we navigated away and back)
    const checkAndPoll = async () => {
      // Drain first to see if there's pending data
      try {
        // @ts-ignore — Wails binding
        const raw = await window.go?.main?.App?.DrainLogStream?.()
        if (raw && raw !== '[]') {
          // Backend was capturing — sync state
          enabledRef.current = true
          setEnabled(true)
          const entries: LogEntry[] = JSON.parse(raw)
          if (entries.length > 0) {
            setLines(entries)
          }
        }
      } catch { /* ignore */ }
    }
    checkAndPoll()
  }, [])

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
        setLines(prev => {
          const next = [...prev, ...entries]
          return next.length > MAX_LINES ? next.slice(-MAX_LINES) : next
        })
      } catch { /* ignore */ }
    }
    const id = setInterval(poll, POLL_INTERVAL)
    return () => clearInterval(id)
  }, [])

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
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: '#0d1117', color: '#c9d1d9', fontFamily: 'var(--font-mono)', fontSize: 12 }}>
      {/* Toolbar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderBottom: '1px solid #21262d', flexShrink: 0 }}>
        <Terminal size={16} style={{ color: '#58a6ff' }} />
        <span style={{ fontWeight: 600, color: '#58a6ff', fontSize: 13 }}>Debug Console</span>

        <button onClick={toggle} style={{
          marginLeft: 8, padding: '4px 10px', borderRadius: 4, border: 'none', cursor: 'pointer',
          background: enabled ? '#da3633' : '#238636', color: '#fff', fontSize: 11, fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 4,
        }}>
          {enabled ? <><Pause size={12} /> Stop</> : <><Play size={12} /> Start</>}
        </button>

        <button onClick={clear} style={{
          padding: '4px 8px', borderRadius: 4, border: '1px solid #30363d', cursor: 'pointer',
          background: 'transparent', color: '#8b949e', fontSize: 11,
          display: 'flex', alignItems: 'center', gap: 4,
        }}>
          <Trash2 size={12} /> Clear
        </button>

        <span style={{ marginLeft: 8, color: '#8b949e', fontSize: 11 }}>
          {filtered.length} / {lines.length} lines
          {enabled && <span style={{ color: '#3fb950', marginLeft: 6 }}>● LIVE</span>}
        </span>

        <div style={{ flex: 1 }} />

        {/* Category filter */}
        <select value={catFilter} onChange={e => setCatFilter(e.target.value)} style={{
          padding: '3px 6px', borderRadius: 4, border: '1px solid #30363d',
          background: '#161b22', color: '#c9d1d9', fontSize: 11,
        }}>
          <option value="">All categories</option>
          {categories.map(c => <option key={c} value={c}>{c}</option>)}
        </select>

        {/* Search */}
        <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
          <Search size={12} style={{ position: 'absolute', left: 6, color: '#484f58' }} />
          <input value={filter} onChange={e => setFilter(e.target.value)} placeholder="Filter..." style={{
            padding: '3px 6px 3px 22px', borderRadius: 4, border: '1px solid #30363d',
            background: '#161b22', color: '#c9d1d9', fontSize: 11, width: 140,
            outline: 'none',
          }} />
        </div>
      </div>

      {/* Log lines */}
      <div ref={containerRef} onScroll={handleScroll} style={{ flex: 1, overflow: 'auto', padding: '4px 0' }}>
        {filtered.map(l => (
          <div key={l.seq} style={{ padding: '1px 12px', display: 'flex', gap: 8, lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            <span style={{ color: '#484f58', flexShrink: 0 }}>{l.time}</span>
            <span style={{ color: catColors[l.category] || '#8b949e', flexShrink: 0, minWidth: 60 }}>{l.category || '—'}</span>
            <span style={{ color: '#c9d1d9' }}>{l.message}</span>
          </div>
        ))}
        {!enabled && lines.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center', color: '#484f58' }}>
            Click "Start" to begin capturing debug logs
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
