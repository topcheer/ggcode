import React, { useState, useEffect } from 'react'
import { ViewMode } from '../types'
import { NavRail } from './NavRail'
import { Sidebar } from './Sidebar'
import { ChatView } from './ChatView'
import { SettingsPage } from './SettingsPage'
import { IMManagement } from './IMManagement'
import { FileBrowser } from './FileBrowser'
import { MCPServers } from './MCPServers'
import { ContextPanel } from './ContextPanel'
import { CommandPalette } from './CommandPalette'
import { ShareDialog, AboutDialog, UpdateNotification } from './Dialogs'
import { StatusBar } from './StatusBar'

export function Layout() {
  const [view, setView] = useState<ViewMode>('chat')
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [contextPanelOpen, setContextPanelOpen] = useState(false)
  const [cmdPaletteOpen, setCmdPaletteOpen] = useState(false)
  const [shareDialogOpen, setShareDialogOpen] = useState(false)
  const [aboutDialogOpen, setAboutDialogOpen] = useState(false)
  const [updateNotifOpen, setUpdateNotifOpen] = useState(false)

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // Cmd+K → Command Palette
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setCmdPaletteOpen(prev => !prev)
      }
      // Cmd+. → Context Panel
      if ((e.metaKey || e.ctrlKey) && e.key === '.') {
        e.preventDefault()
        setContextPanelOpen(prev => !prev)
      }
      // Cmd+B → Sidebar
      if ((e.metaKey || e.ctrlKey) && e.key === 'b') {
        e.preventDefault()
        setSidebarOpen(prev => !prev)
      }
      // Escape → close overlays
      if (e.key === 'Escape') {
        setCmdPaletteOpen(false)
        setShareDialogOpen(false)
        setAboutDialogOpen(false)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  const backToChat = () => setView('chat')

  return (
    <div style={{ position: 'relative', display: 'flex', width: '100%', height: '100%', flexDirection: 'column' }}>
      {/* macOS titlebar drag region */}
      <div className="titlebar-drag" style={{ flexShrink: 0 }} />

      <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        {/* Nav Rail */}
        <NavRail view={view} onViewChange={setView} onAbout={() => setAboutDialogOpen(true)} />

        {/* Sidebar — only in chat view */}
        {sidebarOpen && view === 'chat' && (
          <Sidebar onClose={() => setSidebarOpen(false)} />
        )}

        {/* Main content */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, position: 'relative' }}>
          {view === 'chat' && <ChatView onShare={() => setShareDialogOpen(true)} />}
          {view === 'settings' && <SettingsPage onBack={backToChat} />}
          {view === 'im' && <IMManagement onBack={backToChat} />}
          {view === 'files' && <FileBrowser onBack={backToChat} />}
          {view === 'mcp' && <MCPServers onBack={backToChat} />}
        </div>

        {/* Context Panel — right drawer */}
        {contextPanelOpen && view === 'chat' && (
          <ContextPanel onClose={() => setContextPanelOpen(false)} />
        )}
      </div>

      {/* Status bar */}
      <StatusBar onContextToggle={() => setContextPanelOpen(prev => !prev)} />

      {/* Overlay dialogs */}
      {cmdPaletteOpen && <CommandPalette onClose={() => setCmdPaletteOpen(false)} />}
      {shareDialogOpen && <ShareDialog onClose={() => setShareDialogOpen(false)} />}
      {aboutDialogOpen && <AboutDialog onClose={() => setAboutDialogOpen(false)} />}
      {updateNotifOpen && <UpdateNotification onClose={() => setUpdateNotifOpen(false)} />}
    </div>
  )
}
