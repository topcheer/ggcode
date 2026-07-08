import React from 'react'
import { MessageSquare, FolderOpen, Settings, Radio, Server, Terminal, PanelLeft, Users } from 'lucide-react'
import { ViewMode } from '../types'
import { useTranslation } from '../i18n'

interface Props {
  view: ViewMode
  onViewChange: (v: ViewMode) => void
  onAbout: () => void
  lanChatUnread?: number
  sidebarOpen?: boolean
  onToggleSidebar?: () => void
}

function NavItems() {
  const { t } = useTranslation()
  const shortcuts = ['⌘1', '⌘2', '⌘3', '⌘4', '⌘5', '⌘6']
  const items = [
    { id: 'chat' as ViewMode, icon: <MessageSquare size={18} />, tooltip: t('nav.chat') },
    { id: 'files' as ViewMode, icon: <FolderOpen size={18} />, tooltip: t('nav.files') },
    { id: 'im' as ViewMode, icon: <Radio size={18} />, tooltip: t('nav.im') },
    { id: 'mcp' as ViewMode, icon: <Server size={18} />, tooltip: t('nav.mcp') },
    { id: 'settings' as ViewMode, icon: <Settings size={18} />, tooltip: t('nav.settings') },
    { id: 'debug' as ViewMode, icon: <Terminal size={18} />, tooltip: 'Debug Console' },
  ]
  return items.map((item, i) => ({
    ...item,
    tooltip: `${item.tooltip} (${shortcuts[i]})`,
  }))
}

export function NavRail({ view, onViewChange, onAbout, lanChatUnread = 0, sidebarOpen, onToggleSidebar }: Props) {
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
        width: 32, height: 32,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        marginBottom: 8, border: 'none', cursor: 'pointer',
        padding: 0, overflow: 'hidden', background: 'transparent',
      }}>
        <img src={new URL('../assets/images/app-icon.png', import.meta.url).href}
          alt="GGCode"
          style={{ width: 32, height: 32 }}
        />
      </button>

      {/* Sidebar toggle */}
      <button onClick={onToggleSidebar} title={sidebarOpen ? `Hide sessions (⌘B)` : `Show sessions (⌘B)`} style={{
        width: 36, height: 36, borderRadius: 6,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'transparent',
        color: sidebarOpen ? 'var(--color-primary)' : 'var(--text-secondary)',
        border: 'none', cursor: 'pointer',
        transition: 'background 0.15s',
        marginBottom: 4,
      }}>
        <PanelLeft size={18} />
      </button>

      {/* Nav items */}
      {navItems.map(item => {
        const isActive = view === item.id
        return (
          <button
            key={item.id}
            onClick={() => onViewChange(item.id)}
            title={item.tooltip}
            className="nav-rail-btn"
            style={{
              width: 36, height: 36, borderRadius: 6,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              background: isActive ? 'var(--color-primary)' : 'transparent',
              color: isActive ? '#fff' : 'var(--text-secondary)',
              border: 'none', cursor: 'pointer',
              transition: 'background 0.15s, color 0.15s',
            }}
            onMouseEnter={e => {
              if (!isActive) {
                e.currentTarget.style.background = 'var(--color-nav-hover, rgba(255,255,255,0.08))'
                e.currentTarget.style.color = 'var(--text-primary)'
              }
            }}
            onMouseLeave={e => {
              if (!isActive) {
                e.currentTarget.style.background = 'transparent'
                e.currentTarget.style.color = 'var(--text-secondary)'
              }
            }}
          >
            {item.icon}
          </button>
        )
      })}

      {/* LAN Chat */}
      <button
        onClick={() => onViewChange('lanchat')}
        title={`LAN Chat (⌘7)`}
        style={{
          width: 'var(--nav-rail-width)',
          height: 'var(--nav-rail-width)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: view === 'lanchat' ? 'var(--color-nav-active)' : 'transparent',
          color: view === 'lanchat' ? 'var(--color-primary)' : 'var(--text-secondary)',
          border: 'none', cursor: 'pointer',
          transition: 'background 0.15s, color 0.15s',
          position: 'relative',
        }}
        onMouseEnter={e => {
          if (view !== 'lanchat') {
            e.currentTarget.style.background = 'var(--color-nav-hover, rgba(255,255,255,0.08))'
            e.currentTarget.style.color = 'var(--text-primary)'
          }
        }}
        onMouseLeave={e => {
          if (view !== 'lanchat') {
            e.currentTarget.style.background = 'transparent'
            e.currentTarget.style.color = 'var(--text-secondary)'
          }
        }}
      >
        <Users size={18} />
        {lanChatUnread > 0 && (
          <span style={{
            position: 'absolute', top: 4, right: 4,
            background: '#ef4444', color: '#fff',
            fontSize: 10, fontWeight: 700,
            minWidth: 16, height: 16,
            borderRadius: 8,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            padding: '0 4px',
          }}>
            {lanChatUnread > 99 ? '99+' : lanChatUnread}
          </span>
        )}
      </button>

      {/* Spacer */}
      <div style={{ flex: 1 }} />
    </div>
  )
}
