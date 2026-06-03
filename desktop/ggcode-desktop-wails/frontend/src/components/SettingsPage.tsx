import React, { useState, useEffect, useCallback } from 'react'
import { ArrowLeft, Eye, EyeOff, Plus, Zap } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface Props {
  onBack: () => void
}

type SettingsTab = 'provider' | 'impersonation' | 'addEndpoint'

interface ImpersonationPreset {
  id: string
  displayName: string
  defaultVersion: string
  extraHeaders?: Record<string, string>
}

export function SettingsPage({ onBack }: Props) {
  const [tab, setTab] = useState<SettingsTab>('provider')
  const [vendors, setVendors] = useState<string[]>([])
  const [endpoints, setEndpoints] = useState<{ key: string; displayName: string }[]>([])
  const [models, setModels] = useState<string[]>([])
  const [currentVendor, setCurrentVendor] = useState('')
  const [currentEndpoint, setCurrentEndpoint] = useState('')
  const [currentModel, setCurrentModel] = useState('')
  const [apiKeySet, setApiKeySet] = useState(false)
  const [apiKey, setApiKey] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [language, setLanguage] = useState('en')
  const [defaultMode, setDefaultMode] = useState('supervised')
  const [saving, setSaving] = useState(false)

  // Impersonation state
  const [presets, setPresets] = useState<ImpersonationPreset[]>([])
  const [selectedPreset, setSelectedPreset] = useState('none')
  const [impVersion, setImpVersion] = useState('')

  // Load initial data
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const cfg = await App.GetConfig() as any
        if (cancelled) return
        setCurrentVendor(cfg.vendor || '')
        setCurrentEndpoint(cfg.endpoint || '')
        setCurrentModel(cfg.model || '')
        setApiKeySet(cfg.apiKeySet || false)
        setLanguage(cfg.language || 'en')
        setDefaultMode(cfg.defaultMode || 'supervised')
        setSelectedPreset(cfg.impersonatePreset || 'none')
        setImpVersion(cfg.impersonateCustomVersion || '')

        const v = await App.GetVendors()
        if (cancelled) return
        setVendors(v as string[])

        if (cfg.vendor) {
          const eps = await App.GetEndpoints(cfg.vendor)
          if (cancelled) return
          setEndpoints((eps as any[]) || [])
        }
        if (cfg.vendor && cfg.endpoint) {
          const ms = await App.GetModels(cfg.vendor, cfg.endpoint)
          if (cancelled) return
          setModels((ms as string[]) || [])
        }

        const ps = await App.GetImpersonationPresets()
        if (cancelled) return
        setPresets(ps as ImpersonationPreset[])
      } catch {}
    }
    load()
    return () => { cancelled = true }
  }, [])

  const handleVendorChange = useCallback(async (vendor: string) => {
    setCurrentVendor(vendor)
    setCurrentEndpoint('')
    setCurrentModel('')
    const eps = await App.GetEndpoints(vendor) as any[]
    setEndpoints(eps || [])
    setModels([])
  }, [])

  const handleEndpointChange = useCallback(async (endpoint: string) => {
    setCurrentEndpoint(endpoint)
    setCurrentModel('')
    const ms = await App.GetModels(currentVendor, endpoint) as string[]
    setModels(ms || [])
  }, [currentVendor])

  const save = useCallback(async () => {
    setSaving(true)
    try {
      await App.UpdateConfig({
        vendor: currentVendor,
        endpoint: currentEndpoint,
        model: currentModel,
        language,
        defaultMode,
      } as any)
      if (apiKey) {
        await App.SaveAPIKey(currentVendor, currentEndpoint, apiKey)
        setApiKey('')
        setApiKeySet(true)
      }
    } catch (e) {
      console.error('Save failed:', e)
    } finally {
      setSaving(false)
    }
  }, [currentVendor, currentEndpoint, currentModel, apiKey, language, defaultMode])

  const applyImpersonation = useCallback(async () => {
    setSaving(true)
    try {
      await App.ApplyImpersonation(selectedPreset, impVersion, {} as Record<string, string>)
    } catch (e) {
      console.error('Apply failed:', e)
    } finally {
      setSaving(false)
    }
  }, [selectedPreset, impVersion])

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'provider', label: 'Provider' },
    { id: 'impersonation', label: 'Impersonation' },
    { id: 'addEndpoint', label: '+ Endpoint' },
  ]

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Settings nav */}
      <div style={{
        width: 180, background: 'var(--color-nav)',
        borderRight: '1px solid var(--color-border)',
        padding: '12px 0', flexShrink: 0,
      }}>
        <button onClick={onBack} style={backBtnStyle}>
          <ArrowLeft size={14} /> <span style={{ marginLeft: 4 }}>Back</span>
        </button>
        <div style={{ marginTop: 8 }}>
          {tabs.map(t => (
            <button key={t.id} onClick={() => setTab(t.id)} style={{
              display: 'block', width: '100%', textAlign: 'left',
              padding: '6px 16px', border: 'none', cursor: 'pointer',
              background: tab === t.id ? 'var(--color-primary)' : 'transparent',
              color: tab === t.id ? '#fff' : 'var(--text-secondary)',
              fontSize: 13,
            }}>
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px', maxWidth: 560 }}>
        {/* ── Provider Tab ── */}
        {tab === 'provider' && (
          <>
            <h3 style={sectionTitle}>LLM Provider</h3>
            <FieldRow label="Vendor">
              <select value={currentVendor} onChange={e => handleVendorChange(e.target.value)} style={selectStyle}>
                <option value="">Choose vendor...</option>
                {vendors.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </FieldRow>
            <FieldRow label="Endpoint">
              <select value={currentEndpoint} onChange={e => handleEndpointChange(e.target.value)} style={selectStyle}>
                <option value="">Choose endpoint...</option>
                {endpoints.map(ep => <option key={ep.key} value={ep.key}>{ep.displayName || ep.key}</option>)}
              </select>
            </FieldRow>
            <FieldRow label="Model">
              <select value={currentModel} onChange={e => setCurrentModel(e.target.value)} style={selectStyle}>
                <option value="">Choose model...</option>
                {models.map(m => <option key={m} value={m}>{m}</option>)}
              </select>
            </FieldRow>
            <FieldRow label="API Key">
              <div style={{ display: 'flex', gap: 4 }}>
                <input type={showKey ? 'text' : 'password'} value={apiKey}
                  onChange={e => setApiKey(e.target.value)}
                  placeholder={apiKeySet ? '(saved) Enter new key to change' : 'Enter API key...'}
                  style={{ ...inputStyle, flex: 1 }} />
                <button onClick={() => setShowKey(p => !p)} style={iconBtnStyle}>
                  {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
            </FieldRow>

            <h3 style={{ ...sectionTitle, marginTop: 24 }}>Behavior</h3>
            <FieldRow label="Permission Mode">
              <select value={defaultMode} onChange={e => setDefaultMode(e.target.value)} style={selectStyle}>
                <option value="supervised">Supervised (confirm each tool)</option>
                <option value="bypass">Bypass (auto-approve safe tools)</option>
                <option value="autopilot">Autopilot (approve everything)</option>
              </select>
            </FieldRow>
            <FieldRow label="Language">
              <select value={language} onChange={e => setLanguage(e.target.value)} style={selectStyle}>
                <option value="en">English</option>
                <option value="zh">中文</option>
                <option value="ja">日本語</option>
              </select>
            </FieldRow>

            <button onClick={save} disabled={saving} style={primaryBtnStyle}>
              {saving ? 'Saving...' : 'Save'}
            </button>
          </>
        )}

        {/* ── Impersonation Tab ── */}
        {tab === 'impersonation' && (
          <>
            <h3 style={sectionTitle}>Impersonation</h3>
            <p style={{ fontSize: 12, color: 'var(--text-tertiary)', margin: '0 0 16px' }}>
              Set the User-Agent and headers to impersonate another tool. Some providers require specific headers.
            </p>
            <FieldRow label="Identity">
              <select value={selectedPreset} onChange={e => {
                const id = e.target.value
                setSelectedPreset(id)
                // Auto-fill version from preset default
                const p = presets.find(p => p.id === id)
                if (p && p.defaultVersion) {
                  setImpVersion(p.defaultVersion)
                }
              }} style={selectStyle}>
                {presets.map(p => (
                  <option key={p.id} value={p.id}>{p.displayName}</option>
                ))}
              </select>
            </FieldRow>
            <FieldRow label="Version">
              <input value={impVersion} onChange={e => setImpVersion(e.target.value)}
                placeholder="Version string..." style={inputStyle} />
            </FieldRow>
            {selectedPreset !== 'none' && (() => {
              const p = presets.find(p => p.id === selectedPreset)
              if (!p?.extraHeaders || Object.keys(p.extraHeaders).length === 0) return null
              return (
                <FieldRow label="Extra Headers">
                  <div style={{ fontSize: 12, fontFamily: 'var(--font-mono)', color: 'var(--text-tertiary)' }}>
                    {Object.entries(p.extraHeaders).map(([k, v]) => (
                      <div key={k}>{k}: {v}</div>
                    ))}
                  </div>
                </FieldRow>
              )
            })()}

            <button onClick={applyImpersonation} disabled={saving} style={primaryBtnStyle}>
              {saving ? 'Applying...' : 'Apply'}
            </button>
          </>
        )}

        {/* ── Add Endpoint Tab ── */}
        {tab === 'addEndpoint' && (
          <AddEndpointForm vendors={vendors} currentVendor={currentVendor} onDone={() => {
            // Refresh endpoints after adding
            handleVendorChange(currentVendor)
            setTab('provider')
          }} />
        )}
      </div>
    </div>
  )
}

// ── Add Endpoint Form (mirrors Fyne's showAddEndpointDialog) ──

function AddEndpointForm({ vendors, currentVendor, onDone }: {
  vendors: string[], currentVendor: string, onDone: () => void
}) {
  const [vendor, setVendor] = useState(currentVendor)
  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState('openai')
  const [baseURL, setBaseURL] = useState('')
  const [epApiKey, setEpApiKey] = useState('')
  const [status, setStatus] = useState('')
  const [saving, setSaving] = useState(false)

  const testConnection = useCallback(async () => {
    if (!baseURL) { setStatus('Base URL required'); return }
    setStatus('Testing...')
    try {
      // Use Go backend to test connection
      const result = await App.TestEndpointConnection(protocol, baseURL, epApiKey) as any
      setStatus(result.message || `Found ${result.modelCount || 0} models`)
    } catch (e: any) {
      setStatus('Failed: ' + (e.message || e))
    }
  }, [protocol, baseURL, epApiKey])

  const save = useCallback(async () => {
    if (!name || !baseURL || !vendor) return
    setSaving(true)
    try {
      await App.AddCustomEndpoint(vendor, name, protocol, baseURL, epApiKey)
      onDone()
    } catch (e: any) {
      setStatus('Error: ' + (e.message || e))
    } finally {
      setSaving(false)
    }
  }, [vendor, name, protocol, baseURL, epApiKey, onDone])

  return (
    <>
      <h3 style={sectionTitle}>Add Custom Endpoint</h3>
      <FieldRow label="Vendor">
        <select value={vendor} onChange={e => setVendor(e.target.value)} style={selectStyle}>
          {vendors.map(v => <option key={v} value={v}>{v}</option>)}
        </select>
      </FieldRow>
      <FieldRow label="Name">
        <input value={name} onChange={e => setName(e.target.value)}
          placeholder="My Endpoint" style={inputStyle} />
      </FieldRow>
      <FieldRow label="Protocol">
        <select value={protocol} onChange={e => setProtocol(e.target.value)} style={selectStyle}>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="google">Google</option>
        </select>
      </FieldRow>
      <FieldRow label="Base URL">
        <input value={baseURL} onChange={e => setBaseURL(e.target.value)}
          placeholder="https://api.example.com/v1" style={inputStyle} />
      </FieldRow>
      <FieldRow label="API Key">
        <input type="password" value={epApiKey} onChange={e => setEpApiKey(e.target.value)}
          placeholder="sk-..." style={inputStyle} />
      </FieldRow>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginLeft: 156 }}>
        <button onClick={testConnection} style={secondaryBtnStyle}>
          <Zap size={12} /> Test
        </button>
        {status && <span style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>{status}</span>}
      </div>
      <button onClick={save} disabled={saving || !name || !baseURL} style={{ ...primaryBtnStyle, marginTop: 16 }}>
        {saving ? 'Adding...' : 'Add Endpoint'}
      </button>
    </>
  )
}

// ── Shared Components ──

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', marginBottom: 16 }}>
      <span style={{ width: 140, color: 'var(--text-secondary)', fontSize: 13, flexShrink: 0, paddingTop: 6 }}>{label}</span>
      <div style={{ flex: 1 }}>{children}</div>
    </div>
  )
}

const sectionTitle: React.CSSProperties = {
  fontSize: 16, fontWeight: 600, color: 'var(--text-primary)',
  margin: '0 0 16px', paddingBottom: 8,
  borderBottom: '1px solid var(--color-border)',
}

const selectStyle: React.CSSProperties = {
  width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-bg)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', fontSize: 13, outline: 'none',
}

const inputStyle: React.CSSProperties = {
  width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-bg)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', fontSize: 13, outline: 'none', fontFamily: 'inherit',
}

const backBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--text-secondary)',
  cursor: 'pointer', display: 'flex', alignItems: 'center',
  padding: '6px 16px', fontSize: 13,
}

const iconBtnStyle: React.CSSProperties = {
  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-md)', cursor: 'pointer',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  width: 36, height: 36, color: 'var(--text-tertiary)', flexShrink: 0,
}

const primaryBtnStyle: React.CSSProperties = {
  marginTop: 24, padding: '8px 20px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-primary)', color: '#fff',
  border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500,
}

const secondaryBtnStyle: React.CSSProperties = {
  padding: '6px 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-surface)', color: 'var(--text-secondary)',
  border: '1px solid var(--color-border)', cursor: 'pointer', fontSize: 12,
  display: 'flex', alignItems: 'center', gap: 4,
}
