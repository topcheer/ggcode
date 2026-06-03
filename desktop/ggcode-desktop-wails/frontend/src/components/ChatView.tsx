import React, { useState, useRef, useEffect, useCallback } from 'react'
import { ArrowUp, Square, Share2, ChevronDown, ChevronRight } from 'lucide-react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'
import { marked } from 'marked'

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
  reasoning?: string
  isError?: boolean
  streaming?: boolean
  timestamp: number
}

// ── Event types (mirrors wailskit/chat.go emit()) ────────────────────────────

interface StreamEvent {
  type: 'text' | 'tool_call_chunk' | 'tool_call_done' | 'tool_result' | 'done' | 'error' | 'reasoning' | 'run_done'
    | 'subagent_text' | 'subagent_reasoning' | 'subagent_tool_call' | 'subagent_tool_result'
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
  inputTokens: number
  outputTokens: number
  contextWindow: number
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
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

// ── Component ────────────────────────────────────────────────────────────────

export function ChatView({ onShare }: { onShare?: () => void }) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [statusBar, setStatusBar] = useState<StatusBarState>({
    vendor: '', model: '', inputTokens: 0, outputTokens: 0, contextWindow: 0, status: 'ready',
  })

  // Reasoning buffer: accumulates during streaming, attached to next message
  const reasoningBuf = useRef('')

  // Ref to streaming assistant message ID for efficient updates
  const streamingMsgID = useRef<string | null>(null)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, thinking])

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

  useEffect(() => {
    const off = EventsOn('chat:stream', (...args: any[]) => {
      // Wails v2 EventsEmit may pass data as a single object or as (type, data) args
      let normalizedEvt: StreamEvent | null = null

      if (args.length === 1 && args[0]?.type) {
        normalizedEvt = args[0]
      } else if (args.length >= 2 && typeof args[0] === 'string') {
        normalizedEvt = { type: args[0] as StreamEvent['type'], data: args[1] }
      }

      if (!normalizedEvt?.type) {
        console.warn('[ChatView] unknown event format, args:', args.length, JSON.stringify(args[0])?.slice(0, 200))
        return
      }

      handleStreamEvent(normalizedEvt)
    })

    function handleStreamEvent(evt: StreamEvent) {
      const raw = evt.data

      switch (evt.type) {

        // ── text: assistant streaming chunk (mirrors EventAssistantChunk) ──
        case 'text': {
          const p = parseJSON<TextPayload>(raw)
          if (!p) break
          setThinking(false)

          setMessages(prev => {
            // Find existing streaming assistant message
            const streamingIdx = prev.findIndex(
              m => m.role === 'assistant' && m.streaming
            )
            if (streamingIdx >= 0) {
              // Append to existing streaming message
              const updated = [...prev]
              updated[streamingIdx] = {
                ...updated[streamingIdx],
                content: updated[streamingIdx].content + p.content,
              }
              return updated
            }
            // First chunk: create new streaming assistant message
            const reasoning = reasoningBuf.current
            reasoningBuf.current = '' // clear for next round
            const newMsg: ChatMessage = {
              id: nextID(),
              role: 'assistant',
              content: p.content,
              reasoning,
              streaming: true,
              timestamp: Date.now(),
            }
            streamingMsgID.current = newMsg.id
            return [...prev, newMsg]
          })
          break
        }

        // ── tool_call_done: finalize current assistant text, create a "running" tool card ──
        // Mirrors Fyne: FinalizeStreaming() then AppendChat(tool msg)
        case 'tool_call_done': {
          const p = parseJSON<ToolCallPayload>(raw)
          if (!p) break
          setThinking(false)
          const reasoning = reasoningBuf.current
          reasoningBuf.current = ''

          setMessages(prev => {
            const updated = [...prev]
            // Finalize current streaming assistant message (mirrors FinalizeStreaming)
            for (let i = updated.length - 1; i >= 0; i--) {
              if (updated[i].role === 'assistant' && updated[i].streaming) {
                updated[i] = { ...updated[i], streaming: false }
                break
              }
            }
            // Add tool card in "running" state
            updated.push({
              id: nextID(),
              role: 'tool' as ChatRole,
              content: '',
              toolName: p.name,
              toolID: p.id,
              toolArgs: p.arguments,
              toolDisplayName: p.displayName,
              toolDetail: p.detail,
              reasoning,
              streaming: true,
              timestamp: Date.now(),
            })
            return updated
          })
          break
        }

        // ── tool_result: tool execution finished, fill result ──
        case 'tool_result': {
          const p = parseJSON<ToolResultPayload>(raw)
          if (!p) break
          setMessages(prev => {
            const idx = prev.findIndex(m => m.role === 'tool' && m.toolID === p.id)
            if (idx >= 0) {
              const updated = [...prev]
              updated[idx] = {
                ...updated[idx],
                content: p.result,
                isError: p.isError,
                streaming: false,
              }
              return updated
            }
            return prev
          })
          break
        }

        // ── done: one LLM turn finished (NOT the entire run!) ──
        // Mirrors Fyne: FinalizeStreaming() + update usage
        // The agent may loop (text → tool → text → ...) so don't set idle here.
        case 'done': {
          const p = parseJSON<DonePayload>(raw)
          setThinking(false)

          // Finalize any streaming assistant message
          setMessages(prev => {
            const updated = [...prev]
            for (let i = updated.length - 1; i >= 0; i--) {
              if (updated[i].role === 'assistant' && updated[i].streaming) {
                updated[i] = { ...updated[i], streaming: false }
                break
              }
            }
            // Cancel any tool messages still running (no result received)
            for (let i = 0; i < updated.length; i++) {
              if (updated[i].role === 'tool' && updated[i].streaming) {
                updated[i] = { ...updated[i], streaming: false, content: 'cancelled', isError: true }
              }
            }
            return updated
          })

          // Update token usage (cumulative across turns)
          if (p && (p.inputTokens || p.outputTokens)) {
            setStatusBar(s => ({
              ...s,
              inputTokens: (s.inputTokens || 0) + (p.inputTokens || 0),
              outputTokens: (s.outputTokens || 0) + (p.outputTokens || 0),
              status: 'working',
            }))
          }
          break
        }

        // ── error: display error message (mirrors error role in Fyne) ──
        case 'error': {
          const p = parseJSON<ErrorPayload>(raw)
          setIsStreaming(false)
          setThinking(false)

          // Finalize any streaming messages
          setMessages(prev => [
            ...prev.map(m => m.streaming ? { ...m, streaming: false } : m),
            {
              id: nextID(),
              role: 'error' as ChatRole,
              content: p?.message ?? 'Unknown error',
              timestamp: Date.now(),
            },
          ])
          streamingMsgID.current = null
          setStatusBar(s => ({ ...s, status: 'error' }))
          inputRef.current?.focus()
          break
        }

        // ── run_done: entire agent run finished ──
        // This is when we actually set idle (mirrors Fyne post-RunStreamWithContent)
        case 'run_done': {
          setIsStreaming(false)
          setThinking(false)
          setStatusBar(s => ({ ...s, status: 'ready' }))
          inputRef.current?.focus()
          break
        }

        // ── reasoning: accumulate into buffer, will attach to next message ──
        case 'reasoning': {
          const p = parseJSON<ReasoningPayload>(raw)
          if (!p) break
          if (p.content === '__redacted_thinking__') break
          reasoningBuf.current += p.content
          break
        }

        // ── Sub-agent events ──
        case 'subagent_text': {
          const p = parseJSON<{ agentID: string; content: string }>(raw)
          if (!p) break
          setMessages(prev => {
            // Find existing streaming sub-agent message
            const idx = prev.findIndex(m => m.agentID === p.agentID && m.role === 'assistant' && m.streaming)
            if (idx >= 0) {
              const updated = [...prev]
              updated[idx] = { ...updated[idx], content: updated[idx].content + p.content }
              return updated
            }
            // Create new sub-agent assistant message
            return [...prev, {
              id: nextID(), role: 'assistant' as ChatRole, agentID: p.agentID,
              content: p.content, streaming: true, timestamp: Date.now(),
            }]
          })
          break
        }
        case 'subagent_reasoning': {
          // Sub-agent reasoning — append to a running reasoning buffer for this agent
          // For simplicity, attach as reasoning text to next message from this agent
          const p = parseJSON<{ agentID: string; content: string }>(raw)
          if (!p || p.content === '__redacted_thinking__') break
          // Store in a map-like ref — just accumulate into reasoningBuf with agent prefix
          reasoningBuf.current += p.content
          break
        }
        case 'subagent_tool_call': {
          const p = parseJSON<ToolCallPayload & { agentID: string }>(raw)
          if (!p) break
          setMessages(prev => {
            const updated = [...prev]
            // Finalize current streaming sub-agent assistant message
            for (let i = updated.length - 1; i >= 0; i--) {
              if (updated[i].agentID === p.agentID && updated[i].role === 'assistant' && updated[i].streaming) {
                updated[i] = { ...updated[i], streaming: false }
                break
              }
            }
            updated.push({
              id: nextID(), role: 'tool' as ChatRole, agentID: p.agentID,
              content: '', toolName: p.name, toolID: p.id, toolArgs: p.arguments,
              toolDisplayName: p.displayName, toolDetail: p.detail,
              streaming: true, timestamp: Date.now(),
            })
            return updated
          })
          break
        }
        case 'subagent_tool_result': {
          const p = parseJSON<ToolResultPayload & { agentID: string }>(raw)
          if (!p) break
          setMessages(prev => {
            const idx = prev.findIndex(m => m.role === 'tool' && m.toolID === p.id)
            if (idx >= 0) {
              const updated = [...prev]
              updated[idx] = { ...updated[idx], content: p.result, isError: p.isError, streaming: false }
              return updated
            }
            return prev
          })
          break
        }
      }
    } // end handleStreamEvent

    return () => {
      if (typeof off === 'function') off()
    }
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
            contextWindow: info.contextWindow ?? 0,
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
    if (!text || isStreaming) return

    // Add user message (mirrors Fyne chat_view.go onSend: AppendChat(user))
    const userMsg: ChatMessage = {
      id: nextID(),
      role: 'user',
      content: text,
      timestamp: Date.now(),
    }
    setMessages(prev => [...prev, userMsg])
    setInput('')
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

  // ── Context usage percentage ──────────────────────────────────────────────

  const totalTokens = statusBar.inputTokens + statusBar.outputTokens
  const ctxPct = statusBar.contextWindow > 0
    ? Math.min(100, (totalTokens / statusBar.contextWindow) * 100)
    : 0

  // ── Status bar label ──────────────────────────────────────────────────────

  const statusLabel = statusBar.status === 'working' ? (thinking ? 'Thinking...' : 'Working...')
    : statusBar.status === 'error' ? 'Error'
    : isStreaming ? 'Working...'
    : 'Ready'

  const vendorModel = [statusBar.vendor, statusBar.model].filter(Boolean).join('/') || 'No model'

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
        {/* Vendor badge */}
        <span style={{
          padding: '2px 8px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)',
          color: '#fff', fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 500,
        }}>
          {vendorModel}
        </span>
        <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--text-secondary)' }}>
          {statusLabel}
        </span>
        <div style={{ flex: 1 }} />

        {/* Context pill */}
        {statusBar.contextWindow > 0 && (
          <span style={{
            padding: '2px 8px', borderRadius: 10,
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
            display: 'flex', alignItems: 'center', gap: 6,
          }}>
            {formatTokens(totalTokens)} / {formatTokens(statusBar.contextWindow)}
            <span style={{
              width: 48, height: 4, borderRadius: 2,
              background: 'var(--color-surface)', display: 'inline-block',
              position: 'relative', overflow: 'hidden',
            }}>
              <span style={{
                position: 'absolute', left: 0, top: 0,
                width: `${Math.max(ctxPct, 1)}%`, height: '100%',
                borderRadius: 2,
                background: ctxPct > 80 ? 'var(--color-error)'
                  : ctxPct > 50 ? 'var(--color-warning)'
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

      {/* Messages */}
      <div style={{
        flex: 1, overflowY: 'auto',
        padding: 'var(--spacing-lg)',
        display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      }}>
        {messages.map(msg => (
          <MessageCard key={msg.id} msg={msg} />
        ))}

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
          placeholder={isStreaming ? 'Agent is working...' : 'Message ggcode...'}
          disabled={isStreaming}
          style={{
            flex: 1, height: 40, padding: '0 var(--spacing-md)',
            borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 13,
            opacity: isStreaming ? 0.6 : 1,
          }}
        />
        {isStreaming ? (
          <button onClick={handleCancel} style={{
            width: 36, height: 36, borderRadius: 'var(--radius-lg)',
            background: 'var(--color-error)', border: 'none', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: '#fff', transition: 'background 0.15s',
          }}>
            <Square size={16} fill="currentColor" />
          </button>
        ) : (
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
        )}
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

function MessageCard({ msg }: { msg: ChatMessage }) {
  switch (msg.role) {
    case 'user':
      return <UserMessage msg={msg} />
    case 'assistant':
      return <AssistantMessage msg={msg} />
    case 'tool':
      return <ToolMessage msg={msg} />
    case 'error':
      return <ErrorMessage msg={msg} />
    case 'system':
      return <SystemMessage msg={msg} />
    default:
      return null
  }
}

// ── Reasoning block (collapsible, shown before the message it belongs to) ──

function ReasoningBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
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
        Reasoning
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
  // Use displayName from Go backend (tool.DescribeTool), fallback to prettified name
  const title = msg.toolDisplayName || msg.toolName?.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()) || 'Tool'
  const detail = msg.toolDetail || ''

  return (
    <div style={{ alignSelf: 'flex-start', maxWidth: '75%', marginTop: 4, marginBottom: 4 }}>
      {msg.reasoning && <ReasoningBlock text={msg.reasoning} />}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: '6px 10px',
        borderRadius: 'var(--radius-md)',
        background: msg.isError ? 'rgba(220, 38, 38, 0.12)' : 'rgba(59, 130, 246, 0.08)',
        border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.25)' : 'rgba(59, 130, 246, 0.2)'}`,
        fontSize: 12,
        cursor: msg.content ? 'pointer' : 'default',
        userSelect: 'none',
      }} onClick={() => msg.content && setExpanded(!expanded)}>
        {/* Status icon */}
        {msg.streaming ? (
          <span style={{ color: 'var(--color-warning)', fontSize: 10 }}>●</span>
        ) : msg.isError ? (
          <span style={{ color: '#ef4444', fontSize: 12 }}>✕</span>
        ) : (
          <span style={{ color: 'var(--color-success)', fontSize: 12 }}>✓</span>
        )}

        {/* Title from Go's tool.DescribeTool */}
        <span style={{
          fontWeight: 600,
          color: msg.isError ? '#f87171' : 'var(--color-info)',
        }}>
          {title}
        </span>

        {/* Detail (file path, command, search query, etc.) */}
        {detail && (
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

        {msg.streaming && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--color-warning)' }}>
            running...
          </span>
        )}

        {msg.content && !msg.streaming && (
          <span style={{ color: 'var(--text-tertiary)', fontSize: 10 }}>
            {expanded ? '▲' : '▼'}
          </span>
        )}
      </div>

      {/* Expandable result */}
      {expanded && msg.content && (
        <div style={{
          marginTop: 4, padding: '8px 10px',
          borderRadius: 'var(--radius-md)',
          background: msg.isError ? 'rgba(220, 38, 38, 0.06)' : 'rgba(59, 130, 246, 0.04)',
          border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.15)' : 'rgba(59, 130, 246, 0.12)'}`,
          maxHeight: 240, overflowY: 'auto',
          fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)',
          whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.5,
          textAlign: 'left',
        }}>
          {msg.content}
        </div>
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
