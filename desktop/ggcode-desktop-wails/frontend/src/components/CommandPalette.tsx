import React, { useState } from 'react'
import { Search } from 'lucide-react'

interface CommandItem {
  name: string
  shortcut?: string
  category: string
}

const commands: CommandItem[] = [
  { name: 'New Session', shortcut: '⌘N', category: 'Session' },
  { name: 'Search Sessions', shortcut: '⌘⇧F', category: 'Session' },
  { name: 'Clear History', category: 'Session' },
  { name: 'Compact Context', category: 'Chat' },
  { name: 'Undo Last', shortcut: '⌘Z', category: 'Chat' },
  { name: 'Share Session', shortcut: '⌘⇧S', category: 'Chat' },
  { name: 'Toggle Context Panel', shortcut: '⌘.', category: 'Chat' },
  { name: 'Toggle Theme', shortcut: '⌘⇧T', category: 'Settings' },
  { name: 'Open Settings', shortcut: '⌘,', category: 'Settings' },
  { name: 'Switch Model', category: 'Settings' },
  { name: 'Toggle Sidebar', shortcut: '⌘B', category: 'Navigation' },
  { name: 'Previous Session', shortcut: '⌘↑', category: 'Navigation' },
  { name: 'Next Session', shortcut: '⌘↓', category: 'Navigation' },
]

export function CommandPalette({ onClose }: { onClose: () => void }) {
  const [query, setQuery] = useState('')

  const filtered = commands.filter(c =>
    c.name.toLowerCase().includes(query.toLowerCase()) ||
    c.category.toLowerCase().includes(query.toLowerCase())
  )

  const categories = [...new Set(filtered.map(c => c.category))]

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
          placeholder="Type a command..."
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
        {categories.map(cat => (
          <div key={cat}>
            <div style={{ padding: '8px var(--spacing-lg) 4px', fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)' }}>
              {cat}
            </div>
            {filtered.filter(c => c.category === cat).map((cmd, i) => (
              <div key={cmd.name} style={{
                display: 'flex', alignItems: 'center',
                padding: '6px var(--spacing-lg)',
                background: i === 0 && !query ? 'var(--color-card)' : 'transparent',
                cursor: 'pointer',
              }}>
                <span style={{
                  fontSize: 13,
                  color: (i === 0 && !query) ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}>{cmd.name}</span>
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
