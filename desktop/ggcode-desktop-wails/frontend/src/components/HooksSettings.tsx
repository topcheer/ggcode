import { useState, useEffect, useCallback } from 'react'
import { GetHooks, SaveHooks } from '../../wailsjs/go/main/App'
import { hooks } from '../../wailsjs/go/models'

interface HookData {
  match?: string
  type?: string
  command?: string
  url?: string
  secret?: string
  inject_output?: boolean
}

const EVENT_DEFS = [
  { key: 'on_user_message', label: 'On User Message' },
  { key: 'pre_tool_use', label: 'Pre Tool Use' },
  { key: 'post_tool_use', label: 'Post Tool Use' },
  { key: 'on_agent_stop', label: 'On Agent Stop' },
  { key: 'on_stream_stop', label: 'On Stream Stop' },
] as const

export function HooksSettings() {
  const [config, setConfig] = useState<Record<string, HookData[]>>({})
  const [editingEvent, setEditingEvent] = useState<string | null>(null)
  const [editingIdx, setEditingIdx] = useState<number>(-1)
  const [editForm, setEditForm] = useState<HookData>({})
  const [message, setMessage] = useState('')

  const loadHooks = useCallback(async () => {
    try {
      const cfg = await GetHooks()
      setConfig(cfg as any || {})
    } catch (e) {
      setMessage(`Error loading hooks: ${e}`)
    }
  }, [])

  useEffect(() => { loadHooks() }, [loadHooks])

  const save = async (newConfig: Record<string, HookData[]>) => {
    try {
      await SaveHooks(newConfig as any)
      setConfig(newConfig)
      setMessage('Hooks saved')
    } catch (e) {
      setMessage(`Error saving: ${e}`)
    }
  }

  const getHooks = (eventKey: string): HookData[] => {
    return config[eventKey] || []
  }

  const setHooks = (eventKey: string, hooksList: HookData[]) => {
    const newConfig = { ...config, [eventKey]: hooksList }
    save(newConfig)
  }

  const startAdd = (eventKey: string) => {
    setEditingEvent(eventKey)
    setEditingIdx(-1)
    setEditForm({ match: '*', type: 'command' })
  }

  const startEdit = (eventKey: string, idx: number) => {
    const hooks = getHooks(eventKey)
    setEditingEvent(eventKey)
    setEditingIdx(idx)
    setEditForm({ ...hooks[idx] })
  }

  const saveHook = () => {
    if (!editingEvent) return
    const hooks = [...getHooks(editingEvent)]
    if (editingIdx >= 0) {
      hooks[editingIdx] = editForm
    } else {
      hooks.push(editForm)
    }
    setHooks(editingEvent, hooks)
    setEditingEvent(null)
    setEditForm({})
  }

  const deleteHook = (eventKey: string, idx: number) => {
    const hooks = getHooks(eventKey)
    setHooks(eventKey, hooks.filter((_, i) => i !== idx))
  }

  const toggleInject = (eventKey: string, idx: number) => {
    const hooks = [...getHooks(eventKey)]
    hooks[idx] = { ...hooks[idx], inject_output: !hooks[idx]?.inject_output }
    setHooks(eventKey, hooks)
  }

  if (editingEvent) {
    return (
      <div style={{ padding: '16px', maxWidth: '600px' }}>
        <h3 style={{ marginBottom: '16px' }}>
          {editingIdx >= 0 ? 'Edit' : 'Add'} Hook — {EVENT_DEFS.find(e => e.key === editingEvent)?.label}
        </h3>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>Match (* for all)</span>
            <input
              value={editForm.match || ''}
              onChange={e => setEditForm({ ...editForm, match: e.target.value })}
              style={inputStyle}
              placeholder="*"
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>Type</span>
            <select
              value={editForm.type || 'command'}
              onChange={e => setEditForm({ ...editForm, type: e.target.value })}
              style={inputStyle}
            >
              <option value="command">command (shell)</option>
              <option value="http">http (webhook)</option>
            </select>
          </label>
          {editForm.type === 'http' ? (
            <>
              <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>URL</span>
                <input
                  value={editForm.url || ''}
                  onChange={e => setEditForm({ ...editForm, url: e.target.value })}
                  style={inputStyle}
                  placeholder="https://example.com/webhook"
                />
              </label>
              <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>Secret (HMAC-SHA256)</span>
                <input
                  value={editForm.secret || ''}
                  onChange={e => setEditForm({ ...editForm, secret: e.target.value })}
                  style={inputStyle}
                  placeholder="optional signing key"
                />
              </label>
            </>
          ) : (
            <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
              <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>Command</span>
              <input
                value={editForm.command || ''}
                onChange={e => setEditForm({ ...editForm, command: e.target.value })}
                style={inputStyle}
                placeholder="echo 'hook triggered'"
              />
            </label>
          )}
          {editingEvent === 'post_tool_use' && (
            <label style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <input
                type="checkbox"
                checked={editForm.inject_output || false}
                onChange={e => setEditForm({ ...editForm, inject_output: e.target.checked })}
              />
              <span style={{ fontSize: '13px' }}>Inject output into tool result</span>
            </label>
          )}
        </div>
        <div style={{ marginTop: '16px', display: 'flex', gap: '8px' }}>
          <button style={btnPrimary} onClick={saveHook}>Save</button>
          <button style={btnSecondary} onClick={() => setEditingEvent(null)}>Cancel</button>
        </div>
      </div>
    )
  }

  return (
    <div style={{ padding: '16px' }}>
      <h3 style={{ marginBottom: '16px' }}>Lifecycle Hooks</h3>
      {message && <div style={{ marginBottom: '12px', color: 'var(--text-secondary)', fontSize: '13px' }}>{message}</div>}
      {EVENT_DEFS.map(event => {
        const hooks = getHooks(event.key)
        return (
          <div key={event.key} style={{ marginBottom: '16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '8px' }}>
              <span style={{ fontWeight: 600, fontSize: '14px' }}>
                {event.label} ({hooks.length})
              </span>
              <button style={btnSmall} onClick={() => startAdd(event.key)}>+ Add</button>
            </div>
            {hooks.length === 0 ? (
              <div style={{ fontSize: '13px', color: 'var(--text-tertiary)', paddingLeft: '8px' }}>(none)</div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                {hooks.map((h, i) => (
                  <div key={i} style={{
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                    padding: '6px 10px', borderRadius: 'var(--radius-sm)',
                    background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                  }}>
                    <div style={{ fontSize: '13px', flex: 1 }}>
                      <span style={{ color: 'var(--text-secondary)' }}>{h.type || 'command'}</span>
                      {' | '}
                      <span>{h.type === 'http' ? (h.url || '') : (h.command || '')}</span>
                      {h.match && h.match !== '*' && <span style={{ color: 'var(--text-tertiary)' }}> | match={h.match}</span>}
                      {h.inject_output && <span style={{ color: 'var(--color-primary)' }}> [inject]</span>}
                    </div>
                    <div style={{ display: 'flex', gap: '4px' }}>
                      {event.key === 'post_tool_use' && (
                        <button style={btnTiny} onClick={() => toggleInject(event.key, i)}>inject</button>
                      )}
                      <button style={btnTiny} onClick={() => startEdit(event.key, i)}>edit</button>
                      <button style={btnTinyDanger} onClick={() => deleteHook(event.key, i)}>del</button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  padding: '6px 10px', borderRadius: 'var(--radius-sm)',
  border: '1px solid var(--color-border)', background: 'var(--color-surface)',
  color: 'var(--text-primary)', fontSize: '13px',
}

const btnPrimary: React.CSSProperties = {
  padding: '6px 16px', borderRadius: 'var(--radius-sm)',
  background: 'var(--color-primary)', color: '#fff',
  border: 'none', cursor: 'pointer', fontSize: '13px',
}

const btnSecondary: React.CSSProperties = {
  padding: '6px 16px', borderRadius: 'var(--radius-sm)',
  background: 'var(--color-surface)', color: 'var(--text-primary)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: '13px',
}

const btnSmall: React.CSSProperties = {
  padding: '2px 8px', borderRadius: 'var(--radius-sm)',
  background: 'var(--color-surface)', color: 'var(--text-primary)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: '12px',
}

const btnTiny: React.CSSProperties = {
  padding: '1px 6px', borderRadius: 'var(--radius-sm)',
  background: 'transparent', color: 'var(--text-secondary)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: '11px',
}

const btnTinyDanger: React.CSSProperties = {
  padding: '1px 6px', borderRadius: 'var(--radius-sm)',
  background: 'transparent', color: 'var(--color-danger, #e57373)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: '11px',
}
