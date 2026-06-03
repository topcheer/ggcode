import React, { useState, useRef, useEffect, useCallback } from 'react'
import { ArrowUp, Square, Share2, ChevronDown, ChevronRight } from 'lucide-react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'
import { marked } from 'marked'
import mermaid from 'mermaid'

// Configure mermaid
mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose' })

// Custom marked renderer: render mermaid code blocks into SVG
const renderer = new marked.Renderer()
renderer.code = function ({ text, lang }: { text: string; lang?: string }) {
  if (lang === 'mermaid') {
    const id = 'mermaid-' + Math.random().toString(36).slice(2, 10)
    return `<div class="mermaid-container" data-mermaid-id="${id}" data-mermaid-source="${encodeURIComponent(text)}"></div>`
  }
  return `<pre><code class="language-${lang || ''}">${text}</code></pre>`
}
marked.setOptions({ gfm: true, breaks: true, renderer })

// ── Types (mirrors Go ChatMessage from desktop/ggcode-desktop/types.go) ──────

type ChatRole = 'user' | 'assistant' | 'system' | 'tool' | 'reasoning' | 'error'

interface ChatMessage {
  id: string
  role: ChatRole
  content: string
  toolName?: string
  toolID?: string
  toolArgs?: string
  toolDesc?: string
  isError?: boolean
  streaming?: boolean
  timestamp: number
}

// ── Event types (mirrors wailskit/chat.go emit()) ────────────────────────────

interface StreamEvent {
  type: 'text' | 'tool_call_chunk' | 'tool_call_done' | 'tool_result' | 'done' | 'error' | 'reasoning'
  data: string // JSON-encoded payload
}

interface TextPayload { content: string }
interface ToolCallPayload { id: string; name: string; arguments?: string }
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

  // Reasoning state
  const [reasoningText, setReasoningText] = useState('')
  const [reasoningOpen, setReasoningOpen] = useState(false)

  // Track pending tool calls for matching results (tool_result has no ID in wailskit)
  const pendingToolIDs = useRef<string[]>([])

  // Ref to streaming assistant message ID for efficient updates
  const streamingMsgID = useRef<string | null>(null)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, thinking, reasoningText])

  // Render mermaid diagrams after messages update
  useEffect(() => {
    const containers = document.querySelectorAll('.mermaid-container:not([data-rendered])')
    containers.forEach(async (el) => {
      const source = decodeURIComponent(el.getAttribute('data-mermaid-source') || '')
      const id = el.getAttribute('data-mermaid-id') || 'mermaid'
      if (!source) return
      try {
        const { svg } = await mermaid.render(id, source)
        el.innerHTML = svg
        el.setAttribute('data-rendered', 'true')
      } catch (e) {
        el.innerHTML = `<pre style="color: var(--color-error)">${e}</pre>`
        el.setAttribute('data-rendered', 'true')
      }
    })
  }, [messages])

  // ── Event stream handler (follows Fyne chat_view.go handleEvent) ────────

  useEffect(() => {
    const off = EventsOn('chat:stream', (_evt: StreamEvent) => {
      const evt = _evt
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
            const newMsg: ChatMessage = {
              id: nextID(),
              role: 'assistant',
              content: p.content,
              streaming: true,
              timestamp: Date.now(),
            }
            streamingMsgID.current = newMsg.id
            return [...prev, newMsg]
          })
          break
        }

        // ── tool_call_chunk: tool call started (mirrors creating a running tool card) ──
        case 'tool_call_chunk': {
          const p = parseJSON<ToolCallPayload>(raw)
          if (!p) break
          setThinking(false)
          pendingToolIDs.current.push(p.id)

          // Add a tool message in "running" state
          setMessages(prev => [...prev, {
            id: nextID(),
            role: 'tool',
            content: '',
            toolName: p.name,
            toolID: p.id,
            streaming: true,
            timestamp: Date.now(),
          }])
          break
        }

        // ── tool_call_done: tool call finished, store arguments ──
        case 'tool_call_done': {
          const p = parseJSON<ToolCallPayload>(raw)
          if (!p) break
          setMessages(prev => {
            const idx = prev.findIndex(m => m.toolID === p.id && m.role === 'tool')
            if (idx >= 0) {
              const updated = [...prev]
              updated[idx] = { ...updated[idx], streaming: false, toolArgs: p.arguments }
              return updated
            }
            return prev
          })
          break
        }

        // ── tool_result: update tool card with result ──
        case 'tool_result': {
          const p = parseJSON<ToolResultPayload>(raw)
          if (!p) break
          setMessages(prev => {
            // Match by tool call ID first, then fallback to name
            for (let i = prev.length - 1; i >= 0; i--) {
              if (prev[i].role === 'tool' &&
                  ((p.id && prev[i].toolID === p.id) || (prev[i].toolName === p.name)) &&
                  prev[i].content === '') {
                const updated = [...prev]
                updated[i] = {
                  ...updated[i],
                  content: p.result,
                  isError: p.isError,
                  streaming: false,
                }
                return updated
              }
            }
            return prev
          })
          break
        }

        // ── done: stream finished (mirrors EventStreamDone / FinalizeStreaming) ──
        case 'done': {
          const p = parseJSON<DonePayload>(raw)
          setIsStreaming(false)
          setThinking(false)

          // Finalize streaming: mark all streaming messages as done
          setMessages(prev => prev.map(m =>
            m.streaming ? { ...m, streaming: false } : m
          ))
          streamingMsgID.current = null

          // Update token usage
          if (p && (p.inputTokens || p.outputTokens)) {
            setStatusBar(s => ({
              ...s,
              inputTokens: p.inputTokens ?? s.inputTokens,
              outputTokens: p.outputTokens ?? s.outputTokens,
              status: 'ready',
            }))
          } else {
            setStatusBar(s => ({ ...s, status: 'ready' }))
          }
          // Refocus input
          inputRef.current?.focus()
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

        // ── reasoning: accumulate reasoning text (mirrors EventReasoning) ──
        case 'reasoning': {
          const p = parseJSON<ReasoningPayload>(raw)
          if (!p) break
          // Filter out Anthropic redacted thinking blocks
          if (p.content === '__redacted_thinking__') break
          setThinking(false)
          setReasoningOpen(true)
          setReasoningText(prev => prev + p.content)
          break
        }
      }
    })

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
    setReasoningText('')
    setReasoningOpen(false)
    setStatusBar(s => ({ ...s, status: 'thinking' }))

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

  const statusLabel = statusBar.status === 'thinking' ? 'Thinking...'
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

        {/* Reasoning panel (mirrors Fyne onReasoningChunk accordion) */}
        {reasoningText && (
          <div style={{
            borderRadius: 'var(--radius-md)',
            border: '1px solid var(--color-border)',
            overflow: 'hidden',
            alignSelf: 'flex-start',
            maxWidth: '85%',
          }}>
            <button
              onClick={() => setReasoningOpen(!reasoningOpen)}
              style={{
                width: '100%', padding: '6px 12px',
                background: 'var(--color-card)',
                border: 'none', cursor: 'pointer',
                display: 'flex', alignItems: 'center', gap: 6,
                color: 'var(--text-secondary)', fontSize: 12, fontWeight: 500,
              }}
            >
              {reasoningOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              Reasoning
            </button>
            {reasoningOpen && (
              <div style={{
                padding: '8px 12px',
                background: 'var(--color-surface)',
                color: 'var(--text-secondary)',
                fontSize: 13, lineHeight: 1.6,
                textAlign: 'left',
                maxHeight: 300, overflowY: 'auto',
              }}>
                <div className="markdown-body" dangerouslySetInnerHTML={{ __html: marked.parse(reasoningText) as string }} />
              </div>
            )}
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
  return (
    <div style={{ maxWidth: '85%', alignSelf: 'flex-start' }}>
      <div style={{
        fontSize: 11, fontWeight: 600, marginBottom: 4,
        color: 'var(--color-success)',
        display: 'flex', alignItems: 'center', gap: 6,
      }}>
        Assistant
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
        <div className="markdown-body" dangerouslySetInnerHTML={{ __html: marked.parse(msg.content || '...') as string }} />
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

// toolArgSummary extracts a short human-readable summary from tool arguments.
function toolArgSummary(toolName: string, rawArgs: string): string {
  try {
    const args = JSON.parse(rawArgs)
    switch (toolName) {
      case 'read_file': case 'write_file': case 'edit_file': case 'multi_edit_file':
        return args.path || args.file_path || ''
      case 'run_command': case 'start_command':
        return (args.command || '').slice(0, 80)
      case 'search_files': case 'grep':
        return args.pattern || args.query || ''
      case 'glob': case 'find':
        return args.pattern || ''
      case 'web_search': case 'web_fetch':
        return args.query || args.url || ''
      case 'git_commit':
        return args.message ? `"${(args.message as string).slice(0, 50)}"` : ''
      case 'git_diff': case 'git_show': case 'git_log':
        return args.revision || args.file || ''
      case 'save_memory':
        return args.key || ''
      case 'task_create': case 'swarm_task_create':
        return args.subject || ''
      default: {
        if (args.path) return args.path
        if (args.query) return args.query
        if (args.pattern) return args.pattern
        if (args.command) return (args.command as string).slice(0, 60)
        if (args.url) return args.url
        return ''
      }
    }
  } catch {
    return ''
  }
}

// toolDisplayName returns a friendly display name for a tool.
function toolDisplayName(toolName: string): string {
  const names: Record<string, string> = {
    read_file: 'Read File', write_file: 'Write File', edit_file: 'Edit File', multi_edit_file: 'Multi Edit',
    run_command: 'Run Command', start_command: 'Start Command', search_files: 'Search', grep: 'Grep',
    glob: 'Find Files', web_search: 'Web Search', web_fetch: 'Fetch URL', git_commit: 'Git Commit',
    git_diff: 'Git Diff', git_show: 'Git Show', git_log: 'Git Log', git_add: 'Git Add',
    git_status: 'Git Status', git_blame: 'Git Blame', git_branch_list: 'Git Branch', git_stash: 'Git Stash',
    save_memory: 'Save Memory', task_create: 'Create Task', task_update: 'Update Task', task_list: 'List Tasks',
    lsp_definition: 'Go to Def', lsp_references: 'Find Refs', lsp_hover: 'Hover Info',
    lsp_diagnostics: 'Diagnostics', lsp_rename: 'Rename', delegate: 'Delegate', spawn_agent: 'Spawn Agent',
    ask_user: 'Ask User',
  }
  return names[toolName] || toolName
}

function ToolMessage({ msg }: { msg: ChatMessage }) {
  const [expanded, setExpanded] = useState(false)
  const summary = msg.toolArgs ? toolArgSummary(msg.toolName || '', msg.toolArgs) : ''
  const displayName = toolDisplayName(msg.toolName || '')

  return (
    <div style={{ alignSelf: 'flex-start', maxWidth: '75%', marginTop: 4, marginBottom: 4 }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: '6px 10px',
        borderRadius: 'var(--radius-md)',
        background: msg.isError ? 'rgba(220, 38, 38, 0.12)' : 'rgba(59, 130, 246, 0.12)',
        border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.3)' : 'rgba(59, 130, 246, 0.3)'}`,
        fontSize: 12,
        cursor: msg.content ? 'pointer' : 'default',
        userSelect: 'none',
      }} onClick={() => msg.content && setExpanded(!expanded)}>
        {msg.streaming ? (
          <span style={{ color: 'var(--color-warning)', fontFamily: 'var(--font-mono)', fontSize: 10 }}>●</span>
        ) : msg.isError ? (
          <span style={{ color: '#ef4444', fontSize: 12 }}>✕</span>
        ) : (
          <span style={{ color: 'var(--color-success)', fontSize: 12 }}>✓</span>
        )}
        <span style={{ fontWeight: 600, color: msg.isError ? '#f87171' : 'var(--color-info)' }}>
          {displayName}
        </span>
        {summary && (
          <span style={{
            color: 'var(--text-tertiary)', fontFamily: 'var(--font-mono)', fontSize: 11,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 300,
          }}>
            {summary}
          </span>
        )}
        {msg.streaming && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--color-warning)' }}>running...</span>
        )}
        {msg.content && !msg.streaming && (
          <span style={{ color: 'var(--text-tertiary)', fontSize: 10 }}>{expanded ? '▲' : '▼'}</span>
        )}
      </div>
      {expanded && msg.content && (
        <div style={{
          marginTop: 4, padding: '8px 10px',
          borderRadius: 'var(--radius-md)',
          background: msg.isError ? 'rgba(220, 38, 38, 0.06)' : 'rgba(59, 130, 246, 0.06)',
          border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.15)' : 'rgba(59, 130, 246, 0.15)'}`,
          maxHeight: 240, overflowY: 'auto',
          fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)',
          whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.5,
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
