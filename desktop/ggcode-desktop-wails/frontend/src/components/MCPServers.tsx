import React, { useState, useEffect } from 'react'
import { Plus, Trash2, ChevronLeft, Server, ToggleLeft, ToggleRight } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface MCPServerConfig {
  name: string
  type?: string
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
}

export function MCPServers({ onBack }: { onBack: () => void }) {
  const [servers, setServers] = useState<MCPServerConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showAdd, setShowAdd] = useState(false)

  // Form state for adding
  const [addName, setAddName] = useState('')
  const [addType, setAddType] = useState('stdio')
  const [addCommand, setAddCommand] = useState('')
  const [addUrl, setAddUrl] = useState('')
  const [addError, setAddError] = useState('')

  const loadServers = async () => {
    setLoading(true)
    setError('')
    try {
      const list = await App.ListMCPServers() as MCPServerConfig[]
      setServers(list || [])
    } catch (e: any) {
      setError(e?.message || 'Failed to load MCP servers')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadServers() }, [])

  const handleAdd = async () => {
    if (!addName.trim()) { setAddError('Name is required'); return }
    setAddError('')
    try {
      const values: Record<string, string> = {
        name: addName.trim(),
        type: addType,
      }
      if (addType === 'stdio' && addCommand.trim()) {
        values.command = addCommand.trim()
      }
      if (addType === 'sse' && addUrl.trim()) {
        values.url = addUrl.trim()
      }
      await App.AddMCPServer(values)
      setShowAdd(false)
      setAddName(''); setAddCommand(''); setAddUrl('')
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

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--color-bg)' }}>
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
        <span style={{ fontWeight: 600, fontSize: 14 }}>MCP Servers</span>
        <div style={{ flex: 1 }} />
        <button onClick={() => setShowAdd(!showAdd)} style={{
          padding: '4px 10px', borderRadius: 'var(--radius-sm)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 4,
        }}><Plus size={14} /> Add</button>
      </div>

      {/* Add form */}
      {showAdd && (
        <div style={{
          padding: 12, margin: 8,
          background: 'var(--color-card)', borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border)',
          display: 'flex', flexDirection: 'column', gap: 8,
        }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <input placeholder="Name" value={addName} onChange={e => setAddName(e.target.value)} style={{
              flex: 1, padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: 12, outline: 'none',
            }} />
            <select value={addType} onChange={e => setAddType(e.target.value)} style={{
              padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontSize: 12, outline: 'none',
            }}>
              <option value="stdio">stdio</option>
              <option value="sse">SSE/HTTP</option>
            </select>
          </div>
          {addType === 'stdio' ? (
            <input placeholder="Command (e.g. npx @anthropic-ai/mcp-github)" value={addCommand}
              onChange={e => setAddCommand(e.target.value)} style={{
              padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: 12, outline: 'none',
            }} />
          ) : (
            <input placeholder="URL (e.g. http://localhost:3000/mcp)" value={addUrl}
              onChange={e => setAddUrl(e.target.value)} style={{
              padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: 12, outline: 'none',
            }} />
          )}
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
            }}>Add Server</button>
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
            No MCP servers configured. Click "Add" to add one.
          </div>
        )}

        {servers.map(server => (
          <div key={server.name} style={{
            display: 'flex', alignItems: 'center', gap: 10,
            padding: '10px 12px', marginBottom: 4,
            borderRadius: 'var(--radius-md)',
            background: 'var(--color-card)',
            border: '1px solid var(--color-border)',
          }}>
            <Server size={16} style={{ color: 'var(--color-info)', flexShrink: 0 }} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)' }}>
                  {server.name}
                </span>
                <span style={{
                  fontSize: 10, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                  background: 'rgba(59,130,246,0.15)', color: 'var(--color-info)',
                }}>{server.type || 'stdio'}</span>
              </div>
              <span style={{
                fontSize: 11, fontFamily: 'var(--font-mono)',
                color: 'var(--text-tertiary)',
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                display: 'block',
              }}>
                {server.command || server.url || '—'}
              </span>
              {server.args && server.args.length > 0 && (
                <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
                  args: {server.args.join(' ')}
                </span>
              )}
            </div>
            <button onClick={() => handleRemove(server.name)} style={{
              background: 'none', border: 'none', cursor: 'pointer',
              color: 'var(--text-tertiary)', padding: 4,
              display: 'flex', alignItems: 'center',
            }}><Trash2 size={14} /></button>
          </div>
        ))}
      </div>
    </div>
  )
}
