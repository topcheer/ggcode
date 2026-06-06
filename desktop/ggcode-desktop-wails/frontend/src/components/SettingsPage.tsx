import React, { useState, useEffect, useCallback } from 'react'
import { ArrowLeft, Eye, EyeOff, Plus, Zap, RefreshCw, Check } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { EventsEmit } from '../../wailsjs/runtime/runtime'
import { useTranslation, type Locale } from '../i18n'

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
  const { t, locale, setLocale } = useTranslation()
  const [tab, setTab] = useState<SettingsTab>('provider')
  const [vendors, setVendors] = useState<string[]>([])
  const [endpoints, setEndpoints] = useState<{ key: string; displayName: string }[]>([])
  const [models, setModels] = useState<string[]>([])
  const [currentVendor, setCurrentVendor] = useState('')
  const [currentEndpoint, setCurrentEndpoint] = useState('')
  const [currentModel, setCurrentModel] = useState('')

  // Resolved endpoint info
  const [resolvedBaseURL, setResolvedBaseURL] = useState('')
  const [resolvedProtocol, setResolvedProtocol] = useState('')
  const [apiKeySet, setApiKeySet] = useState(false)
  const [apiKeyMasked, setApiKeyMasked] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [showKey, setShowKey] = useState(false)

  // Model refresh
  const [modelsLoading, setModelsLoading] = useState(false)
  const [modelsSource, setModelsSource] = useState<'static' | 'dynamic' | 'error'>('static')
  const [modelsError, setModelsError] = useState('')

  const [language, setLanguage] = useState<Locale>('en')
  const [defaultMode, setDefaultMode] = useState('supervised')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  // Impersonation state
  const [presets, setPresets] = useState<ImpersonationPreset[]>([])
  const [selectedPreset, setSelectedPreset] = useState('none')
  const [impVersion, setImpVersion] = useState('')

  // Load initial data
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        // Load resolved endpoint info (includes masked API key, base URL, model)
        const resolved = await App.GetResolvedEndpoint() as any
        if (cancelled) return
        if (resolved) {
          setCurrentVendor(resolved.vendorId || '')
          setCurrentEndpoint(resolved.endpointId || '')
          setCurrentModel(resolved.model || '')
          setResolvedBaseURL(resolved.baseUrl || '')
          setResolvedProtocol(resolved.protocol || '')
          setApiKeySet(resolved.apiKeySet || false)
          setApiKeyMasked(resolved.apiKeyMasked || '')
          // Use models from resolved if available
          if (resolved.models && resolved.models.length > 0) {
            setModels(resolved.models)
          }
        }

        // Load general config for language, mode, impersonation
        const cfg = await App.GetConfig() as any
        if (cancelled) return
        setLanguage((cfg.language === 'zh' || cfg.language === 'zh-CN') ? 'zh' : 'en')
        setDefaultMode(cfg.defaultMode || 'supervised')
        setSelectedPreset(cfg.impersonatePreset || 'none')
        setImpVersion(cfg.impersonateCustomVersion || '')

        // Vendor list
        const v = await App.GetVendors()
        if (cancelled) return
        setVendors(v as string[])

        // Endpoints for current vendor
        if (resolved?.vendorId) {
          const eps = await App.GetEndpoints(resolved.vendorId)
          if (cancelled) return
          setEndpoints((eps as any[]) || [])
        }

        // If no models from resolved, load static list
        if ((!resolved?.models || resolved.models.length === 0) && resolved?.vendorId && resolved?.endpointId) {
          const ms = await App.GetModels(resolved.vendorId, resolved.endpointId)
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
    setResolvedBaseURL('')
    setApiKeySet(false)
    setApiKeyMasked('')
    setModels([])
    setModelsSource('static')
    const eps = await App.GetEndpoints(vendor) as any[]
    setEndpoints(eps || [])
  }, [])

  const handleEndpointChange = useCallback(async (endpoint: string) => {
    setCurrentEndpoint(endpoint)
    setCurrentModel('')
    setModelsSource('static')
    setModelsError('')

    // Get endpoint details (base URL, masked API key, models)
    try {
      const details = await App.GetEndpointDetails(currentVendor, endpoint) as any
      if (details) {
        setResolvedBaseURL(details.baseUrl || '')
        setResolvedProtocol(details.protocol || '')
        setApiKeySet(details.apiKeySet || false)
        setApiKeyMasked(details.apiKeyMasked || '')
        if (details.models && details.models.length > 0) {
          setModels(details.models)
        }
      }
    } catch {}

    // Also load static models as fallback
    try {
      const ms = await App.GetModels(currentVendor, endpoint) as string[]
      if (ms && ms.length > 0) {
        setModels(prev => prev.length > 0 ? prev : ms)
      }
    } catch {}
  }, [currentVendor])

  // Refresh models dynamically from API
  const handleRefreshModels = useCallback(async () => {
    if (!currentVendor || !currentEndpoint) return
    setModelsLoading(true)
    setModelsError('')
    try {
      // Pass user-entered API key if available, otherwise backend auto-resolves from config
      const ms = await App.FetchModels(currentVendor, currentEndpoint, apiKey, '') as string[]
      if (ms && ms.length > 0) {
        setModels(ms)
        setModelsSource('dynamic')
      } else {
        setModelsError('No models found')
      }
    } catch (e: any) {
      setModelsError(e?.message || 'Failed to fetch models')
    } finally {
      setModelsLoading(false)
    }
  }, [currentVendor, currentEndpoint, apiKey])

  const save = useCallback(async () => {
    setSaving(true)
    setSaved(false)
    try {
      await App.UpdateConfig({
        vendor: currentVendor,
        endpoint: currentEndpoint,
        model: currentModel,
        language,
        defaultMode,
        baseURL: resolvedBaseURL,
      } as any)
      if (apiKey) {
        await App.SaveAPIKey(currentVendor, currentEndpoint, apiKey)
        setApiKey('')
        setApiKeySet(true)
      }
      setSaved(true)
      EventsEmit('config:updated')
      setTimeout(() => setSaved(false), 2000)
    } catch (e) {
      console.error('Save failed:', e)
    } finally {
      setSaving(false)
    }
  }, [currentVendor, currentEndpoint, currentModel, apiKey, language, defaultMode, resolvedBaseURL])

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
    { id: 'provider', label: t('settings.title') },
    { id: 'impersonation', label: t('settings.impersonate') },
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
          <ArrowLeft size={14} /> <span style={{ marginLeft: 4 }}>{t('onboarding.back')}</span>
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
      <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px', maxWidth: 600 }}>
        {/* Provider Tab */}
        {tab === 'provider' && (
          <>
            <h3 style={sectionTitle}>LLM {t('settings.title')}</h3>

            <FieldRow label={t('settings.vendor')}>
              <select value={currentVendor} onChange={e => handleVendorChange(e.target.value)} style={selectStyle}>
                <option value="">Choose vendor...</option>
                {vendors.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </FieldRow>

            <FieldRow label={t('settings.endpoint')}>
              <select value={currentEndpoint} onChange={e => handleEndpointChange(e.target.value)} style={selectStyle}>
                <option value="">Choose endpoint...</option>
                {endpoints.map(ep => <option key={ep.key} value={ep.key}>{ep.displayName || ep.key}</option>)}
              </select>
            </FieldRow>

            {/* Resolved info: Base URL + Protocol */}
            <FieldRow label={t('settings.baseUrl')}>
              <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <input
                  value={resolvedBaseURL}
                  onChange={e => setResolvedBaseURL(e.target.value)}
                  placeholder="https://api.example.com/v1"
                  style={{
                    ...inputStyle,
                    fontFamily: 'var(--font-mono)', fontSize: 12,
                    flex: 1,
                  }}
                />
                {resolvedProtocol && (
                  <span style={{
                    fontSize: 10, padding: '2px 6px', borderRadius: 'var(--radius-sm)',
                    background: 'rgba(59,130,246,0.15)', color: 'var(--color-info)',
                    flexShrink: 0,
                  }}>{resolvedProtocol}</span>
                )}
              </div>
            </FieldRow>

            {/* API Key: show masked + edit */}
            <FieldRow label={t('settings.apiKey')}>
              <div style={{ display: 'flex', gap: 4, flexDirection: 'column' }}>
                {apiKeySet && !apiKey && (
                  <div style={{
                    display: 'flex', alignItems: 'center', gap: 6,
                    fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--color-success)',
                  }}>
                    <span style={{
                      width: 8, height: 8, borderRadius: 4, background: 'var(--color-success)',
                      display: 'inline-block',
                    }} />
                    <span>Configured: {apiKeyMasked}</span>
                  </div>
                )}
                <div style={{ display: 'flex', gap: 4 }}>
                  <input type={showKey ? 'text' : 'password'} value={apiKey}
                    onChange={e => setApiKey(e.target.value)}
                    placeholder={apiKeySet ? '' : t('settings.apiKeyPlaceholder')}
                    style={{ ...inputStyle, flex: 1 }} />
                  <button onClick={() => setShowKey(p => !p)} style={iconBtnStyle}>
                    {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
                  </button>
                </div>
              </div>
            </FieldRow>

            {/* Model selection with refresh */}
            <FieldRow label={t('settings.model')}>
              <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <select value={currentModel} onChange={e => setCurrentModel(e.target.value)}
                  style={{ ...selectStyle, flex: 1 }}>
                  <option value="">Choose model...</option>
                  {models.map(m => <option key={m} value={m}>{m}</option>)}
                </select>
                <button onClick={handleRefreshModels} disabled={modelsLoading || !currentVendor || !currentEndpoint}
                  title="Refresh models from API" style={iconBtnStyle}>
                  <RefreshCw size={14} className={modelsLoading ? 'spin' : ''} />
                </button>
              </div>
              {modelsSource === 'dynamic' && (
                <span style={{ fontSize: 10, color: 'var(--color-success)', marginTop: 2, display: 'block' }}>
                  {models.length} models loaded from API
                </span>
              )}
              {modelsError && (
                <span style={{ fontSize: 10, color: '#f87171', marginTop: 2, display: 'block' }}>{modelsError}</span>
              )}
            </FieldRow>

            <h3 style={{ ...sectionTitle, marginTop: 24 }}>{t('settings.permissionMode')}</h3>
            <FieldRow label={t('settings.permissionMode')}>
              <select value={defaultMode} onChange={e => setDefaultMode(e.target.value)} style={selectStyle}>
                <option value="supervised">Supervised (confirm each tool)</option>
                <option value="bypass">Bypass (auto-approve safe tools)</option>
                <option value="autopilot">Autopilot (approve everything)</option>
              </select>
            </FieldRow>
            <FieldRow label={t('settings.language')}>
              <select value={language} onChange={e => {
                const newLang = e.target.value as Locale
                setLanguage(newLang)
                setLocale(newLang) // Immediate UI switch
              }} style={selectStyle}>
                <option value="en">English</option>
                <option value="zh">中文</option>
              </select>
            </FieldRow>

            <button onClick={save} disabled={saving || !currentVendor || !currentEndpoint}
              style={{ ...primaryBtnStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
              {saved ? <><Check size={14} /> {t('settings.saved')}</> : saving ? t('settings.saving') : t('settings.save')}
            </button>
          </>
        )}

        {/* Impersonation Tab */}
        {tab === 'impersonation' && (
          <>
            <h3 style={sectionTitle}>{t('settings.impersonate')}</h3>
            <p style={{ fontSize: 12, color: 'var(--text-tertiary)', margin: '0 0 16px' }}>
              {t('settings.impersonateHint')} — some providers require specific headers.
            </p>
            <FieldRow label={t('settings.identity')}>
              <select value={selectedPreset} onChange={e => {
                const id = e.target.value
                setSelectedPreset(id)
                const p = presets.find(p => p.id === id)
                if (p && p.defaultVersion) setImpVersion(p.defaultVersion)
              }} style={selectStyle}>
                {presets.map(p => (
                  <option key={p.id} value={p.id}>{p.displayName}</option>
                ))}
              </select>
            </FieldRow>
            <FieldRow label={t('settings.impersonateVersion')}>
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
              {saving ? t('settings.saving') : t('settings.impersonateApply')}
            </button>
          </>
        )}

        {/* Add Endpoint Tab */}
        {tab === 'addEndpoint' && (
          <AddEndpointForm vendors={vendors} currentVendor={currentVendor} onDone={() => {
            handleVendorChange(currentVendor)
            setTab('provider')
          }} />
        )}
      </div>

      {/* CSS for spinning refresh icon */}
      <style>{`
        @keyframes spin { to { transform: rotate(360deg) } }
        .spin { animation: spin 1s linear infinite; }
      `}</style>
    </div>
  )
}

// Add Endpoint Form
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

// Shared Components
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
