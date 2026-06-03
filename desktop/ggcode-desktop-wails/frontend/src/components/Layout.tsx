import React, { useState } from 'react'
import { ViewMode } from '../types'
import { NavRail } from './NavRail'
import { Sidebar } from './Sidebar'
import { ChatView } from './ChatView'
import { SettingsPage } from './SettingsPage'
import { StatusBar } from './StatusBar'

export function Layout() {
  const [view, setView] = useState<ViewMode>('chat')
  const [sidebarOpen, setSidebarOpen] = useState(true)

  return (
    <div style={{ display: 'flex', width: '100%', height: '100%', flexDirection: 'column' }}>
      {/* macOS titlebar drag region */}
      <div className="titlebar-drag" style={{ flexShrink: 0 }} />
      
      <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        {/* Nav Rail */}
        <NavRail view={view} onViewChange={setView} />
        
        {/* Sidebar (session list) — only in chat view */}
        {sidebarOpen && view === 'chat' && (
          <Sidebar onClose={() => setSidebarOpen(false)} />
        )}
        
        {/* Main content */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
          {view === 'chat' && <ChatView />}
          {view === 'settings' && <SettingsPage onBack={() => setView('chat')} />}
        </div>
      </div>
      
      {/* Status bar */}
      <StatusBar />
    </div>
  )
}
