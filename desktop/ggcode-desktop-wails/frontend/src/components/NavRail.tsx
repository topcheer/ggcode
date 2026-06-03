import React from 'react'
import { MessageSquare, FolderOpen, Settings, User, Radio, Server } from 'lucide-react'
import { ViewMode } from '../types'

interface Props {
  view: ViewMode
  onViewChange: (v: ViewMode) => void
  onAbout: () => void
}

const navItems: { id: ViewMode; icon: React.ReactNode; tooltip: string }[] = [
  { id: 'chat', icon: <MessageSquare size={18} />, tooltip: 'Chat (⌘1)' },
  { id: 'files', icon: <FolderOpen size={18} />, tooltip: 'Files (⌘2)' },
  { id: 'im', icon: <Radio size={18} />, tooltip: 'IM Adapters' },
  { id: 'mcp', icon: <Server size={18} />, tooltip: 'MCP Servers' },
  { id: 'settings', icon: <Settings size={18} />, tooltip: 'Settings (⌘,)' },
]

export function NavRail({ view, onViewChange, onAbout }: Props) {
  return (
    <div style={{
      width: 'var(--nav-rail-width)',
      height: '100%',
      background: 'var(--color-nav)',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      padding: '52px 0 12px 0',
      gap: 4,
      flexShrink: 0,
    }}>
      {/* Logo */}
      <button onClick={onAbout} style={{
        width: 32, height: 32, borderRadius: 8,
        background: 'var(--color-primary)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: '#fff', fontWeight: 700, fontSize: 14,
        marginBottom: 8, border: 'none', cursor: 'pointer',
      }}>
        G
      </button>

      {/* Nav items */}
      {navItems.map(item => (
        <button
          key={item.id}
          onClick={() => onViewChange(item.id)}
          title={item.tooltip}
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
