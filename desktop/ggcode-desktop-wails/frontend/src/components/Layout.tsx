import React, { useState, useEffect, useCallback, useMemo } from 'react'
import { ViewMode, StatusBarData } from '../types'
import { I18nProvider, useTranslation, type Locale } from '../i18n'
import { NavRail } from './NavRail'
import { Sidebar } from './Sidebar'
import { ChatView } from './ChatView'
import { SettingsPage } from './SettingsPage'
import DebugConsole from './DebugConsole'
import { IMManagement } from './IMManagement'
import { FileBrowser } from './FileBrowser'
import { MCPServers } from './MCPServers'
import { LanChatView } from './LanChatView'
import { ContextPanel } from './ContextPanel'
import { CommandPalette, type CommandAction } from './CommandPalette'
import RealShareDialog from './ShareDialog'
import { AboutDialog, UpdateNotification } from './Dialogs'
import { StatusBar } from './StatusBar'
import { Onboarding } from './Onboarding'
import { TopDragBar } from './TopDragBar'
import { ApprovalDialog, ApprovalRequest } from './ApprovalDialog'
import { AskUserDialog, AskUserRequest } from './AskUserDialog'
import { PairingCodeDialog, PairingRequest } from './PairingCodeDialog'
import { LanChatApprovalDialog } from './LanChatApprovalDialog'
import { Toast, ToastMessage, ToastType } from './Toast'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'

// Inner layout that uses useTranslation (must be inside I18nProvider)
function LayoutInner() {
  const [view, setView] = useState<ViewMode>('chat')
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [contextPanelOpen, setContextPanelOpen] = useState(false)
  const [cmdPaletteOpen, setCmdPaletteOpen] = useState(false)
  const [shareDialogOpen, setShareDialogOpen] = useState(false)
  const [aboutDialogOpen, setAboutDialogOpen] = useState(false)
  const [updateNotifOpen, setUpdateNotifOpen] = useState(false)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [lanChatUnread, setLanChatUnread] = useState(0)
  const [askUserRequest, setAskUserRequest] = useState<AskUserRequest | null>(null)
  const [pairingRequest, setPairingRequest] = useState<PairingRequest | null>(null)
  const [activeSessionId, setActiveSessionId] = useState<string | undefined>()
  const [needsOnboard, setNeedsOnboard] = useState(false)
  const [currentWorkspace, setCurrentWorkspace] = useState('')
  const [toast, setToast] = useState<ToastMessage | null>(null)

  const showToast = useCallback((type: ToastType, message: string) => {
    setToast({ id: Date.now(), type, message })
  }, [])

  const dismissToast = useCallback(() => {
    setToast(null)
  }, [])

  useEffect(() => {
    setToast(null)
  }, [view])

  // Shared status bar data
  const [statusBarData, setStatusBarData] = useState<StatusBarData>({
    vendor: '...',
    model: '...',
    mode: 'auto',
    contextUsed: 0,
    contextTotal: 0,
    usagePercent: 0,
    remainingPercent: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cacheHit: 0,
    status: 'Ready',
  })

  const { setLocale } = useTranslation()

  // Load initial config for shared state
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const cfg = await App.GetConfig()
        const [dir, info] = await Promise.all([App.GetWorkDir(), App.GetModelInfo()])
        if (cancelled) return
        setNeedsOnboard(cfg.needsSetup || false)
        setCurrentWorkspace(dir || cfg.workDir || '')
        // Initialize locale from saved language preference
        if (cfg.language === 'zh' || cfg.language === 'zh-CN') {
          setLocale('zh')
        }
        if (!cfg.needsSetup) {
          setStatusBarData(prev => ({
            ...prev,
            vendor: (info as any)?.vendor || cfg.vendor || prev.vendor,
            model: (info as any)?.model || cfg.model || prev.model,
            mode: (info as any)?.mode || prev.mode,
            contextUsed: (info as any)?.contextUsed ?? prev.contextUsed,
            contextTotal: (info as any)?.contextTotal ?? prev.contextTotal,
            usagePercent: (info as any)?.usagePercent ?? prev.usagePercent,
            remainingPercent: (info as any)?.remainingPercent ?? prev.remainingPercent,
            inputTokens: (info as any)?.inputTokens ?? prev.inputTokens,
            outputTokens: (info as any)?.outputTokens ?? prev.outputTokens,
            cacheRead: (info as any)?.cacheRead ?? prev.cacheRead,
            cacheWrite: (info as any)?.cacheWrite ?? prev.cacheWrite,
            cacheHit: (info as any)?.cacheHit ?? prev.cacheHit,
          }))
        }
      } catch {
        // Config not available yet
      }
    }
    load()
    return () => { cancelled = true }
  }, [setLocale])

  // Sync active session ID on mount — the backend may have auto-loaded
  // a session in EnsureSession before this React component mounted, so
  // the session:changed event was missed. Retry briefly in case the
  // backend hasn't finished initializing.
  useEffect(() => {
    let cancelled = false
    let attempts = 0
    const check = () => {
      if (cancelled || attempts > 10) return
      attempts++
      App.GetCurrentSessionID().then((id: any) => {
        if (cancelled) return
        const sid = typeof id === 'string' ? id : (id as any)?.toString?.() || ''
        if (sid) {
          setActiveSessionId(sid)
        } else {
          setTimeout(check, 100)
        }
      }).catch(() => setTimeout(check, 100))
    }
    check()
    return () => { cancelled = true }
  }, [])

  // Refresh status bar when config changes (e.g. after settings save)
  useEffect(() => {
    const refreshConfig = () => {
      Promise.all([App.GetConfig(), App.GetWorkDir()]).then(([cfg, dir]) => {
        setCurrentWorkspace((dir as string) || cfg.workDir || '')
        setStatusBarData(prev => ({
          ...prev,
          vendor: cfg.vendor || prev.vendor,
          model: cfg.model || prev.model,
          mode: cfg.defaultMode || prev.mode,
        }))
      }).catch(() => {})
    }
    const off = EventsOn('config:updated', refreshConfig)
    return () => { if (typeof off === 'function') off() }
  }, [])

  useEffect(() => {
    const off = EventsOn('workspace:changed', (event: any) => {
      const dir = event?.workDir || ''
      setCurrentWorkspace(dir)
      setActiveSessionId(undefined)
      setContextPanelOpen(false)
      setShareDialogOpen(false)
      Promise.all([App.GetConfig(), App.GetModelInfo()]).then(([cfg, info]) => {
        setNeedsOnboard(cfg.needsSetup || false)
        setStatusBarData(prev => ({
          ...prev,
          vendor: (info as any)?.vendor || cfg.vendor || prev.vendor,
          model: (info as any)?.model || cfg.model || prev.model,
        }))
      }).catch(() => {})
    })
    return () => { if (typeof off === 'function') off() }
  }, [])

  // Listen for chat:stream events to update shared status
  useEffect(() => {
    let cancelled = false
    const refresh = async () => {
      try {
        const [info, working] = await Promise.all([App.GetModelInfo(), App.IsWorking()])
        if (cancelled || !info) return
        setStatusBarData(prev => ({
          ...prev,
          vendor: (info as any).vendor ?? prev.vendor,
          model: (info as any).model ?? prev.model,
          mode: (info as any).mode ?? prev.mode,
          contextUsed: (info as any).contextUsed ?? prev.contextUsed,
          contextTotal: (info as any).contextTotal ?? prev.contextTotal,
          usagePercent: (info as any).usagePercent ?? prev.usagePercent,
          remainingPercent: (info as any).remainingPercent ?? prev.remainingPercent,
          inputTokens: (info as any).inputTokens ?? prev.inputTokens,
          outputTokens: (info as any).outputTokens ?? prev.outputTokens,
          cacheRead: (info as any).cacheRead ?? prev.cacheRead,
          cacheWrite: (info as any).cacheWrite ?? prev.cacheWrite,
          cacheHit: (info as any).cacheHit ?? prev.cacheHit,
          status: working ? 'Working' : 'Ready',
        }))
      } catch {}
    }
    void refresh()
    const id = window.setInterval(() => { void refresh() }, 2000)
    return () => { cancelled = true; window.clearInterval(id) }
  }, [])

  // Listen for approval and ask_user events from Go backend
  useEffect(() => {
    const offApprovalRequest = EventsOn('approval:request', (data: any) => {
      setApprovalRequest(data as ApprovalRequest)
    })
    const offAskUserRequest = EventsOn('ask_user:request', (data: any) => {
      setAskUserRequest(data as AskUserRequest)
    })
    const offPairing = EventsOn('im:pairing', (data: any) => {
      setPairingRequest(data as PairingRequest)
    })
    const offPairingDone = EventsOn('im:pairing_done', () => {
      setPairingRequest(null)
    })
    // Cancel events close any open dialogs
    const offApprovalCancel = EventsOn('approval:cancel', () => {
      setApprovalRequest(null)
    })
    const offAskUserCancel = EventsOn('ask_user:cancel', () => {
      setAskUserRequest(null)
    })
    const offTunnelSessionChanged = EventsOn('tunnel:session_changed', (data: any) => {
      showToast('info', data?.message || 'Mobile sharing stopped. Scan again to reconnect.')
    })
    // Backend loaded a session on startup (or via EnsureSession) — sync frontend state
    const offSessionChanged = EventsOn('session:changed', (data: any) => {
      const sid = data?.sessionId
      if (sid && sid !== activeSessionId) {
        setActiveSessionId(sid)
      }
    })
    return () => {
      for (const off of [offApprovalRequest, offAskUserRequest, offPairing, offPairingDone, offApprovalCancel, offAskUserCancel, offTunnelSessionChanged, offSessionChanged]) {
        if (typeof off === 'function') off()
      }
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
  const handleWorkspaceSelected = useCallback((dir: string) => {
    setCurrentWorkspace(dir)
    setActiveSessionId(undefined)
    setContextPanelOpen(false)
    setShareDialogOpen(false)
  }, [])

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
          <TopDragBar subtitle={currentWorkspace ? currentWorkspace.split('/').filter(Boolean).slice(-2).join(' / ') : undefined} />

          {/* Main body: NavRail + Sidebar + Content + ContextPanel */}
          <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
            <NavRail view={view} onViewChange={setView} onAbout={() => setAboutDialogOpen(true)} lanChatUnread={view === 'lanchat' ? 0 : lanChatUnread} sidebarOpen={sidebarOpen} onToggleSidebar={() => setSidebarOpen(!sidebarOpen)} />

            {sidebarOpen && view === 'chat' && (
              <Sidebar key={currentWorkspace || 'default-workspace'} workspace={currentWorkspace} onClose={() => setSidebarOpen(false)} activeSessionId={activeSessionId} onSessionSelect={setActiveSessionId} onShare={() => setShareDialogOpen(true)} showToast={showToast} />
            )}

            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, position: 'relative' }}>
              {/* ChatView always mounted — hidden via display:none to preserve state */}
              <div style={{ display: view === 'chat' ? 'flex' : 'none', flex: 1, flexDirection: 'column', minWidth: 0, overflow: 'hidden', height: 0 }}>
                <ChatView key={currentWorkspace || 'default-workspace'} workspace={currentWorkspace} sessionId={activeSessionId} onWorkspaceSelected={handleWorkspaceSelected} onShare={() => setShareDialogOpen(true)} showToast={showToast} />
              </div>
              {view === 'settings' && <SettingsPage onBack={backToChat} onNavigate={setView} onOpenContext={() => { setView('chat'); setContextPanelOpen(true) }} onOpenShare={() => setShareDialogOpen(true)} onOpenAbout={() => setAboutDialogOpen(true)} showToast={showToast} />}
              {view === 'debug' && <DebugConsole />}
              {view === 'im' && <IMManagement />}
              {view === 'files' && <FileBrowser onBack={backToChat} />}
              {view === 'mcp' && <MCPServers onBack={backToChat} />}
              {view === 'lanchat' && <LanChatView onUnreadChange={setLanChatUnread} />}
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
      {cmdPaletteOpen && (
        <CommandPalette
          onClose={() => setCmdPaletteOpen(false)}
          actions={useMemo<CommandAction[]>(() => [
            { nameKey: 'cmd.newSession', shortcut: '⌘N', categoryKey: 'cmd.cat.session', action: () => { App.NewSession().then((id: any) => { if (typeof id === 'string') setActiveSessionId(id) }).catch(() => {}) } },
            { nameKey: 'cmd.searchSessions', shortcut: '⌘⇧F', categoryKey: 'cmd.cat.session', action: () => { setView('chat'); setSidebarOpen(true) } },
            { nameKey: 'cmd.shareSession', shortcut: '⌘⇧S', categoryKey: 'cmd.cat.chat', action: () => setShareDialogOpen(true) },
            { nameKey: 'cmd.toggleContext', shortcut: '⌘.', categoryKey: 'cmd.cat.chat', action: () => setContextPanelOpen(prev => !prev) },
            { nameKey: 'cmd.toggleTheme', shortcut: '⌘⇧T', categoryKey: 'cmd.cat.settings', action: () => { document.documentElement.classList.toggle('dark') } },
            { nameKey: 'cmd.openSettings', shortcut: '⌘,', categoryKey: 'cmd.cat.settings', action: () => setView('settings') },
            { nameKey: 'cmd.switchModel', categoryKey: 'cmd.cat.settings', action: () => setView('settings') },
            { nameKey: 'cmd.toggleSidebar', shortcut: '⌘B', categoryKey: 'cmd.cat.navigation', action: () => setSidebarOpen(prev => !prev) },
          ], [setView, setSidebarOpen, setContextPanelOpen, setShareDialogOpen])}
        />
      )}
      {shareDialogOpen && <RealShareDialog onClose={() => setShareDialogOpen(false)} />}
      {aboutDialogOpen && <AboutDialog onClose={() => setAboutDialogOpen(false)} />}
      {updateNotifOpen && <UpdateNotification onClose={() => setUpdateNotifOpen(false)} />}
      {approvalRequest && <ApprovalDialog request={approvalRequest} onClose={() => setApprovalRequest(null)} />}
      {askUserRequest && <AskUserDialog request={askUserRequest} onClose={() => setAskUserRequest(null)} />}
      {pairingRequest && <PairingCodeDialog request={pairingRequest} onClose={() => setPairingRequest(null)} />}
      <LanChatApprovalDialog />
      <Toast toast={toast} onClose={dismissToast} />
    </div>
  )
}

// Top-level Layout wraps everything in I18nProvider
export function Layout() {
  const handleLocaleChange = useCallback(async (locale: Locale) => {
    try {
      await App.UpdateConfig({ language: locale })
    } catch {
      // ignore save errors during locale switch
    }
  }, [])

  return (
    <I18nProvider initialLocale="en" onLocaleChange={handleLocaleChange}>
      <LayoutInner />
    </I18nProvider>
  )
}
