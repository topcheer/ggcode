import { useState, useEffect, useCallback } from 'react'
import { GetHooks, SaveHooks } from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'

interface HookData {
  match?: string
  type?: string
  command?: string
  url?: string
  secret?: string
  inject_output?: boolean
}

const EVENT_KEYS = [
  { key: 'on_user_message', labelKey: 'settings.hooksOnUserMessage' },
  { key: 'pre_tool_use', labelKey: 'settings.hooksPreToolUse' },
  { key: 'post_tool_use', labelKey: 'settings.hooksPostToolUse' },
  { key: 'on_agent_stop', labelKey: 'settings.hooksOnAgentStop' },
  { key: 'on_stream_stop', labelKey: 'settings.hooksOnStreamStop' },
] as const

export function HooksSettings() {
  const { t } = useTranslation()
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
      setMessage(t('settings.hooksErrorLoad', { 0: String(e) }))
    }
  }, [t])

  useEffect(() => { loadHooks() }, [loadHooks])

  const save = async (newConfig: Record<string, HookData[]>) => {
    try {
      await SaveHooks(newConfig as any)
      setConfig(newConfig)
      setMessage(t('settings.hooksSaved'))
    } catch (e) {
      setMessage(t('settings.hooksErrorSave', { 0: String(e) }))
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

  const eventLabels = EVENT_KEYS.map(e => ({ ...e, label: t(e.labelKey as any) }))

  if (editingEvent) {
    const evLabel = eventLabels.find(e => e.key === editingEvent)?.label || editingEvent
    const isEdit = editingIdx >= 0
    return (
      <div style={{ padding: '16px', maxWidth: '600px' }}>
        <h3 style={{ marginBottom: '16px' }}>
          {t('settings.hooksEditTitle', { 0: isEdit ? t('settings.hooksEditBtn' as any) : t('settings.hooksAddBtn' as any), 1: evLabel })}
        </h3>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>{t('settings.hooksMatch')}</span>
            <input
              value={editForm.match || ''}
              onChange={e => setEditForm({ ...editForm, match: e.target.value })}
              style={inputStyle}
              placeholder="*"
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>{t('settings.hooksType')}</span>
            <select
              value={editForm.type || 'command'}
              onChange={e => setEditForm({ ...editForm, type: e.target.value })}
              style={inputStyle}
            >
              <option value="command">command</option>
              <option value="http">http</option>
            </select>
          </label>
          {editForm.type === 'http' ? (
            <>
              <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>{t('settings.hooksURL')}</span>
                <input
                  value={editForm.url || ''}
                  onChange={e => setEditForm({ ...editForm, url: e.target.value })}
                  style={inputStyle}
                  placeholder="https://example.com/webhook"
                />
              </label>
              <label style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>{t('settings.hooksSecret')}</span>
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
              <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>{t('settings.hooksCommand')}</span>
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
              <span style={{ fontSize: '13px' }}>{t('settings.hooksInjectLabel')}</span>
            </label>
          )}
        </div>
        <div style={{ marginTop: '16px', display: 'flex', gap: '8px' }}>
          <button style={btnPrimary} onClick={saveHook}>{t('settings.hooksSaveBtn')}</button>
          <button style={btnSecondary} onClick={() => setEditingEvent(null)}>{t('settings.hooksCancelBtn')}</button>
        </div>
      </div>
    )
  }

  return (
    <div style={{ padding: '16px' }}>
      <h3 style={{ marginBottom: '16px' }}>{t('settings.hooksTitle')}</h3>
      {message && <div style={{ marginBottom: '12px', color: 'var(--text-secondary)', fontSize: '13px' }}>{message}</div>}
      {eventLabels.map(event => {
        const hooks = getHooks(event.key)
        return (
          <div key={event.key} style={{ marginBottom: '16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '8px' }}>
              <span style={{ fontWeight: 600, fontSize: '14px' }}>
                {event.label} ({hooks.length})
              </span>
              <button style={btnSmall} onClick={() => startAdd(event.key)}>{t('settings.hooksAdd')}</button>
            </div>
            {hooks.length === 0 ? (
              <div style={{ fontSize: '13px', color: 'var(--text-tertiary)', paddingLeft: '8px' }}>{t('settings.hooksNone')}</div>
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
                        <button style={btnTiny} onClick={() => toggleInject(event.key, i)}>{t('settings.hooksInject')}</button>
                      )}
                      <button style={btnTiny} onClick={() => startEdit(event.key, i)}>{t('settings.hooksEdit')}</button>
                      <button style={btnTinyDanger} onClick={() => deleteHook(event.key, i)}>{t('settings.hooksDelete')}</button>
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
  background: 'transparent', color: 'var(--color-danger)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: '11px',
}
