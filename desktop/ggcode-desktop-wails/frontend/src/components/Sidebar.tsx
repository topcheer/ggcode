import React, { useState, useEffect } from 'react'
import { Plus, Search, Smartphone, Trash2 } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'

interface Props {
  onClose: () => void
  onSessionSelect?: (id: string) => void
  onShare?: () => void
  activeSessionId?: string
  workspace?: string
  showToast?: (type: 'success' | 'error' | 'info', message: string) => void
}

interface SessionItem {
  id: string
  title: string
  updatedAt: string
  workspace: string
  model: string
  msgCount: number
}

function relativeTime(dateStr: string, t: (key: any, params?: Record<string, string | number>) => string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return dateStr
  const now = Date.now()
  const diff = now - d.getTime()
  if (diff < 60000) return t('sidebar.time.justNow')
  if (diff < 3600000) return t('sidebar.time.minutesAgo', { n: Math.floor(diff / 60000) })
  if (diff < 86400000) return t('sidebar.time.hoursAgo', { n: Math.floor(diff / 3600000) })
  if (diff < 604800000) return t('sidebar.time.yesterday')
  return d.toLocaleDateString()
}

export function Sidebar({ onClose, onSessionSelect, onShare, activeSessionId, workspace, showToast }: Props) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [sessions, setSessions] = useState<SessionItem[]>([])
  const [loading, setLoading] = useState(true)

  const [hoveredSessionId, setHoveredSessionId] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        // Get workDir to display current workspace
        const dir = await App.GetWorkDir() as string
        console.log('[Sidebar] workDir:', dir)

        const result = await App.ListSessions()
        if (cancelled || !result) return
        // result is SessionInfo[] from Go
        setSessions((result as any[]).map((s: any) => ({
          id: s.ID || s.id || '',
          title: s.Title || s.title || t('sidebar.untitled'),
          updatedAt: s.UpdatedAt || s.updatedAt || '',
          workspace: s.Workspace || s.workspace || '',
          model: s.Model || s.model || '',
          msgCount: s.MsgCount || s.msgCount || 0,
        })))
      } catch (e) {
        showToast?.('error', `Failed to load sessions: ${e instanceof Error ? e.message : String(e)}`)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [workspace, t])

  const filtered = sessions.filter(s =>
    s.title.toLowerCase().includes(search.toLowerCase())
  )

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation()
    if (!window.confirm('Delete this session? This cannot be undone.')) return
    try {
      await App.DeleteSession(id)
      setSessions(prev => prev.filter(s => s.id !== id))
      showToast?.('success', 'Session deleted')
    } catch (e) {
      showToast?.('error', `Failed to delete session: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const handleNew = async () => {
    try {
      const id = await App.NewSession()
      const list = await App.ListSessions()
      setSessions(list || [])
      onSessionSelect?.(id || '')
    } catch (e) {
      showToast?.('error', `Failed to create session: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const handleSelect = async (id: string) => {
    try {
      await App.LoadSession(id)
      onSessionSelect?.(id)
    } catch (e) {
      showToast?.('error', `Failed to open session: ${e instanceof Error ? e.message : String(e)}`)
    }
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
          placeholder={t('sidebar.search')}
          style={{
            flex: 1, border: 'none', background: 'transparent',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 12,
          }}
        />
      </div>

      {/* Session list */}
      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--spacing-xs) 0', textAlign: 'left' }}>
        {loading && (
          <div style={{ padding: 'var(--spacing-md)', color: 'var(--text-tertiary)', fontSize: 12, textAlign: 'center' }}>
            {t('sidebar.loading')}
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
            onMouseEnter={() => setHoveredSessionId(s.id)}
            onMouseLeave={() => setHoveredSessionId(prev => prev === s.id ? null : prev)}
            style={{
              padding: 'var(--spacing-sm) var(--spacing-md)',
              background: s.id === activeSessionId ? 'var(--color-card)' : 'transparent',
              borderLeft: s.id === activeSessionId ? '2px solid var(--color-primary)' : '2px solid transparent',
              cursor: 'pointer',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              width: '100%',
              boxSizing: 'border-box',
              gap: 2,
              transition: 'background 0.1s',
              position: 'relative',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, width: '100%' }}>
              <span style={{
                fontSize: 13, fontWeight: s.id === activeSessionId ? 500 : 400,
                color: s.id === activeSessionId ? 'var(--text-primary)' : 'var(--text-secondary)',
                whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                flex: 1,
              }}>
                {s.title || t('sidebar.untitled')}
              </span>
              <button
                onClick={e => handleDelete(e, s.id)}
                style={{
                  background: 'none', border: 'none',
                  color: 'var(--text-tertiary)', cursor: 'pointer',
                  opacity: hoveredSessionId === s.id || s.id === activeSessionId ? 0.7 : 0.28,
                  transition: 'opacity 0.15s, color 0.15s',
                  display: 'flex', alignItems: 'center',
                  flexShrink: 0,
                  padding: 4,
                }}
                aria-label="Delete session"
                title="Delete session"
                onMouseEnter={e => { e.currentTarget.style.opacity = '1'; e.currentTarget.style.color = 'var(--color-error)' }}
                onMouseLeave={e => { e.currentTarget.style.opacity = hoveredSessionId === s.id || s.id === activeSessionId ? '0.7' : '0.28'; e.currentTarget.style.color = 'var(--text-tertiary)' }}
              >
                <Trash2 size={12} />
              </button>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                {relativeTime(s.updatedAt, t)}
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

      {/* Bottom bar */}
      <div style={{
        display: 'flex', alignItems: 'center',
        padding: 'var(--spacing-xs) var(--spacing-sm)',
        borderTop: '1px solid var(--color-border)',
      }}>
        <button onClick={handleNew} style={{
          padding: '4px 10px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 4,
        }}><Plus size={14} /> New</button>
        <div style={{ flex: 1 }} />
        {onShare && (
          <button onClick={onShare} title="Share with mobile" style={{
            padding: '4px 8px', borderRadius: 'var(--radius-sm)',
            background: 'transparent', color: 'var(--text-secondary)',
            border: 'none', cursor: 'pointer',
            display: 'flex', alignItems: 'center',
          }}><Smartphone size={16} /></button>
        )}
      </div>
    </div>
  )
}
