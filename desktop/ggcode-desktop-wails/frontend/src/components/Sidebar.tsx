import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Plus, Search, Smartphone, Trash2, Lock, FolderOpen, Copy, Pencil, X, MessageSquare, Pin } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'
import { SkeletonList } from './Skeleton'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'

interface Props {
  onClose: () => void
  onSessionSelect?: (id: string) => void
  onShare?: () => void
  activeSessionId?: string
  workspace?: string
  showToast?: (type: 'success' | 'error' | 'info', message: string) => void
  width?: number
}

interface SessionItem {
  id: string
  title: string
  updatedAt: string
  workspace: string
  model: string
  msgCount: number
  locked: boolean
  lastMessage?: string
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

function getDateGroup(dateStr: string): string {
  if (!dateStr) return 'older'
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return 'older'
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 86400000)
  const weekAgo = new Date(today.getTime() - 7 * 86400000)
  if (d >= today) return 'today'
  if (d >= yesterday) return 'yesterday'
  if (d >= weekAgo) return 'thisWeek'
  return 'older'
}

export function Sidebar({ onClose, onSessionSelect, onShare, activeSessionId, workspace, showToast, width }: Props) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [sessions, setSessions] = useState<SessionItem[]>([])
  const [loading, setLoading] = useState(true)

  const [hoveredSessionId, setHoveredSessionId] = useState<string | null>(null)
  const [selectedIndex, setSelectedIndex] = useState(0)
  const itemRefs = useRef<(HTMLDivElement | null)[]>([])
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; session: SessionItem } | null>(null)
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const renameInputRef = useRef<HTMLInputElement>(null)

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
          locked: s.Locked || s.locked || false,
          lastMessage: s.LastMessage || s.lastMessage || '',
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

  // Sort filtered sessions by date desc for grouping
  const sortedFiltered = useMemo(() =>
    [...filtered].sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()),
    [filtered]
  )

  // Group sessions by date
  // Pinned sessions (localStorage-based, workspace-scoped)
  const pinnedKey = `ggcode:pinned:${workspace || 'default'}`
  const [pinnedIds, setPinnedIds] = useState<Set<string>>(() => {
    try {
      const raw = localStorage.getItem(pinnedKey)
      return new Set(raw ? JSON.parse(raw) : [])
    } catch { return new Set() }
  })

  const handleTogglePin = useCallback((s: SessionItem) => {
    setPinnedIds(prev => {
      const next = new Set(prev)
      if (next.has(s.id)) { next.delete(s.id) } else { next.add(s.id) }
      try { localStorage.setItem(pinnedKey, JSON.stringify([...next])) } catch {}
      return next
    })
  }, [pinnedKey])

  const grouped = useMemo(() => {
    const groups: Record<string, SessionItem[]> = { pinned: [], today: [], yesterday: [], thisWeek: [], older: [] }
    sortedFiltered.forEach(s => {
      if (pinnedIds.has(s.id)) { groups.pinned.push(s) }
      else { groups[getDateGroup(s.updatedAt)].push(s) }
    })
    return groups
  }, [sortedFiltered, pinnedIds])

  const groupOrder: string[] = ['pinned', 'today', 'yesterday', 'thisWeek', 'older']
  const groupLabels: Record<string, string> = {
    pinned: t('sidebar.group.pinned'),
    today: t('sidebar.group.today'),
    yesterday: t('sidebar.group.yesterday'),
    thisWeek: t('sidebar.group.thisWeek'),
    older: t('sidebar.group.older'),
  }

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
      setSessions((list as any[] || []).map((s: any) => ({
        id: s.ID || s.id || '',
        title: s.Title || s.title || t('sidebar.untitled'),
        updatedAt: s.UpdatedAt || s.updatedAt || '',
        workspace: s.Workspace || s.workspace || '',
        model: s.Model || s.model || '',
        msgCount: s.MsgCount || s.msgCount || 0,
        locked: s.Locked || s.locked || false,
        lastMessage: s.LastMessage || s.lastMessage || '',
      })))
      onSessionSelect?.(id || '')
    } catch (e) {
      showToast?.('error', `Failed to create session: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const handleSelect = async (s: SessionItem) => {
    if (s.locked) {
      showToast?.('info', 'This session is locked by another instance')
      return
    }
    try {
      await App.LoadSession(s.id)
      onSessionSelect?.(s.id)
    } catch (e) {
      showToast?.('error', `Failed to open session: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const handleContextMenu = (e: React.MouseEvent, s: SessionItem) => {
    e.preventDefault()
    e.stopPropagation()
    setCtxMenu({ x: e.clientX, y: e.clientY, session: s })
  }

  const handleCopyId = async (s: SessionItem) => {
    try {
      await navigator.clipboard.writeText(s.id)
      showToast?.('success', 'Session ID copied')
    } catch {
      showToast?.('error', 'Failed to copy')
    }
  }

  const handleRename = (s: SessionItem) => {
    setRenamingId(s.id)
    setRenameValue(s.title || '')
    // Focus input after render
    setTimeout(() => renameInputRef.current?.focus(), 50)
  }

  const handleRenameSubmit = async () => {
    if (!renamingId) return
    const newTitle = renameValue.trim()
    if (!newTitle) {
      setRenamingId(null)
      return
    }
    try {
      await App.RenameSession(renamingId, newTitle)
      setSessions(prev => prev.map(s => s.id === renamingId ? { ...s, title: newTitle } : s))
      showToast?.('success', 'Session renamed')
    } catch (e) {
      showToast?.('error', `Rename failed: ${e instanceof Error ? e.message : String(e)}`)
    }
    setRenamingId(null)
  }

  const handleRenameKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') { e.preventDefault(); handleRenameSubmit() }
    else if (e.key === 'Escape') { e.preventDefault(); setRenamingId(null) }
  }

  // Reset selection when filter changes
  useEffect(() => { setSelectedIndex(0) }, [search])

  // Keyboard navigation
  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (sortedFiltered.length === 0) return
    // Don't hijack keyboard when user is typing in an input/textarea
    const target = e.target as HTMLElement
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return
    if (e.key === 'ArrowDown' || e.key === 'j') {
      e.preventDefault()
      setSelectedIndex(prev => Math.min(prev + 1, sortedFiltered.length - 1))
    } else if (e.key === 'ArrowUp' || e.key === 'k') {
      e.preventDefault()
      setSelectedIndex(prev => Math.max(prev - 1, 0))
    } else if (e.key === 'Enter' && sortedFiltered[selectedIndex]) {
      e.preventDefault()
      handleSelect(sortedFiltered[selectedIndex])
    }
  }, [sortedFiltered, selectedIndex])

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handleKeyDown])

  // Auto-scroll selected item into view
  useEffect(() => {
    const el = itemRefs.current[selectedIndex]
    if (el) el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [selectedIndex])

  // On initial load, scroll active session into view
  const scrolledToActive = useRef(false)
  useEffect(() => {
    if (scrolledToActive.current || loading || sortedFiltered.length === 0) return
    if (!activeSessionId) { scrolledToActive.current = true; return }
    const idx = sortedFiltered.findIndex(s => s.id === activeSessionId)
    if (idx >= 0) {
      const el = itemRefs.current[idx]
      if (el) el.scrollIntoView({ block: 'nearest' })
    }
    scrolledToActive.current = true
  }, [loading, sortedFiltered, activeSessionId])

  return (
    <div style={{
      width: width ? `${width}px` : 'var(--sidebar-width)',
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
          {t('sidebar.sessions')}
        </span>
        {sessions.length > 0 && (
          <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
            {sessions.length}
          </span>
        )}
        <div style={{ flex: 1 }} />
        <button onClick={handleNew} title={t('sidebar.newSession')} style={{
          width: 28, height: 28, borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          <Plus size={15} />
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
          placeholder={t('sidebar.search')}
          style={{
            flex: 1, border: 'none', background: 'transparent',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 12,
          }}
        />
        {search && (
          <button
            onClick={() => setSearch('')}
            title="Clear"
            style={{
              flexShrink: 0, background: 'none', border: 'none', cursor: 'pointer',
              padding: 2, display: 'flex', alignItems: 'center',
              color: 'var(--text-tertiary)',
            }}
          >
            <X size={13} />
          </button>
        )}
      </div>
      {search && sortedFiltered.length > 0 && (
        <div style={{ padding: '0 var(--spacing-md) var(--spacing-xs)', fontSize: 11, color: 'var(--text-tertiary)' }}>
          {sortedFiltered.length} {sortedFiltered.length === 1 ? 'match' : 'matches'}
        </div>
      )}

      {/* Session list */}
      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--spacing-xs) 0', textAlign: 'left' }}>
        {loading && (
          <SkeletonList count={5} variant="session" />
        )}
        {!loading && sortedFiltered.length === 0 && (
          <div style={{
            padding: 'var(--spacing-xl) var(--spacing-md)',
            textAlign: 'center',
            display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--spacing-sm)',
          }}>
            {search ? (
              <>
                <div style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
                  No sessions matching "{search}"
                </div>
                <button
                  onClick={() => setSearch('')}
                  style={{
                    background: 'none', border: '1px solid var(--color-border)',
                    borderRadius: 'var(--radius-sm)', padding: '4px 12px',
                    color: 'var(--text-secondary)', fontSize: 11, cursor: 'pointer',
                  }}
                >
                  Clear search
                </button>
              </>
            ) : (
              <>
                <MessageSquare size={28} style={{ color: 'var(--text-tertiary)', opacity: 0.5 }} />
                <div style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
                  No sessions yet
                </div>
                <button
                  onClick={handleNew}
                  style={{
                    background: 'var(--color-primary)', border: 'none',
                    borderRadius: 'var(--radius-sm)', padding: '6px 16px',
                    color: '#fff', fontSize: 12, cursor: 'pointer',
                    display: 'flex', alignItems: 'center', gap: 4,
                  }}
                >
                  <Plus size={13} /> Start a conversation
                </button>
              </>
            )}
          </div>
        )}
        {sortedFiltered.map((s, idx) => {
          const group = getDateGroup(s.updatedAt)
          const prevGroup = idx > 0 ? getDateGroup(sortedFiltered[idx - 1].updatedAt) : null
          const showHeader = group !== prevGroup
          const isSelected = idx === selectedIndex
          return (
            <React.Fragment key={s.id}>
              {showHeader && (
                <div style={{
                  padding: 'var(--spacing-sm) var(--spacing-md) var(--spacing-xs)',
                  fontSize: 11, fontWeight: 600,
                  color: 'var(--text-tertiary)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.05em',
                }}>
                  {groupLabels[group]}
                </div>
              )}
              <div
                ref={el => { itemRefs.current[idx] = el }}
                onClick={() => { setSelectedIndex(idx); handleSelect(s) }}
                onContextMenu={e => handleContextMenu(e, s)}
                onMouseEnter={() => { setHoveredSessionId(s.id); setSelectedIndex(idx) }}
                onMouseLeave={() => setHoveredSessionId(prev => prev === s.id ? null : prev)}
                style={{
                  padding: 'var(--spacing-sm) var(--spacing-md)',
                  background: s.id === activeSessionId
                    ? 'var(--color-card)'
                    : isSelected
                      ? 'rgba(128,128,128,0.08)'
                      : 'transparent',
                  borderLeft: s.id === activeSessionId ? '2px solid var(--color-primary)' : '2px solid transparent',
                  cursor: s.locked ? 'not-allowed' : 'pointer',
                  opacity: s.locked ? 0.5 : 1,
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
                  {renamingId === s.id ? (
                    <input
                      ref={renameInputRef}
                      value={renameValue}
                      onChange={e => setRenameValue(e.target.value)}
                      onKeyDown={handleRenameKeyDown}
                      onBlur={handleRenameSubmit}
                      onClick={e => e.stopPropagation()}
                      style={{
                        fontSize: 13, fontWeight: 400,
                        color: 'var(--text-primary)',
                        flex: 1, minWidth: 0,
                        border: '1px solid var(--color-primary)',
                        borderRadius: 'var(--radius-sm)',
                        background: 'var(--color-card)',
                        outline: 'none',
                        padding: '2px 6px',
                      }}
                    />
                  ) : (
                    <span style={{
                      fontSize: 13, fontWeight: s.id === activeSessionId ? 500 : 400,
                      color: s.id === activeSessionId ? 'var(--text-primary)' : 'var(--text-secondary)',
                      whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                      flex: 1,
                    }}>
                      {s.title || t('sidebar.untitled')}
                    </span>
                  )}
                  {pinnedIds.has(s.id) && (
                    <Pin size={11} style={{ color: 'var(--color-primary)', flexShrink: 0, marginRight: 2, fill: 'currentColor' }} />
                  )}
                  {s.locked && (
                    <Lock size={11} style={{ color: 'var(--text-tertiary)', flexShrink: 0, marginRight: 2 }} />
                  )}
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
                {s.lastMessage && (
                  <div style={{
                    fontSize: 11,
                    color: 'var(--text-tertiary)',
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    marginTop: 1,
                    lineHeight: 1.3,
                  }}>
                    {s.lastMessage}
                  </div>
                )}
                <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                  <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                    {relativeTime(s.updatedAt, t)}
                  </span>
                  {s.msgCount > 0 && (
                    <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                      {s.msgCount} msgs
                    </span>
                  )}
                  {s.model && (
                    <span style={{
                      fontSize: 10, color: 'var(--text-tertiary)',
                      background: 'var(--color-card)',
                      borderRadius: 'var(--radius-sm)',
                      padding: '1px 5px',
                      maxWidth: 100,
                      whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                    }}>
                      {s.model}
                    </span>
                  )}
                </div>
              </div>
            </React.Fragment>
          )
        })}
      </div>

      {/* Bottom bar */}
      <div style={{
        display: 'flex', alignItems: 'center',
        padding: 'var(--spacing-xs) var(--spacing-sm)',
        borderTop: '1px solid var(--color-border)',
      }}>
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

      {/* Right-click context menu */}
      {ctxMenu && (
        <ContextMenu
          x={ctxMenu.x}
          y={ctxMenu.y}
          onClose={() => setCtxMenu(null)}
          items={[
            {
              label: 'Open',
              icon: <FolderOpen size={14} />,
              onClick: () => handleSelect(ctxMenu.session),
              disabled: ctxMenu.session.locked,
            },
            {
              label: 'Rename',
              icon: <Pencil size={14} />,
              onClick: () => handleRename(ctxMenu.session),
              disabled: ctxMenu.session.locked,
            },
            {
              label: pinnedIds.has(ctxMenu.session.id) ? 'Unpin' : 'Pin',
              icon: <Pin size={14} />,
              onClick: () => handleTogglePin(ctxMenu.session),
            },
            {
              label: 'Copy ID',
              icon: <Copy size={14} />,
              onClick: () => handleCopyId(ctxMenu.session),
            },
            {
              label: 'Delete',
              icon: <Trash2 size={14} />,
              onClick: () => handleDelete({ stopPropagation: () => {} } as React.MouseEvent, ctxMenu.session.id),
              danger: true,
            },
          ]}
        />
      )}
    </div>
  )
}
