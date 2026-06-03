import React from 'react'
import { Plus } from 'lucide-react'

interface MCPServer {
  name: string
  cmd: string
  status: 'running' | 'stopped'
  tools: number
}

const mockServers: MCPServer[] = [
  { name: 'pencil', cmd: 'npx @anthropic-ai/mcp-pencil', status: 'running', tools: 8 },
  { name: 'github', cmd: 'npx @anthropic-ai/mcp-github', status: 'running', tools: 52 },
  { name: 'web-reader', cmd: 'npx @anthropic-ai/mcp-reader', status: 'running', tools: 3 },
  { name: 'filesystem', cmd: 'npx @anthropic-ai/mcp-fs', status: 'stopped', tools: 5 },
]

export function MCPServers({ onBack }: { onBack: () => void }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', height: '100%',
      padding: 'var(--spacing-xl) 32px', gap: 'var(--spacing-md)',
    }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <button onClick={onBack} style={{
          background: 'none', border: 'none', color: 'var(--text-secondary)',
          cursor: 'pointer', display: 'flex', alignItems: 'center',
        }}>
          <BackArrow />
        </button>
        <h2 style={{ fontSize: 16, fontWeight: 600 }}>MCP Servers</h2>
        <div style={{ flex: 1 }} />
        <button style={{
          padding: '4px 10px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)', border: 'none',
          color: '#fff', cursor: 'pointer', fontSize: 11, fontWeight: 500,
          display: 'flex', alignItems: 'center', gap: 4,
        }}><Plus size={12} /> Add Server</button>
      </div>

      {/* Server list */}
      <div style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 8 }}>
        {mockServers.map(srv => (
          <div key={srv.name} style={{
            padding: 'var(--spacing-md)', borderRadius: 'var(--radius-lg)',
            background: 'var(--color-card)', display: 'flex', flexDirection: 'column', gap: 6,
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{
                width: 8, height: 8, borderRadius: 4,
                background: srv.status === 'running' ? 'var(--color-success)' : 'var(--color-error)',
              }} />
              <span style={{ fontSize: 13, fontWeight: 500 }}>{srv.name}</span>
              <span style={{ fontSize: 11, color: 'var(--text-secondary)' }}>{srv.tools} tools</span>
              <div style={{ flex: 1 }} />
              <span style={{
                fontSize: 11,
                color: srv.status === 'running' ? 'var(--color-success)' : 'var(--text-tertiary)',
              }}>
                {srv.status === 'running' ? '● Running' : '○ Stopped'}
              </span>
            </div>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-tertiary)' }}>
              {srv.cmd}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function BackArrow() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M10 3L5 8L10 13" />
    </svg>
  )
}
