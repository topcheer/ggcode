import React from 'react'
import { MessageSquare, FolderOpen, Settings, User } from 'lucide-react'
import { ViewMode } from '../types'

interface Props {
  view: ViewMode
  onViewChange: (v: ViewMode) => void
}

const navItems: { id: ViewMode; icon: React.ReactNode }[] = [
  { id: 'chat', icon: <MessageSquare size={18} /> },
  { id: 'files', icon: <FolderOpen size={18} /> },
  { id: 'settings', icon: <Settings size={18} /> },
]

export function NavRail({ view, onViewChange }: Props) {
  return (
    <div style={{
      width: 'var(--nav-rail-width)',
      height: '100%',
      background: 'var(--color-nav)',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      padding: '12px 0',
      gap: 4,
      flexShrink: 0,
    }}>
      {/* Logo */}
      <div style={{
        width: 32, height: 32, borderRadius: 8,
        background: 'var(--color-primary)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: '#fff', fontWeight: 700, fontSize: 14,
        marginBottom: 8,
      }}>
        G
      </div>

      {/* Nav items */}
      {navItems.map(item => (
        <button
          key={item.id}
          onClick={() => onViewChange(item.id)}
          style={{
            width: 36, height: 36, borderRadius: 6,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            background: view === item.id ? 'var(--color-primary)' : 'transparent',
            color: view === item.id ? '#fff' : 'var(--text-secondary)',
            border: 'none', cursor: 'pointer',
            transition: 'background 0.15s',
          }}
        >
          {item.icon}
        </button>
      ))}

      {/* Spacer */}
      <div style={{ flex: 1 }} />

      {/* User avatar */}
      <div style={{
        width: 32, height: 32, borderRadius: 16,
        background: '#6E40C9',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: '#fff', fontSize: 12, fontWeight: 600,
      }}>
        <User size={16} />
      </div>
    </div>
  )
}
