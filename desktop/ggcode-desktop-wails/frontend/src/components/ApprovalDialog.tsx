import React, { useState, useMemo, useEffect, useCallback } from 'react'
import { ShieldAlert, XCircle, CheckCircle2, ShieldCheck, FileEdit, FilePlus, Terminal, FileJson, Copy, Check, Zap, AlertTriangle, Eye } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'

export interface ApprovalRequest {
  requestId: string
  toolName: string
  input: string
}

interface ApprovalDialogProps {
  request: ApprovalRequest
  onClose: () => void
}

// ─── Diff helpers ──────────────────────────────────────────────

const kbdStyle: React.CSSProperties = {
  display: 'inline-block',
  padding: '1px 5px',
  fontSize: 10,
  fontFamily: 'var(--font-mono)',
  background: 'var(--color-card)',
  border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-sm)',
  marginRight: 3,
}

interface DiffLine {
  type: 'context' | 'add' | 'remove'
  text: string
  oldNum?: number
  newNum?: number
}

/** Lightweight LCS-based line diff (no external deps) */
function computeDiff(oldText: string, newText: string): DiffLine[] {
  const oldLines = oldText.split('\n')
  const newLines = newText.split('\n')
  const m = oldLines.length
  const n = newLines.length

  // DP table for LCS
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0))
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      if (oldLines[i] === newLines[j]) {
        dp[i][j] = dp[i + 1][j + 1] + 1
      } else {
        dp[i][j] = Math.max(dp[i + 1][j], dp[i][j + 1])
      }
    }
  }

  const result: DiffLine[] = []
  let i = 0, j = 0
  let oldNum = 1, newNum = 1

  while (i < m && j < n) {
    if (oldLines[i] === newLines[j]) {
      result.push({ type: 'context', text: oldLines[i], oldNum, newNum })
      i++; j++; oldNum++; newNum++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      result.push({ type: 'remove', text: oldLines[i], oldNum })
      i++; oldNum++
    } else {
      result.push({ type: 'add', text: newLines[j], newNum })
      j++; newNum++
    }
  }
  while (i < m) {
    result.push({ type: 'remove', text: oldLines[i], oldNum })
    i++; oldNum++
  }
  while (j < n) {
    result.push({ type: 'add', text: newLines[j], newNum })
    j++; newNum++
  }

  // Collapse long unchanged runs to keep focus on the diff
  const collapsed: DiffLine[] = []
  let contextCount = 0
  for (const line of result) {
    if (line.type === 'context') {
      contextCount++
      if (contextCount <= 3 || contextCount === result.filter(l => l.type === 'context').length) {
        collapsed.push(line)
      } else if (contextCount === 4) {
        collapsed.push({ type: 'context', text: '  ⋯', oldNum: undefined, newNum: undefined })
      }
    } else {
      contextCount = 0
      collapsed.push(line)
    }
  }

  return collapsed
}

function DiffView({ diff }: { diff: DiffLine[] }) {
  return (
    <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.5 }}>
      {diff.map((line, idx) => {
        const bg = line.type === 'add' ? 'rgba(34,197,94,0.12)'
          : line.type === 'remove' ? 'rgba(239,68,68,0.12)'
          : 'transparent'
        const color = line.type === 'add' ? '#4ade80'
          : line.type === 'remove' ? '#f87171'
          : 'var(--text-secondary)'
        const prefix = line.type === 'add' ? '+' : line.type === 'remove' ? '-' : ' '
        return (
          <div key={idx} style={{
            display: 'flex', background: bg,
            padding: '1px 0', minHeight: 18,
          }}>
            <span style={{
              width: 32, flexShrink: 0, textAlign: 'right',
              paddingRight: 6, color: 'var(--text-tertiary)', fontSize: 11,
              userSelect: 'none',
            }}>
              {line.oldNum ?? ''}
            </span>
            <span style={{
              width: 32, flexShrink: 0, textAlign: 'right',
              paddingRight: 6, color: 'var(--text-tertiary)', fontSize: 11,
              userSelect: 'none',
            }}>
              {line.newNum ?? ''}
            </span>
            <span style={{ color, whiteSpace: 'pre-wrap', wordBreak: 'break-word', paddingRight: 8 }}>
              {prefix} {line.text}
            </span>
          </div>
        )
      })}
    </div>
  )
}

// ─── Risk level + diff stats helpers ───────────────────────────

type RiskLevel = 'high' | 'medium' | 'low'

/** Assess risk level based on tool type and arguments */
function getRiskLevel(toolName: string, input: string): RiskLevel {
  // High risk: commands can execute arbitrary code; git ops modify repo state
  if (toolName === 'run_command' || toolName.startsWith('git_')) return 'high'
  // High risk: write_file/multi_file_write can overwrite entire files
  if (toolName === 'write_file' || toolName === 'multi_file_write') {
    const parsed = parseInput(input)
    const totalLen = parsed?.args?.content?.length || 0
    const files = parsed?.args?.files
    if (files && Array.isArray(files)) {
      return files.some((f: any) => (f.content || '').length > 500) ? 'high' : 'medium'
    }
    return totalLen > 500 ? 'high' : 'medium'
  }
  // Medium risk: file edits modify existing code
  if (toolName === 'edit_file' || toolName === 'multi_edit_file' || toolName === 'multi_file_edit') {
    return 'medium'
  }
  // Low risk: everything else (read-only, search, etc.)
  return 'low'
}

interface DiffStats {
  additions: number
  deletions: number
  filesChanged: number
}

/** Count diff additions/deletions from tool input */
function getDiffStats(toolName: string, input: string): DiffStats | null {
  const parsed = parseInput(input)
  if (!parsed) return null

  const countDiff = (oldText: string, newText: string) => {
    const diff = computeDiff(oldText, newText)
    return {
      additions: diff.filter(l => l.type === 'add').length,
      deletions: diff.filter(l => l.type === 'remove').length,
    }
  }

  if (toolName === 'edit_file') {
    const { old_text, new_text } = parsed.args
    if (typeof old_text === 'string' && typeof new_text === 'string') {
      const c = countDiff(old_text, new_text)
      return { ...c, filesChanged: 1 }
    }
  }

  if (toolName === 'multi_edit_file') {
    const edits = parsed.args.edits
    if (Array.isArray(edits)) {
      let additions = 0, deletions = 0
      for (const edit of edits) {
        if (typeof edit.old_text === 'string' && typeof edit.new_text === 'string') {
          const c = countDiff(edit.old_text, edit.new_text)
          additions += c.additions
          deletions += c.deletions
        }
      }
      return { additions, deletions, filesChanged: 1 }
    }
  }

  if (toolName === 'multi_file_edit') {
    const files = parsed.args.files
    if (Array.isArray(files)) {
      let additions = 0, deletions = 0
      for (const f of files) {
        const oldText = f.old_text || ''
        const newText = f.new_text || f.content || ''
        if (oldText && newText) {
          const c = countDiff(oldText, newText)
          additions += c.additions
          deletions += c.deletions
        }
      }
      return { additions, deletions, filesChanged: files.length }
    }
  }

  if (toolName === 'write_file' || toolName === 'multi_file_write') {
    const files = parsed.args.files
    if (files && Array.isArray(files)) {
      let additions = 0
      for (const f of files) {
        additions += (f.content || '').split('\n').length
      }
      return { additions, deletions: 0, filesChanged: files.length }
    }
    const content = parsed.args.content || ''
    return { additions: content.split('\n').length, deletions: 0, filesChanged: 1 }
  }

  return null
}

function RiskBadge({ level }: { level: RiskLevel }) {
  const config = {
    high: { color: '#f87171', bg: 'rgba(220,38,38,0.15)', icon: Zap, label: 'High Risk' },
    medium: { color: '#fbbf24', bg: 'rgba(251,191,36,0.15)', icon: AlertTriangle, label: 'Medium Risk' },
    low: { color: '#4ade80', bg: 'rgba(34,197,94,0.15)', icon: Eye, label: 'Low Risk' },
  }[level]
  const Icon = config.icon
  return (
    <div style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      padding: '3px 8px', borderRadius: 'var(--radius-sm)',
      background: config.bg, color: config.color,
      fontWeight: 600, fontSize: 11,
    }}>
      <Icon size={11} />
      {config.label}
    </div>
  )
}

function CopyButton({ getText }: { getText: () => string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(getText()).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }).catch(() => {})
  }, [getText])
  return (
    <button
      onClick={handleCopy}
      title="Copy to clipboard"
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 4,
        padding: '3px 8px', borderRadius: 'var(--radius-sm)',
        background: 'var(--color-surface)', border: '1px solid var(--color-border)',
        color: copied ? '#4ade80' : 'var(--text-tertiary)',
        cursor: 'pointer', fontSize: 11, fontWeight: 500,
        transition: 'color 0.2s',
      }}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

// ─── Tool-specific renderers ───────────────────────────────────

interface ParsedInput {
  toolName: string
  args: Record<string, any>
}

function parseInput(input: string): ParsedInput | null {
  try {
    const parsed = JSON.parse(input)
    if (typeof parsed === 'object' && parsed !== null) {
      return { toolName: '', args: parsed }
    }
  } catch {
    // not JSON
  }
  return null
}

function FilePath({ path }: { path: string }) {
  const parts = path.split('/')
  const filename = parts[parts.length - 1]
  const dir = parts.slice(0, -1).join('/')
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 6,
      padding: '4px 10px', borderRadius: 'var(--radius-sm)',
      background: 'rgba(99,102,241,0.1)', fontFamily: 'var(--font-mono)',
      fontSize: 12,
    }}>
      <span style={{ color: 'var(--text-tertiary)' }}>{dir}/</span>
      <span style={{ color: '#818cf8', fontWeight: 600 }}>{filename}</span>
    </div>
  )
}

/** Render tool-specific preview content */
function ToolPreview({ toolName, input }: { toolName: string; input: string }) {
  const parsed = parseInput(input)

  // ─── edit_file: diff view ───
  if (toolName === 'edit_file' && parsed) {
    const { file_path, old_text, new_text } = parsed.args
    if (typeof old_text === 'string' && typeof new_text === 'string') {
      return (
        <div>
          {file_path && <div style={{ marginBottom: 8 }}><FilePath path={file_path} /></div>}
          <DiffView diff={computeDiff(old_text, new_text)} />
        </div>
      )
    }
  }

  // ─── multi_edit_file: multiple diffs ───
  if (toolName === 'multi_edit_file' && parsed) {
    const { file_path, edits } = parsed.args
    if (Array.isArray(edits)) {
      return (
        <div>
          {file_path && <div style={{ marginBottom: 8 }}><FilePath path={file_path} /></div>}
          {edits.map((edit: any, idx: number) => {
            if (typeof edit.old_text === 'string' && typeof edit.new_text === 'string') {
              return (
                <div key={idx} style={{ marginBottom: idx < edits.length - 1 ? 12 : 0 }}>
                  <div style={{
                    fontSize: 10, color: 'var(--text-tertiary)', marginBottom: 2,
                    fontFamily: 'var(--font-mono)',
                  }}>
                    Edit {idx + 1}/{edits.length}
                  </div>
                  <DiffView diff={computeDiff(edit.old_text, edit.new_text)} />
                </div>
              )
            }
            return null
          })}
        </div>
      )
    }
  }

  // ─── multi_file_edit: multiple files ───
  if (toolName === 'multi_file_edit' && parsed) {
    const { files } = parsed.args
    if (Array.isArray(files)) {
      return (
        <div>
          {files.map((f: any, idx: number) => {
            const path = f.path || f.file_path || `file ${idx + 1}`
            const oldText = f.old_text || ''
            const newText = f.new_text || f.content || ''
            if (oldText && newText) {
              return (
                <div key={idx} style={{ marginBottom: idx < files.length - 1 ? 12 : 0 }}>
                  <div style={{ marginBottom: 4 }}><FilePath path={path} /></div>
                  <DiffView diff={computeDiff(oldText, newText)} />
                </div>
              )
            }
            return (
              <div key={idx} style={{ marginBottom: 4 }}>
                <FilePath path={path} />
                <pre style={{ margin: '4px 0', fontSize: 12, fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)', whiteSpace: 'pre-wrap' }}>
                  {typeof newText === 'string' ? newText.slice(0, 200) : JSON.stringify(f, null, 2)}
                </pre>
              </div>
            )
          })}
        </div>
      )
    }
  }

  // ─── write_file: new content ───
  if ((toolName === 'write_file' || toolName === 'multi_file_write') && parsed) {
    const path = parsed.args.path || parsed.args.file_path || ''
    const files = parsed.args.files
    if (files && Array.isArray(files)) {
      // multi_file_write
      return (
        <div>
          {files.map((f: any, idx: number) => (
            <div key={idx} style={{ marginBottom: idx < files.length - 1 ? 12 : 0 }}>
              <div style={{ marginBottom: 4 }}><FilePath path={f.path || `file ${idx + 1}`} /></div>
              <pre style={{
                margin: 0, fontSize: 12, fontFamily: 'var(--font-mono)',
                color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
                maxHeight: 200, overflow: 'auto',
              }}>
                {(f.content || '').slice(0, 1000)}
                {(f.content || '').length > 1000 ? '\n⋯' : ''}
              </pre>
            </div>
          ))}
        </div>
      )
    }
    const content = parsed.args.content || ''
    return (
      <div>
        {path && <div style={{ marginBottom: 8 }}><FilePath path={path} /></div>}
        <pre style={{
          margin: 0, fontSize: 12, fontFamily: 'var(--font-mono)',
          color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
          maxHeight: 300, overflow: 'auto',
        }}>
          {content.slice(0, 2000)}
          {content.length > 2000 ? '\n⋯' : ''}
        </pre>
      </div>
    )
  }

  // ─── run_command: terminal style ───
  if (toolName === 'run_command' && parsed) {
    const cmd = parsed.args.command || input
    return (
      <div style={{
        background: 'rgba(0,0,0,0.3)', borderRadius: 'var(--radius-sm)',
        padding: 12, fontFamily: 'var(--font-mono)', fontSize: 13,
        color: '#4ade80', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
      }}>
        <span style={{ color: 'var(--text-tertiary)' }}>$ </span>
        {typeof cmd === 'string' ? cmd : JSON.stringify(cmd, null, 2)}
      </div>
    )
  }

  // ─── git operations: show command/message ───
  if (toolName.startsWith('git_') && parsed) {
    const msg = parsed.args.message || parsed.args.files || ''
    return (
      <div style={{
        fontFamily: 'var(--font-mono)', fontSize: 13,
        color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
      }}>
        {msg ? (typeof msg === 'string' ? msg : JSON.stringify(msg, null, 2)) : formatJSON(input)}
      </div>
    )
  }

  // ─── Fallback: pretty JSON ───
  return (
    <pre style={{
      margin: 0, fontSize: 12, fontFamily: 'var(--font-mono)',
      color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
      wordBreak: 'break-word', lineHeight: 1.5,
    }}>
      {formatJSON(input)}
    </pre>
  )
}

function formatJSON(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2)
  } catch {
    return input
  }
}

/** Get appropriate icon for tool type */
function ToolIcon({ toolName }: { toolName: string }) {
  const size = 16
  if (toolName === 'edit_file' || toolName === 'multi_edit_file' || toolName === 'multi_file_edit') {
    return <FileEdit size={size} style={{ color: '#fbbf24' }} />
  }
  if (toolName === 'write_file' || toolName === 'multi_file_write') {
    return <FilePlus size={size} style={{ color: '#4ade80' }} />
  }
  if (toolName === 'run_command' || toolName.startsWith('git_')) {
    return <Terminal size={size} style={{ color: '#818cf8' }} />
  }
  return <FileJson size={size} style={{ color: 'var(--text-tertiary)' }} />
}

// ─── Main component ────────────────────────────────────────────

export function ApprovalDialog({ request, onClose }: ApprovalDialogProps) {
  const { t } = useTranslation()
  const [responding, setResponding] = useState(false)

  const handleRespond = useCallback(async (decision: 'deny' | 'allow' | 'always_allow') => {
    if (responding) return
    setResponding(true)
    try {
      await App.RespondApproval(request.requestId, decision)
    } catch (e) {
      console.error('Approval response error:', e)
    }
    onClose()
  }, [responding, request.requestId, onClose])

  // Keyboard shortcuts: Esc=Deny, Enter=Allow, Shift+Enter=Always Allow
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        handleRespond('deny')
      } else if (e.key === 'Enter') {
        e.preventDefault()
        handleRespond(e.shiftKey ? 'always_allow' : 'allow')
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [handleRespond])

  const isWide = ['edit_file', 'multi_edit_file', 'multi_file_edit', 'write_file', 'multi_file_write', 'run_command'].includes(request.toolName)

  // Compute risk level and diff stats once per request
  const riskLevel = useMemo(() => getRiskLevel(request.toolName, request.input), [request.toolName, request.input])
  const diffStats = useMemo(() => getDiffStats(request.toolName, request.input), [request.toolName, request.input])

  return (
    <div role="dialog" aria-modal="true" aria-label="Tool approval" style={{
      position: 'fixed', inset: 0,
      background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 1000,
    }}>
      <div style={{
        background: 'var(--color-surface)',
        borderRadius: 'var(--radius-lg)',
        border: '1px solid var(--color-border)',
        width: isWide ? 760 : 560,
        maxWidth: '90vw',
        maxHeight: '85vh',
        boxShadow: '0 20px 60px rgba(0,0,0,0.4)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '16px 20px',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <ShieldAlert size={20} style={{ color: 'var(--color-warning)', flexShrink: 0 }} />
          <span style={{ fontWeight: 700, fontSize: 15, color: 'var(--text-primary)' }}>
            {t('approval.title')}
          </span>
        </div>

        {/* Tool badge + risk + stats */}
        <div style={{ padding: '12px 20px 0', display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '4px 10px', borderRadius: 'var(--radius-md)',
            background: 'rgba(99,102,241,0.15)', color: '#818cf8',
            fontWeight: 600, fontSize: 13, fontFamily: 'var(--font-mono)',
          }}>
            <ToolIcon toolName={request.toolName} />
            {request.toolName}
          </div>
          <RiskBadge level={riskLevel} />
          {diffStats && (
            <div style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              fontSize: 11, fontFamily: 'var(--font-mono)',
              color: 'var(--text-tertiary)',
            }}>
              <span style={{ color: '#4ade80' }}>+{diffStats.additions}</span>
              <span style={{ color: '#f87171' }}>-{diffStats.deletions}</span>
              {diffStats.filesChanged > 1 && <span>in {diffStats.filesChanged} files</span>}
            </div>
          )}
        </div>

        {/* Preview area */}
        <div style={{
          flex: 1, minHeight: 0, margin: '12px 20px',
          padding: 12, borderRadius: 'var(--radius-md)',
          background: 'var(--color-card)', border: '1px solid var(--color-border)',
          overflow: 'auto', maxHeight: 400,
          position: 'relative',
        }}>
          {/* Copy button — top-right of preview area */}
          <div style={{ position: 'absolute', top: 8, right: 8, zIndex: 10, opacity: 0.7 }}>
            <CopyButton getText={() => request.input} />
          </div>
          <ToolPreview toolName={request.toolName} input={request.input} />
        </div>

        {/* Buttons */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          justifyContent: 'space-between',
          padding: '0 20px 16px',
        }}>
          {/* Keyboard hint */}
          <div style={{
            fontSize: 11, color: 'var(--text-tertiary)',
            display: 'flex', alignItems: 'center', gap: 8,
          }}>
            <span><kbd style={kbdStyle}>Esc</kbd> Deny</span>
            <span><kbd style={kbdStyle}>Enter</kbd> Allow</span>
            <span><kbd style={kbdStyle}>Shift+Enter</kbd> Always</span>
          </div>
          <div style={{ display: 'flex', gap: 10 }}>
            <button
              onClick={() => handleRespond('deny')}
              disabled={responding}
              style={{
                padding: '8px 20px', borderRadius: 'var(--radius-md)',
                background: 'rgba(220,38,38,0.15)', color: '#f87171',
                border: '1px solid rgba(220,38,38,0.3)',
                cursor: responding ? 'not-allowed' : 'pointer',
                fontWeight: 600, fontSize: 13,
                display: 'flex', alignItems: 'center', gap: 6,
                opacity: responding ? 0.5 : 1,
              }}
            >
              <XCircle size={15} /> {t('approval.deny')}
            </button>
            <button
              onClick={() => handleRespond('allow')}
              disabled={responding}
              style={{
                padding: '8px 20px', borderRadius: 'var(--radius-md)',
                background: 'var(--color-primary)', color: '#fff',
                border: 'none',
                cursor: responding ? 'not-allowed' : 'pointer',
                fontWeight: 600, fontSize: 13,
                display: 'flex', alignItems: 'center', gap: 6,
                opacity: responding ? 0.5 : 1,
              }}
            >
              <CheckCircle2 size={15} /> {t('approval.allow')}
            </button>
            <button
              onClick={() => handleRespond('always_allow')}
              disabled={responding}
              style={{
                padding: '8px 20px', borderRadius: 'var(--radius-md)',
                background: 'rgba(34,197,94,0.15)', color: '#4ade80',
                border: '1px solid rgba(34,197,94,0.3)',
                cursor: responding ? 'not-allowed' : 'pointer',
                fontWeight: 600, fontSize: 13,
                display: 'flex', alignItems: 'center', gap: 6,
                opacity: responding ? 0.5 : 1,
              }}
            >
              <ShieldCheck size={15} /> {t('approval.alwaysAllow')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
