import React, { useState } from 'react'

interface DiffLine {
  type: 'add' | 'remove' | 'context' | 'header'
  content: string
  oldLine?: number
  newLine?: number
}

function parseDiff(text: string): DiffLine[] {
  const lines = text.split('\n')
  const result: DiffLine[] = []
  for (const line of lines) {
    if (line.startsWith('@@')) {
      result.push({ type: 'header', content: line })
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      result.push({ type: 'add', content: line.slice(1) })
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      result.push({ type: 'remove', content: line.slice(1) })
    } else if (line.startsWith(' ')) {
      result.push({ type: 'context', content: line.slice(1) })
    } else {
      result.push({ type: 'context', content: line })
    }
  }
  return result
}

interface Props {
  fileName: string
  diffText: string
}

export function DiffRender({ fileName, diffText }: Props) {
  const [expanded, setExpanded] = useState(true)
  const lines = parseDiff(diffText)
  const addCount = lines.filter(l => l.type === 'add').length
  const removeCount = lines.filter(l => l.type === 'remove').length

  return (
    <div style={{
      borderRadius: 'var(--radius-lg)',
      border: '1px solid var(--color-border)',
      overflow: 'hidden', marginTop: 'var(--spacing-sm)',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: 'var(--spacing-sm) var(--spacing-md)',
        background: 'var(--color-card)',
        cursor: 'pointer',
      }} onClick={() => setExpanded(!expanded)}>
        <span style={{
          fontSize: 10, color: 'var(--text-tertiary)',
          transition: 'transform 0.15s',
          display: 'inline-block',
          transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)',
        }}>▶</span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--color-info)' }}>{fileName}</span>
        <span style={{ fontSize: 11, color: 'var(--color-success)' }}>+{addCount}</span>
        <span style={{ fontSize: 11, color: 'var(--color-error)' }}>-{removeCount}</span>
      </div>

      {/* Diff body */}
      {expanded && (
        <div style={{
          maxHeight: 300, overflowY: 'auto',
          background: 'var(--color-bg)',
          fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.6,
        }}>
          {lines.map((line, i) => (
            <div key={i} style={{
              display: 'flex',
              background: line.type === 'add' ? 'rgba(63, 185, 80, 0.1)' :
                line.type === 'remove' ? 'rgba(248, 81, 73, 0.1)' :
                  line.type === 'header' ? 'rgba(31, 111, 235, 0.1)' : 'transparent',
            }}>
              <span style={{
                width: 32, textAlign: 'right', color: 'var(--text-tertiary)',
                userSelect: 'none', paddingRight: 8, flexShrink: 0,
              }}>{line.oldLine ?? ''}</span>
              <span style={{
                width: 32, textAlign: 'right', color: 'var(--text-tertiary)',
                userSelect: 'none', paddingRight: 8, flexShrink: 0,
                borderRight: '1px solid var(--color-border)',
              }}>{line.newLine ?? ''}</span>
              <span style={{
                padding: '0 8px', minWidth: 16,
                color: line.type === 'add' ? 'var(--color-success)' :
                  line.type === 'remove' ? 'var(--color-error)' : 'var(--text-tertiary)',
              }}>
                {line.type === 'add' ? '+' : line.type === 'remove' ? '-' : ' '}
              </span>
              <span style={{
                color: line.type === 'add' ? 'var(--color-success)' :
                  line.type === 'remove' ? 'var(--color-error)' :
                    line.type === 'header' ? 'var(--color-info)' : 'var(--text-secondary)',
              }}>{line.content}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
