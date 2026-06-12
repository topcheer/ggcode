import React, { useState } from 'react'
import { Search } from 'lucide-react'
import { useTranslation, type TranslationKey } from '../i18n'

interface CommandItem {
  nameKey: TranslationKey
  shortcut?: string
  categoryKey: TranslationKey
}

const commands: CommandItem[] = [
  { nameKey: 'cmd.newSession', shortcut: '⌘N', categoryKey: 'cmd.cat.session' },
  { nameKey: 'cmd.searchSessions', shortcut: '⌘⇧F', categoryKey: 'cmd.cat.session' },
  { nameKey: 'cmd.clearHistory', categoryKey: 'cmd.cat.session' },
  { nameKey: 'cmd.compactContext', categoryKey: 'cmd.cat.chat' },
  { nameKey: 'cmd.undoLast', shortcut: '⌘Z', categoryKey: 'cmd.cat.chat' },
  { nameKey: 'cmd.shareSession', shortcut: '⌘⇧S', categoryKey: 'cmd.cat.chat' },
  { nameKey: 'cmd.toggleContext', shortcut: '⌘.', categoryKey: 'cmd.cat.chat' },
  { nameKey: 'cmd.toggleTheme', shortcut: '⌘⇧T', categoryKey: 'cmd.cat.settings' },
  { nameKey: 'cmd.openSettings', shortcut: '⌘,', categoryKey: 'cmd.cat.settings' },
  { nameKey: 'cmd.switchModel', categoryKey: 'cmd.cat.settings' },
  { nameKey: 'cmd.toggleSidebar', shortcut: '⌘B', categoryKey: 'cmd.cat.navigation' },
  { nameKey: 'cmd.prevSession', shortcut: '⌘↑', categoryKey: 'cmd.cat.navigation' },
  { nameKey: 'cmd.nextSession', shortcut: '⌘↓', categoryKey: 'cmd.cat.navigation' },
]

export function CommandPalette({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')

  const filtered = commands.filter(c =>
    t(c.nameKey).toLowerCase().includes(query.toLowerCase()) ||
    t(c.categoryKey).toLowerCase().includes(query.toLowerCase())
  )

  const categories = [...new Set(filtered.map(c => c.categoryKey))]

  return (
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
          value={query}
          onChange={e => setQuery(e.target.value)}
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
        {categories.map(catKey => (
          <div key={catKey}>
            <div style={{ padding: '8px var(--spacing-lg) 4px', fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)' }}>
              {t(catKey)}
            </div>
            {filtered.filter(c => c.categoryKey === catKey).map((cmd, i) => (
              <div key={cmd.nameKey} style={{
                display: 'flex', alignItems: 'center',
                padding: '6px var(--spacing-lg)',
                background: i === 0 && !query ? 'var(--color-card)' : 'transparent',
                cursor: 'pointer',
              }}>
                <span style={{
                  fontSize: 13,
                  color: (i === 0 && !query) ? 'var(--text-primary)' : 'var(--text-secondary)',
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
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
