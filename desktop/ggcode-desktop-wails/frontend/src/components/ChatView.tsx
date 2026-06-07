import React, { useState, useRef, useEffect, useCallback } from 'react'
import { ArrowUp, Square, Share2, ChevronDown, ChevronRight } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { marked } from 'marked'
import { useTranslation } from '../i18n'

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

interface ChatMessage {
  id: string
  role: ChatRole
  content: string
  toolName?: string
  toolID?: string
  toolArgs?: string
  toolDisplayName?: string
  toolDetail?: string
  agentID?: string
  teammateName?: string
  reasoning?: string
  isError?: boolean
  streaming?: boolean
  timestamp: number
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
  type: 'text' | 'tool_call_chunk' | 'tool_call_done' | 'tool_result' | 'done' | 'error' | 'reasoning' | 'run_done'
    | 'subagent_text' | 'subagent_reasoning' | 'subagent_tool_call' | 'subagent_tool_result'
    | 'swarm_text' | 'swarm_tool_call' | 'swarm_tool_result' | 'swarm_spawned' | 'swarm_idle' | 'usage_update'
  data: string // JSON-encoded payload
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

function parseJSON<T>(raw: string): T | null {
  try {
    return JSON.parse(raw) as T
  } catch {
    return null
  }
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
      id: previous[index]?.id || nextID(),
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

// ── Component ────────────────────────────────────────────────────────────────

export function ChatView({ onShare, sessionId, workspace, onWorkspaceSelected }: { onShare?: () => void; sessionId?: string; workspace?: string; onWorkspaceSelected?: (dir: string) => void }) {
  const { t } = useTranslation()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [agentPanels, setAgentPanels] = useState<Map<string, AgentPanel>>(new Map())
  const [activeTab, setActiveTab] = useState<string>('main') // 'main' or agentID
  const [input, setInput] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [statusBar, setStatusBar] = useState<StatusBarState>({
    vendor: '', model: '', mode: 'auto', contextUsed: 0, contextTotal: 0, usagePercent: 0, remainingPercent: 0, inputTokens: 0, outputTokens: 0, cacheRead: 0, cacheWrite: 0, cacheHit: 0, status: 'ready',
  })
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [availableModels, setAvailableModels] = useState<string[]>([])

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

  // Reasoning buffer: accumulates during streaming, attached to next message
  const reasoningBuf = useRef('')
  const messagesRef = useRef<ChatMessage[]>([])

  // Load session history when sessionId changes
  useEffect(() => {
    if (!sessionId) {
      setMessages([])
      messagesRef.current = []
      setThinking(false)
      return
    }
    App.GetSessionHistory().then((history: any[]) => {
      if (!history || history.length === 0) {
        setMessages([])
        messagesRef.current = []
        setThinking(false)
        return
      }
      autoScrollByTabRef.current.main = true
      const loaded = materializeHistory(history, messagesRef.current)
      messagesRef.current = loaded
      setMessages(loaded)
      setThinking(false)
    }).catch(() => {})
  }, [sessionId])

  // Initial load only. Event stream handles incremental updates.
  // run_done event does a final consistency check via GetSessionHistory.
  useEffect(() => {
    let cancelled = false
    App.GetSessionHistory().then((history: any[]) => {
      if (cancelled || !history) return
      const loaded = materializeHistory(history, messagesRef.current)
      messagesRef.current = loaded
      setMessages(loaded)
    }).catch(() => {})
    return () => { cancelled = true }
  }, [])

  // Ref to streaming assistant message ID for efficient updates
  const streamingMsgID = useRef<string | null>(null)

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
  }, [messages])

  // ── Event stream handler (follows Fyne chat_view.go handleEvent) ────────

  const handleStreamEvent = useCallback((evt: StreamEvent) => {
      const raw = evt.data

      switch (evt.type) {
        case 'user_message': {
          const p = parseJSON<{ content: string }>(raw)
          if (!p) break
          setMessages(prev => [...prev, {
            id: nextID(), role: 'user' as const,
            content: p.content, timestamp: Date.now(),
          }])
          break
        }
        case 'text': {
          const p = parseJSON<{ content: string }>(raw)
          if (!p) break
          setMessages(prev => {
            const msgs = [...prev]
            const idx = msgs.findIndex(m => m.role === 'assistant' && m.streaming)
            if (idx >= 0) {
              msgs[idx] = { ...msgs[idx], content: msgs[idx].content + p.content }
            } else {
              const reasoning = reasoningBuf.current
              reasoningBuf.current = ''
              msgs.push({
                id: nextID(), role: 'assistant' as const,
                content: p.content, reasoning, streaming: true,
                timestamp: Date.now(),
              })
            }
            return msgs
          })
          break
        }
        case 'reasoning': {
          const p = parseJSON<{ content: string }>(raw)
          if (!p) break
          reasoningBuf.current += p.content
          break
        }
        case 'tool_call_done': {
          const p = parseJSON<{ id: string; name: string; arguments?: string; displayName?: string; detail?: string }>(raw)
          if (!p) break
          setMessages(prev => {
            const msgs = prev.map(m => m.streaming ? { ...m, streaming: false } : m)
            msgs.push({
              id: nextID(), role: 'tool' as const, content: '',
              toolName: p.name, toolID: p.id,
              toolArgs: p.arguments, toolDisplayName: p.displayName,
              toolDetail: p.detail, streaming: true,
              timestamp: Date.now(),
            })
            return msgs
          })
          break
        }
        case 'tool_result': {
          const p = parseJSON<{ id: string; result: string; isError?: boolean }>(raw)
          if (!p) break
          setMessages(prev => {
            const msgs = [...prev]
            const idx = msgs.findIndex(m => m.role === 'tool' && m.toolID === p.id)
            if (idx >= 0) {
              msgs[idx] = { ...msgs[idx], content: p.result, isError: p.isError, streaming: false }
            }
            return msgs
          })
          break
        }
        case 'done': {
          setMessages(prev => prev.map(m => m.streaming ? { ...m, streaming: false } : m))
          break
        }
        case 'error': {
          const p = parseJSON<{ message: string }>(raw)
          setMessages(prev => [...prev, {
            id: nextID(), role: 'error' as const,
            content: p?.message || raw, timestamp: Date.now(),
          }])
          break
        }
        case 'run_done': {
          setIsStreaming(false)
          setThinking(false)
          setStatusBar(s => ({ ...s, status: 'idle' }))
          // Final consistency check: reload from backend to ensure nothing was missed
          App.GetSessionHistory().then((history: any[]) => {
            if (history) {
              const loaded = materializeHistory(history, messagesRef.current)
              messagesRef.current = loaded
              setMessages(loaded)
            }
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
          if (!p || p.content === '__redacted_thinking__') break
          ensureAgentPanel(p.agentID, p.title || p.agentID, 'subagent')
          updateAgentPanel(p.agentID, panel => ({ ...panel, reasoningBuf: panel.reasoningBuf + p.content }))
          break
        }
        case 'subagent_tool_call': {
          const p = parseJSON<ToolCallPayload & { agentID: string; title?: string }>(raw)
          if (!p) break
          ensureAgentPanel(p.agentID, p.title || p.agentID, 'subagent')
          updateAgentPanel(p.agentID, panel => {
            const msgs = panel.messages.map(m => m.streaming ? { ...m, streaming: false } : m)
            const reasoning = panel.reasoningBuf
            msgs.push({
              id: nextID(), role: 'tool' as ChatRole, content: '',
              toolName: p.name, toolID: p.id, toolArgs: p.arguments,
              toolDisplayName: p.displayName, toolDetail: p.detail,
              reasoning, streaming: true, timestamp: Date.now(),
            })
            return { ...panel, messages: msgs, reasoningBuf: '' }
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
            const msgs = panel.messages.map(m => m.streaming ? { ...m, streaming: false } : m)
            const reasoning = panel.reasoningBuf
            msgs.push({
              id: nextID(), role: 'tool' as ChatRole, content: '',
              toolName: p.name, toolID: p.id, toolArgs: p.arguments,
              toolDisplayName: p.displayName, toolDetail: p.detail,
              teammateName: p.teammateName, reasoning, streaming: true, timestamp: Date.now(),
            })
            return { ...panel, messages: msgs, reasoningBuf: '' }
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
      }
    }, [ensureAgentPanel, updateAgentPanel])

  useEffect(() => {
    let cancelled = false
    const pump = async () => {
      if (cancelled) return
      try {
        const events = await App.DrainStreamEvents() as StreamEvent[] | undefined
        if (!cancelled && events && events.length > 0) {
          for (const evt of events) {
            if (!evt?.type) continue
            handleStreamEvent(evt)
          }
        }
      } catch {}
    }
    void pump()
    const id = window.setInterval(() => { void pump() }, 150)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [handleStreamEvent])

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

  const handleSend = useCallback(async () => {
    const text = input.trim()
    if (!text) return

    // Add user message to chat
    const userMsg: ChatMessage = {
      id: nextID(),
      role: 'user',
      content: text,
      timestamp: Date.now(),
    }
    setMessages(prev => [...prev, userMsg])
    setInput('')

    if (isStreaming) {
      // Agent is busy — send to backend for queueing.
      // The user_message event will render it when backend processes it.
      setInput('')
      try {
        await App.SendMessage(text)
      } catch (err: any) {
        setMessages(prev => [...prev, {
          id: nextID(),
          role: 'error' as const,
          content: err?.message ?? String(err),
          timestamp: Date.now(),
        }])
      }
      return
    }

    setIsStreaming(true)
    setThinking(true)
    reasoningBuf.current = ''
    setStatusBar(s => ({ ...s, status: 'working' }))

    try {
      // Call Go backend SendMessage
      await App.SendMessage(text)
    } catch (err: any) {
      // Error handling (mirrors Fyne onSend error path)
      setIsStreaming(false)
      setThinking(false)
      setMessages(prev => [...prev, {
        id: nextID(),
        role: 'error',
        content: err?.message ?? String(err),
        timestamp: Date.now(),
      }])
      setStatusBar(s => ({ ...s, status: 'error' }))
    }
  }, [input, isStreaming])

  // ── Cancel ────────────────────────────────────────────────────────────────

  const handleCancel = useCallback(() => {
    try {
      App.CancelMessage()
    } catch { /* ignore */ }
  }, [])

  // ── Key handler ───────────────────────────────────────────────────────────

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }, [handleSend])

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

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
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
        <div style={{ flex: 1 }} />

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
              <MessageCard key={msg.id} msg={msg} />
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
      <div style={{
        padding: 'var(--spacing-md) var(--spacing-lg)',
        borderTop: '1px solid var(--color-border)',
        display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center',
        flexShrink: 0,
      }}>
        <input
          ref={inputRef}
          value={input}
          onChange={e => setInput(e.target.value)}
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
        <button onClick={handleSend} disabled={!input.trim()} style={{
            width: 36, height: 36, borderRadius: 'var(--radius-lg)',
            background: input.trim() ? 'var(--color-primary)' : 'var(--color-surface)',
            border: 'none', cursor: input.trim() ? 'pointer' : 'default',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: input.trim() ? '#fff' : 'var(--text-tertiary)',
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
      `}</style>
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

function MessageCard({ msg }: { msg: ChatMessage }) {
  switch (msg.role) {
    case 'user':
      return <UserMessage msg={msg} />
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

function UserMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', maxWidth: '80%', alignSelf: 'flex-end' }}>
      <div style={{
        padding: 'var(--spacing-sm) var(--spacing-md)',
        borderRadius: 'var(--radius-lg)',
        background: 'var(--color-primary)',
        color: '#fff',
        lineHeight: 1.6,
      }}>
        {msg.content}
      </div>
    </div>
  )
}

function AssistantMessage({ msg }: { msg: ChatMessage }) {
  const isSubAgent = !!msg.agentID
  return (
    <div style={{ maxWidth: '85%', alignSelf: 'flex-start' }}>
      {msg.reasoning && <ReasoningBlock text={msg.reasoning} />}
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
      {msg.reasoning && <ReasoningBlock text={msg.reasoning} />}
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
          <span style={{ color: 'var(--color-warning)', fontSize: 10 }}>●</span>
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
