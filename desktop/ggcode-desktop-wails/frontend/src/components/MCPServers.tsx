import React, { useState, useEffect } from 'react'
import { ChevronLeft, Server, Plus, Trash2, Terminal, Globe, Wifi, RefreshCw, Power } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { useTranslation } from '../i18n'

type TransportType = 'stdio' | 'http' | 'ws'

interface MCPServerConfig {
  name: string
  type?: string
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
  status?: string
  error?: string
  disabled?: boolean
  connected?: boolean
}

const TRANSPORTS: { value: TransportType; label: string; icon: React.ReactNode; desc: string }[] = [
  { value: 'stdio', label: 'Stdio', icon: <Terminal size={16} />, desc: 'Local process via stdin/stdout' },
  { value: 'http', label: 'HTTP / SSE', icon: <Globe size={16} />, desc: 'Remote server via HTTP (Streamable HTTP / SSE)' },
  { value: 'ws', label: 'WebSocket', icon: <Wifi size={16} />, desc: 'Remote server via WebSocket' },
]

function transportIcon(type?: string) {
  switch (type) {
    case 'http': return <Globe size={16} style={{ color: '#3FB950' }} />
    case 'ws': return <Wifi size={16} style={{ color: '#A371F7' }} />
    default: return <Terminal size={16} style={{ color: '#58A6FF' }} />
  }
}

function transportLabel(type?: string) {
  switch (type) {
    case 'http': return 'HTTP'
    case 'ws': return 'WebSocket'
    default: return 'Stdio'
  }
}

function transportColor(type?: string) {
  switch (type) {
    case 'http': return 'rgba(63,185,80,0.15)'
    case 'ws': return 'rgba(163,113,247,0.15)'
    default: return 'rgba(88,166,255,0.15)'
  }
}

function transportTextColor(type?: string) {
  switch (type) {
    case 'http': return '#3FB950'
    case 'ws': return '#A371F7'
    default: return '#58A6FF'
  }
}

export function MCPServers({ onBack }: { onBack: () => void }) {
  const { t: tr } = useTranslation()
  const [servers, setServers] = useState<MCPServerConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showAdd, setShowAdd] = useState(false)

  // Add form state
  const [addType, setAddType] = useState<TransportType>('stdio')
  const [addName, setAddName] = useState('')
  const [addCommand, setAddCommand] = useState('')
  const [addArgs, setAddArgs] = useState('')
  const [addEnv, setAddEnv] = useState('')
  const [addUrl, setAddUrl] = useState('')
  const [addHeaders, setAddHeaders] = useState('')
  const [addError, setAddError] = useState('')

  const loadServers = async () => {
    setLoading(true)
    setError('')
    try {
      console.log('[MCP] Calling ListMCPServers...')
      const list = await App.ListMCPServers() as MCPServerConfig[]
      console.log('[MCP] Result:', list)
      setServers(list || [])
    } catch (e: any) {
      console.error('[MCP] Error:', e)
      setError(e?.message || 'Failed to load MCP servers')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadServers()
    // Real-time updates: backend pushes mcp:status when server connects/disconnects/fails
    EventsOn('mcp:status', () => { loadServers() })
    return () => { EventsOff('mcp:status') }
  }, [])

  const handleAdd = async () => {
    if (!addName.trim()) { setAddError('Name is required'); return }
    if (addType === 'stdio' && !addCommand.trim()) { setAddError('Command is required for stdio'); return }
    if ((addType === 'http' || addType === 'ws') && !addUrl.trim()) { setAddError('URL is required for remote transport'); return }

    setAddError('')
    try {
      const values: Record<string, string> = {
        name: addName.trim(),
        type: addType,
      }
      if (addType === 'stdio') {
        values.command = addCommand.trim()
        if (addArgs.trim()) values.args = addArgs.trim()
      } else {
        values.url = addUrl.trim()
        // Parse headers: "Key: Value" per line
        if (addHeaders.trim()) {
          addHeaders.trim().split('\n').forEach(line => {
            const idx = line.indexOf(':')
            if (idx > 0) {
              values[`headers_${line.slice(0, idx).trim()}`] = line.slice(idx + 1).trim()
            }
          })
        }
      }
      // Parse env: "KEY=VALUE" per line
      if (addEnv.trim()) {
        addEnv.trim().split('\n').forEach(line => {
          const idx = line.indexOf('=')
          if (idx > 0) {
            values[`env_${line.slice(0, idx).trim()}`] = line.slice(idx + 1).trim()
          }
        })
      }

      await App.AddMCPServer(values)
      setShowAdd(false)
      setAddName(''); setAddCommand(''); setAddArgs(''); setAddEnv(''); setAddUrl(''); setAddHeaders('')
      await loadServers()
    } catch (e: any) {
      setAddError(e?.message || 'Failed to add server')
    }
  }

  const handleRemove = async (name: string) => {
    try {
      await App.RemoveMCPServer(name)
      await loadServers()
    } catch {}
  }

  const handleToggleEnabled = async (name: string, enabled: boolean) => {
    try {
      await App.SetMCPServerEnabled(name, enabled)
      await loadServers()
    } catch {}
  }

  const handleReconnect = async (name: string) => {
    try {
      await App.ReconnectMCPServer(name)
      await loadServers()
    } catch {}
  }

  const inputStyle: React.CSSProperties = {
    padding: '5px 8px', borderRadius: 'var(--radius-sm)',
    background: 'var(--color-bg)', border: '1px solid var(--color-border)',
    color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: 12, outline: 'none',
    width: '100%', boxSizing: 'border-box',
  }

  const labelStyle: React.CSSProperties = {
    fontSize: 11, color: 'var(--text-secondary)', marginBottom: 2,
  }

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', background: 'var(--color-bg)', overflow: 'auto', minHeight: 0 }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: '8px 16px',
        borderBottom: '1px solid var(--color-border)',
      }}>
        <button onClick={onBack} style={{
          background: 'none', border: 'none', cursor: 'pointer',
          color: 'var(--text-secondary)', display: 'flex', alignItems: 'center',
        }}><ChevronLeft size={18} /></button>
        <Server size={16} style={{ color: 'var(--color-primary)' }} />
        <span style={{ fontWeight: 600, fontSize: 14 }}>{tr('mcp.title')}</span>
        <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>{servers.length} configured</span>
        <div style={{ flex: 1 }} />
        <button onClick={() => setShowAdd(!showAdd)} style={{
          padding: '4px 10px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 4,
        }}><Plus size={14} /> {tr('mcp.add')}</button>
      </div>

      {/* Add form */}
      {showAdd && (
        <div style={{
          padding: 12, margin: 8,
          background: 'var(--color-card)', borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border)',
          display: 'flex', flexDirection: 'column', gap: 10,
        }}>
          {/* Transport type selector */}
          <div style={labelStyle}>Transport</div>
          <div style={{ display: 'flex', gap: 6 }}>
            {TRANSPORTS.map(t => (
              <button key={t.value} onClick={() => setAddType(t.value)} style={{
                flex: 1, padding: '6px 8px', borderRadius: 'var(--radius-sm)',
                background: addType === t.value ? 'var(--color-primary)' : 'var(--color-bg)',
                border: `1px solid ${addType === t.value ? 'var(--color-primary)' : 'var(--color-border)'}`,
                color: addType === t.value ? '#fff' : 'var(--text-secondary)',
                cursor: 'pointer', fontSize: 11, fontWeight: 500,
                display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2,
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  {t.icon} {t.label}
                </div>
                <span style={{ fontSize: 9, opacity: 0.7 }}>{t.desc}</span>
              </button>
            ))}
          </div>

          {/* Name */}
          <div style={labelStyle}>Name</div>
          <input placeholder="e.g. github, filesystem" value={addName}
            onChange={e => setAddName(e.target.value)} style={inputStyle} />

          {/* Stdio fields */}
          {addType === 'stdio' && (
            <>
              <div style={labelStyle}>Command</div>
              <input placeholder="e.g. npx -y @anthropic-ai/mcp-github" value={addCommand}
                onChange={e => setAddCommand(e.target.value)} style={inputStyle} />
              <div style={labelStyle}>Arguments (optional, space-separated)</div>
              <input placeholder="e.g. --verbose --port 3000" value={addArgs}
                onChange={e => setAddArgs(e.target.value)} style={inputStyle} />
            </>
          )}

          {/* HTTP/WebSocket fields */}
          {(addType === 'http' || addType === 'ws') && (
            <>
              <div style={labelStyle}>URL</div>
              <input placeholder={addType === 'ws' ? 'ws://localhost:8080/mcp' : 'http://localhost:3000/mcp'}
                value={addUrl} onChange={e => setAddUrl(e.target.value)} style={inputStyle} />
              <div style={labelStyle}>Headers (optional, one per line: Key: Value)</div>
              <textarea placeholder={"Authorization: Bearer token123\nX-Custom: value"}
                value={addHeaders} onChange={e => setAddHeaders(e.target.value)}
                rows={3} style={{ ...inputStyle, resize: 'vertical' }} />
            </>
          )}

          {/* Common: Environment */}
          <div style={labelStyle}>Environment Variables (optional, one per line: KEY=VALUE)</div>
          <textarea placeholder={"API_KEY=sk-xxx\nDEBUG=true"}
            value={addEnv} onChange={e => setAddEnv(e.target.value)}
            rows={2} style={{ ...inputStyle, resize: 'vertical' }} />

          {addError && <span style={{ fontSize: 11, color: '#f87171' }}>{addError}</span>}
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <button onClick={() => setShowAdd(false)} style={{
              padding: '4px 10px', borderRadius: 'var(--radius-sm)',
              background: 'transparent', border: '1px solid var(--color-border)',
              color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 12,
            }}>Cancel</button>
            <button onClick={handleAdd} style={{
              padding: '4px 10px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-primary)', border: 'none',
              color: '#fff', cursor: 'pointer', fontSize: 12, fontWeight: 600,
            }}>{tr('mcp.add')} Server</button>
          </div>
        </div>
      )}

      {/* Server list */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '8px 16px' }}>
        {loading && (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-tertiary)', fontSize: 12 }}>
            Loading servers...
          </div>
        )}
        {error && (
          <div style={{ padding: 12, background: 'rgba(220,38,38,0.1)', borderRadius: 'var(--radius-md)',
            color: '#f87171', fontSize: 12, marginBottom: 8 }}>{error}</div>
        )}
        {!loading && servers.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--text-tertiary)', fontSize: 12 }}>
            No MCP servers configured. Click "{tr('mcp.add')}" to add one.
          </div>
        )}

        {servers.map(server => {
          const t = server.type || 'stdio'
          return (
            <div key={server.name} style={{
              display: 'flex', alignItems: 'flex-start', gap: 10,
              padding: '10px 12px', marginBottom: 6,
              borderRadius: 'var(--radius-md)',
              background: 'var(--color-card)',
              border: '1px solid var(--color-border)',
            }}>
              <div style={{ marginTop: 2, flexShrink: 0 }}>{transportIcon(t)}</div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)' }}>
                    {server.name}
                  </span>
                  <span style={{
                    fontSize: 10, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                    background: transportColor(t), color: transportTextColor(t),
                    fontWeight: 500,
                  }}>{transportLabel(t)}</span>
                  <span style={{
                    fontSize: 10, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                    background: server.disabled ? 'rgba(148,163,184,0.15)' : server.connected ? 'rgba(63,185,80,0.15)' : server.status === 'failed' ? 'rgba(239,68,68,0.15)' : 'rgba(234,179,8,0.15)',
                    color: server.disabled ? '#94a3b8' : server.connected ? '#3FB950' : server.status === 'failed' ? '#ef4444' : '#eab308',
                    fontWeight: 500,
                  }}>{server.disabled ? 'disabled' : (server.status || 'unknown')}</span>
                </div>
                {/* Transport-specific details */}
                {t === 'stdio' ? (
                  <div style={{ marginTop: 2 }}>
                    <span style={{
                      fontSize: 11, fontFamily: 'var(--font-mono)',
                      color: 'var(--text-tertiary)',
                      wordBreak: 'break-all',
                    }}>
                      {server.command}{server.args && server.args.length > 0 ? ` ${server.args.join(' ')}` : ''}
                    </span>
                  </div>
                ) : (
                  <div style={{ marginTop: 2 }}>
                    <span style={{
                      fontSize: 11, fontFamily: 'var(--font-mono)',
                      color: transportTextColor(t),
                      wordBreak: 'break-all',
                    }}>{server.url}</span>
                  </div>
                )}
                {/* Environment vars summary */}
                {server.env && Object.keys(server.env).length > 0 && (
                  <div style={{ marginTop: 2 }}>
                    <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                      env: {Object.keys(server.env).join(', ')}
                    </span>
                  </div>
                )}
                {/* Headers summary */}
                {server.headers && Object.keys(server.headers).length > 0 && (
                  <div style={{ marginTop: 2 }}>
                    <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                      headers: {Object.keys(server.headers).join(', ')}
                    </span>
                  </div>
                )}
                {server.error && !server.disabled && (
                  <div style={{ marginTop: 4, fontSize: 10, color: '#f87171' }}>{server.error}</div>
                )}
              </div>
              <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
                <button onClick={() => handleToggleEnabled(server.name, !!server.disabled)} title={server.disabled ? 'Enable' : 'Disable'} style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: server.disabled ? '#3FB950' : 'var(--text-tertiary)', padding: 4,
                }}><Power size={14} /></button>
                <button onClick={() => handleReconnect(server.name)} title="Reconnect" style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: 'var(--text-tertiary)', padding: 4,
                }}><RefreshCw size={14} /></button>
                <button onClick={() => handleRemove(server.name)} title="Remove" style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: 'var(--text-tertiary)', padding: 4,
                }}><Trash2 size={14} /></button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
