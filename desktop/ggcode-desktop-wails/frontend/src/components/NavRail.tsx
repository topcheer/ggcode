import React from 'react'
import { MessageSquare, FolderOpen, Settings, User, Radio, Server, Terminal } from 'lucide-react'
import { ViewMode } from '../types'
import { useTranslation } from '../i18n'

interface Props {
  view: ViewMode
  onViewChange: (v: ViewMode) => void
  onAbout: () => void
}

function NavItems() {
  const { t } = useTranslation()
  return [
    { id: 'chat' as ViewMode, icon: <MessageSquare size={18} />, tooltip: t('nav.chat') },
    { id: 'files' as ViewMode, icon: <FolderOpen size={18} />, tooltip: t('nav.files') },
    { id: 'im' as ViewMode, icon: <Radio size={18} />, tooltip: t('nav.im') },
    { id: 'mcp' as ViewMode, icon: <Server size={18} />, tooltip: t('nav.mcp') },
    { id: 'settings' as ViewMode, icon: <Settings size={18} />, tooltip: t('nav.settings') },
    { id: 'debug' as ViewMode, icon: <Terminal size={18} />, tooltip: 'Debug Console' },
  ]
}

export function NavRail({ view, onViewChange, onAbout }: Props) {
  const navItems = NavItems()
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
