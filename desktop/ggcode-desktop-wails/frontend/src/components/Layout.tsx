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
import RealShareDialog from './ShareDialog'
import { AboutDialog, UpdateNotification } from './Dialogs'
import { StatusBar } from './StatusBar'
import { Onboarding } from './Onboarding'
import { TopDragBar } from './TopDragBar'
import { ApprovalDialog, ApprovalRequest } from './ApprovalDialog'
import { AskUserDialog, AskUserRequest } from './AskUserDialog'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'

export function Layout() {
  const [view, setView] = useState<ViewMode>('chat')
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [contextPanelOpen, setContextPanelOpen] = useState(false)
  const [cmdPaletteOpen, setCmdPaletteOpen] = useState(false)
  const [shareDialogOpen, setShareDialogOpen] = useState(false)
  const [aboutDialogOpen, setAboutDialogOpen] = useState(false)
  const [updateNotifOpen, setUpdateNotifOpen] = useState(false)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [askUserRequest, setAskUserRequest] = useState<AskUserRequest | null>(null)
  const [activeSessionId, setActiveSessionId] = useState<string | undefined>()
  const [needsOnboard, setNeedsOnboard] = useState(false)

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
        setNeedsOnboard(cfg.needsSetup || false)
        if (!cfg.needsSetup) {
          setStatusBarData(prev => ({
            ...prev,
            vendor: cfg.vendor || prev.vendor,
            model: cfg.model || prev.model,
          }))
        }
      } catch {
        // Config not available yet
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  // Refresh status bar when config changes (e.g. after settings save)
  useEffect(() => {
    const refreshConfig = () => {
      App.GetConfig().then(cfg => {
        setStatusBarData(prev => ({
          ...prev,
          vendor: cfg.vendor || prev.vendor,
          model: cfg.model || prev.model,
        }))
      }).catch(() => {})
    }
    EventsOn('config:updated', refreshConfig)
    return () => { EventsOff('config:updated') }
  }, [])

  // Listen for chat:stream events to update shared status
  useEffect(() => {
    const off = EventsOn('chat:stream', (event: any) => {
      if (!event) return
      const { type, data } = event
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

  // Listen for approval and ask_user events from Go backend
  useEffect(() => {
    EventsOn('approval:request', (data: any) => {
      setApprovalRequest(data as ApprovalRequest)
    })
    EventsOn('ask_user:request', (data: any) => {
      setAskUserRequest(data as AskUserRequest)
    })
    return () => {
      EventsOff('approval:request')
      EventsOff('ask_user:request')
    }
  }, [])

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setCmdPaletteOpen(prev => !prev)
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '.') {
        e.preventDefault()
        setContextPanelOpen(prev => !prev)
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 'b') {
        e.preventDefault()
        setSidebarOpen(prev => !prev)
      }
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
    <div style={{ display: 'flex', flexDirection: 'column', width: '100%', height: '100%' }}>
      {/* Onboarding flow */}
      {needsOnboard ? (
        <Onboarding onComplete={() => {
          setNeedsOnboard(false)
          App.GetConfig().then(cfg => {
            setStatusBarData(prev => ({
              ...prev,
              vendor: cfg.vendor || prev.vendor,
              model: cfg.model || prev.model,
            }))
          }).catch(() => {})
        }} />
      ) : (
        <>
          {/* Global titlebar drag — spans entire width */}
          <TopDragBar />

          {/* Main body: NavRail + Sidebar + Content + ContextPanel */}
          <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
            <NavRail view={view} onViewChange={setView} onAbout={() => setAboutDialogOpen(true)} />

            {sidebarOpen && view === 'chat' && (
              <Sidebar onClose={() => setSidebarOpen(false)} activeSessionId={activeSessionId} onSessionSelect={setActiveSessionId} onShare={() => setShareDialogOpen(true)} />
            )}

            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, position: 'relative' }}>
              {view === 'chat' && <ChatView sessionId={activeSessionId} onShare={() => setShareDialogOpen(true)} />}
              {view === 'settings' && <SettingsPage onBack={backToChat} />}
              {view === 'im' && <IMManagement />}
              {view === 'files' && <FileBrowser onBack={backToChat} />}
              {view === 'mcp' && <MCPServers onBack={backToChat} />}
            </div>

            {contextPanelOpen && view === 'chat' && (
              <ContextPanel
                onClose={() => setContextPanelOpen(false)}
                statusBarData={statusBarData}
              />
            )}
          </div>

          {/* Status bar at bottom */}
          <StatusBar
            onContextToggle={() => setContextPanelOpen(prev => !prev)}
            data={statusBarData}
          />
        </>
      )}

      {/* Overlay dialogs */}
      {cmdPaletteOpen && <CommandPalette onClose={() => setCmdPaletteOpen(false)} />}
      {shareDialogOpen && <RealShareDialog onClose={() => setShareDialogOpen(false)} />}
      {aboutDialogOpen && <AboutDialog onClose={() => setAboutDialogOpen(false)} />}
      {updateNotifOpen && <UpdateNotification onClose={() => setUpdateNotifOpen(false)} />}
      {approvalRequest && <ApprovalDialog request={approvalRequest} onClose={() => setApprovalRequest(null)} />}
      {askUserRequest && <AskUserDialog request={askUserRequest} onClose={() => setAskUserRequest(null)} />}
    </div>
  )
}
