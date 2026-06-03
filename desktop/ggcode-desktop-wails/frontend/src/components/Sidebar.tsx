import React, { useState, useEffect } from 'react'
import { Plus, Search, Trash2 } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface Props {
  onClose: () => void
  onSessionSelect?: (id: string) => void
  activeSessionId?: string
}

interface SessionItem {
  id: string
  title: string
  updatedAt: string
  workspace: string
  model: string
  msgCount: number
}

function relativeTime(dateStr: string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return dateStr
  const now = Date.now()
  const diff = now - d.getTime()
  if (diff < 60000) return 'just now'
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  if (diff < 604800000) return `${Math.floor(diff / 86400000)}d ago`
  return d.toLocaleDateString()
}

export function Sidebar({ onClose, onSessionSelect, activeSessionId }: Props) {
  const [search, setSearch] = useState('')
  const [sessions, setSessions] = useState<SessionItem[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const result = await App.ListSessions()
        if (cancelled || !result) return
        // result is SessionInfo[] from Go
        setSessions((result as any[]).map((s: any) => ({
          id: s.ID || s.id || '',
          title: s.Title || s.title || 'Untitled',
          updatedAt: s.UpdatedAt || s.updatedAt || '',
          workspace: s.Workspace || s.workspace || '',
          model: s.Model || s.model || '',
          msgCount: s.MsgCount || s.msgCount || 0,
        })))
      } catch {
        // Fallback: no sessions yet
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  const filtered = sessions.filter(s =>
    s.title.toLowerCase().includes(search.toLowerCase())
  )

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation()
    try {
      await App.DeleteSession(id)
      setSessions(prev => prev.filter(s => s.id !== id))
    } catch {}
  }

  const handleNew = async () => {
    try {
      await App.NewSession()
      onSessionSelect?.('')
    } catch {}
  }

  const handleSelect = async (id: string) => {
    try {
      await App.LoadSession(id)
      onSessionSelect?.(id)
    } catch {}
  }

  return (
    <div style={{
      width: 'var(--sidebar-width)',
      height: '100%',
      background: 'var(--color-bg)',
      borderRight: '1px solid var(--color-border)',
      display: 'flex',
      flexDirection: 'column',
      flexShrink: 0,
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center',
        padding: '0 var(--spacing-md)',
        height: 52, gap: 8,
      }}>
        <span style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)' }}>
          Sessions
        </span>
        <div style={{ flex: 1 }} />
        <button onClick={handleNew} style={{
          padding: '3px 8px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)',
          color: '#fff', border: 'none', cursor: 'pointer',
          display: 'flex', alignItems: 'center', gap: 4,
          fontSize: 11, fontWeight: 500,
        }}>
          <Plus size={12} /> New
        </button>
      </div>

      {/* Search */}
      <div style={{
        margin: 'var(--spacing-sm) var(--spacing-md)',
        height: 32, borderRadius: 'var(--radius-md)',
        background: 'var(--color-card)',
        border: '1px solid var(--color-border)',
        display: 'flex', alignItems: 'center',
        padding: '0 var(--spacing-sm)', gap: 6,
      }}>
        <Search size={14} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} />
        <input
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search sessions..."
          style={{
            flex: 1, border: 'none', background: 'transparent',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 12,
          }}
        />
      </div>

      {/* Session list */}
      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--spacing-xs) 0' }}>
        {loading && (
          <div style={{ padding: 'var(--spacing-md)', color: 'var(--text-tertiary)', fontSize: 12, textAlign: 'center' }}>
            Loading...
          </div>
        )}
        {!loading && filtered.length === 0 && (
          <div style={{ padding: 'var(--spacing-md)', color: 'var(--text-tertiary)', fontSize: 12, textAlign: 'center' }}>
            {search ? 'No matches' : 'No sessions yet'}
          </div>
        )}
        {filtered.map(s => (
          <div
            key={s.id}
            onClick={() => handleSelect(s.id)}
            style={{
              padding: 'var(--spacing-sm) var(--spacing-md)',
              background: s.id === activeSessionId ? 'var(--color-card)' : 'transparent',
              borderLeft: s.id === activeSessionId ? '2px solid var(--color-primary)' : '2px solid transparent',
              cursor: 'pointer',
              display: 'flex',
              flexDirection: 'column',
              gap: 2,
              transition: 'background 0.1s',
              position: 'relative',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
              <span style={{
                fontSize: 13, fontWeight: s.id === activeSessionId ? 500 : 400,
                color: s.id === activeSessionId ? 'var(--text-primary)' : 'var(--text-secondary)',
                whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                flex: 1,
              }}>
                {s.title || 'Untitled'}
              </span>
              <button
                onClick={e => handleDelete(e, s.id)}
                style={{
                  background: 'none', border: 'none',
                  color: 'var(--text-tertiary)', cursor: 'pointer',
                  opacity: 0, transition: 'opacity 0.15s',
                  display: 'flex', alignItems: 'center',
                  flexShrink: 0,
                }}
                onMouseEnter={e => (e.currentTarget.style.opacity = '1')}
                onMouseLeave={e => (e.currentTarget.style.opacity = '0')}
              >
                <Trash2 size={12} />
              </button>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                {relativeTime(s.updatedAt)}
              </span>
              {s.msgCount > 0 && (
                <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                  {s.msgCount} msgs
                </span>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
