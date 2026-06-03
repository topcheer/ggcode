import React, { useState } from 'react'
import { Plus, Search, MessageSquare } from 'lucide-react'

interface Props {
  onClose: () => void
}

interface SessionItem {
  id: string
  title: string
  time: string
  active: boolean
}

const mockSessions: SessionItem[] = [
  { id: '1', title: 'Refactor auth middleware', time: '2m ago', active: true },
  { id: '2', title: 'Fix WebSocket reconnection', time: '1h ago', active: false },
  { id: '3', title: 'Add rate limiting to API', time: '3h ago', active: false },
  { id: '4', title: 'Docker compose setup', time: 'Yesterday', active: false },
  { id: '5', title: 'Database migration script', time: '2d ago', active: false },
]

export function Sidebar({ onClose }: Props) {
  const [search, setSearch] = useState('')
  const [sessions, setSessions] = useState(mockSessions)

  const filtered = sessions.filter(s =>
    s.title.toLowerCase().includes(search.toLowerCase())
  )

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
        height: 40, gap: 8,
      }}>
        <span style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)' }}>
          Sessions
        </span>
        <div style={{ flex: 1 }} />
        <button style={{
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
        {filtered.map(s => (
          <div
            key={s.id}
            onClick={() => setSessions(prev => prev.map(x => ({ ...x, active: x.id === s.id })))}
            style={{
              padding: 'var(--spacing-sm) var(--spacing-md)',
              background: s.active ? 'var(--color-card)' : 'transparent',
              borderLeft: s.active ? '2px solid var(--color-primary)' : '2px solid transparent',
              cursor: 'pointer',
              display: 'flex',
              flexDirection: 'column',
              gap: 2,
              transition: 'background 0.1s',
            }}
          >
            <span style={{
              fontSize: 13, fontWeight: s.active ? 500 : 400,
              color: s.active ? 'var(--text-primary)' : 'var(--text-secondary)',
              whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
            }}>
              {s.title}
            </span>
            <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
              {s.time}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
