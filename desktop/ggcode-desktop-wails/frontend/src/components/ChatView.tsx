import React, { useState, useRef, useEffect } from 'react'
import { Send, ArrowUp, Share2 } from 'lucide-react'

interface ChatMsg {
  id: string
  role: 'user' | 'assistant'
  content: string
  toolCalls?: { name: string; status: 'success' | 'error' | 'running'; file?: string }[]
}

const mockMessages: ChatMsg[] = [
  {
    id: '1', role: 'user',
    content: 'Refactor the auth middleware to support JWT rotation. Make sure old tokens are invalidated properly when a new one is issued.',
  },
  {
    id: '2', role: 'assistant',
    content: "Here's the refactored auth middleware with JWT rotation support:",
    toolCalls: [
      { name: 'ReadFile', status: 'success', file: 'internal/middleware/auth.go' },
      { name: 'EditFile', status: 'success', file: 'internal/middleware/auth.go' },
    ],
  },
  {
    id: '3', role: 'assistant',
    content: 'The key change is calling `m.revoke(old)` before issuing the new token. This ensures there is no window where both tokens are valid simultaneously.',
  },
]

export function ChatView({ onShare }: { onShare?: () => void }) {
  const [input, setInput] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)

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
          zai/glm-5.1
        </span>
        <span style={{ fontSize: 13, fontWeight: 500 }}>
          Refactor auth middleware
        </span>
        <div style={{ flex: 1 }} />

        {/* Context pill */}
        <span style={{
          padding: '2px 8px', borderRadius: 10,
          background: 'var(--color-card)',
          border: '1px solid var(--color-border)',
          fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-secondary)',
          display: 'flex', alignItems: 'center', gap: 6,
        }}>
          12.4K / 204.8K
          <span style={{
            width: 48, height: 4, borderRadius: 2,
            background: 'var(--color-surface)', display: 'inline-block',
            position: 'relative', overflow: 'hidden',
          }}>
            <span style={{
              position: 'absolute', left: 0, top: 0, width: '6%', height: '100%',
              borderRadius: 2, background: 'var(--color-success)',
            }} />
          </span>
        </span>

        <button style={{
          width: 28, height: 28, borderRadius: 'var(--radius-sm)',
          background: 'var(--color-surface)', border: 'none',
          color: 'var(--text-secondary)', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <Share2 size={14} />
        </button>
      </div>

      {/* Messages */}
      <div style={{
        flex: 1, overflowY: 'auto',
        padding: 'var(--spacing-lg)',
        display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)',
      }}>
        {mockMessages.map(msg => (
          <div key={msg.id}>
            {/* Role label */}
            <div style={{
              fontSize: 11, fontWeight: 600, marginBottom: 4,
              color: msg.role === 'user' ? 'var(--color-info)' : 'var(--color-success)',
            }}>
              {msg.role === 'user' ? 'You' : 'Assistant'}
            </div>

            {/* Content */}
            <div style={{
              ...(msg.role === 'user' ? {
                padding: 'var(--spacing-sm) var(--spacing-md)',
                borderRadius: 'var(--radius-lg)',
                background: 'var(--color-card)',
              } : {}),
              color: msg.role === 'user' ? 'var(--text-primary)' : 'var(--text-secondary)',
              lineHeight: 1.6,
            }}>
              {msg.content}
            </div>

            {/* Tool calls */}
            {msg.toolCalls?.map((tc, i) => (
              <div key={i} style={{
                marginTop: 'var(--spacing-sm)',
                padding: 'var(--spacing-sm) var(--spacing-md)',
                borderRadius: 'var(--radius-md)',
                background: '#0C2D6B',
                border: '1px solid var(--color-primary)',
                display: 'flex', alignItems: 'center', gap: 6,
                fontSize: 12,
              }}>
                <span style={{ color: 'var(--color-success)', fontWeight: 700 }}>✓</span>
                <span style={{
                  fontFamily: 'var(--font-mono)', fontWeight: 600,
                  color: 'var(--color-info)',
                }}>
                  {tc.name}
                </span>
                {tc.file && (
                  <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)', fontSize: 11 }}>
                    {tc.file}
                  </span>
                )}
              </div>
            ))}
          </div>
        ))}
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
          value={input}
          onChange={e => setInput(e.target.value)}
          placeholder="Message ggcode..."
          style={{
            flex: 1, height: 40, padding: '0 var(--spacing-md)',
            borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--text-primary)', outline: 'none',
            fontSize: 13,
          }}
        />
        <button style={{
          width: 36, height: 36, borderRadius: 'var(--radius-lg)',
          background: input ? 'var(--color-primary)' : 'var(--color-surface)',
          border: 'none', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: input ? '#fff' : 'var(--text-tertiary)',
          transition: 'background 0.15s',
        }}>
          <ArrowUp size={18} strokeWidth={2.5} />
        </button>
      </div>
    </div>
  )
}
