import React, { useState, useEffect } from 'react'
import { Plus, Trash2, Power, PowerOff } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

// ── Types matching Go structs ──

interface IMAdapterInfo {
  name: string
  enabled: boolean
  platform: string
  transport: string
  command: string
  extra: Record<string, string>
  targets: string[]
}

interface IMPlatformField {
  key: string
  label: string
  placeholder: string
  secret?: boolean
}

interface IMPlatformMeta {
  id: string
  displayName: string
  fields: IMPlatformField[]
  qrAuth: boolean
}

// ── Component ──

export function IMManagement() {
  const [adapters, setAdapters] = useState<IMAdapterInfo[]>([])
  const [platforms, setPlatforms] = useState<IMPlatformMeta[]>([])
  const [showAdd, setShowAdd] = useState(false)
  const [editAdapter, setEditAdapter] = useState<string | null>(null)
  const [editFields, setEditFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')

  useEffect(() => {
    loadData()
  }, [])

  async function loadData() {
    try {
      const [adaptersResult, platformsResult] = await Promise.all([
        App.ListIMAdapters() as Promise<IMAdapterInfo[]>,
        App.GetIMPlatformRegistry() as Promise<IMPlatformMeta[]>,
      ])
      setAdapters(adaptersResult || [])
      setPlatforms(platformsResult || [])
    } catch (e: any) {
      setError(e?.message || 'Failed to load IM config')
    }
  }

  // Find platform meta by ID
  function getPlatform(id: string): IMPlatformMeta | undefined {
    return platforms.find(p => p.id === id)
  }

  // ── Add adapter dialog ──
  if (showAdd) {
    return <AddAdapterDialog
      platforms={platforms}
      onAdd={async (name, platform, fields) => {
        try {
          const values: Record<string, string> = { platform, ...fields }
          await App.SaveIMAdapter(name, values)
          setShowAdd(false)
          loadData()
        } catch (e: any) {
          setError(e?.message || 'Failed to save adapter')
        }
      }}
      onCancel={() => setShowAdd(false)}
      error={error}
    />
  }

  // ── Edit adapter ──
  if (editAdapter) {
    const adapter = adapters.find(a => a.name === editAdapter)
    const platform = adapter ? getPlatform(adapter.platform) : undefined
    return <EditAdapterDialog
      adapter={adapter!}
      platform={platform}
      fields={editFields}
      setFields={setEditFields}
      onSave={async () => {
        try {
          const values: Record<string, string> = { platform: adapter!.platform, ...editFields }
          await App.SaveIMAdapter(adapter!.name, values)
          setEditAdapter(null)
          setEditFields({})
          loadData()
        } catch (e: any) {
          setError(e?.message || 'Failed to save')
        }
      }}
      onCancel={() => { setEditAdapter(null); setEditFields({}) }}
      error={error}
    />
  }

  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <h3 style={{ margin: 0, color: 'var(--text-primary)' }}>IM Adapters</h3>
        <div style={{ flex: 1 }} />
        <button onClick={() => { setShowAdd(true); setError('') }} style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '6px 14px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontSize: 13,
        }}>
          <Plus size={14} /> Add Adapter
        </button>
      </div>

      {error && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error}</div>}

      {/* Adapter list */}
      {adapters.length === 0 ? (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 13, textAlign: 'center', padding: 40 }}>
          No IM adapters configured. Click "Add Adapter" to get started.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {adapters.map(adapter => {
            const platform = getPlatform(adapter.platform)
            return (
              <div key={adapter.name} style={{
                padding: '12px 16px', borderRadius: 'var(--radius-md)',
                background: 'var(--color-card)', border: '1px solid var(--color-border)',
                display: 'flex', alignItems: 'center', gap: 12,
                opacity: adapter.enabled ? 1 : 0.6,
              }}>
                {/* Status dot */}
                <div style={{
                  width: 8, height: 8, borderRadius: '50%', flexShrink: 0,
                  background: adapter.enabled ? 'var(--color-success)' : 'var(--color-border)',
                }} />

                {/* Info */}
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)' }}>
                    {adapter.name}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
                    {platform?.displayName || adapter.platform}
                    {adapter.targets?.length > 0 && ` · ${adapter.targets.length} target(s)`}
                  </div>
                </div>

                {/* Actions */}
                <button onClick={async () => {
                  try {
                    await App.SetIMAdapterEnabled(adapter.name, !adapter.enabled)
                    loadData()
                  } catch {}
                }} style={{
                  padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                  border: 'none', cursor: 'pointer',
                  background: adapter.enabled ? 'var(--color-warning)' : 'var(--color-success)',
                  color: '#fff', fontSize: 11,
                  display: 'flex', alignItems: 'center', gap: 4,
                }}>
                  {adapter.enabled ? <><PowerOff size={12} /> Disable</> : <><Power size={12} /> Enable</>}
                </button>

                <button onClick={() => {
                  const fields: Record<string, string> = {}
                  if (adapter.extra) {
                    for (const [k, v] of Object.entries(adapter.extra)) {
                      fields[k] = String(v)
                    }
                  }
                  setEditFields(fields)
                  setEditAdapter(adapter.name)
                  setError('')
                }} style={{
                  padding: '4px 10px', borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border)', cursor: 'pointer',
                  background: 'var(--color-surface)', color: 'var(--text-secondary)',
                  fontSize: 11,
                }}>
                  Edit
                </button>

                <button onClick={async () => {
                  if (!confirm(`Remove adapter "${adapter.name}"?`)) return
                  try {
                    await App.RemoveIMAdapter(adapter.name)
                    loadData()
                  } catch {}
                }} style={{
                  padding: '4px 6px', borderRadius: 'var(--radius-sm)',
                  border: 'none', cursor: 'pointer',
                  background: 'transparent', color: 'var(--color-error)',
                }}>
                  <Trash2 size={14} />
                </button>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ── Add Adapter Dialog ──

function AddAdapterDialog({ platforms, onAdd, onCancel, error }: {
  platforms: IMPlatformMeta[]
  onAdd: (name: string, platform: string, fields: Record<string, string>) => void
  onCancel: () => void
  error: string
}) {
  const [selectedPlatform, setSelectedPlatform] = useState('')
  const [adapterName, setAdapterName] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [localError, setLocalError] = useState('')

  const platform = platforms.find(p => p.id === selectedPlatform)

  function handleAdd() {
    if (!selectedPlatform) { setLocalError('Select a platform'); return }
    if (!adapterName.trim()) { setLocalError('Enter an adapter name'); return }
    if (platform && !platform.qrAuth) {
      for (const f of platform.fields) {
        if (!fields[f.key]?.trim()) { setLocalError(`${f.label} is required`); return }
      }
    }
    onAdd(adapterName.trim(), selectedPlatform, fields)
  }

  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 480 }}>
      <h3 style={{ margin: 0 }}>Add IM Adapter</h3>

      {(error || localError) && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error || localError}</div>}

      {/* Platform select */}
      <label style={{ display: 'block' }}>
        <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>Platform</span>
        <select value={selectedPlatform} onChange={e => { setSelectedPlatform(e.target.value); setFields({}); setLocalError('') }} style={{
          width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-bg)', border: '1px solid var(--color-border)',
          color: 'var(--text-primary)', fontSize: 13, outline: 'none',
        }}>
          <option value="">Select platform...</option>
          {platforms.map(p => <option key={p.id} value={p.id}>{p.displayName}</option>)}
        </select>
      </label>

      {/* Adapter name */}
      <label style={{ display: 'block' }}>
        <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>Adapter Name</span>
        <input value={adapterName} onChange={e => setAdapterName(e.target.value)} placeholder="e.g. dingtalk-alerts, telegram-dev" style={{
          width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-bg)', border: '1px solid var(--color-border)',
          color: 'var(--text-primary)', fontSize: 13, outline: 'none',
        }} />
      </label>

      {/* Platform fields */}
      {platform?.qrAuth && (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 12, fontStyle: 'italic' }}>
          This platform uses QR code authentication. Save to start the pairing process.
        </div>
      )}
      {platform?.fields.map(f => (
        <label key={f.key} style={{ display: 'block' }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{f.label}</span>
          <input
            type={f.secret ? 'password' : 'text'}
            value={fields[f.key] || ''}
            onChange={e => setFields(prev => ({ ...prev, [f.key]: e.target.value }))}
            placeholder={f.placeholder}
            style={{
              width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontSize: 13, outline: 'none',
            }}
          />
        </label>
      ))}

      <div style={{ display: 'flex', gap: 12 }}>
        <button onClick={onCancel} style={{
          flex: 1, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-surface)', border: '1px solid var(--color-border)',
          color: 'var(--text-secondary)', cursor: 'pointer',
        }}>Cancel</button>
        <button onClick={handleAdd} style={{
          flex: 2, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontWeight: 600,
        }}>Add Adapter</button>
      </div>
    </div>
  )
}

// ── Edit Adapter Dialog ──

function EditAdapterDialog({ adapter, platform, fields, setFields, onSave, onCancel, error }: {
  adapter: IMAdapterInfo
  platform?: IMPlatformMeta
  fields: Record<string, string>
  setFields: (f: Record<string, string>) => void
  onSave: () => void
  onCancel: () => void
  error: string
}) {
  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 480 }}>
      <h3 style={{ margin: 0 }}>Edit: {adapter.name}</h3>
      <div style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>
        {platform?.displayName || adapter.platform}
      </div>

      {error && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error}</div>}

      {platform?.fields.map(f => (
        <label key={f.key} style={{ display: 'block' }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{f.label}</span>
          <input
            type={f.secret ? 'password' : 'text'}
            value={fields[f.key] || ''}
            onChange={e => setFields({ ...fields, [f.key]: e.target.value })}
            placeholder={f.placeholder}
            style={{
              width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontSize: 13, outline: 'none',
            }}
          />
        </label>
      ))}

      {!platform?.fields?.length && !platform?.qrAuth && (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
          No configurable fields for this adapter.
        </div>
      )}

      <div style={{ display: 'flex', gap: 12 }}>
        <button onClick={onCancel} style={{
          flex: 1, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-surface)', border: '1px solid var(--color-border)',
          color: 'var(--text-secondary)', cursor: 'pointer',
        }}>Cancel</button>
        <button onClick={onSave} style={{
          flex: 2, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontWeight: 600,
        }}>Save</button>
      </div>
    </div>
  )
}
