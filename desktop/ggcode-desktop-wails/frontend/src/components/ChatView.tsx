import React, { useState, useRef, useEffect, useCallback } from 'react'
import { ArrowUp, Square, Share2, ChevronDown, ChevronRight, ClipboardPaste, User } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { ClipboardGetText, EventsOn } from '../../wailsjs/runtime/runtime'
import { marked } from 'marked'
import { useTranslation } from '../i18n'
import { TeamBoard, type TeamBoardSnapshot } from './TeamBoard'
import { appendAssistantChunk, appendReasoningChunk, appendUserMessage, finishAssistantMessage, finishAssistantRun, parseStreamData } from './chatStreamState'

// Configure marked — no special mermaid handling, we split manually.
marked.setOptions({ gfm: true, breaks: true })

// Split content into segments: plain markdown, mermaid blocks, and svg blocks.
// During streaming, an unclosed block is held back until completed.
function splitContent(content: string): { type: 'markdown' | 'mermaid' | 'svg'; text: string }[] {
  const segments: { type: 'markdown' | 'mermaid' | 'svg'; text: string }[] = []
  let i = 0
  while (i < content.length) {
    // Look for the next code fence
    const fenceMatch = content.slice(i).match(/```(mermaid|svg)\n?/)
    if (!fenceMatch) {
      const rest = content.slice(i)
      if (rest) segments.push({ type: 'markdown', text: rest })
      break
    }
    const lang = fenceMatch[1] as 'mermaid' | 'svg'
    const absStart = i + fenceMatch.index!

    // Plain markdown before this fence
    const before = content.slice(i, absStart)
    if (before) segments.push({ type: 'markdown', text: before })

    // Find the opening newline after ```mermaid/svg
    const afterOpen = absStart + fenceMatch[0].length
    // Look for closing ```
    const closeFence = content.indexOf('\n```', afterOpen)
    if (closeFence === -1) {
      // Unclosed block — hold back, don't show
      break
    }
    const blockSrc = content.slice(afterOpen, closeFence)
    segments.push({ type: lang, text: blockSrc })
    i = closeFence + 4 // skip past \n```
  }
  return segments
}

// Safe markdown render with error protection
function safeMarkdown(text: string): string {
  if (!text) return ''
  try {
    return marked.parse(text) as string
  } catch {
    return text.replace(/</g, '&lt;').replace(/>/g, '&gt;')
  }
}

// Render message content: split into markdown + mermaid segments
function MessageContent({ content }: { content: string }) {
  const segments = splitContent(content)
  return (
    <>
      {segments.map((seg, i) => {
        if (seg.type === 'markdown') {
          return <div key={i} className="markdown-body" dangerouslySetInnerHTML={{ __html: safeMarkdown(seg.text) }} />
        }
        if (seg.type === 'svg') {
          return <div key={i} className="svg-rendered" dangerouslySetInnerHTML={{ __html: seg.text }} />
        }
        // Mermaid block — will be rendered by useEffect
        return (
          <pre key={i} className="mermaid-src">
            <code className="language-mermaid">{seg.text}</code>
          </pre>
        )
      })}
    </>
  )
}

// ── Types (mirrors Go ChatMessage from desktop/ggcode-desktop/types.go) ──────

type ChatRole = 'user' | 'assistant' | 'system' | 'tool' | 'reasoning' | 'error'

interface PastedImageAttachment {
  id: string
  mimeType: string
  data: string
  previewUrl: string
  name?: string
}

interface ChatMessage {
  id: string
  role: ChatRole
  content: string
  images?: PastedImageAttachment[]
  toolName?: string
  toolID?: string
  toolArgs?: string
  toolDisplayName?: string
  toolDetail?: string
  agentID?: string
  teammateName?: string
  isError?: boolean
  streaming?: boolean
  timestamp: number
  source?: string // 'im' | 'mobile' | 'lanchat' — non-UI message origin
  lanchatFrom?: string // parsed nick from [LAN Chat from xxx]:
  deliveryStatus?: 'pending' | 'sent' | 'failed'
}

/** Parse LAN Chat injection format:
 *  "New user guidance arrived...\n\n[LAN Chat from nick]: content"
 *  or "[LAN Chat from nick]: content"
 *  Returns the extracted nick and cleaned content (without the prefix).
 */
function parseLanChatContent(raw: string): { from: string; content: string } | null {
  const match = raw.match(/\[LAN Chat from (.+?)\]:\s*/m)
  if (!match) return null
  const from = match[1]
  let content = raw.slice(match.index! + match[0].length)
  return { from, content }
}

// Agent panel: a sub-agent or teammate has its own tab with its own message stream
interface AgentPanel {
  id: string
  name: string
  kind: 'subagent' | 'teammate'
  status: 'running' | 'completed' | 'failed' | 'idle'
  task: string
  messages: ChatMessage[]
  reasoningBuf: string
}

// ── Event types (mirrors wailskit/chat.go emit()) ────────────────────────────

interface StreamEvent {
  type: 'text' | 'tool_call_chunk' | 'tool_call_done' | 'tool_result' | 'done' | 'error' | 'reasoning' | 'reasoning_done' | 'run_done' | 'system'
    | 'subagent_text' | 'subagent_reasoning' | 'subagent_tool_call' | 'subagent_tool_result' | 'subagent_done'
    | 'swarm_text' | 'swarm_tool_call' | 'swarm_tool_result' | 'swarm_spawned' | 'swarm_idle' | 'swarm_board_updated' | 'usage_update' | 'user_message'
  data: unknown
}

interface TextPayload { content: string }
interface ToolCallPayload { id: string; name: string; arguments?: string; displayName?: string; detail?: string }
interface ToolResultPayload { id?: string; name: string; result: string; isError: boolean }
interface DonePayload { inputTokens?: number; outputTokens?: number }
interface ErrorPayload { message: string }
interface ReasoningPayload { content: string }

// ── Status bar state ─────────────────────────────────────────────────────────

interface StatusBarState {
  vendor: string
  model: string
  mode: string
  effort: string
  contextUsed: number
  contextTotal: number
  usagePercent: number
  remainingPercent: number
  inputTokens: number
  outputTokens: number
  cacheRead: number
  cacheWrite: number
  cacheHit: number
  status: string
}

// ── Helpers ──────────────────────────────────────────────────────────────────

let idCounter = 0
function nextID(): string {
  return `msg-${Date.now()}-${++idCounter}`
}

function parseJSON<T>(raw: unknown): T | null {
  return parseStreamData<T>(raw)
}

function formatTokens(n: number): string {
  if (n >= 1_000_000 && n % 1_000_000 === 0) return `${n / 1_000_000}m`
  if (n >= 1_000 && n % 1_000 === 0) return `${n / 1_000}k`
  return String(n)
}

function sameMessageShape(a: ChatMessage, b: ChatMessage): boolean {
  return a.role === b.role &&
    a.content === b.content &&
    a.toolName === b.toolName &&
    a.toolID === b.toolID &&
    a.toolArgs === b.toolArgs &&
    a.toolDisplayName === b.toolDisplayName &&
    a.toolDetail === b.toolDetail &&
    a.isError === b.isError &&
    a.streaming === b.streaming
}

function materializeHistory(history: any[], previous: ChatMessage[]): ChatMessage[] {
  const next = history.map((h: any, index: number) => {
    const candidate: ChatMessage = {
      id: h.id || h.ID || previous[index]?.id || nextID(),
      role: (h.role || 'assistant') as ChatRole,
      content: h.content || '',
      toolName: h.toolName,
      toolID: h.toolID,
      toolArgs: h.toolArgs,
      toolDisplayName: h.toolDisplayName,
      toolDetail: h.toolDetail,
      isError: h.isError,
      streaming: !!h.streaming,
      timestamp: previous[index]?.timestamp || Date.now(),
    }
    // Parse LAN Chat source from content for user messages loaded from history
    if (candidate.role === 'user' && typeof candidate.content === 'string') {
      const parsed = parseLanChatContent(candidate.content)
      if (parsed) {
        candidate.content = parsed.content
        candidate.source = 'lanchat'
        candidate.lanchatFrom = parsed.from
      }
    }
    const prev = previous[index]
    if (prev && sameMessageShape(prev, candidate)) {
      return prev
    }
    return candidate
  })
  if (next.length === previous.length && next.every((msg, index) => msg === previous[index])) {
    return previous
  }
  return next
}

function isNearBottom(el: HTMLElement | null, threshold = 48): boolean {
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold
}

function getTabAutoScroll(state: Record<string, boolean>, tab: string): boolean {
  return state[tab] ?? true
}

function imageFromClipboardAttachment(att: any): PastedImageAttachment | null {
  if (att?.kind !== 'image' || !att.data) return null
  const mimeType = att.mimeType || 'image/png'
  return {
    id: nextID(),
    mimeType,
    data: att.data,
    previewUrl: `data:${mimeType};base64,${att.data}`,
    name: att.name || 'clipboard-image',
  }
}

function formatTextAttachment(att: any): string {
  const name = att?.name || 'clipboard-file'
  const path = att?.path ? ` (${att.path})` : ''
  return `\n\n--- ${name}${path} ---\n${att.content || ''}\n--- end ${name} ---`
}

// ── Component ────────────────────────────────────────────────────────────────

export function ChatView({ onShare, sessionId, workspace, onWorkspaceSelected, showToast }: { onShare?: () => void; sessionId?: string; workspace?: string; onWorkspaceSelected?: (dir: string) => void; showToast?: (type: 'success' | 'error' | 'info', message: string) => void }) {
  const { t } = useTranslation()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [agentPanels, setAgentPanels] = useState<Map<string, AgentPanel>>(new Map())
  const [activeTab, setActiveTab] = useState<string>('main') // 'main' or agentID
  const [input, setInput] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [statusBar, setStatusBar] = useState<StatusBarState>({
    vendor: '', model: '', mode: 'auto', effort: 'auto', contextUsed: 0, contextTotal: 0, usagePercent: 0, remainingPercent: 0, inputTokens: 0, outputTokens: 0, cacheRead: 0, cacheWrite: 0, cacheHit: 0, status: 'ready',
  })
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [teamBoard, setTeamBoard] = useState<TeamBoardSnapshot[]>([])
  const [teamBoardOpen, setTeamBoardOpen] = useState(false)
  const teamBoardDismissedRef = useRef(false)

  // --- Identity (nick/role/team) ---
  const [selfNick, setSelfNick] = useState('')
  const [selfRole, setSelfRole] = useState('')
  const [selfTeam, setSelfTeam] = useState('')
  const [identityOpen, setIdentityOpen] = useState(false)
  const [editNick, setEditNick] = useState('')
  const [editRole, setEditRole] = useState('')
  const [editTeam, setEditTeam] = useState('')

  // Load identity on mount, and refresh when session changes
  useEffect(() => {
    let mounted = true
    const refresh = async () => {
      try {
        const s = await App.LanChatSelf() as any
        if (mounted && s) {
          setSelfNick(s.human_nick || '')
          setSelfRole(s.role || '')
          setSelfTeam(s.team || '')
        }
      } catch {}
    }
    refresh()
    const off = EventsOn('lanchat:identity_updated', refresh)
    return () => { mounted = false; off() }
  }, [])

  const refreshTeamBoard = useCallback(async () => {
    try {
      const getter = (App as unknown as { GetTeamBoard?: () => Promise<TeamBoardSnapshot[]> }).GetTeamBoard
      if (!getter) return
      const board = await getter()
      const teams = board || []
      setTeamBoard(teams)
      if (teams.length > 0 && !teamBoardDismissedRef.current) {
        setTeamBoardOpen(true)
      }
    } catch {
      // Team board is an auxiliary view; keep chat usable if the snapshot fails.
    }
  }, [])

  const closeTeamBoard = useCallback(() => {
    teamBoardDismissedRef.current = true
    setTeamBoardOpen(false)
  }, [])

  const openTeamBoard = useCallback(() => {
    teamBoardDismissedRef.current = false
    setTeamBoardOpen(true)
    void refreshTeamBoard()
  }, [refreshTeamBoard])

  const selectTeammatePanel = useCallback((teammateID: string) => {
    if (agentPanels.has(teammateID)) {
      setActiveTab(teammateID)
    }
  }, [agentPanels])

  // Helper: update an agent panel's messages
  const updateAgentPanel = useCallback((agentID: string, updater: (panel: AgentPanel) => AgentPanel) => {
    setAgentPanels(prev => {
      const next = new Map(prev)
      const panel = next.get(agentID)
      if (panel) {
        next.set(agentID, updater(panel))
      }
      return next
    })
  }, [])

  // Helper: ensure agent panel exists, create if not
  const ensureAgentPanel = useCallback((agentID: string, name: string, kind: 'subagent' | 'teammate') => {
    setAgentPanels(prev => {
      if (prev.has(agentID)) return prev
      const next = new Map(prev)
      next.set(agentID, { id: agentID, name, kind, status: 'running', task: '', messages: [], reasoningBuf: '' })
      return next
    })
  }, [])

  const messagesRef = useRef<ChatMessage[]>([])

  // Load session history when sessionId changes.
  // Do not let an async history response overwrite an active live stream.
  useEffect(() => {
    // If no sessionId from parent, try to fetch it directly from backend.
    // This handles the race where Layout's mount polling hasn't completed yet
    // or the session:changed event was missed.
    if (!sessionId) {
      setMessages([])
      messagesRef.current = []
      setThinking(false)
      setStatusBar({
        vendor: '', model: '', mode: 'auto', effort: 'auto', contextUsed: 0, contextTotal: 0, usagePercent: 0, remainingPercent: 0, inputTokens: 0, outputTokens: 0, cacheRead: 0, cacheWrite: 0, cacheHit: 0, status: 'ready',
      })
      // Try to fetch session ID directly from backend as fallback
      let cancelled = false
      App.GetCurrentSessionID().then((id: any) => {
        if (cancelled) return
        const sid = typeof id === 'string' ? id : (id as any)?.toString?.() || ''
        if (sid) {
          // Load history directly
          App.GetSessionHistory().then((history: any[]) => {
            if (cancelled || !history || history.length === 0) return
            autoScrollByTabRef.current.main = true
            const loaded = materializeHistory(history, [])
            messagesRef.current = loaded
            setMessages(loaded)
          }).catch(() => {})
        }
      }).catch(() => {})
      return () => { cancelled = true }
    }
    // Session changed — clear old messages immediately so stale content
    // from the previous session doesn't linger while loading.
    setMessages([])
    messagesRef.current = []
    setThinking(false)
    let cancelled = false
    App.GetSessionHistory().then((history: any[]) => {
      if (cancelled || runActiveRef.current) return
      if (!history || history.length === 0) {
        // New/empty session — nothing to load, messages already cleared above
        return
      }
      autoScrollByTabRef.current.main = true
      const loaded = materializeHistory(history, messagesRef.current)
      if (cancelled || runActiveRef.current) return
      messagesRef.current = loaded
      setMessages(loaded)
    }).catch(() => {})
    return () => { cancelled = true }
  }, [sessionId])

  // Ref to streaming assistant message ID for efficient updates
  const streamingMsgID = useRef<string | null>(null)
  const isStreamingRef = useRef(false)
  const runActiveRef = useRef(false)

  useEffect(() => {
    isStreamingRef.current = isStreaming
  }, [isStreaming])

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const autoScrollByTabRef = useRef<Record<string, boolean>>({ main: true })
  const lastManualScrollAtByTabRef = useRef<Record<string, number>>({})
  const suppressNextScrollEventRef = useRef(false)
  const currentTabStreaming = activeTab === 'main'
    ? isStreaming
    : (agentPanels.get(activeTab)?.status === 'running')

  // Auto-scroll to bottom
  useEffect(() => {
    if (!getTabAutoScroll(autoScrollByTabRef.current, activeTab)) return
    const container = scrollContainerRef.current
    if (container) {
      suppressNextScrollEventRef.current = true
      container.scrollTop = container.scrollHeight
    } else {
      messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
    }
  }, [messages, agentPanels, thinking, activeTab])

  useEffect(() => {
    messagesRef.current = messages
  }, [messages])

  useEffect(() => {
    if (!currentTabStreaming) return
    const id = window.setInterval(() => {
      if (getTabAutoScroll(autoScrollByTabRef.current, activeTab)) return
      const last = lastManualScrollAtByTabRef.current[activeTab] ?? 0
      if (last === 0) return
      if (Date.now() - last < 10_000) return
      autoScrollByTabRef.current[activeTab] = true
      const container = scrollContainerRef.current
      if (container) {
        suppressNextScrollEventRef.current = true
        container.scrollTop = container.scrollHeight
      } else {
        messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
      }
    }, 500)
    return () => window.clearInterval(id)
  }, [currentTabStreaming, activeTab])

  // Render mermaid diagrams — eagerly try on every message update.
  // Success: replace with SVG, mark data-done (no retry).
  // Failure (incomplete syntax): mark data-fail, will retry on next chunk.
  // Failure (permanent): mark data-done, leave as code block.
  useEffect(() => {
    const blocks = document.querySelectorAll('.mermaid-src:not([data-done])')
    if (blocks.length === 0) return
    let cancelled = false;
    (async () => {
      const mermaid = (await import('mermaid')).default
      mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose' })
      for (const block of Array.from(blocks)) {
        if (cancelled) break
        const code = block.querySelector('code')
        const src = code?.textContent || ''
        if (!src.trim()) continue
        try {
          const id = `mermaid-svg-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
          const { svg } = await mermaid.render(id, src)
          // Success — replace with SVG, never retry
          block.setAttribute('data-done', '1')
          const wrapper = document.createElement('div')
          wrapper.className = 'mermaid-rendered'
          wrapper.innerHTML = svg
          block.replaceWith(wrapper)
        } catch (e: any) {
          const msg = String(e?.message || e || '')
          // If it looks like a syntax error from incomplete input, allow retry
          if (msg.includes('Parse error') || msg.includes('Lexical error') || msg.includes('unexpected')) {
            block.setAttribute('data-fail', '1')
          } else {
            // Permanent error — stop retrying
            block.setAttribute('data-done', '1')
          }
        }
      }
    })()
    return () => { cancelled = true }
  }, [sessionId])

  // ── Event stream handler (follows Fyne chat_view.go handleEvent) ────────

  const handleStreamEvent = useCallback((evt: StreamEvent) => {
      const raw = evt.data

      switch (evt.type) {
        case 'text': {
          const p = parseJSON<{ content: string; message_id?: string }>(raw)
          if (!p || !p.content) break
          setMessages(prev => {
            const result = appendAssistantChunk(prev, streamingMsgID.current, p.content, nextID, undefined, p.message_id)
            streamingMsgID.current = result.streamingMessageID
            return result.messages
          })
          break
        }
        case 'reasoning': {
          const p = parseJSON<{ content: string; message_id?: string }>(raw)
          if (!p || !p.content) break
          setMessages(prev => appendReasoningChunk(prev, p.content, nextID, undefined, p.message_id))
          break
        }
        case 'reasoning_done': {
          setMessages(prev => prev.map(m =>
            m.role === 'reasoning' && m.streaming ? { ...m, streaming: false } : m
          ))
          break
        }
        case 'tool_call_done': {
          const p = parseJSON<{ id?: string; toolID?: string; tool_id?: string; name: string; arguments?: string; displayName?: string; detail?: string }>(raw)
          const toolID = p?.toolID || p?.tool_id || p?.id
          if (!p?.name || !toolID) break

          // exit_plan_mode: render plan as markdown, skip tool call UI
          if (p.name === 'exit_plan_mode') {
            const planArgs = parseJSON<{ plan: string }>(p.arguments || '{}')
            if (planArgs?.plan) {
              setMessages(prev => [...prev, {
                id: nextID(), role: 'assistant' as const,
                content: planArgs.plan, streaming: false,
                timestamp: Date.now(),
              }])
            }
            break
          }

          // enter_plan_mode: just show brief indicator, skip tool call UI
          if (p.name === 'enter_plan_mode') {
            setMessages(prev => [...prev, {
              id: nextID(), role: 'assistant' as const,
              content: 'Entering plan mode...', streaming: false,
              timestamp: Date.now(),
            }])
            break
          }

          setMessages(prev => {
            const exists = prev.some(m => m.role === 'tool' && m.toolID === toolID)
            if (exists) return prev
            // Close streaming for text/reasoning only, not other tool calls
            const msgs = prev.map(m =>
              m.streaming && m.role !== 'tool' ? { ...m, streaming: false } : m
            )
            msgs.push({
              id: nextID(), role: 'tool' as const,
              content: '', toolName: p.name, toolID,
              toolArgs: p.arguments, toolDisplayName: p.displayName,
              toolDetail: p.detail, streaming: true,
              timestamp: Date.now(),
            })
            return msgs
          })
          break
        }
        case 'tool_result': {
          const p = parseJSON<{ id?: string; toolID?: string; tool_id?: string; result?: string; isError?: boolean; displayName?: string; detail?: string }>(raw)
          const toolID = p?.toolID || p?.tool_id || p?.id
          if (!toolID) break
          setMessages(prev => prev.map(m =>
            m.role === 'tool' && m.toolID === toolID
              ? { ...m, content: p.result || '', isError: !!p.isError, toolDisplayName: p.displayName || m.toolDisplayName, toolDetail: p.detail || m.toolDetail, streaming: false }
              : m
          ))
          break
        }
        case 'done': {
          const p = parseJSON<{ message_id?: string }>(raw)
          setThinking(false)
          streamingMsgID.current = null
          // Close streaming for text/reasoning messages only. The overall run
          // remains busy until run_done because done only marks one model
          // iteration, and tools or follow-up model iterations may still run.
          // Tool messages stay streaming until their tool_result arrives.
          setMessages(prev => finishAssistantMessage(prev, p?.message_id))
          break
        }
        case 'error': {
          const p = parseJSON<{ message: string }>(raw)
          setMessages(prev => [...prev, {
            id: nextID(), role: 'error' as const,
            content: p?.message || 'Error', timestamp: Date.now(),
          }])
          break
        }
        case 'user_message': {
          const p = parseJSON<{ text: string; source: string }>(raw)
          if (p?.text) {
            streamingMsgID.current = null
            // Parse LAN Chat injection format
            let displayContent = p.text
            let lanchatFrom: string | undefined
            const parsed = parseLanChatContent(p.text)
            if (parsed) {
              displayContent = parsed.content
              lanchatFrom = parsed.from
            }
            setMessages(prev => appendUserMessage(prev, {
              id: nextID(), role: 'user' as const,
              content: displayContent,
              timestamp: Date.now(),
              ...(lanchatFrom ? { source: 'lanchat', lanchatFrom } : p.source ? { source: p.source } : {}),
            }))
          }
          break
        }
        case 'system': {
          const p = parseJSON<{ text: string }>(raw)
          if (!p?.text) break
          setMessages(prev => [...prev, {
            id: nextID(), role: 'system' as ChatRole,
            content: p.text,
            timestamp: Date.now(),
          }])
          break
        }
        case 'run_done': {
          setIsStreaming(false)
          runActiveRef.current = false
          setThinking(false)
          streamingMsgID.current = null
          setStatusBar(s => ({ ...s, status: 'idle' }))
          setMessages(prev => finishAssistantRun(prev))
          // Clear completed agent panels
          setAgentPanels(prev => {
            const next = new Map<string, AgentPanel>()
            for (const [id, panel] of prev) {
              if (panel.status === 'running') {
                next.set(id, { ...panel, status: 'completed', messages: panel.messages.map(m => m.streaming ? { ...m, streaming: false } : m) })
              }
            }
            return next
          })
          break
        }
        case 'usage_update': {
          const p = parseJSON<any>(raw)
          setStatusBar(s => ({
            ...s,
            inputTokens: p?.inputTokens ?? s.inputTokens,
            outputTokens: p?.outputTokens ?? s.outputTokens,
            cacheRead: p?.cacheRead ?? s.cacheRead,
            cacheWrite: p?.cacheWrite ?? s.cacheWrite,
            cacheHit: p?.cacheHit ?? s.cacheHit,
            contextUsed: p?.contextUsed ?? s.contextUsed,
            contextTotal: p?.contextTotal ?? s.contextTotal,
            usagePercent: p?.usagePercent ?? s.usagePercent,
            remainingPercent: p?.remainingPercent ?? s.remainingPercent,
          }))
          break
        }
        case 'swarm_board_updated': {
          void refreshTeamBoard()
          break
        }

        // ── Sub-agent events ──
        // ── Sub-agent events → route to agent panel ──
        case 'subagent_text': {
          const p = parseJSON<{ agentID: string; title?: string; content: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.agentID, p.title || p.agentID, 'subagent')
          updateAgentPanel(p.agentID, panel => {
            const msgs = [...panel.messages]
            const idx = msgs.findIndex(m => m.role === 'assistant' && m.streaming)
            if (idx >= 0) {
              msgs[idx] = { ...msgs[idx], content: msgs[idx].content + p.content }
            } else {
              msgs.push({ id: nextID(), role: 'assistant', content: p.content, streaming: true, timestamp: Date.now() })
            }
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'subagent_reasoning': {
          const p = parseJSON<{ agentID: string; title?: string; content: string }>(raw)
          if (!p || p.content === '__redacted_thinking__' || !p.content) break
          ensureAgentPanel(p.agentID, p.title || p.agentID, 'subagent')
          updateAgentPanel(p.agentID, panel => {
            const msgs = [...panel.messages]
            const idx = msgs.findIndex(m => m.role === 'reasoning' && m.streaming)
            if (idx >= 0) {
              msgs[idx] = { ...msgs[idx], content: msgs[idx].content + p.content }
            } else {
              msgs.push({ id: nextID(), role: 'reasoning', content: p.content, streaming: true, timestamp: Date.now() })
            }
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'subagent_tool_call': {
          const p = parseJSON<ToolCallPayload & { agentID: string; title?: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.agentID, p.title || p.agentID, 'subagent')
          updateAgentPanel(p.agentID, panel => {
            // Only close text/reasoning streaming, not other tool calls
            const msgs = panel.messages.map(m =>
              m.streaming && m.role !== 'tool' ? { ...m, streaming: false } : m
            )
            msgs.push({
              id: nextID(), role: 'tool' as ChatRole, content: '',
              toolName: p.name, toolID: p.id, toolArgs: p.arguments,
              toolDisplayName: p.displayName, toolDetail: p.detail,
              streaming: true, timestamp: Date.now(),
            })
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'subagent_tool_result': {
          const p = parseJSON<ToolResultPayload & { agentID: string; title?: string }>(raw)
          if (!p) break
          updateAgentPanel(p.agentID, panel => {
            const msgs = [...panel.messages]
            const idx = msgs.findIndex(m => m.role === 'tool' && m.toolID === p.id)
            if (idx >= 0) msgs[idx] = { ...msgs[idx], content: p.result, isError: p.isError, streaming: false }
            return { ...panel, messages: msgs }
          })
          break
        }

        // ── Swarm/teammate events → route to agent panel ──
        case 'swarm_text': {
          const p = parseJSON<{ teammateID: string; teammateName: string; content: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.teammateID, p.teammateName, 'teammate')
          updateAgentPanel(p.teammateID, panel => {
            const msgs = [...panel.messages]
            const idx = msgs.findIndex(m => m.role === 'assistant' && m.streaming)
            if (idx >= 0) {
              msgs[idx] = { ...msgs[idx], content: msgs[idx].content + p.content }
            } else {
              msgs.push({ id: nextID(), role: 'assistant', content: p.content, streaming: true, timestamp: Date.now() })
            }
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'swarm_tool_call': {
          const p = parseJSON<ToolCallPayload & { teammateID: string; teammateName: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.teammateID, p.teammateName, 'teammate')
          updateAgentPanel(p.teammateID, panel => {
            // Only close text/reasoning streaming, not other tool calls
            const msgs = panel.messages.map(m =>
              m.streaming && m.role !== 'tool' ? { ...m, streaming: false } : m
            )
            msgs.push({
              id: nextID(), role: 'tool' as ChatRole, content: '',
              toolName: p.name, toolID: p.id, toolArgs: p.arguments,
              toolDisplayName: p.displayName, toolDetail: p.detail,
              teammateName: p.teammateName, streaming: true, timestamp: Date.now(),
            })
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'swarm_tool_result': {
          const p = parseJSON<ToolResultPayload & { teammateID: string; teammateName: string }>(raw)
          if (!p) break
          updateAgentPanel(p.teammateID, panel => {
            const msgs = [...panel.messages]
            const idx = msgs.findIndex(m => m.role === 'tool' && m.toolID === p.id)
            if (idx >= 0) msgs[idx] = { ...msgs[idx], content: p.result, isError: p.isError, streaming: false }
            return { ...panel, messages: msgs }
          })
          break
        }
        case 'swarm_spawned': {
          const p = parseJSON<{ teammateID: string; teammateName: string; teamID: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.teammateID, p.teammateName, 'teammate')
          // Also show a system message in main chat
          setMessages(prev => [...prev, {
            id: nextID(), role: 'system' as ChatRole,
            content: `Teammate "${p.teammateName}" spawned`,
            timestamp: Date.now(),
          }])
          break
        }
        case 'swarm_idle': {
          const p = parseJSON<{ teammateID: string; teammateName: string; content: string }>(raw)
          if (!p) break
          updateAgentPanel(p.teammateID, panel => ({
            ...panel,
            status: p.content ? 'completed' : 'idle',
            messages: panel.messages.map(m => m.streaming ? { ...m, streaming: false } : m),
          }))
          break
        }
        case 'subagent_done': {
          const p = parseJSON<{ agentID: string; title: string; isError: boolean }>(raw)
          if (!p) break
          updateAgentPanel(p.agentID, panel => ({
            ...panel,
            status: p.isError ? 'failed' as const : 'completed' as const,
            messages: panel.messages.map(m => m.streaming ? { ...m, streaming: false } : m),
          }))
          break
        }
      }
    }, [ensureAgentPanel, refreshTeamBoard, updateAgentPanel])

  useEffect(() => {
    const off = EventsOn('chat:stream', (evt: StreamEvent) => {
      if (!evt?.type) return
      try {
        handleStreamEvent(evt)
      } catch (err) {
        console.error('[chat] handle stream event failed', err, evt)
      }
    })
    return () => { off() }
  }, [handleStreamEvent])

  useEffect(() => {
    void refreshTeamBoard()
  }, [])

  // ── Load model info on mount ──────────────────────────────────────────────

  useEffect(() => {
    async function load() {
      try {
        // GetModelInfo is exposed on the Go App struct but may not be in generated bindings
        const info = await App.GetModelInfo() as Record<string, any> | null
        if (info) {
          setStatusBar(s => ({
            ...s,
            vendor: info.vendor ?? '',
            model: info.model ?? '',
            mode: info.mode ?? s.mode,
            effort: info.effort ?? s.effort,
            contextTotal: info.contextTotal ?? info.contextWindow ?? 0,
            contextUsed: info.contextUsed ?? s.contextUsed,
            usagePercent: info.usagePercent ?? s.usagePercent,
            remainingPercent: info.remainingPercent ?? s.remainingPercent,
          }))
        }
      } catch {
        // Silently ignore if GetModelInfo not available
      }
    }
    load()
  }, [])

  // ── Send message ──────────────────────────────────────────────────────────

  const [pastedImages, setPastedImages] = useState<PastedImageAttachment[]>([])

  const markMessageDelivery = useCallback((id: string, deliveryStatus: ChatMessage['deliveryStatus']) => {
    setMessages(prev => prev.map(msg => msg.id === id ? { ...msg, deliveryStatus } : msg))
  }, [])

  const sendUserText = useCallback(async (text: string, existingID?: string, images: PastedImageAttachment[] = []) => {
    const messageID = existingID || nextID()
    if (!existingID) {
      const userMsg: ChatMessage = {
        id: messageID,
        role: 'user',
        content: text,
        images,
        timestamp: Date.now(),
        deliveryStatus: 'pending',
      }
      streamingMsgID.current = null
      setMessages(prev => appendUserMessage(prev, userMsg))
    } else {
      markMessageDelivery(messageID, 'pending')
    }

    const wasStreaming = isStreaming
    if (!wasStreaming) {
      runActiveRef.current = true
      setIsStreaming(true)
      setThinking(true)
      setStatusBar(s => ({ ...s, status: 'working' }))
    }

    try {
      if (images.length > 0) {
        await App.SendMessageWithImages(text, images.map(img => ({
          mimeType: img.mimeType,
          data: img.data,
          name: img.name,
        })))
      } else {
        await App.SendMessage(text)
      }
      markMessageDelivery(messageID, 'sent')
    } catch (err: any) {
      const message = err?.message ?? String(err)
      markMessageDelivery(messageID, 'failed')
      showToast?.('error', `Failed to send message: ${message}`)
      if (!wasStreaming) {
        runActiveRef.current = false
        setIsStreaming(false)
        setThinking(false)
        setStatusBar(s => ({ ...s, status: 'error' }))
      }
      setMessages(prev => [...prev, {
        id: nextID(),
        role: 'error' as const,
        content: message,
        timestamp: Date.now(),
      }])
    }
  }, [isStreaming, markMessageDelivery, showToast])

  const handleSend = useCallback(async () => {
    const text = input.trim()
    if (!text && pastedImages.length === 0) return

    const images = pastedImages
    setInput('')
    setPastedImages([])
    await sendUserText(text || 'Please analyze this image.', undefined, images)
  }, [input, pastedImages, sendUserText])

  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLInputElement>) => {
    const items = Array.from(e.clipboardData?.items || [])
    const imageFiles = items
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter((file): file is File => !!file)
    if (imageFiles.length === 0) return

    e.preventDefault()
    void Promise.all(imageFiles.map(file => new Promise<PastedImageAttachment>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => {
        const previewUrl = String(reader.result || '')
        const comma = previewUrl.indexOf(',')
        resolve({
          id: nextID(),
          mimeType: file.type || 'image/png',
          data: comma >= 0 ? previewUrl.slice(comma + 1) : previewUrl,
          previewUrl,
          name: file.name || 'pasted-image',
        })
      }
      reader.onerror = () => reject(reader.error || new Error('Failed to read pasted image'))
      reader.readAsDataURL(file)
    }))).then(images => {
      setPastedImages(prev => [...prev, ...images])
    }).catch(err => {
      showToast?.('error', err?.message || 'Failed to paste image')
    })
  }, [showToast])

  const handlePasteButton = useCallback(async () => {
    inputRef.current?.focus()

    try {
      if (document.queryCommandSupported?.('paste') && document.execCommand('paste')) {
        return
      }
    } catch {
      // Most WebViews/browsers block programmatic paste. Fall back to Wails clipboard APIs below.
    }

    try {
      const readClipboardAttachments = (window as any)?.go?.main?.App?.ReadClipboardAttachments
      if (typeof readClipboardAttachments === 'function') {
        const attachments = await App.ReadClipboardAttachments()
        if (attachments?.length) {
          const images = attachments.map(imageFromClipboardAttachment).filter((img): img is PastedImageAttachment => !!img)
          if (images.length > 0) {
            setPastedImages(prev => [...prev, ...images])
          }
          const textBlocks = attachments.filter(att => att.kind === 'text' && att.content).map(formatTextAttachment)
          if (textBlocks.length > 0) {
            setInput(prev => `${prev}${textBlocks.join('')}`)
          }
          const unsupported = attachments.filter(att => att.kind !== 'text' && att.kind !== 'image')
          if (unsupported.length > 0) {
            showToast?.('info', unsupported.map(att => `${att.name}: ${att.error || 'unsupported file'}`).join('\n'))
          }
          if (images.length > 0 || textBlocks.length > 0 || unsupported.length > 0) {
            return
          }
        }
      }

      const readClipboardImage = (window as any)?.go?.main?.App?.ReadClipboardImage
      if (typeof readClipboardImage === 'function') {
        const image = await App.ReadClipboardImage()
        if (image?.data) {
          setPastedImages(prev => [...prev, {
            id: nextID(),
            mimeType: image.mimeType || 'image/png',
            data: image.data,
            previewUrl: `data:${image.mimeType || 'image/png'};base64,${image.data}`,
            name: image.name || 'clipboard-image',
          }])
          return
        }
      }

      const text = await ClipboardGetText()
      if (text) {
        setInput(prev => prev ? `${prev}${text}` : text)
        return
      }
      showToast?.('info', 'Clipboard is empty or contains unsupported content')
    } catch (err: any) {
      showToast?.('error', err?.message || 'Failed to paste from clipboard')
    }
  }, [showToast])

  const handleRetrySend = useCallback((id: string, text: string, images?: PastedImageAttachment[]) => {
    void sendUserText(text, id, images || [])
  }, [sendUserText])

  // ── Cancel ────────────────────────────────────────────────────────────────

  const handleCancel = useCallback(() => {
    try {
      App.CancelMessage()
    } catch { /* ignore */ }
  }, [])

  const cycleReasoningEffort = useCallback(async () => {
    try {
      const result = await App.CycleReasoningEffort() as Record<string, any>
      if (result?.supported) {
        setStatusBar(s => ({ ...s, effort: result.effort ?? 'auto' }))
      } else {
        showToast?.('info', 'Reasoning effort is not supported by the current model')
      }
    } catch (err: any) {
      showToast?.('error', err?.message || 'Failed to switch reasoning effort')
    }
  }, [showToast])

  // ── Key handler ───────────────────────────────────────────────────────────

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'g') {
      e.preventDefault()
      void cycleReasoningEffort()
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }, [cycleReasoningEffort, handleSend])

  // ── Status bar label ──────────────────────────────────────────────────────

  const statusLabel = statusBar.status === 'working' ? (thinking ? 'Thinking...' : 'Working...')
    : statusBar.status === 'error' ? 'Error'
    : isStreaming ? 'Working...'
    : 'Ready'

  const openModelPicker = async () => {
    try {
      const models = await App.GetAvailableModels()
      if (models && models.length > 0) {
        setAvailableModels(models)
        setModelPickerOpen(true)
      }
    } catch {}
  }
  const selectModel = async (model: string) => {
    setModelPickerOpen(false)
    try {
      await App.SwitchModel(model)
      setStatusBar(s => ({ ...s, model }))
    } catch {}
  }
  // Close model picker on outside click
  useEffect(() => {
    if (!modelPickerOpen) return
    const handler = (e: MouseEvent) => { setModelPickerOpen(false) }
    document.addEventListener('click', handler)
    return () => document.removeEventListener('click', handler)
  }, [modelPickerOpen])
  const shortModel = statusBar.model.split('/').pop() || statusBar.model
  const workspaceLabel = workspace || 'Workspace'
  const modeOptions = ['supervised', 'plan', 'auto', 'bypass', 'autopilot'] as const
  const cycleMode = async () => {
    const idx = modeOptions.indexOf(statusBar.mode as any)
    const next = modeOptions[(idx + 1) % modeOptions.length]
    try {
      await App.SetPermissionMode(next)
      setStatusBar(s => ({ ...s, mode: next }))
    } catch {}
  }
  const selectWorkspace = async () => {
    try {
      const dir = await App.SelectWorkspace()
      if (dir) {
        onWorkspaceSelected?.(dir)
      }
    } catch {}
  }

  const effortValue = (statusBar.effort || 'auto').toLowerCase()
  const effortTone = effortValue === 'high'
    ? { background: 'rgba(239, 68, 68, 0.12)', border: 'rgba(239, 68, 68, 0.28)', color: 'rgb(248, 113, 113)' }
    : effortValue === 'medium'
      ? { background: 'rgba(234, 179, 8, 0.12)', border: 'rgba(234, 179, 8, 0.28)', color: 'rgb(250, 204, 21)' }
      : effortValue === 'low'
        ? { background: 'rgba(59, 130, 246, 0.11)', border: 'rgba(59, 130, 246, 0.26)', color: 'rgb(96, 165, 250)' }
        : { background: 'var(--color-card)', border: 'var(--color-border)', color: 'var(--text-secondary)' }

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', height: '100%', minWidth: 0 }}>
      <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minWidth: 0, flex: 1 }}>
      {/* Top bar */}
      <div style={{
        height: 'var(--topbar-height)',
        display: 'flex', alignItems: 'center',
        padding: '0 var(--spacing-lg)', gap: 8,
        borderBottom: '1px solid var(--color-border)',
        flexShrink: 0,
      }}>
        {/* Mode switcher — click to cycle supervised → plan → auto → bypass → autopilot */}
        <button
          onClick={selectWorkspace}
          title={workspace || 'Select workspace'}
          style={{
            padding: '2px 10px', borderRadius: 'var(--radius-sm)',
            background: 'var(--color-card)',
            color: 'var(--text-secondary)',
            border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 600,
            cursor: 'pointer',
            maxWidth: 420,
            overflowX: 'auto',
            whiteSpace: 'nowrap',
          }}
        >
          {workspaceLabel}
        </button>
        <button
          onClick={cycleMode}
          title={`Permission mode: ${statusBar.mode}. Click to switch.`}
          style={{
            padding: '2px 10px', borderRadius: 'var(--radius-sm)',
            background: statusBar.mode === 'autopilot'
              ? 'var(--color-error)'
              : statusBar.mode === 'bypass'
              ? 'var(--color-warning)'
              : statusBar.mode === 'plan'
              ? 'var(--color-primary)'
              : 'var(--color-card)',
            color: ['autopilot', 'bypass', 'plan'].includes(statusBar.mode)
              ? '#fff'
              : 'var(--text-secondary)',
            border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, fontWeight: 600,
            cursor: 'pointer', textTransform: 'uppercase', letterSpacing: 0.5,
          }}
        >
          {statusBar.mode}
        </button>
        <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--text-secondary)' }}>
          {statusLabel}
        </span>
        <button
          type="button"
          onClick={cycleReasoningEffort}
          title={`Reasoning effort: ${statusBar.effort || 'auto'}. Click to switch. Shortcut: Cmd/Ctrl+G.`}
          style={{
            padding: '2px 8px', borderRadius: 'var(--radius-sm)',
            background: effortTone.background,
            border: `1px solid ${effortTone.border}`,
            fontSize: 12, fontWeight: 500, color: effortTone.color,
            cursor: 'pointer',
            transition: 'background 0.15s, border-color 0.15s, color 0.15s',
          }}
        >
          Effort: {statusBar.effort || 'auto'}
        </button>
        <div style={{ flex: 1 }} />

        {/* Identity pill — click to edit nickname/role/team */}
        <button
          onClick={() => { setEditNick(selfNick); setEditRole(selfRole); setEditTeam(selfTeam); setIdentityOpen(true) }}
          title="Click to edit your nickname, role, and team"
          style={{
            padding: '2px 8px', borderRadius: 'var(--radius-sm)',
            background: 'var(--color-card)', border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
            cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4,
            maxWidth: 200, overflow: 'hidden', whiteSpace: 'nowrap',
          }}
        >
          <User size={11} style={{ flexShrink: 0 }} />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {selfNick || 'unnamed'}{selfRole || selfTeam ? ` ${[selfRole, selfTeam].filter(Boolean).join('/')}` : ''}
          </span>
        </button>

        {/* Identity edit modal */}
        {identityOpen && (
          <div
            onClick={() => setIdentityOpen(false)}
            style={{
              position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
              background: 'rgba(0,0,0,0.4)', display: 'flex',
              alignItems: 'center', justifyContent: 'center', zIndex: 200,
            }}
          >
            <div
              onClick={e => e.stopPropagation()}
              onKeyDown={e => {
                if (e.key === 'Enter') {
                  const combined = [editNick, editRole, editTeam].map(s => s.trim()).join('@')
                  App.LanChatSetNick(combined).then(() => {
                    setSelfNick(editNick.trim()); setSelfRole(editRole.trim()); setSelfTeam(editTeam.trim())
                    setIdentityOpen(false)
                  }).catch(() => {})
                } else if (e.key === 'Escape') {
                  setIdentityOpen(false)
                }
              }}
              style={{
                background: 'var(--color-card)', border: '1px solid var(--color-border)',
                borderRadius: 'var(--radius-md)', padding: 20, minWidth: 300, maxWidth: 360,
                boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
              }}
            >
              <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 16, color: 'var(--text-primary)' }}>
                Your Identity
              </div>
              <label style={{ fontSize: 11, color: 'var(--text-tertiary)', display: 'block', marginBottom: 4 }}>Nickname</label>
              <input
                value={editNick} onChange={e => setEditNick(e.target.value)} autoFocus
                placeholder="alice"
                style={{ width: '100%', padding: '6px 10px', marginBottom: 12, fontSize: 13,
                  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                  borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', outline: 'none',
                  fontFamily: 'var(--font-mono)' }}
              />
              <label style={{ fontSize: 11, color: 'var(--text-tertiary)', display: 'block', marginBottom: 4 }}>Role</label>
              <input
                value={editRole} onChange={e => setEditRole(e.target.value)}
                placeholder="developer"
                style={{ width: '100%', padding: '6px 10px', marginBottom: 12, fontSize: 13,
                  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                  borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', outline: 'none',
                  fontFamily: 'var(--font-mono)' }}
              />
              <label style={{ fontSize: 11, color: 'var(--text-tertiary)', display: 'block', marginBottom: 4 }}>Team</label>
              <input
                value={editTeam} onChange={e => setEditTeam(e.target.value)}
                placeholder="fluui"
                style={{ width: '100%', padding: '6px 10px', marginBottom: 16, fontSize: 13,
                  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                  borderRadius: 'var(--radius-sm)', color: 'var(--text-primary)', outline: 'none',
                  fontFamily: 'var(--font-mono)' }}
              />
              <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                <button onClick={() => setIdentityOpen(false)} style={{
                  padding: '5px 14px', fontSize: 12, border: '1px solid var(--color-border)',
                  borderRadius: 'var(--radius-sm)', background: 'transparent',
                  color: 'var(--text-secondary)', cursor: 'pointer',
                }}>Cancel</button>
                <button onClick={async () => {
                  const combined = [editNick, editRole, editTeam].map(s => s.trim()).join('@')
                  try {
                    await App.LanChatSetNick(combined)
                    setSelfNick(editNick.trim()); setSelfRole(editRole.trim()); setSelfTeam(editTeam.trim())
                    setIdentityOpen(false)
                  } catch {}
                }} style={{
                  padding: '5px 14px', fontSize: 12, border: 'none',
                  borderRadius: 'var(--radius-sm)', background: 'var(--color-primary)',
                  color: '#fff', cursor: 'pointer', fontWeight: 500,
                }}>Save</button>
              </div>
            </div>
          </div>
        )}

        {/* Model picker */}
        <div style={{ position: 'relative' }}>
          <button
            onClick={(e) => { e.stopPropagation(); openModelPicker() }}
            title={`Current model: ${statusBar.model}. Click to switch.`}
            style={{
              padding: '2px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-card)', border: '1px solid var(--color-border)',
              fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
              cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4,
            }}
          >
            {shortModel || 'No model'}
            <ChevronDown size={10} />
          </button>
          {modelPickerOpen && (
            <div style={{
              position: 'absolute', right: 0, top: '100%', marginTop: 4,
              background: 'var(--color-card)', border: '1px solid var(--color-border)',
              borderRadius: 'var(--radius-md)', boxShadow: '0 4px 12px rgba(0,0,0,0.3)',
              minWidth: 200, maxHeight: 240, overflow: 'auto', zIndex: 100,
            }}>
              {availableModels.map(m => {
                const short = m.split('/').pop() || m
                const active = m === statusBar.model
                return (
                  <button key={m} onClick={() => selectModel(m)} style={{
                    display: 'block', width: '100%', textAlign: 'left',
                    padding: '6px 12px', border: 'none', background: active ? 'var(--color-primary)' : 'transparent',
                    color: active ? '#fff' : 'var(--text-primary)',
                    fontFamily: 'var(--font-mono)', fontSize: 11, cursor: 'pointer',
                  }}>
                    {short}
                  </button>
                )
              })}
            </div>
          )}
        </div>

        {teamBoard.length > 0 && !teamBoardOpen && (
          <button onClick={openTeamBoard} title="Open team board" style={{
            padding: '2px 8px', borderRadius: 'var(--radius-sm)',
            background: 'var(--color-card)', border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
            cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4,
          }}>
            Team · {teamBoard.reduce((sum, team) => sum + (team.tasks?.length || 0), 0)}
          </button>
        )}

        {/* Context pill */}
        {statusBar.contextTotal > 0 && (
          <span style={{
            padding: '2px 8px', borderRadius: 10,
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
            display: 'flex', alignItems: 'center', gap: 6,
          }}>
            {formatTokens(statusBar.contextUsed)} / {formatTokens(statusBar.contextTotal)}
            <span style={{
              width: 48, height: 4, borderRadius: 2,
              background: 'var(--color-surface)', display: 'inline-block',
              position: 'relative', overflow: 'hidden',
            }}>
              <span style={{
                position: 'absolute', left: 0, top: 0,
              width: `${Math.max(statusBar.usagePercent, 1)}%`, height: '100%',
                borderRadius: 2,
              background: statusBar.usagePercent > 80 ? 'var(--color-error)'
                : statusBar.usagePercent > 50 ? 'var(--color-warning)'
                  : 'var(--color-success)',
              }} />
            </span>
          </span>
        )}

        {onShare && (
          <button onClick={onShare} style={{
            width: 28, height: 28, borderRadius: 'var(--radius-sm)',
            background: 'var(--color-surface)', border: 'none',
            color: 'var(--text-secondary)', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <Share2 size={14} />
          </button>
        )}
      </div>

      {/* Tab bar — main chat + agent panels */}
      {agentPanels.size > 0 && (
        <div style={{
          display: 'flex', borderBottom: '1px solid var(--color-border)',
          background: 'var(--color-surface)', overflowX: 'auto',
        }}>
          <TabButton label="Chat" active={activeTab === 'main'} onClick={() => setActiveTab('main')} />
          {Array.from(agentPanels.values()).map(panel => (
            <TabButton
              key={panel.id}
              label={`${panel.status === 'running' ? '● ' : panel.status === 'completed' ? '✓ ' : ''}${panel.name}`}
              active={activeTab === panel.id}
              onClick={() => setActiveTab(panel.id)}
              color={panel.status === 'running' ? 'var(--color-warning)' : panel.status === 'completed' ? 'var(--color-success)' : undefined}
            />
          ))}
        </div>
      )}

      {/* Messages — render active tab's content */}
      <div
        ref={scrollContainerRef}
        onScroll={() => {
          if (suppressNextScrollEventRef.current) {
            suppressNextScrollEventRef.current = false
            return
          }
          const nearBottom = isNearBottom(scrollContainerRef.current)
          autoScrollByTabRef.current[activeTab] = nearBottom
          lastManualScrollAtByTabRef.current[activeTab] = Date.now()
        }}
        style={{
        flex: 1, overflowY: 'auto',
        padding: 'var(--spacing-lg)',
        display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      }}>
        {activeTab === 'main' ? (
          // Main chat messages
          <>
            {messages.map(msg => (
              <MessageCard key={msg.id} msg={msg} onRetry={handleRetrySend} />
            ))}
          </>
        ) : (
          // Agent panel messages
          (() => {
            const panel = agentPanels.get(activeTab)
            if (!panel) return null
            return <>
              {/* Agent header */}
              <div style={{
                padding: '8px 12px', borderRadius: 'var(--radius-md)',
                background: 'var(--color-card)', border: '1px solid var(--color-border)',
                fontSize: 13, color: 'var(--text-secondary)',
              }}>
                <span style={{ fontWeight: 600, color: panel.status === 'running' ? 'var(--color-warning)' : 'var(--color-success)' }}>
                  {panel.name}
                </span>
                <span style={{ marginLeft: 8, fontSize: 11 }}>
                  {panel.status === 'running' ? '(working...)' : panel.status === 'completed' ? '(done)' : panel.status}
                </span>
              </div>
              {panel.messages.map(msg => (
                <MessageCard key={msg.id} msg={msg} />
              ))}
            </>
          })()
        )}

        {/* Thinking indicator (mirrors Fyne showThinking) */}
        {thinking && (
          <div>
            <div style={{
              fontSize: 11, fontWeight: 600, marginBottom: 4,
              color: 'var(--color-warning)',
              display: 'flex', alignItems: 'center', gap: 6,
            }}>
              <span style={{
                display: 'inline-block', width: 6, height: 6, borderRadius: '50%',
                background: 'var(--color-warning)',
                animation: 'pulse 1.5s ease-in-out infinite',
              }} />
              Agent
            </div>
            <div style={{ color: 'var(--text-tertiary)', fontStyle: 'italic', fontSize: 13 }}>
              Thinking...
            </div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {/* Input area */}
      {pastedImages.length > 0 && (
        <div style={{
          padding: '8px var(--spacing-lg) 0',
          borderTop: '1px solid var(--color-border)',
          display: 'flex', gap: 8, flexWrap: 'wrap', flexShrink: 0,
        }}>
          {pastedImages.map(img => (
            <div key={img.id} style={{ position: 'relative' }}>
              <img src={img.previewUrl} alt={img.name || 'pasted image'} style={{
                width: 72, height: 72, objectFit: 'cover', borderRadius: 'var(--radius-md)',
                border: '1px solid var(--color-border)', background: 'var(--color-card)',
              }} />
              <button type="button" onClick={() => setPastedImages(prev => prev.filter(x => x.id !== img.id))} title="Remove image" style={{
                position: 'absolute', top: -6, right: -6, width: 18, height: 18, borderRadius: 9,
                border: '1px solid var(--color-border)', background: 'var(--color-card)',
                color: 'var(--text-primary)', cursor: 'pointer', fontSize: 12, lineHeight: '16px', padding: 0,
              }}>×</button>
            </div>
          ))}
        </div>
      )}
      <div style={{
        padding: 'var(--spacing-md) var(--spacing-lg)',
        borderTop: '1px solid var(--color-border)',
        display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center',
        flexShrink: 0,
      }}>
        <button type="button" onClick={handlePasteButton} title="Paste from clipboard" style={{
          width: 36, height: 36, borderRadius: 'var(--radius-lg)',
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--text-tertiary)',
          transition: 'background 0.15s',
        }}>
          <ClipboardPaste size={16} />
        </button>
        <input
          ref={inputRef}
          value={input}
          onChange={e => setInput(e.target.value)}
          onPaste={handlePaste}
          onKeyDown={handleKeyDown}
          placeholder={isStreaming ? t('chat.agentWorking') : t('chat.placeholder')}
          style={{
            flex: 1, height: 40, padding: '0 var(--spacing-md)',
            borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 13,
          }}
        />
        {isStreaming ? (
          <button onClick={handleCancel} title="Cancel" style={{
            width: 36, height: 36, borderRadius: 'var(--radius-lg)',
            background: 'var(--color-error)', border: 'none', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: '#fff', transition: 'background 0.15s',
          }}>
            <Square size={16} fill="currentColor" />
          </button>
        ) : null}
        <button onClick={handleSend} disabled={!input.trim() && pastedImages.length === 0} style={{
            width: 36, height: 36, borderRadius: 'var(--radius-lg)',
            background: input.trim() || pastedImages.length > 0 ? 'var(--color-primary)' : 'var(--color-surface)',
            border: 'none', cursor: input.trim() || pastedImages.length > 0 ? 'pointer' : 'default',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: input.trim() || pastedImages.length > 0 ? '#fff' : 'var(--text-tertiary)',
            transition: 'background 0.15s',
          }}>
            <ArrowUp size={18} strokeWidth={2.5} />
          </button>
      </div>

      {/* Pulse animation keyframe */}
      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.3; }
        }
        @keyframes spin {
          to { transform: rotate(360deg); }
        }
      `}</style>
      </div>
      {teamBoardOpen && (
        <TeamBoard teams={teamBoard} onClose={closeTeamBoard} onSelectTeammate={selectTeammatePanel} />
      )}
    </div>
  )
}

// ── MessageCard ──────────────────────────────────────────────────────────────

// ── Tab button component ──
function TabButton({ label, active, onClick, color }: { label: string; active: boolean; onClick: () => void; color?: string }) {
  return (
    <button onClick={onClick} style={{
      padding: '6px 14px', border: 'none', cursor: 'pointer',
      fontSize: 12, fontWeight: active ? 600 : 400,
      color: active ? (color || 'var(--text-primary)') : 'var(--text-tertiary)',
      background: active ? 'var(--color-card)' : 'transparent',
      borderBottom: active ? '2px solid var(--color-primary)' : '2px solid transparent',
      whiteSpace: 'nowrap' as const,
    }}>
      {label}
    </button>
  )
}

function MessageCard({ msg, onRetry }: { msg: ChatMessage; onRetry?: (id: string, text: string, images?: PastedImageAttachment[]) => void }) {
  switch (msg.role) {
    case 'user':
      return <UserMessage msg={msg} onRetry={onRetry} />
    case 'assistant':
      return <AssistantMessage msg={msg} />
    case 'tool':
      return <ToolMessage msg={msg} />
    case 'reasoning':
      return <ReasoningMessage msg={msg} />
    case 'error':
      return <ErrorMessage msg={msg} />
    case 'system':
      return <SystemMessage msg={msg} />
    default:
      return null
  }
}

// ── Reasoning block (collapsible, shown before the message it belongs to) ──

function ReasoningBlock({ text, defaultOpen = false, label = 'Reasoning' }: { text: string; defaultOpen?: boolean; label?: string }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div style={{
      borderRadius: 'var(--radius-md)',
      border: '1px solid var(--color-border)',
      overflow: 'hidden',
      marginBottom: 4,
    }}>
      <button onClick={() => setOpen(!open)} style={{
        width: '100%', padding: '4px 10px',
        background: 'transparent', border: 'none', cursor: 'pointer',
        display: 'flex', alignItems: 'center', gap: 4,
        color: 'var(--text-tertiary)', fontSize: 11,
      }}>
        {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        {label}
      </button>
      {open && (
        <div style={{
          padding: '6px 10px',
          background: 'rgba(0,0,0,0.15)',
          color: 'var(--text-secondary)',
          fontSize: 12, lineHeight: 1.5,
          textAlign: 'left',
          maxHeight: 200, overflowY: 'auto',
        }}>
          <div className="markdown-body" style={{ fontSize: 12 }} dangerouslySetInnerHTML={{ __html: safeMarkdown(text) }} />
        </div>
      )}
    </div>
  )
}

function ReasoningMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{ maxWidth: '85%', alignSelf: 'flex-start' }}>
      <ReasoningBlock text={msg.content} defaultOpen={true} label={msg.streaming ? 'Thinking...' : 'Thought'} />
    </div>
  )
}

function UserMessage({ msg, onRetry }: { msg: ChatMessage; onRetry?: (id: string, text: string, images?: PastedImageAttachment[]) => void }) {
  const failed = msg.deliveryStatus === 'failed'
  const pending = msg.deliveryStatus === 'pending'
  const isLanChat = msg.source === 'lanchat' || (typeof msg.content === 'string' && msg.content.includes('[LAN Chat from '))
  const isMarkdown = isLanChat || msg.source === 'im' || msg.source === 'mobile'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', maxWidth: '80%', alignSelf: 'flex-end' }}>
      {msg.lanchatFrom && (
        <span style={{ fontSize: 10, color: 'var(--color-text-tertiary)', marginBottom: 2, marginRight: 4 }}>
          LAN Chat from {msg.lanchatFrom}
        </span>
      )}
      {!msg.lanchatFrom && msg.source && (
        <span style={{ fontSize: 10, color: 'var(--color-text-tertiary)', marginBottom: 2, marginRight: 4 }}>
          via {msg.source === 'im' ? 'IM' : msg.source === 'mobile' ? 'Mobile' : msg.source}
        </span>
      )}
      <div style={{
        padding: 'var(--spacing-sm) var(--spacing-md)',
        borderRadius: 'var(--radius-lg)',
        background: failed ? 'rgba(239, 68, 68, 0.16)' : 'var(--color-primary)',
        border: failed ? '1px solid rgba(239, 68, 68, 0.45)' : '1px solid transparent',
        color: failed ? '#fecaca' : '#fff',
        lineHeight: 1.6,
      }}>
        {msg.images && msg.images.length > 0 && (
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: msg.content ? 8 : 0 }}>
            {msg.images.map(img => (
              <img key={img.id} src={img.previewUrl} alt={img.name || 'pasted image'} style={{
                maxWidth: 180, maxHeight: 180, objectFit: 'cover', borderRadius: 'var(--radius-md)',
                border: '1px solid rgba(255,255,255,0.25)',
              }} />
            ))}
          </div>
        )}
        {isMarkdown
          ? <div className="markdown-body" style={{ fontSize: 13 }} dangerouslySetInnerHTML={{ __html: safeMarkdown(msg.content) }} />
          : msg.content}
      </div>
      {(pending || failed) && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4, marginRight: 4, fontSize: 11, color: failed ? 'var(--color-error)' : 'var(--text-tertiary)' }}>
          <span>{pending ? 'Sending...' : 'Failed to send'}</span>
          {failed && onRetry && (
            <button
              type="button"
              onClick={() => onRetry(msg.id, msg.content, msg.images)}
              style={{
                border: 'none',
                background: 'transparent',
                color: 'var(--color-primary)',
                cursor: 'pointer',
                fontSize: 11,
                fontWeight: 600,
                padding: 0,
              }}
            >
              Retry
            </button>
          )}
        </div>
      )}
    </div>
  )
}

function AssistantMessage({ msg }: { msg: ChatMessage }) {
  const isSubAgent = !!msg.agentID
  return (
    <div style={{ maxWidth: '85%', alignSelf: 'flex-start' }}>
      <div style={{
        fontSize: 11, fontWeight: 600, marginBottom: 4,
        color: isSubAgent ? 'var(--color-info)' : 'var(--color-success)',
        display: 'flex', alignItems: 'center', gap: 6,
      }}>
        {isSubAgent ? `Agent: ${msg.agentID}` : 'Assistant'}
        {msg.streaming && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--color-warning)' }}>
            writing...
          </span>
        )}
      </div>
      <div style={{
        padding: 'var(--spacing-sm) var(--spacing-md)',
        borderRadius: 'var(--radius-lg)',
        background: 'var(--color-card)',
        color: 'var(--text-primary)',
        lineHeight: 1.6,
        fontSize: 14,
        textAlign: 'left',
      }}>
        <MessageContent content={msg.content || '...'} />
        {msg.streaming && (
          <span style={{
            display: 'inline-block', width: 2, height: 14,
            background: 'var(--color-success)', marginLeft: 2,
            animation: 'pulse 1s ease-in-out infinite',
            verticalAlign: 'text-bottom',
          }} />
        )}
      </div>
    </div>
  )
}

function ToolMessage({ msg }: { msg: ChatMessage }) {
  const [expanded, setExpanded] = useState(false)

  const isCommandTool = ['run_command', 'start_command', 'bash', 'powershell'].includes(msg.toolName || '')

  // Title: displayName from Go's DescribeTool (now uses description/first-line comment for commands)
  // Append prettified tool name in parens
  const prettifiedName = msg.toolName?.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()) || 'Tool'
  const displayName = msg.toolDisplayName || ''
  const detail = msg.toolDetail || ''

  // For command tools: title = displayName, content = detail (the full command)
  // For other tools: title = displayName || prettifiedName, detail shown inline
  const title = isCommandTool
    ? (displayName ? `${displayName}` : prettifiedName)
    : (displayName || prettifiedName)

  const showCommandBlock = isCommandTool && detail && expanded
  const commandContent = detail // Go's DescribeTool now returns command as Detail

  return (
    <div style={{ alignSelf: 'flex-start', maxWidth: '80%', marginTop: 4, marginBottom: 4, width: '100%' }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: '6px 10px',
        borderRadius: 'var(--radius-md)',
        background: msg.isError ? 'rgba(220, 38, 38, 0.12)' : 'rgba(59, 130, 246, 0.08)',
        border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.25)' : 'rgba(59, 130, 246, 0.2)'}`,
        fontSize: 12,
        cursor: (msg.content || commandContent) ? 'pointer' : 'default',
        userSelect: 'none',
      }} onClick={() => (msg.content || commandContent) && setExpanded(!expanded)}>
        {/* Status icon */}
        {msg.streaming ? (
          <span style={{
            display: 'inline-block',
            width: 14, height: 14,
            border: '2px solid var(--color-border)',
            borderTopColor: 'var(--color-info)',
            borderRadius: '50%',
            animation: 'spin 0.8s linear infinite',
            flexShrink: 0,
          }} />
        ) : msg.isError ? (
          <span style={{ color: '#ef4444', fontSize: 12 }}>✕</span>
        ) : (
          <span style={{ color: 'var(--color-success)', fontSize: 12 }}>✓</span>
        )}

        {/* Title */}
        <span style={{
          fontWeight: 600,
          color: msg.isError ? '#f87171' : 'var(--color-info)',
        }}>
          {title}
        </span>

        {/* Prettified tool name in parens for command tools */}
        {isCommandTool && displayName && (
          <span style={{ color: 'var(--text-tertiary)', fontSize: 10, fontStyle: 'italic' }}>
            ({prettifiedName})
          </span>
        )}

        {/* Inline detail for non-command tools */}
        {!isCommandTool && detail && (
          <span style={{
            color: 'var(--text-tertiary)',
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const,
            maxWidth: 300,
          }}>
            {detail}
          </span>
        )}

        {/* Command preview for non-expanded command tools */}
        {isCommandTool && !expanded && commandContent && (
          <span style={{
            color: 'var(--text-tertiary)',
            fontFamily: 'var(--font-mono)',
            fontSize: 11,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const,
            maxWidth: 250,
          }}>
            {commandContent.split('\n')[0]}
          </span>
        )}

        {msg.streaming && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--color-warning)' }}>
            running...
          </span>
        )}

        {(msg.content || commandContent) && !msg.streaming && (
          <span style={{ color: 'var(--text-tertiary)', fontSize: 10 }}>
            {expanded ? '▲' : '▼'}
          </span>
        )}
      </div>

      {/* Expanded content */}
      {expanded && (
        <>
          {/* Command code block (for command tools) */}
          {showCommandBlock && (
            <div style={{
              marginTop: 4,
              borderRadius: 'var(--radius-md)',
              background: '#1e1e2e',
              overflow: 'hidden',
            }}>
              <div style={{
                padding: '4px 10px',
                background: 'rgba(255,255,255,0.05)',
                fontSize: 10,
                color: 'rgba(255,255,255,0.5)',
                fontFamily: 'var(--font-mono)',
              }}>
                {prettifiedName}
              </div>
              <pre style={{
                margin: 0, padding: '8px 10px',
                fontFamily: 'var(--font-mono)', fontSize: 12,
                color: '#cdd6f4',
                whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.5,
              }}>
                {commandContent}
              </pre>
            </div>
          )}

          {/* Result (terminal style) */}
          {msg.content && (
            <div style={{
              marginTop: 4, padding: '8px 10px',
              borderRadius: 'var(--radius-md)',
              background: msg.isError ? '#2d1b1b' : '#0d1117',
              border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.3)' : 'rgba(255,255,255,0.1)'}`,
              maxHeight: 300, overflowY: 'auto',
              fontFamily: 'var(--font-mono)', fontSize: 12,
              color: msg.isError ? '#f87171' : '#8b949e',
              whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.5,
              textAlign: 'left',
            }}>
              {msg.content}
            </div>
          )}
        </>
      )}
    </div>
  )
}

function ErrorMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{
      padding: 'var(--spacing-sm) var(--spacing-md)',
      borderRadius: 'var(--radius-md)',
      background: 'rgba(239, 68, 68, 0.1)',
      border: '1px solid var(--color-error)',
      color: 'var(--color-error)',
      fontSize: 13, lineHeight: 1.6,
    }}>
      {msg.content}
    </div>
  )
}

function SystemMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{
      padding: '4px 12px',
      color: 'var(--text-tertiary)',
      fontSize: 11, fontStyle: 'italic',
      textAlign: 'center',
    }}>
      {msg.content}
    </div>
  )
}
