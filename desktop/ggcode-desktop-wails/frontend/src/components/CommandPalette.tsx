import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Search, type LucideIcon } from 'lucide-react'
import { useTranslation, type TranslationKey } from '../i18n'

export interface CommandAction {
  nameKey: TranslationKey
  shortcut?: string
  categoryKey: TranslationKey
  icon?: LucideIcon
  action: () => void
}

const RECENT_KEY = 'ggcode-cmd-recent'
const MAX_RECENT = 5

/** Load recently used command nameKeys from localStorage */
function loadRecent(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY)
    return raw ? JSON.parse(raw) : []
  } catch { return [] }
}

/** Save a command as recently used (moves to front, dedupes, caps at MAX_RECENT) */
function saveRecent(nameKey: string) {
  try {
    const recent = loadRecent().filter(k => k !== nameKey)
    recent.unshift(nameKey)
    localStorage.setItem(RECENT_KEY, JSON.stringify(recent.slice(0, MAX_RECENT)))
  } catch { /* ignore */ }
}

interface Props {
  onClose: () => void
  actions: CommandAction[]
}

/** Simple fuzzy match: checks if all chars of query appear in order in target */
function fuzzyMatch(query: string, target: string): boolean {
  if (!query) return true
  const q = query.toLowerCase()
  const t = target.toLowerCase()
  let qi = 0
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++
  }
  return qi === q.length
}

export function CommandPalette({ onClose, actions }: Props) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const itemRefs = useRef<(HTMLDivElement | null)[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  const filtered = useMemo(() =>
    actions.filter(c =>
      fuzzyMatch(query, t(c.nameKey)) || fuzzyMatch(query, t(c.categoryKey))
    ),
  [actions, query, t])

  // Reset selection when filter changes
  useEffect(() => { setSelectedIndex(0) }, [query])

  const executeSelected = useCallback(() => {
    const cmd = filtered[selectedIndex]
    if (cmd) {
      saveRecent(cmd.nameKey as string)
      cmd.action()
      onClose()
    }
  }, [filtered, selectedIndex, onClose])

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'j') {
      e.preventDefault()
      e.stopPropagation()
      setSelectedIndex(prev => Math.min(prev + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp' || e.key === 'k') {
      e.preventDefault()
      e.stopPropagation()
      setSelectedIndex(prev => Math.max(prev - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      executeSelected()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }, [filtered.length, executeSelected, onClose])

  useEffect(() => {
    // Capture keyboard events at the document level with capture phase
    // so we intercept before any other handlers
    document.addEventListener('keydown', handleKeyDown, true)
    return () => document.removeEventListener('keydown', handleKeyDown, true)
  }, [handleKeyDown])

  // Auto-scroll selected item into view
  useEffect(() => {
    const el = itemRefs.current[selectedIndex]
    if (el) el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [selectedIndex])

  // Split into recent and rest
  const recentKeys = useMemo(() => loadRecent(), [])
  const recentKeySet = useMemo(() => new Set(recentKeys), [recentKeys])
  const recentCmds = useMemo(() =>
    filtered.filter(c => recentKeySet.has(c.nameKey as string))
      .sort((a, b) => recentKeys.indexOf(a.nameKey as string) - recentKeys.indexOf(b.nameKey as string)),
    [filtered, recentKeySet, recentKeys])

  const categories = useMemo(() =>
    [...new Set(filtered.map(c => c.categoryKey))],
    [filtered])

  let runningIndex = -1

  return (
    <>
      {/* Backdrop — click outside to close */}
      <div
        onClick={onClose}
        style={{
          position: 'absolute', inset: 0, zIndex: 99,
          background: 'rgba(0,0,0,0.3)',
        }}
      />
      <div style={{
        position: 'absolute', top: '20%', left: '50%', transform: 'translateX(-50%)',
        width: 560, maxHeight: 420,
        background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
        border: '1px solid var(--color-border)',
        boxShadow: '0 16px 48px rgba(0,0,0,0.5)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden', zIndex: 100,
      }}>
        {/* Search */}
        <div style={{
          display: 'flex', alignItems: 'center',
          padding: '0 var(--spacing-lg)', height: 48,
          borderBottom: '1px solid var(--color-border)',
          gap: 10,
        }}>
          <Search size={14} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} />
          <input
            ref={inputRef}
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => { e.stopPropagation() }}
            placeholder={t('cmd.placeholder')}
            autoFocus
            style={{
              flex: 1, border: 'none', background: 'transparent',
              color: 'var(--text-primary)', outline: 'none', fontSize: 14,
            }}
          />
          <button onClick={onClose} style={{
            background: 'var(--color-surface)', border: 'none', borderRadius: 'var(--radius-sm)',
            padding: '2px 6px', color: 'var(--text-tertiary)', fontSize: 11, cursor: 'pointer',
          }}>ESC</button>
        </div>

        {/* Commands */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--spacing-sm) 0', background: 'var(--color-bg)' }}>
          {filtered.length === 0 && (
            <div style={{ padding: '24px', textAlign: 'center', color: 'var(--text-tertiary)', fontSize: 13 }}>
              {t('cmd.noResults')}
            </div>
          )}
          {/* Recently used section */}
          {recentCmds.length > 0 && (
            <div>
              <div style={{
                padding: '8px var(--spacing-lg) 4px',
                fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)',
                textTransform: 'uppercase', letterSpacing: '0.05em',
              }}>
                {t('cmd.recent')}
              </div>
              {recentCmds.map((cmd) => {
                runningIndex++
                const idx = runningIndex
                const isSelected = idx === selectedIndex
                const Icon = cmd.icon
                return (
                  <div
                    key={'recent-' + (cmd.nameKey as string)}
                    ref={el => { itemRefs.current[idx] = el }}
                    onClick={() => { saveRecent(cmd.nameKey as string); cmd.action(); onClose() }}
                    onMouseEnter={() => setSelectedIndex(idx)}
                    style={{
                      display: 'flex', alignItems: 'center',
                      padding: '7px var(--spacing-lg)',
                      background: isSelected ? 'rgba(128,128,128,0.12)' : 'transparent',
                      cursor: 'pointer',
                      transition: 'background 0.08s',
                    }}
                  >
                    {Icon && <Icon size={14} style={{ marginRight: 8, color: 'var(--text-tertiary)', flexShrink: 0 }} />}
                    <span style={{
                      fontSize: 13,
                      color: isSelected ? 'var(--text-primary)' : 'var(--text-secondary)',
                      fontWeight: isSelected ? 500 : 400,
                    }}>{t(cmd.nameKey)}</span>
                    <div style={{ flex: 1 }} />
                    {cmd.shortcut && (
                      <span style={{
                        fontSize: 11, color: 'var(--text-tertiary)',
                        padding: '2px 6px', borderRadius: 'var(--radius-sm)',
                        background: 'var(--color-surface)',
                      }}>{cmd.shortcut}</span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
          {categories.map(catKey => (
            <div key={catKey}>
              <div style={{
                padding: '8px var(--spacing-lg) 4px',
                fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)',
                textTransform: 'uppercase', letterSpacing: '0.05em',
              }}>
                {t(catKey)}
              </div>
              {filtered.filter(c => c.categoryKey === catKey).map((cmd) => {
                runningIndex++
                const idx = runningIndex
                const isSelected = idx === selectedIndex
                const Icon = cmd.icon
                return (
                  <div
                    key={cmd.nameKey as string}
                    ref={el => { itemRefs.current[idx] = el }}
                    onClick={() => { saveRecent(cmd.nameKey as string); cmd.action(); onClose() }}
                    onMouseEnter={() => setSelectedIndex(idx)}
                    style={{
                      display: 'flex', alignItems: 'center',
                      padding: '7px var(--spacing-lg)',
                      background: isSelected ? 'rgba(128,128,128,0.12)' : 'transparent',
                      cursor: 'pointer',
                      transition: 'background 0.08s',
                    }}
                  >
                    {Icon && <Icon size={14} style={{ marginRight: 8, color: 'var(--text-tertiary)', flexShrink: 0 }} />}
                    <span style={{
                      fontSize: 13,
                      color: isSelected ? 'var(--text-primary)' : 'var(--text-secondary)',
                      fontWeight: isSelected ? 500 : 400,
                    }}>{t(cmd.nameKey)}</span>
                    <div style={{ flex: 1 }} />
                    {cmd.shortcut && (
                      <span style={{
                        fontSize: 11, color: 'var(--text-tertiary)',
                        padding: '2px 6px', borderRadius: 'var(--radius-sm)',
                        background: 'var(--color-surface)',
                      }}>{cmd.shortcut}</span>
                    )}
                  </div>
                )
              })}
            </div>
          ))}
        </div>

        {/* Footer hint */}
        <div style={{
          padding: '6px var(--spacing-lg)',
          borderTop: '1px solid var(--color-border)',
          display: 'flex', gap: 12, alignItems: 'center',
          fontSize: 11, color: 'var(--text-tertiary)',
        }}>
          <span>↑↓ navigate</span>
          <span>↵ select</span>
          <span>esc close</span>
          <div style={{ flex: 1 }} />
          <span>{filtered.length} results</span>
        </div>
      </div>
    </>
  )
}
