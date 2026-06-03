import React, { useState, useEffect } from 'react'
import { ViewMode, StatusBarData } from '../types'
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
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'

export function Layout() {
  const [view, setView] = useState<ViewMode>('chat')
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [contextPanelOpen, setContextPanelOpen] = useState(false)
  const [cmdPaletteOpen, setCmdPaletteOpen] = useState(false)
  const [shareDialogOpen, setShareDialogOpen] = useState(false)
  const [aboutDialogOpen, setAboutDialogOpen] = useState(false)
  const [updateNotifOpen, setUpdateNotifOpen] = useState(false)
  const [activeSessionId, setActiveSessionId] = useState<string | undefined>()

  // Shared status bar data
  const [statusBarData, setStatusBarData] = useState<StatusBarData>({
    vendor: '...',
    model: '...',
    contextUsed: 0,
    contextTotal: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheHit: 0,
    status: 'Ready',
  })

  // Load initial config for shared state
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const cfg = await App.GetConfig()
        if (cancelled) return
        setStatusBarData(prev => ({
          ...prev,
          vendor: cfg.vendor || prev.vendor,
          model: cfg.model || prev.model,
        }))
      } catch {
        // Config not available yet
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  // Listen for chat:stream events to update shared status
  useEffect(() => {
    const off = EventsOn('chat:stream', (event: any) => {
      if (!event) return
      const { type, data } = event
      // data is a JSON string from Go
      let parsed: any = {}
      if (data) {
        try { parsed = JSON.parse(data) } catch { parsed = {} }
      }
      if (type === 'done') {
        setStatusBarData(prev => ({
          ...prev,
          inputTokens: parsed.inputTokens ?? prev.inputTokens,
          outputTokens: parsed.outputTokens ?? prev.outputTokens,
          contextUsed: parsed.contextUsed ?? prev.contextUsed,
          contextTotal: parsed.contextTotal ?? prev.contextTotal,
          cacheHit: parsed.cacheHit ?? prev.cacheHit,
          status: 'Ready',
        }))
      } else if (type === 'text') {
        setStatusBarData(prev => ({ ...prev, status: 'Streaming' }))
      } else if (type === 'error') {
        setStatusBarData(prev => ({ ...prev, status: 'Error' }))
      }
    })
    return () => { if (typeof off === 'function') off() }
  }, [])

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
    <div style={{ display: 'flex', width: '100%', height: '100%' }}>
      <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        {/* Nav Rail */}
        <NavRail view={view} onViewChange={setView} onAbout={() => setAboutDialogOpen(true)} />

        {/* Sidebar — only in chat view */}
        {sidebarOpen && view === 'chat' && (
          <Sidebar onClose={() => setSidebarOpen(false)} activeSessionId={activeSessionId} onSessionSelect={setActiveSessionId} />
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
          <ContextPanel
            onClose={() => setContextPanelOpen(false)}
            statusBarData={statusBarData}
          />
        )}
      </div>

      {/* Status bar */}
      <StatusBar
        onContextToggle={() => setContextPanelOpen(prev => !prev)}
        data={statusBarData}
      />

      {/* Overlay dialogs */}
      {cmdPaletteOpen && <CommandPalette onClose={() => setCmdPaletteOpen(false)} />}
      {shareDialogOpen && <ShareDialog onClose={() => setShareDialogOpen(false)} />}
      {aboutDialogOpen && <AboutDialog onClose={() => setAboutDialogOpen(false)} />}
      {updateNotifOpen && <UpdateNotification onClose={() => setUpdateNotifOpen(false)} />}
    </div>
  )
}
