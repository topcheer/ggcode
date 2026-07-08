import React, { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { ArrowUp, Square, Share2, ChevronDown, ChevronRight, ClipboardPaste, User, Copy, Check, ClipboardCopy, Search, X, ChevronUp, Download, ImagePlus } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { ClipboardGetText, EventsOn, BrowserOpenURL } from '../../wailsjs/runtime/runtime'
import { marked } from 'marked'
import hljs from 'highlight.js'
import { useTranslation } from '../i18n'
import { SkeletonMessages } from './Skeleton'
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

// Post-process rendered HTML: add copy button + language label to <pre> blocks
function enhanceCodeBlocks(container: HTMLElement) {
  const preBlocks = container.querySelectorAll('pre')
  preBlocks.forEach((pre) => {
    if (pre.querySelector('.code-block-header')) return // already enhanced

    const code = pre.querySelector('code')
    if (!code) return

    // Extract language from class name (e.g., "language-go" → "go")
    const langClass = Array.from(code.classList).find((c: string) => c.startsWith('language-'))
    const lang = langClass ? langClass.replace('language-', '') : ''

    // Apply syntax highlighting (CSS theme already imported via FileBrowser)
    try {
      if (!code.hasAttribute('data-highlighted')) {
        if (lang && hljs.getLanguage(lang)) {
          hljs.highlightElement(code)
        } else {
          hljs.highlightElement(code) // auto-detect
        }
        code.setAttribute('data-highlighted', 'true')
      }
    } catch { /* ignore highlighting errors */ }

    // Create wrapper: make <pre> position relative, add header bar
    pre.style.position = 'relative'

    const header = document.createElement('div')
    header.className = 'code-block-header'
    header.style.cssText = 'display:flex;align-items:center;justify-content:space-between;padding:2px 0 6px;font-size:11px;color:var(--text-tertiary);font-family:var(--font-mono,SF Mono,monospace);'

    // Language label
    const langSpan = document.createElement('span')
    langSpan.textContent = lang || 'text'
    langSpan.style.cssText = 'opacity:0.7;text-transform:lowercase;'
    header.appendChild(langSpan)

    // Copy button
    const copyBtn = document.createElement('button')
    copyBtn.textContent = 'Copy'
    copyBtn.style.cssText = 'padding:1px 8px;border-radius:3px;border:1px solid var(--color-border);background:var(--color-surface);color:var(--text-tertiary);cursor:pointer;font-size:11px;opacity:0;transition:opacity 0.15s;'
    copyBtn.addEventListener('mouseenter', () => { copyBtn.style.opacity = '1' })
    copyBtn.addEventListener('mouseleave', () => { copyBtn.style.opacity = '0' })

    pre.addEventListener('mouseenter', () => { copyBtn.style.opacity = '1' })
    pre.addEventListener('mouseleave', () => { copyBtn.style.opacity = '0' })

    copyBtn.addEventListener('click', (e) => {
      e.preventDefault()
      e.stopPropagation()
      const text = code.textContent || ''
      navigator.clipboard.writeText(text).then(() => {
        copyBtn.textContent = '✓ Copied'
        copyBtn.style.color = 'var(--color-success)'
        copyBtn.style.borderColor = 'var(--color-success)'
        copyBtn.style.opacity = '1'
        setTimeout(() => {
          copyBtn.textContent = 'Copy'
          copyBtn.style.color = ''
          copyBtn.style.borderColor = ''
          copyBtn.style.opacity = '0'
        }, 1500)
      }).catch(() => {})
    })
    header.appendChild(copyBtn)

    // Insert header at top of <pre>, before <code>
    pre.insertBefore(header, code)

    // Adjust <pre> padding top since header is now inside
    pre.style.paddingTop = '4px'

    // Collapse long code blocks (>30 lines) with expand toggle
    const lineCount = (code.textContent || '').split('\n').length
    if (lineCount > 30) {
      const collapsedMaxHeight = '420px'
      pre.style.maxHeight = collapsedMaxHeight
      pre.style.overflow = 'hidden'
      pre.style.position = 'relative'

      const expandBtn = document.createElement('button')
      expandBtn.textContent = `Expand (${lineCount} lines)`
      expandBtn.style.cssText = 'position:absolute;bottom:0;left:0;right:0;padding:6px 0;background:linear-gradient(transparent,var(--color-surface));border:none;color:var(--color-primary);cursor:pointer;font-size:11px;font-family:var(--font-mono,monospace);text-align:center;transition:background 0.15s;'
      let expanded = false
      expandBtn.addEventListener('click', (e) => {
        e.preventDefault()
        e.stopPropagation()
        expanded = !expanded
        if (expanded) {
          pre.style.maxHeight = ''
          pre.style.overflow = ''
          expandBtn.textContent = 'Collapse'
          expandBtn.style.position = 'sticky'
          expandBtn.style.background = 'var(--color-surface)'
        } else {
          pre.style.maxHeight = collapsedMaxHeight
          pre.style.overflow = 'hidden'
          expandBtn.textContent = `Expand (${lineCount} lines)`
          expandBtn.style.position = 'absolute'
          expandBtn.style.background = 'linear-gradient(transparent,var(--color-surface))'
        }
      })
      pre.appendChild(expandBtn)
    }
    // Intercept external links: open in system browser instead of navigating webview
    const anchors = container.querySelectorAll<HTMLAnchorElement>('a[href]')
    anchors.forEach((a) => {
      if (a.getAttribute('data-external')) return // already intercepted
      const href = a.getAttribute('href') || ''
      if (href.startsWith('http://') || href.startsWith('https://')) {
        a.setAttribute('data-external', 'true')
        a.addEventListener('click', (e) => {
          e.preventDefault()
          e.stopPropagation()
          BrowserOpenURL(href)
        })
      }
    })
  })
}

// Highlight diff code blocks: green for added lines, red for removed lines
function enhanceDiffBlocks(container: HTMLElement) {
  const diffBlocks = container.querySelectorAll<HTMLPreElement>('pre code.language-diff')
  diffBlocks.forEach((code) => {
    if (code.getAttribute('data-diff-enhanced')) return
    code.setAttribute('data-diff-enhanced', 'true')
    const lines = (code.textContent || '').split('\n')
    code.innerHTML = lines.map((line) => {
      const escaped = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      if (line.startsWith('+') && !line.startsWith('+++')) {
        return `<span class="diff-add">${escaped}</span>`
      }
      if (line.startsWith('-') && !line.startsWith('---')) {
        return `<span class="diff-del">${escaped}</span>`
      }
      if (line.startsWith('@@')) {
        return `<span class="diff-hunk">${escaped}</span>`
      }
      return escaped
    }).join('\n')
  })
}

// Render message content: split into markdown + mermaid segments
function MessageContent({ content }: { content: string }) {
  const segments = splitContent(content)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (containerRef.current) {
      enhanceCodeBlocks(containerRef.current)
      enhanceDiffBlocks(containerRef.current)
    }
  }, [segments])

  return (
    <div ref={containerRef}>
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
    </div>
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
  reasoningDuration?: number // seconds, set when reasoning completes
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

// ── Code paste detection ────────────────────────────────────────────────────

/** Detects programming language from text content using heuristics. */
function detectCodeLanguage(text: string): string | null {
  const t = text.trim()
  const first50 = t.slice(0, 50)

  // Shebang line
  if (/^#!\/(usr\/)?bin\//.test(t)) {
    if (/bash|sh|zsh/.test(first50)) return 'bash'
    if (/python/.test(first50)) return 'python'
    if (/node/.test(first50)) return 'javascript'
    if (/ruby/.test(first50)) return 'ruby'
    return 'bash'
  }

  // Dockerfile directives
  if (/^(FROM|RUN|COPY|ADD|CMD|ENTRYPOINT|ENV|ARG|WORKDIR|EXPOSE|VOLUME|LABEL|HEALTHCHECK)\s/m.test(t)) return 'dockerfile'

  // Go: package + func + :=
  if (/^package\s+\w+/m.test(t) || /\bfunc\s+(\(\s*\w+\s+\*?\w+\s*\)\s+)?\w+\s*\(/.test(t)) return 'go'

  // Rust
  if (/^(pub\s+)?(fn|impl|trait|use|mod|struct|enum|match)\s/m.test(t)) return 'rust'

  // Python: def/class/import + colon-indent
  if (/^(def |class |import |from )/m.test(t) || /^\s+(if |elif |else:|for |while |try:|except|finally:)/m.test(t)) return 'python'

  // TypeScript/JavaScript: import/export/const/=>/interface
  if (/\b(import|export)\s+/.test(t) || /\binterface\s+\w+/.test(t)) return 'typescript'
  if (/=>/.test(t) || /\bconst\s+\w+\s*=/.test(t) || /\blet\s+\w+\s*=/.test(t)) return 'javascript'

  // Java/C#: class with access modifiers
  if (/\b(public|private|protected)\s+(static\s+)?(class|void|int|String|boolean)\b/.test(t)) return 'java'

  // C/C++: #include + main
  if (/^#include\s+[<\"]/.test(t) || /\bint\s+main\s*\(/.test(t)) return 'cpp'

  // SQL
  if (/\b(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)\s+/i.test(t) && /\b(FROM|INTO|TABLE|SET|WHERE)\b/i.test(t)) return 'sql'

  // HTML/XML
  if (/^<\?xml/.test(t) || /^<!DOCTYPE\s+html/i.test(t) || /^<html/i.test(t)) return 'html'

  // CSS/SCSS: selector { ... }
  if (/^[\w\-.#:\[\]>,\s]+\s*\{[^}]*[:;]/m.test(t)) return 'css'

  // YAML: key: value on multiple lines
  if (/^[\w\-.]+\s*:\s/m.test(t) && /\n[\w\-.]+\s*:\s/m.test(t)) return 'yaml'

  // JSON (starts with { or [)
  if (/^[{[]/.test(t) && /[}\]]\s*$/.test(t)) return 'json'

  // Shell commands
  if (/^(echo |cd |ls |mkdir |rm |cp |mv |sudo |apt |brew |git |npm |yarn |pip |cargo |go |docker |kubectl )/m.test(t)) return 'bash'

  return null
}

/** Heuristic: is this pasted text likely to be source code? */
function isLikelyCode(text: string): { isCode: boolean; language: string | null } {
  const trimmed = text.trim()
  if (trimmed.length < 10) return { isCode: false, language: null }

  // Reject URLs (commonly pasted, can match patterns)
  if (/^https?:\/\//.test(trimmed)) return { isCode: false, language: null }

  const lines = trimmed.split('\n')

  // Strong signal: tab indentation
  if (/\t/.test(text) && lines.length > 1) {
    return { isCode: true, language: detectCodeLanguage(text) }
  }

  // Strong signal: 4+ space indentation on multiple lines
  const indentedLines = lines.filter(l => /^ {4,}\S/.test(l))
  if (indentedLines.length >= 2) {
    return { isCode: true, language: detectCodeLanguage(text) }
  }

  // Language-specific patterns
  const language = detectCodeLanguage(text)
  if (language) {
    return { isCode: true, language }
  }

  // Multiple lines ending with semicolons (C-like)
  const semicolonLines = lines.filter(l => /;\s*$/.test(l))
  if (semicolonLines.length >= 3 && lines.length > 2) {
    return { isCode: true, language: null }
  }

  // Multiple brace lines
  if (/\{/.test(text) && /\}/.test(text)) {
    const braceLines = lines.filter(l => /[{}]/.test(l))
    if (braceLines.length >= 3) {
      return { isCode: true, language: null }
    }
  }

  return { isCode: false, language: null }
}

// ── Slash command definitions ────────────────────────────────────────────────

interface SlashCommand {
  cmd: string
  desc: string
  category: 'chat' | 'config' | 'session' | 'code' | 'system'
}

const SLASH_COMMANDS: SlashCommand[] = [
  { cmd: '/help', desc: 'Show available commands', category: 'system' },
  { cmd: '/model', desc: 'Switch or view AI model', category: 'config' },
  { cmd: '/mode', desc: 'Change permission mode (auto/supervised/plan/bypass)', category: 'config' },
  { cmd: '/clear', desc: 'Clear chat history', category: 'chat' },
  { cmd: '/compact', desc: 'Compact context window', category: 'chat' },
  { cmd: '/context', desc: 'Show context usage details', category: 'chat' },
  { cmd: '/cost', desc: 'Show token usage and cost', category: 'chat' },
  { cmd: '/copy', desc: 'Copy conversation to clipboard', category: 'chat' },
  { cmd: '/export', desc: 'Download conversation as markdown file', category: 'chat' },
  { cmd: '/sessions', desc: 'Browse and switch sessions', category: 'session' },
  { cmd: '/config', desc: 'View or edit configuration', category: 'config' },
  { cmd: '/status', desc: 'Show runtime status', category: 'system' },
  { cmd: '/skills', desc: 'Browse available skills', category: 'system' },
  { cmd: '/mcp', desc: 'Manage MCP servers', category: 'system' },
  { cmd: '/review', desc: 'Review code changes (git diff)', category: 'code' },
  { cmd: '/diff', desc: 'Show working tree diff', category: 'code' },
  { cmd: '/reflect', desc: 'Trigger self-reflection', category: 'system' },
  { cmd: '/undo', desc: 'Undo last file edit', category: 'code' },
  { cmd: '/regenerate', desc: 'Regenerate last response', category: 'chat' },
  { cmd: '/todo', desc: 'Task management', category: 'system' },
  { cmd: '/init', desc: 'Initialize project memory files', category: 'system' },
  { cmd: '/lang', desc: 'Change interface language', category: 'config' },
  { cmd: '/bug', desc: 'Report a bug', category: 'system' },
]

// ── Component ────────────────────────────────────────────────────────────────

export function ChatView({ onShare, sessionId, workspace, onWorkspaceSelected, showToast }: { onShare?: () => void; sessionId?: string; workspace?: string; onWorkspaceSelected?: (dir: string) => void; showToast?: (type: 'success' | 'error' | 'info', message: string) => void }) {
  const { t } = useTranslation()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [agentPanels, setAgentPanels] = useState<Map<string, AgentPanel>>(new Map())
  const [activeTab, setActiveTab] = useState<string>('main') // 'main' or agentID
  const [input, setInput] = useState('')
  const [inputHistory, setInputHistory] = useState<string[]>([])
  const historyIdxRef = useRef(-1)
  const [isStreaming, setIsStreaming] = useState(false)
  const [thinking, setThinking] = useState(false)
  const [historyLoading, setHistoryLoading] = useState(false)
  const [agentElapsed, setAgentElapsed] = useState(0) // seconds since agent started working
  const [statusBar, setStatusBar] = useState<StatusBarState>({
    vendor: '', model: '', mode: 'auto', effort: 'auto', contextUsed: 0, contextTotal: 0, usagePercent: 0, remainingPercent: 0, inputTokens: 0, outputTokens: 0, cacheRead: 0, cacheWrite: 0, cacheHit: 0, status: 'ready',
  })
  const [modelPickerOpen, setModelPickerOpen] = useState(false)
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [teamBoard, setTeamBoard] = useState<TeamBoardSnapshot[]>([])
  const [teamBoardOpen, setTeamBoardOpen] = useState(false)
  const teamBoardDismissedRef = useRef(false)

  // --- Agent working timer ---
  useEffect(() => {
    if (!thinking && !isStreaming) {
      setAgentElapsed(0)
      return
    }
    const start = Date.now()
    const interval = setInterval(() => {
      setAgentElapsed(Math.floor((Date.now() - start) / 1000))
    }, 1000)
    return () => clearInterval(interval)
  }, [thinking, isStreaming])

  // --- In-conversation search ---
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [searchMatchIdx, setSearchMatchIdx] = useState(0)
  const searchInputRef = useRef<HTMLInputElement>(null)

  // --- Slash command autocomplete ---
  const [slashOpen, setSlashOpen] = useState(false)
  const [slashIdx, setSlashIdx] = useState(0)
  const slashListRef = useRef<HTMLDivElement>(null)

  // Filter commands based on current input
  const slashFiltered = useMemo(() => {
    const trimmed = input.trimStart()
    if (!trimmed.startsWith('/')) return []
    // Only trigger when the slash is at the beginning of the line
    const beforeCursor = input.slice(0, inputRef.current?.selectionStart ?? input.length)
    const lineStart = beforeCursor.lastIndexOf('\n') + 1
    const linePrefix = beforeCursor.slice(lineStart)
    if (!linePrefix.startsWith('/')) return []
    // Don't trigger if there's a space (command name complete)
    if (linePrefix.includes(' ')) return []
    const query = linePrefix.toLowerCase()
    return SLASH_COMMANDS.filter(c => c.cmd.startsWith(query))
  }, [input])

  useEffect(() => {
    if (slashFiltered.length > 0 && !slashOpen) {
      setSlashOpen(true)
      setSlashIdx(0)
    } else if (slashFiltered.length === 0 && slashOpen) {
      setSlashOpen(false)
    }
  }, [slashFiltered, slashOpen])

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

  // Helper: remove a completed/failed agent panel (close tab)
  const removeAgentPanel = useCallback((agentID: string) => {
    setAgentPanels(prev => {
      const next = new Map(prev)
      next.delete(agentID)
      return next
    })
    setActiveTab(prev => prev === agentID ? 'main' : prev)
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
          setHistoryLoading(true)
          App.GetSessionHistory().then((history: any[]) => {
            if (cancelled || !history || history.length === 0) { setHistoryLoading(false); return }
            autoScrollByTabRef.current.main = true
            const loaded = materializeHistory(history, [])
            messagesRef.current = loaded
            setMessages(loaded)
            setHistoryLoading(false)
          }).catch(() => { setHistoryLoading(false) })
        }
      }).catch(() => {})
      return () => { cancelled = true }
    }
    // Session changed — clear old messages immediately so stale content
    // from the previous session doesn't linger while loading.
    setMessages([])
    messagesRef.current = []
    setThinking(false)
    setHistoryLoading(true)
    let cancelled = false
    App.GetSessionHistory().then((history: any[]) => {
      if (cancelled || runActiveRef.current) { setHistoryLoading(false); return }
      if (!history || history.length === 0) {
        setHistoryLoading(false)
        return
      }
      autoScrollByTabRef.current.main = true
      const loaded = materializeHistory(history, messagesRef.current)
      if (cancelled || runActiveRef.current) { setHistoryLoading(false); return }
      messagesRef.current = loaded
      setMessages(loaded)
      setHistoryLoading(false)
    }).catch(() => { setHistoryLoading(false) })
    return () => { cancelled = true }
  }, [sessionId])

  // Draft persistence: save/restore unsent input text per session
  useEffect(() => {
    if (!sessionId || !input) return
    try { sessionStorage.setItem(`draft:${sessionId}`, input) } catch {}
  }, [input, sessionId])
  useEffect(() => {
    if (!sessionId) { setInput(''); return }
    try {
      const draft = sessionStorage.getItem(`draft:${sessionId}`)
      setInput(draft ?? '')
    } catch { setInput('') }
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
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const autoScrollByTabRef = useRef<Record<string, boolean>>({ main: true })
  const lastManualScrollAtByTabRef = useRef<Record<string, number>>({})
  const suppressNextScrollEventRef = useRef(false)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  const [unreadCount, setUnreadCount] = useState(0)
  const [contextBannerDismissed, setContextBannerDismissed] = useState(false)
  const prevMsgCountRef = useRef(0)

  // Track unread messages when scrolled up
  useEffect(() => {
    const delta = messages.length - prevMsgCountRef.current
    if (delta > 0 && showScrollBtn) {
      setUnreadCount(c => c + delta)
    }
    prevMsgCountRef.current = messages.length
  }, [messages, showScrollBtn])

  // Reset unread when scrolled back to bottom
  useEffect(() => { if (!showScrollBtn) setUnreadCount(0) }, [showScrollBtn])
  // Re-enable context banner when usage drops back below 80%
  useEffect(() => { if (statusBar.usagePercent < 80) setContextBannerDismissed(false) }, [statusBar.usagePercent])
  const [isDragOver, setIsDragOver] = useState(false)
  const dragCounterRef = useRef(0)
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
          const doneTime = Date.now()
          setMessages(prev => prev.map(m =>
            m.role === 'reasoning' && m.streaming
              ? { ...m, streaming: false, reasoningDuration: Math.max(1, Math.round((doneTime - m.timestamp) / 1000)) }
              : m
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
  }, [sessionId])

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

  // ── Export / Download conversation ───────────────────────────────────────

  const [exported, setExported] = useState(false)
  const handleExportConversation = useCallback(() => {
    const lines = messages.filter(m => !m.toolName && m.content).map(m => {
      const role = m.role === 'user' ? '**User**' : m.agentID ? `**Agent (${m.agentID})**` : '**Assistant**'
      const ts = m.timestamp ? new Date(m.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
      return `${role}${ts ? ` _${ts}_` : ''}\n\n${m.content}\n`
    })
    const text = lines.join('\n---\n\n')
    navigator.clipboard.writeText(text).then(() => {
      setExported(true)
      showToast?.('success', `Copied ${lines.length} messages to clipboard`)
      setTimeout(() => setExported(false), 2000)
    }).catch(() => {
      showToast?.('error', 'Failed to copy conversation')
    })
  }, [messages, showToast])

  const [downloaded, setDownloaded] = useState(false)
  const handleDownloadConversation = useCallback(() => {
    const lines = messages.filter(m => !m.toolName && m.content).map(m => {
      const role = m.role === 'user' ? '**User**' : m.agentID ? `**Agent (${m.agentID})**` : '**Assistant**'
      const ts = m.timestamp
        ? new Date(m.timestamp).toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
        : ''
      return `${role}${ts ? ` _${ts}_` : ''}\n\n${m.content}\n`
    })
    const header = `# ggcode Conversation\n\nExported: ${new Date().toLocaleString()}\nMessages: ${lines.length}\n\n---\n\n`
    const text = header + lines.join('\n---\n\n')
    const blob = new Blob([text], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const now = new Date()
    const stamp = `${now.getFullYear()}${String(now.getMonth() + 1).padStart(2, '0')}${String(now.getDate()).padStart(2, '0')}-${String(now.getHours()).padStart(2, '0')}${String(now.getMinutes()).padStart(2, '0')}`
    a.download = `ggcode-conversation-${stamp}.md`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    setDownloaded(true)
    showToast?.('success', `Downloaded ${lines.length} messages as markdown`)
    setTimeout(() => setDownloaded(false), 2000)
  }, [messages, showToast])

  const handleSend = useCallback(async () => {
    const text = input.trim()
    if (!text && pastedImages.length === 0) return

    // Save to input history (dedup consecutive, max 50)
    if (text) {
      setInputHistory(prev => {
        if (prev.length > 0 && prev[prev.length - 1] === text) return prev
        return [...prev.slice(-49), text]
      })
    }
    historyIdxRef.current = -1

    // Intercept frontend-only slash commands
    if (text === '/copy' || text === '/export') {
      setInput('')
      if (sessionId) { try { sessionStorage.removeItem(`draft:${sessionId}`) } catch {} }
      if (inputRef.current) { inputRef.current.style.height = 'auto' }
      if (text === '/copy') handleExportConversation()
      else handleDownloadConversation()
      return
    }

    const images = pastedImages
    setInput('')
    if (sessionId) { try { sessionStorage.removeItem(`draft:${sessionId}`) } catch {} }
    setPastedImages([])
    // Reset textarea height after clearing
    if (inputRef.current) {
      inputRef.current.style.height = 'auto'
    }
    await sendUserText(text || 'Please analyze this image.', undefined, images)
  }, [input, pastedImages, sendUserText, handleExportConversation, handleDownloadConversation, sessionId])

  // ── Drag-and-drop image support ────────────────────────────────────────────

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer?.types?.includes('Files')) return
    e.preventDefault()
    dragCounterRef.current++
    setIsDragOver(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounterRef.current--
    if (dragCounterRef.current <= 0) {
      dragCounterRef.current = 0
      setIsDragOver(false)
    }
  }, [])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    if (!e.dataTransfer?.types?.includes('Files')) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
  }, [])

  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleFilePick = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || [])
    const imageFiles = files.filter(f => f.type.startsWith('image/'))
    // Reset input so the same file can be selected again
    e.target.value = ''
    if (imageFiles.length === 0) return
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
          name: file.name || 'attached-image',
        })
      }
      reader.onerror = () => reject(reader.error || new Error('Failed to read attached image'))
      reader.readAsDataURL(file)
    }))).then(images => {
      setPastedImages(prev => [...prev, ...images])
    }).catch(err => {
      showToast?.('error', err?.message || 'Failed to process attached image')
    })
  }, [showToast])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounterRef.current = 0
    setIsDragOver(false)
    const files = Array.from(e.dataTransfer?.files || [])
    const imageFiles = files.filter(f => f.type.startsWith('image/'))
    if (imageFiles.length === 0) return
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
          name: file.name || 'dropped-image',
        })
      }
      reader.onerror = () => reject(reader.error || new Error('Failed to read dropped image'))
      reader.readAsDataURL(file)
    }))).then(images => {
      setPastedImages(prev => [...prev, ...images])
    }).catch(err => {
      showToast?.('error', err?.message || 'Failed to process dropped image')
    })
  }, [showToast])

  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = Array.from(e.clipboardData?.items || [])
    const imageFiles = items
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter((file): file is File => !!file)
    if (imageFiles.length > 0) {
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
      return
    }

    // ── Code paste detection ──
    const text = e.clipboardData?.getData('text/plain') || ''
    if (text.trim().length < 10) return // too short to be code

    const detection = isLikelyCode(text)
    if (!detection.isCode) return // normal text paste

    e.preventDefault()
    const lang = detection.language || ''
    const needsLeadingNewline = input.length > 0 && !input.endsWith('\n')
    const fenced = `${needsLeadingNewline ? '\n' : ''}\`\`\`${lang}\n${text}\n\`\`\`\n`

    const textarea = inputRef.current
    if (textarea) {
      const start = textarea.selectionStart
      const end = textarea.selectionEnd
      const newValue = input.slice(0, start) + fenced + input.slice(end)
      setInput(newValue)
      requestAnimationFrame(() => {
        textarea.focus()
        const pos = start + fenced.length
        textarea.setSelectionRange(pos, pos)
      })
    } else {
      setInput(prev => prev + fenced)
    }

    showToast?.('info', `Code detected${detection.language ? ` (${detection.language})` : ''}, wrapped in code block`)
  }, [showToast, input])

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

  // Edit & resend: populate input with original message text for user to modify
  const handleEditMessage = useCallback((text: string) => {
    setInput(text)
    // Focus and move cursor to end
    requestAnimationFrame(() => {
      const el = inputRef.current
      if (el) {
        el.focus()
        el.setSelectionRange(text.length, text.length)
        // Trigger auto-resize
        el.style.height = 'auto'
        el.style.height = Math.min(el.scrollHeight, 200) + 'px'
      }
    })
  }, [])

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
    // Slash autocomplete navigation
    if (slashOpen && slashFiltered.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSlashIdx(i => (i + 1) % slashFiltered.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSlashIdx(i => (i - 1 + slashFiltered.length) % slashFiltered.length)
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        const sel = slashFiltered[slashIdx]
        if (sel) {
          setInput(prev => sel.cmd + ' ')
          setSlashOpen(false)
        }
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setSlashOpen(false)
        return
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'g') {
      e.preventDefault()
      void cycleReasoningEffort()
      return
    }
    // Input history navigation (up/down arrows when not in slash menu)
    if (!slashOpen && inputHistory.length > 0) {
      const el = e.currentTarget as HTMLTextAreaElement
      const atStart = el.selectionStart === 0 && el.selectionEnd === 0
      const atEnd = el.selectionStart === input.length && el.selectionEnd === input.length
      if (e.key === 'ArrowUp' && atStart) {
        e.preventDefault()
        if (historyIdxRef.current === -1) {
          historyIdxRef.current = inputHistory.length - 1
        } else if (historyIdxRef.current > 0) {
          historyIdxRef.current--
        }
        const h = inputHistory[historyIdxRef.current]
        setInput(h)
        requestAnimationFrame(() => {
          if (inputRef.current) {
            inputRef.current.style.height = 'auto'
            inputRef.current.style.height = Math.min(inputRef.current.scrollHeight, 200) + 'px'
            inputRef.current.setSelectionRange(h.length, h.length)
          }
        })
        return
      }
      if (e.key === 'ArrowDown' && atEnd && historyIdxRef.current !== -1) {
        e.preventDefault()
        if (historyIdxRef.current < inputHistory.length - 1) {
          historyIdxRef.current++
          const h = inputHistory[historyIdxRef.current]
          setInput(h)
          requestAnimationFrame(() => {
            if (inputRef.current) {
              inputRef.current.style.height = 'auto'
              inputRef.current.style.height = Math.min(inputRef.current.scrollHeight, 200) + 'px'
              inputRef.current.setSelectionRange(h.length, h.length)
            }
          })
        } else {
          historyIdxRef.current = -1
          setInput('')
          if (inputRef.current) { inputRef.current.style.height = 'auto' }
        }
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }, [cycleReasoningEffort, handleSend, slashOpen, slashFiltered, slashIdx, inputHistory, input])

  // ── In-conversation search ────────────────────────────────────────────────

  // Compute matches from current messages
  const searchMatches = useMemo(() => {
    if (!searchQuery.trim()) return [] as number[] // indices into messages array
    const q = searchQuery.toLowerCase()
    return messages
      .map((msg, i) => ({ idx: i, content: msg.content }))
      .filter(m => m.content.toLowerCase().includes(q))
      .map(m => m.idx)
  }, [messages, searchQuery])

  // Compute sets for persistent highlighting (message IDs)
  const searchMatchIds = useMemo(() => {
    if (!searchQuery.trim()) return new Set<string>()
    return new Set(searchMatches.map(idx => messages[idx]?.id).filter(Boolean) as string[])
  }, [searchMatches, messages, searchQuery])

  const activeSearchMatchId = useMemo(() => {
    if (searchMatches.length === 0) return null
    const idx = searchMatches[Math.min(searchMatchIdx, searchMatches.length - 1)]
    return messages[idx]?.id ?? null
  }, [searchMatches, searchMatchIdx, messages])

  // Reset match index when query changes
  useEffect(() => { setSearchMatchIdx(0) }, [searchQuery])

  // Focus search input when opened
  useEffect(() => {
    if (searchOpen) {
      searchInputRef.current?.focus()
      searchInputRef.current?.select()
    }
  }, [searchOpen])

  // Global Cmd+F / Ctrl+F to open search, Esc to close
  // Cmd+Shift+Down/Up to scroll to bottom/top of conversation
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        e.stopPropagation()
        setSearchOpen(prev => !prev)
        return
      }
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'ArrowDown') {
        e.preventDefault()
        const c = scrollContainerRef.current
        if (c) c.scrollTo({ top: c.scrollHeight, behavior: 'smooth' })
        return
      }
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'ArrowUp') {
        e.preventDefault()
        const c = scrollContainerRef.current
        if (c) c.scrollTo({ top: 0, behavior: 'smooth' })
        return
      }
      if (e.key === 'Escape' && searchOpen) {
        e.preventDefault()
        setSearchOpen(false)
      }
    }
    document.addEventListener('keydown', handler, true)
    return () => document.removeEventListener('keydown', handler, true)
  }, [searchOpen])

  // Scroll current match into view
  useEffect(() => {
    if (!searchOpen || searchMatches.length === 0) return
    const idx = searchMatches[Math.min(searchMatchIdx, searchMatches.length - 1)]
    if (idx === undefined) return
    const msgId = messages[idx]?.id
    if (!msgId) return
    const el = document.querySelector(`[data-msg-id="${msgId}"]`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  }, [searchMatchIdx, searchMatches, searchOpen, messages])

  const searchNext = useCallback(() => {
    if (searchMatches.length === 0) return
    setSearchMatchIdx(prev => (prev + 1) % searchMatches.length)
  }, [searchMatches.length])

  const searchPrev = useCallback(() => {
    if (searchMatches.length === 0) return
    setSearchMatchIdx(prev => (prev - 1 + searchMatches.length) % searchMatches.length)
  }, [searchMatches.length])

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
      <div
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
        style={{ display: 'flex', flexDirection: 'column', height: '100%', minWidth: 0, flex: 1, position: 'relative' }}>
      {/* Drop zone overlay */}
      {isDragOver && (
        <div style={{
          position: 'absolute', inset: 0, zIndex: 999,
          background: 'rgba(59, 130, 246, 0.08)',
          border: '2px dashed var(--color-primary)',
          borderRadius: 'var(--radius-lg)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          pointerEvents: 'none',
        }}>
          <div style={{
            padding: 'var(--spacing-lg) var(--spacing-xl)',
            borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-primary)',
            fontSize: 14, fontWeight: 600,
            color: 'var(--color-primary)',
          }}>
            Drop images to attach
          </div>
        </div>
      )}
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
        <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 6 }}>
          {(statusLabel === 'Working...' || statusLabel === 'Thinking...') && (
            <span style={{
              display: 'inline-block', width: 6, height: 6, borderRadius: '50%',
              background: 'var(--color-warning)',
              animation: 'pulse 1.2s ease-in-out infinite',
            }} />
          )}
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

        {messages.length > 0 && (
          <span style={{
            fontSize: 10, color: 'var(--text-tertiary)',
            fontFamily: 'var(--font-mono)', opacity: 0.7,
          }}>
            {messages.length} {messages.length === 1 ? 'msg' : 'msgs'}
          </span>
        )}
        {messages.length > 0 && (
          <>
            <button
              onClick={() => setSearchOpen(prev => !prev)}
              title="Search in conversation (Cmd+F)"
              style={{
                width: 28, height: 28, borderRadius: 'var(--radius-sm)',
                background: searchOpen ? 'color-mix(in srgb, var(--color-primary) 15%, transparent)' : 'var(--color-surface)',
                border: '1px solid ' + (searchOpen ? 'var(--color-primary)' : 'transparent'),
                color: searchOpen ? 'var(--color-primary)' : 'var(--text-secondary)',
                cursor: 'pointer',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                transition: 'all 0.15s',
              }}
            >
              <Search size={14} />
            </button>
            <button onClick={handleExportConversation} title="Copy conversation to clipboard" style={{
              width: 28, height: 28, borderRadius: 'var(--radius-sm)',
              background: exported ? 'rgba(34,197,94,0.15)' : 'var(--color-surface)',
              border: '1px solid ' + (exported ? 'var(--color-success)' : 'transparent'),
              color: exported ? 'var(--color-success)' : 'var(--text-secondary)',
              cursor: 'pointer',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              transition: 'all 0.15s',
            }}>
              {exported ? <Check size={14} /> : <ClipboardCopy size={14} />}
            </button>
            <button onClick={handleDownloadConversation} title="Download as markdown file" style={{
              width: 28, height: 28, borderRadius: 'var(--radius-sm)',
              background: downloaded ? 'rgba(34,197,94,0.15)' : 'var(--color-surface)',
              border: '1px solid ' + (downloaded ? 'var(--color-success)' : 'transparent'),
              color: downloaded ? 'var(--color-success)' : 'var(--text-secondary)',
              cursor: 'pointer',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              transition: 'all 0.15s',
            }}>
              {downloaded ? <Check size={14} /> : <Download size={14} />}
            </button>
          </>
        )}
        {onShare && (
          <button onClick={onShare} title="Share session (Cmd+Shift+S)" style={{
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
              label={panel.name}
              active={activeTab === panel.id}
              onClick={() => setActiveTab(panel.id)}
              status={panel.status}
              title={panel.task || panel.status}
              onClose={() => removeAgentPanel(panel.id)}
            />
          ))}
        </div>
      )}

      {/* Context window warning banner */}
      {statusBar.usagePercent > 80 && !contextBannerDismissed && (
        <div style={{
          padding: '6px var(--spacing-lg)',
          background: statusBar.usagePercent > 90 ? 'rgba(239, 68, 68, 0.12)' : 'rgba(245, 158, 11, 0.10)',
          borderBottom: '1px solid var(--color-border)',
          display: 'flex', alignItems: 'center', gap: 8,
          fontSize: 12,
        }}>
          <span style={{ fontSize: 14 }}>{statusBar.usagePercent > 90 ? '🔴' : '🟡'}</span>
          <span style={{ color: statusBar.usagePercent > 90 ? 'var(--color-error)' : 'var(--color-warning)', flex: 1 }}>
            {t(
              statusBar.usagePercent > 90 ? 'context.critical' : 'context.warning',
              { pct: Math.round(statusBar.usagePercent) }
            )}
          </span>
          <button onClick={() => setContextBannerDismissed(true)} style={{
            background: 'none', border: 'none',
            color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 13,
            padding: '0 4px',
          }}>✕</button>
        </div>
      )}

      {/* In-conversation search bar */}
      {searchOpen && (
        <div style={{
          position: 'absolute', top: 8, right: 12, zIndex: 200,
          display: 'flex', alignItems: 'center', gap: 0,
          background: 'var(--color-card)', border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius-md)', boxShadow: '0 4px 16px rgba(0,0,0,0.35)',
          padding: '2px 4px 2px 8px', maxWidth: 340,
        }}>
          <Search size={13} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} />
          <input
            ref={searchInputRef}
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); e.shiftKey ? searchPrev() : searchNext() }
              if (e.key === 'Escape') { setSearchOpen(false) }
            }}
            placeholder="Find in conversation..."
            style={{
              background: 'none', border: 'none', outline: 'none',
              color: 'var(--text-primary)', fontSize: 13,
              fontFamily: 'var(--font-mono)',
              padding: '4px 6px', width: 140, minWidth: 0,
            }}
          />
          {searchQuery.trim() && (
            <span style={{
              fontSize: 11, color: 'var(--text-tertiary)',
              fontFamily: 'var(--font-mono)', whiteSpace: 'nowrap',
              padding: '0 4px',
            }}>
              {searchMatches.length > 0
                ? `${Math.min(searchMatchIdx + 1, searchMatches.length)}/${searchMatches.length}`
                : '0/0'}
            </span>
          )}
          <button onClick={searchPrev} disabled={searchMatches.length === 0} title="Previous (Shift+Enter)" style={{
            background: 'none', border: 'none', cursor: searchMatches.length ? 'pointer' : 'default',
            color: searchMatches.length ? 'var(--text-secondary)' : 'var(--text-tertiary)',
            padding: '3px', display: 'flex', alignItems: 'center', justifyContent: 'center',
            opacity: searchMatches.length ? 1 : 0.4,
          }}>
            <ChevronUp size={15} />
          </button>
          <button onClick={searchNext} disabled={searchMatches.length === 0} title="Next (Enter)" style={{
            background: 'none', border: 'none', cursor: searchMatches.length ? 'pointer' : 'default',
            color: searchMatches.length ? 'var(--text-secondary)' : 'var(--text-tertiary)',
            padding: '3px', display: 'flex', alignItems: 'center', justifyContent: 'center',
            opacity: searchMatches.length ? 1 : 0.4,
          }}>
            <ChevronDown size={15} />
          </button>
          <button onClick={() => setSearchOpen(false)} title="Close (Esc)" style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--text-secondary)', padding: '3px',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <X size={14} />
          </button>
        </div>
      )}

      {/* Messages — render active tab's content */}
      <div
        ref={scrollContainerRef}
        role="log"
        aria-live="polite"
        aria-atomic="false"
        onScroll={() => {
          if (suppressNextScrollEventRef.current) {
            suppressNextScrollEventRef.current = false
            return
          }
          const nearBottom = isNearBottom(scrollContainerRef.current)
          autoScrollByTabRef.current[activeTab] = nearBottom
          lastManualScrollAtByTabRef.current[activeTab] = Date.now()
          setShowScrollBtn(!nearBottom && messages.length > 3)
        }}
        style={{
        flex: 1, overflowY: 'auto',
        padding: 'var(--spacing-lg)',
        display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      }}>
        {activeTab === 'main' ? (
          // Main chat messages
          <>
            {historyLoading && <SkeletonMessages count={5} />}
            {messages.length === 0 && !thinking && !historyLoading && (
              <WelcomeScreen workspace={workspace} onPick={(text) => {
                setInput(text)
                inputRef.current?.focus()
              }} />
            )}
            {messages.map((msg, i) => {
              const showSep = i === 0 || (() => {
                const prev = messages[i - 1]
                if (!prev?.timestamp || !msg.timestamp) return false
                const prevDay = new Date(prev.timestamp); prevDay.setHours(0, 0, 0, 0)
                const currDay = new Date(msg.timestamp); currDay.setHours(0, 0, 0, 0)
                return prevDay.getTime() !== currDay.getTime()
              })()
              return (
                <div
                  key={msg.id}
                  className={[
                    searchMatchIds.has(msg.id) ? 'search-match-highlight' : '',
                    activeSearchMatchId === msg.id ? 'search-active-match' : '',
                  ].filter(Boolean).join(' ')}
                >
                  {showSep && msg.timestamp && <DateSeparator label={dateLabel(msg.timestamp)} />}
                  {(() => {
                    // Consecutive message grouping: same role + within 2 min of previous message
                    const prevMsg = i > 0 ? messages[i - 1] : null
                    const isContinuation = prevMsg &&
                      prevMsg.role === msg.role &&
                      (msg.agentID || '') === (prevMsg.agentID || '') &&
                      msg.timestamp && prevMsg.timestamp &&
                      (msg.timestamp - prevMsg.timestamp) < 120000
                    // Only show hover timestamp on first message in group
                    const showTimestamp = msg.timestamp && !isContinuation
                    return <>
                      {showTimestamp && (
                        <span className="msg-timestamp" title={new Date(msg.timestamp).toLocaleString()}>
                          {new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                        </span>
                      )}
                      <div className={isContinuation ? 'message-row message-row--continuation' : 'message-row'} data-msg-id={msg.id}>
                        <MessageCard msg={msg} onRetry={handleRetrySend} onEdit={handleEditMessage} />
                      </div>
                    </>
                  })()}
                </div>
              )
            })}
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

        {/* Scroll-to-bottom floating button */}
        {showScrollBtn && (
          <button
            onClick={() => {
              autoScrollByTabRef.current[activeTab] = true
              const container = scrollContainerRef.current
              if (container) {
                suppressNextScrollEventRef.current = true
                container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
              }
              setShowScrollBtn(false)
            }}
            style={{
              position: 'absolute',
              bottom: 80,
              right: 20,
              width: 36, height: 36,
              borderRadius: '50%',
              background: 'var(--color-card)',
              border: '1px solid var(--color-border)',
              boxShadow: '0 2px 8px rgba(0,0,0,0.3)',
              color: 'var(--text-secondary)',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              zIndex: 50,
              transition: 'opacity 0.2s, transform 0.2s',
            }}
            title="Scroll to bottom"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="6 9 12 15 18 9" />
            </svg>
            {unreadCount > 0 && (
              <span style={{
                position: 'absolute', top: -4, right: -4,
                minWidth: 16, height: 16, padding: '0 4px',
                borderRadius: 8,
                background: 'var(--color-primary)',
                color: '#fff',
                fontSize: 10, fontWeight: 600, lineHeight: '16px',
                textAlign: 'center',
                boxShadow: '0 1px 4px rgba(0,0,0,0.3)',
              }}>
                {unreadCount > 99 ? '99+' : unreadCount}
              </span>
            )}
          </button>
        )}

        {/* Typing indicator — appears while agent is thinking/working before first token */}
        {(thinking || (isStreaming && !streamingMsgID.current)) && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10,
            padding: '8px 0',
          }}>
            <div style={{
              fontSize: 11, fontWeight: 600,
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
            {/* Animated typing dots */}
            <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
              {[0, 1, 2].map(i => (
                <span key={i} style={{
                  width: 7, height: 7, borderRadius: '50%',
                  background: 'var(--text-tertiary)',
                  animation: `typBounce 1.2s ease-in-out ${i * 0.15}s infinite`,
                }} />
              ))}
            </div>
            <span style={{ color: 'var(--text-tertiary)', fontStyle: 'italic', fontSize: 13 }}>
              {thinking ? 'Thinking...' : 'Working...'}
            </span>
            {agentElapsed > 0 && (
              <span style={{
                color: 'var(--text-tertiary)', fontSize: 11,
                fontFamily: 'var(--font-mono)',
                background: 'var(--color-surface)',
                padding: '1px 6px', borderRadius: 4,
                border: '1px solid var(--color-border)',
              }}>
                {agentElapsed < 60 ? `${agentElapsed}s` : `${Math.floor(agentElapsed / 60)}m${agentElapsed % 60}s`}
              </span>
            )}
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
        position: 'relative',
        padding: 'var(--spacing-md) var(--spacing-lg)',
        borderTop: '1px solid var(--color-border)',
        display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'flex-end',
        flexShrink: 0,
      }}>
        {/* Slash command autocomplete dropdown */}
        {slashOpen && slashFiltered.length > 0 && (
          <div ref={slashListRef} style={{
            position: 'absolute', bottom: '100%', left: 'var(--spacing-lg)',
            width: 320, maxHeight: 280, overflowY: 'auto',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
            boxShadow: '0 -4px 16px rgba(0,0,0,0.15)',
            zIndex: 100, marginBottom: 4,
          }}>
            {slashFiltered.map((c, i) => (
              <div key={c.cmd} onClick={() => {
                setInput(c.cmd + ' ')
                setSlashOpen(false)
                inputRef.current?.focus()
              }} style={{
                padding: '8px 12px', cursor: 'pointer',
                display: 'flex', alignItems: 'center', gap: 8,
                background: i === slashIdx ? 'var(--color-surface)' : 'transparent',
                borderBottom: i < slashFiltered.length - 1 ? '1px solid var(--color-border)' : 'none',
              }}>
                <code style={{ color: 'var(--color-primary)', fontSize: 13, fontWeight: 600 }}>{c.cmd}</code>
                <span style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>{c.desc}</span>
              </div>
            ))}
          </div>
        )}
        {/* Hidden file input for image attachment */}
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          multiple
          style={{ display: 'none' }}
          onChange={handleFilePick}
        />
        <button type="button" onClick={() => fileInputRef.current?.click()} title="Attach image" style={{
          width: 36, height: 36, borderRadius: 'var(--radius-lg)',
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--text-tertiary)',
          transition: 'background 0.15s',
        }}>
          <ImagePlus size={16} />
        </button>
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
        <textarea
          ref={inputRef}
          value={input}
          rows={1}
          onChange={e => {
            setInput(e.target.value)
            // Auto-resize: reset to scrollHeight, capped at 200px
            const el = e.target
            el.style.height = 'auto'
            el.style.height = Math.min(el.scrollHeight, 200) + 'px'
          }}
          onPaste={handlePaste}
          onKeyDown={handleKeyDown}
          placeholder={isStreaming ? t('chat.agentWorking') : t('chat.placeholder')}
          style={{
            flex: 1, minHeight: 40, maxHeight: 200, padding: '8px var(--spacing-md)',
            borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 13, lineHeight: 1.5, resize: 'none', overflowY: 'auto',
            fontFamily: 'inherit',
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
        <button onClick={handleSend} disabled={!input.trim() && pastedImages.length === 0} title="Send message (Enter)" style={{
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
      <div style={{
        padding: '0 var(--spacing-lg) var(--spacing-xs)',
        textAlign: 'right', fontSize: 10,
        color: 'var(--text-tertiary)', opacity: 0.6,
        pointerEvents: 'none', userSelect: 'none',
      }}>
        {t('chat.inputHint')}
        {input.length > 0 && (
          <span style={{ marginLeft: 8, color: input.length > 4000 ? 'var(--color-error)' : input.length > 2000 ? 'var(--color-warning)' : 'var(--text-tertiary)' }}>
            {input.length > 1000 ? `~${Math.ceil(input.length / 4)} tok · ` : ''}{input.length} chars
          </span>
        )}
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
        @keyframes typBounce {
          0%, 60%, 100% { transform: translateY(0); opacity: 0.4; }
          30% { transform: translateY(-6px); opacity: 1; }
        }
      `}</style>
      </div>
      {teamBoardOpen && (
        <TeamBoard teams={teamBoard} onClose={closeTeamBoard} onSelectTeammate={selectTeammatePanel} />
      )}
    </div>
  )
}

// ── Date separator ───────────────────────────────────────────────────────────

function dateLabel(ts: number): string {
  const d = new Date(ts)
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 86400000)
  const msgDay = new Date(d.getFullYear(), d.getMonth(), d.getDate())
  if (msgDay.getTime() === today.getTime()) return 'Today'
  if (msgDay.getTime() === yesterday.getTime()) return 'Yesterday'
  return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: msgDay.getFullYear() !== now.getFullYear() ? 'numeric' : undefined })
}

function DateSeparator({ label }: { label: string }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
      margin: 'var(--spacing-sm) 0', flexShrink: 0,
    }}>
      <div style={{ flex: 1, height: 1, background: 'var(--color-border)' }} />
      <span style={{
        fontSize: 11, color: 'var(--text-tertiary)',
        fontWeight: 500, whiteSpace: 'nowrap' as const,
      }}>{label}</span>
      <div style={{ flex: 1, height: 1, background: 'var(--color-border)' }} />
    </div>
  )
}

// ── Welcome screen (empty state) ─────────────────────────────────────────────

function WelcomeScreen({ onPick, workspace }: { onPick: (text: string) => void; workspace?: string }) {
  const { t } = useTranslation()
  const prompts = [
    { icon: '🔍', text: t('chat.welcome.prompt.explain') },
    { icon: '📝', text: t('chat.welcome.prompt.review') },
    { icon: '🧪', text: t('chat.welcome.prompt.test') },
    { icon: '🐛', text: t('chat.welcome.prompt.debug') },
  ]

  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      gap: 'var(--spacing-lg)', padding: 'var(--spacing-xl)',
      opacity: 0,
      animation: 'fadeIn 0.3s ease forwards',
    }}>
      <style>{`@keyframes fadeIn { to { opacity: 1; } }`}</style>
      {/* Logo/icon */}
      <div style={{
        width: 64, height: 64, borderRadius: '50%',
        background: 'var(--color-primary)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: 32, color: '#fff', fontWeight: 700,
        boxShadow: '0 4px 24px rgba(59, 130, 246, 0.25)',
      }}>
        G
      </div>
      <div style={{ textAlign: 'center', maxWidth: 480 }}>
        <h2 style={{
          fontSize: 22, fontWeight: 600,
          color: 'var(--text-primary)',
          margin: 0, marginBottom: 6,
        }}>
          {t('chat.welcome.title')}
        </h2>
        <p style={{
          fontSize: 13, color: 'var(--text-tertiary)',
          margin: 0,
        }}>
          {workspace ? `${workspace} • ` : ''}{t('chat.welcome.subtitle')}
        </p>
      </div>
      {/* Quick-start prompts */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr',
        gap: 'var(--spacing-sm)',
        maxWidth: 520, width: '100%',
      }}>
        {prompts.map((p, i) => (
          <button
            key={i}
            onClick={() => onPick(p.text)}
            style={{
              padding: 'var(--spacing-sm) var(--spacing-md)',
              borderRadius: 'var(--radius-lg)',
              background: 'var(--color-card)',
              border: '1px solid var(--color-border)',
              color: 'var(--text-secondary)',
              cursor: 'pointer',
              fontSize: 13,
              textAlign: 'left',
              display: 'flex', alignItems: 'center', gap: 8,
              transition: 'all 0.15s ease',
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-primary)'
              e.currentTarget.style.background = 'var(--color-surface)'
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-border)'
              e.currentTarget.style.background = 'var(--color-card)'
            }}
          >
            <span style={{ fontSize: 16 }}>{p.icon}</span>
            {p.text}
          </button>
        ))}
      </div>
    </div>
  )
}

// ── MessageCard ──────────────────────────────────────────────────────────────

// ── Tab button component ──
function TabButton({ label, active, onClick, color, status, title, onClose }: {
  label: string; active: boolean; onClick: () => void; color?: string;
  status?: 'running' | 'completed' | 'failed' | 'idle';
  title?: string; onClose?: () => void;
}) {
  const [hovered, setHovered] = useState(false)
  const dotColor = status === 'running' ? 'var(--color-warning)' : status === 'completed' ? 'var(--color-success)' : status === 'failed' ? 'var(--color-error)' : undefined
  return (
    <div
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      title={title}
      style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: '6px 10px', cursor: 'pointer', position: 'relative',
        fontSize: 12, fontWeight: active ? 600 : 400,
        color: active ? (color || 'var(--text-primary)') : 'var(--text-tertiary)',
        background: active ? 'var(--color-card)' : 'transparent',
        borderBottom: active ? '2px solid var(--color-primary)' : '2px solid transparent',
        whiteSpace: 'nowrap' as const,
        transition: 'background 0.15s',
        ...(hovered && !active ? { background: 'var(--color-card)' } : {}),
      }}
    >
      {dotColor && (
        <span
          className={status === 'running' ? 'agent-status-dot' : undefined}
          style={{
            width: 7, height: 7, borderRadius: '50%',
            background: dotColor, flexShrink: 0,
            display: 'inline-block',
          }}
        />
      )}
      <span>{label}</span>
      {onClose && status !== 'running' && hovered && (
        <span
          onClick={(e) => { e.stopPropagation(); onClose() }}
          style={{
            marginLeft: 2, cursor: 'pointer', fontSize: 14, lineHeight: 1,
            color: 'var(--text-tertiary)', display: 'flex', alignItems: 'center',
          }}
        >
          ×
        </span>
      )}
    </div>
  )
}

function MessageCard({ msg, onRetry, onEdit }: {
    msg: ChatMessage;
    onRetry?: (id: string, text: string, images?: PastedImageAttachment[]) => void;
    onEdit?: (text: string) => void;
  }) {
  switch (msg.role) {
    case 'user':
      return <UserMessage msg={msg} onRetry={onRetry} onEdit={onEdit} />
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

function ReasoningBlock({ text, defaultOpen = false, label = 'Reasoning', streaming = false, durationSec }: {
  text: string; defaultOpen?: boolean; label?: string; streaming?: boolean; durationSec?: number
}) {
  const [open, setOpen] = useState(defaultOpen)
  const wasStreamingRef = useRef(false)

  // Auto-collapse when streaming transitions from true → false
  useEffect(() => {
    if (wasStreamingRef.current && !streaming) {
      setOpen(false)
    }
    wasStreamingRef.current = streaming
  }, [streaming])

  const displayLabel = streaming
    ? 'Thinking...'
    : (durationSec ? `Thought for ${durationSec}s` : label)

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
        {displayLabel}
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
          <div className="markdown-body" style={{ fontSize: 'var(--font-size-small)' }} dangerouslySetInnerHTML={{ __html: safeMarkdown(text) }} />
        </div>
      )}
    </div>
  )
}

function ReasoningMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{ maxWidth: '85%', alignSelf: 'flex-start' }}>
      <ReasoningBlock
        text={msg.content}
        defaultOpen={true}
        streaming={msg.streaming}
        durationSec={msg.reasoningDuration}
      />
    </div>
  )
}

function UserMessage({ msg, onRetry, onEdit }: {
    msg: ChatMessage;
    onRetry?: (id: string, text: string, images?: PastedImageAttachment[]) => void;
    onEdit?: (text: string) => void;
  }) {
  const failed = msg.deliveryStatus === 'failed'
  const pending = msg.deliveryStatus === 'pending'
  const isLanChat = msg.source === 'lanchat' || (typeof msg.content === 'string' && msg.content.includes('[LAN Chat from '))
  const isMarkdown = isLanChat || msg.source === 'im' || msg.source === 'mobile'
  const [copied, setCopied] = useState(false)
  const [hovered, setHovered] = useState(false)

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(msg.content).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }, [msg.content])

  return (
    <div
      style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', maxWidth: '80%', alignSelf: 'flex-end' }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
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
          ? <div className="markdown-body" style={{ fontSize: 'var(--font-size-base)' }} dangerouslySetInnerHTML={{ __html: safeMarkdown(msg.content) }} />
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
      {(msg.timestamp || msg.content) && !pending && !failed && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginTop: 2, marginRight: 4 }}>
          {msg.timestamp && (
            <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
              {formatTimestamp(msg.timestamp)}
            </span>
          )}
          {msg.content && onEdit && (
            <button
              onClick={() => onEdit(msg.content)}
              title="Edit & resend"
              style={{
                padding: '1px 6px', borderRadius: 3,
                background: 'transparent',
                border: '1px solid var(--color-border)',
                color: 'var(--text-tertiary)',
                cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
                opacity: hovered ? 1 : 0,
                transition: 'opacity 0.15s ease',
              }}
            >
              Edit
            </button>
          )}
          {msg.content && (
            <button
              onClick={handleCopy}
              title="Copy message"
              style={{
                padding: '1px 6px', borderRadius: 3,
                background: copied ? 'rgba(34,197,94,0.15)' : 'transparent',
                border: '1px solid var(--color-border)',
                color: copied ? 'var(--color-success)' : 'var(--text-tertiary)',
                cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
                opacity: hovered || copied ? 1 : 0,
                transition: 'opacity 0.15s ease',
              }}
            >
              {copied ? '✓' : 'Copy'}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

function formatTimestamp(ts: number): string {
  if (!ts) return ''
  const d = new Date(ts)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function AssistantMessage({ msg }: { msg: ChatMessage }) {
  const isSubAgent = !!msg.agentID
  const [copied, setCopied] = useState(false)
  const [hovered, setHovered] = useState(false)

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(msg.content).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }, [msg.content])

  const handleRegenerate = useCallback(() => {
    App.SendMessage('/regenerate').catch(() => {})
  }, [])

  return (
    <div
      style={{ maxWidth: '85%', alignSelf: 'flex-start', position: 'relative' }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div className="msg-role-label" style={{
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
        {/* Action buttons — appear on hover */}
        {!msg.streaming && msg.content && (
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 4, opacity: hovered || copied ? 1 : 0, transition: 'opacity 0.15s ease' }}>
            <button
              onClick={handleRegenerate}
              title="Regenerate response"
              style={{
                padding: '1px 6px', borderRadius: 3,
                background: 'transparent',
                border: '1px solid var(--color-border)',
                color: 'var(--text-tertiary)',
                cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
              }}
            >
              ⟳ Regenerate
            </button>
            <button
              onClick={handleCopy}
              title="Copy message"
              style={{
                padding: '1px 6px', borderRadius: 3,
                background: copied ? 'rgba(34,197,94,0.15)' : 'transparent',
                border: '1px solid var(--color-border)',
                color: copied ? 'var(--color-success)' : 'var(--text-tertiary)',
                cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
              }}
            >
              {copied ? '✓ Copied' : 'Copy'}
            </button>
          </div>
        )}
      </div>
      <div style={{
        padding: 'var(--spacing-sm) var(--spacing-md)',
        borderRadius: 'var(--radius-lg)',
        background: 'var(--color-card)',
        color: 'var(--text-primary)',
        lineHeight: 1.6,
        fontSize: 'var(--font-size-base)',
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
        {msg.timestamp && !msg.streaming && (
          <span style={{ fontSize: 10, color: 'var(--text-tertiary)', marginTop: 2, marginLeft: 2 }}>
            {formatTimestamp(msg.timestamp)}
          </span>
        )}
      </div>
    </div>
  )
}

function ToolMessage({ msg }: { msg: ChatMessage }) {
  const [expanded, setExpanded] = useState(false)
  const [copied, setCopied] = useState(false)

  const isCommandTool = ['run_command', 'start_command', 'bash', 'powershell'].includes(msg.toolName || '')
  const MAX_RESULT_LINES = 200
  const resultLines = msg.content ? msg.content.split('\n') : []
  const truncatedResult = resultLines.length > MAX_RESULT_LINES
    ? resultLines.slice(0, MAX_RESULT_LINES).join('\n') + `\n\n... (${resultLines.length - MAX_RESULT_LINES} more lines, click Copy for full output)`
    : msg.content

  const handleCopyResult = useCallback(() => {
    navigator.clipboard.writeText(msg.content || '').then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }, [msg.content])

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

        {/* Line count indicator for non-streaming results */}
        {msg.content && !msg.streaming && !expanded && (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-tertiary)' }}>
            {resultLines.length > 1 ? `${resultLines.length} lines` : `${msg.content.length} chars`}
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
            <div style={{ marginTop: 4, position: 'relative' }}>
              <div style={{
                padding: '8px 10px',
                borderRadius: 'var(--radius-md)',
                background: msg.isError ? '#2d1b1b' : '#0d1117',
                border: `1px solid ${msg.isError ? 'rgba(220, 38, 38, 0.3)' : 'rgba(255,255,255,0.1)'}`,
                maxHeight: 300, overflowY: 'auto',
                fontFamily: 'var(--font-mono)', fontSize: 12,
                color: msg.isError ? '#f87171' : '#8b949e',
                whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.5,
                textAlign: 'left',
              }}>
                {truncatedResult || msg.content}
              </div>
              {/* Copy button for result */}
              <button
                onClick={(e) => { e.stopPropagation(); handleCopyResult() }}
                title="Copy result"
                style={{
                  position: 'absolute', top: 4, right: 4,
                  padding: '2px 8px', borderRadius: 3,
                  background: copied ? 'rgba(34,197,94,0.15)' : 'rgba(255,255,255,0.05)',
                  border: '1px solid rgba(255,255,255,0.15)',
                  color: copied ? 'var(--color-success)' : 'rgba(255,255,255,0.6)',
                  cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
                }}
              >
                {copied ? '✓ Copied' : 'Copy'}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function ErrorMessage({ msg }: { msg: ChatMessage }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(msg.content).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }, [msg.content])

  return (
    <div style={{
      padding: 'var(--spacing-sm) var(--spacing-md)',
      borderRadius: 'var(--radius-md)',
      background: 'rgba(239, 68, 68, 0.1)',
      border: '1px solid var(--color-error)',
      color: 'var(--color-error)',
      fontSize: 'var(--font-size-base)', lineHeight: 1.6,
      position: 'relative',
    }}>
      {msg.content}
      <button
        onClick={handleCopy}
        title="Copy error"
        style={{
          position: 'absolute', top: 4, right: 4,
          padding: '1px 6px', borderRadius: 3,
          background: copied ? 'rgba(34,197,94,0.15)' : 'transparent',
          border: '1px solid var(--color-border)',
          color: copied ? 'var(--color-success)' : 'var(--text-tertiary)',
          cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)',
        }}
      >
        {copied ? '✓' : 'Copy'}
      </button>
    </div>
  )
}

function SystemMessage({ msg }: { msg: ChatMessage }) {
  return (
    <div style={{
      padding: '4px 12px',
      color: 'var(--text-tertiary)',
      fontSize: 'var(--font-size-small)', fontStyle: 'italic',
      textAlign: 'center',
    }}>
      {msg.content}
    </div>
  )
}
