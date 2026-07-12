import React, { useState, useEffect } from 'react'
import { ChevronRight, FolderOpen } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation, LOCALE_LABELS, type Locale } from '../i18n'

interface Props {
  onComplete: () => void
}

const MODES = [
  { id: 'supervised', labelKey: 'onboarding.modeSupervised', descKey: 'onboarding.modeSupervisedDesc', color: '#eab308' },
  { id: 'auto', labelKey: 'onboarding.modeAuto', descKey: 'onboarding.modeAutoDesc', color: '#22c55e' },
  { id: 'bypass', labelKey: 'onboarding.modeBypass', descKey: 'onboarding.modeBypassDesc', color: '#ef4444' },
  { id: 'autopilot', labelKey: 'onboarding.modeAutopilot', descKey: 'onboarding.modeAutopilotDesc', color: '#a855f7' },
]

interface VendorPreset {
  id: string
  displayName: string
  endpoints: EndpointPreset[]
}

interface EndpointPreset {
  id: string
  displayName: string
  models: string[]
  defaultEndpoint: boolean
}

const CUSTOM_ID = '__custom__'
const CUSTOM_PROTOCOLS = ['openai', 'anthropic', 'ollama']

export function Onboarding({ onComplete }: Props) {
  const { t, locale, setLocale } = useTranslation()
  const [step, setStep] = useState<'language' | 'workspace' | 'setup' | 'mode'>('language')
  const [workDir, setWorkDir] = useState('')

  // Onboard form state
  const [presets, setPresets] = useState<VendorPreset[]>([])
  const [selectedVendor, setSelectedVendor] = useState('')
  const [selectedEndpoint, setSelectedEndpoint] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [selectedMode, setSelectedMode] = useState('bypass')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  // Custom provider state
  const [customName, setCustomName] = useState('')
  const [customProtocol, setCustomProtocol] = useState('openai')
  const [customBaseURL, setCustomBaseURL] = useState('')
  const [customModels, setCustomModels] = useState<string[]>([])
  const [fetchingModels, setFetchingModels] = useState(false)

  // A2A enabled by default (aligned with TUI)
  const [a2aEnabled, setA2aEnabled] = useState(true)
  // Auto-fetched models for standard vendor endpoints
  const [fetchedModels, setFetchedModels] = useState<string[]>([])
  const [autoFetching, setAutoFetching] = useState(false)

  const isCustom = selectedVendor === CUSTOM_ID

  const currentPreset = presets.find(p => p.id === selectedVendor)
  const currentEndpoint = currentPreset?.endpoints.find(e => e.id === selectedEndpoint)
  const presetModels = currentEndpoint?.models || []
  // Use fetched models if available, otherwise fall back to preset models
  const models = fetchedModels.length > 0 ? fetchedModels : presetModels
  const showModels = apiKey !== '' && models.length > 0

  useEffect(() => {
    App.GetVendorPresets().then(p => {
      setPresets((p as any[]) || [])
    }).catch(() => {})
  }, [])

  // Auto-fetch models when standard vendor endpoint + API key are set
  useEffect(() => {
    if (isCustom || !selectedVendor || !selectedEndpoint || !apiKey.trim()) {
      setFetchedModels([])
      return
    }
    let cancelled = false
    setAutoFetching(true)
    App.FetchModels(selectedVendor, selectedEndpoint, apiKey, '').then(models => {
      if (!cancelled) {
        const m = (models as string[]) || []
        setFetchedModels(m)
      }
    }).catch(() => {
      if (!cancelled) setFetchedModels([])
    }).finally(() => {
      if (!cancelled) setAutoFetching(false)
    })
    return () => { cancelled = true }
  }, [selectedVendor, selectedEndpoint, apiKey, isCustom])

  const handleSelectDir = async () => {
    try {
      const dir = await App.SelectWorkspace()
      if (dir) {
        setWorkDir(dir)
        // Check if already onboarded
        const needs = await App.NeedsOnboard()
        if (!needs) {
          onComplete()
          return
        }
        setStep('setup')
      }
    } catch {}
  }

  const sanitizeVendorID = (name: string): string =>
    name.trim().toLowerCase().replace(/[^a-z0-9_-]/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '') || 'custom'

  const handleFetchModels = async () => {
    if (!customBaseURL.trim() || !apiKey.trim()) return
    setFetchingModels(true)
    setError('')
    try {
      const models = await App.FetchModels(sanitizeVendorID(customName), 'default', apiKey, customBaseURL)
      setCustomModels((models as string[]) || [])
      if (!models || models.length === 0) {
        setError(t('onboarding.noModelsFound'))
      }
    } catch (e: any) {
      setError(e?.message || t('onboarding.fetchModelsFailed'))
    } finally {
      setFetchingModels(false)
    }
  }

  const handleSubmit = async () => {
    setSaving(true)
    setError('')
    try {
      if (isCustom) {
        const vendorID = sanitizeVendorID(customName)
        const model = selectedModel || customModels[0] || ''
        if (!customName.trim() || !customBaseURL.trim() || !apiKey.trim() || !model) {
          setError(t('onboarding.fillAllCustom'))
          setSaving(false)
          return
        }
        await App.AddCustomEndpoint(vendorID, 'default', customProtocol, customBaseURL, apiKey)
        await App.CompleteOnboard(vendorID, 'default', model, apiKey)
      } else {
        if (!selectedVendor || !selectedEndpoint || !apiKey || !selectedModel) {
          setError(t('onboarding.fillAllFields'))
          setSaving(false)
          return
        }
        await App.CompleteOnboard(selectedVendor, selectedEndpoint, selectedModel, apiKey)
      }
      // Save permission mode
      try { await App.SaveDefaultMode(selectedMode) } catch {}
      // Save A2A setting
      try { await App.SaveA2AEnabled(a2aEnabled) } catch {}
      onComplete()
    } catch (e: any) {
      setError(e?.message || t('onboarding.saveConfigFailed'))
    } finally {
      setSaving(false)
    }
  }

  // ─── Step 0: Language Selection ───
  if (step === 'language') {
    const locales = Object.entries(LOCALE_LABELS) as [Locale, string][]
    return (
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column',
        alignItems: 'center', justifyContent: 'center', gap: 24,
      }}>
        <h1 style={{ fontSize: 28, fontWeight: 700, color: 'var(--text-primary)', margin: 0 }}>
          {t('onboarding.languageTitle')}
        </h1>
        <p style={{ fontSize: 15, color: 'var(--text-secondary)', margin: 0 }}>
          {t('onboarding.languageHint')}
        </p>
        <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)',
          gap: 10, maxWidth: 480, width: '100%',
        }}>
          {locales.map(([code, label]) => (
            <button
              key={code}
              onClick={() => setLocale(code)}
              style={{
                padding: '14px 12px', borderRadius: 'var(--radius-md)',
                border: `2px solid ${locale === code ? 'var(--color-primary)' : 'var(--color-border)'}`,
                background: locale === code ? 'var(--color-primary)' : 'var(--color-card)',
                color: locale === code ? '#fff' : 'var(--text-primary)',
                cursor: 'pointer', fontSize: 14, fontWeight: 500,
                textAlign: 'center',
              }}
            >
              {label}
            </button>
          ))}
        </div>
        <button
          onClick={() => setStep('workspace')}
          style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '12px 32px', borderRadius: 'var(--radius-lg)',
            background: 'var(--color-primary)', color: '#fff',
            border: 'none', cursor: 'pointer', fontSize: 14, fontWeight: 500,
          }}
        >
          {t('onboarding.next')}
          <ChevronRight size={16} />
        </button>
      </div>
    )
  }

  // ─── Step 1: Workspace Selection ───
  if (step === 'workspace') {
    return (
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column',
        alignItems: 'center', justifyContent: 'center', gap: 24,
      }}>
        <h1 style={{ fontSize: 28, fontWeight: 700, color: 'var(--text-primary)', margin: 0 }}>
          {t('onboarding.welcome')}
        </h1>
        <p style={{ fontSize: 15, color: 'var(--text-secondary)', margin: 0 }}>
          {t('onboarding.selectDirHint')}
        </p>
        <button
          onClick={handleSelectDir}
          style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '12px 24px', borderRadius: 'var(--radius-lg)',
            background: 'var(--color-primary)', color: '#fff',
            border: 'none', cursor: 'pointer', fontSize: 14, fontWeight: 500,
          }}
        >
          <FolderOpen size={18} />
          {t('onboarding.chooseDir')}
        </button>
      </div>
    )
  }

  // ─── Step 2: API Setup ───
  // ─── Step 2.5: Permission Mode ───
  if (step === 'mode') {
    return (
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column',
        alignItems: 'center', justifyContent: 'center', gap: 20,
      }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: 'var(--text-primary)', margin: 0 }}>
          {t('settings.permissionMode')}
        </h2>
        <p style={{ color: 'var(--text-secondary)', fontSize: 13, margin: 0, maxWidth: 400, textAlign: 'center' }}>
          {t('onboarding.configHint')}
        </p>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, width: 420 }}>
          {MODES.map(m => (
            <button key={m.id} onClick={() => setSelectedMode(m.id)} style={{
              display: 'flex', alignItems: 'flex-start', gap: 12,
              padding: '14px 16px', borderRadius: 'var(--radius-md)',
              border: `2px solid ${selectedMode === m.id ? m.color : 'var(--color-border)'}`,
              background: selectedMode === m.id ? `${m.color}15` : 'var(--color-card)',
              cursor: 'pointer', textAlign: 'left',
            }}>
              <div style={{
                width: 12, height: 12, borderRadius: '50%',
                background: m.color, marginTop: 2, flexShrink: 0,
                outline: selectedMode === m.id ? '2px solid var(--text-primary)' : 'none',
                outlineOffset: 2,
              }} />
              <div>
                <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--text-primary)' }}>{t(m.labelKey as any)}</div>
                <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 2 }}>{t(m.descKey as any)}</div>
              </div>
            </button>
          ))}
        </div>

        {/* A2A Toggle */}
        <label style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '12px 16px', borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border)', background: 'var(--color-card)',
          cursor: 'pointer', width: 420,
        }}>
          <input
            type="checkbox"
            checked={a2aEnabled}
            onChange={e => setA2aEnabled(e.target.checked)}
            style={{ width: 16, height: 16, cursor: 'pointer' }}
          />
          <div>
            <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--text-primary)' }}>{t('onboarding.a2a')}</div>
            <div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{t('onboarding.a2aDesc')}</div>
          </div>
        </label>

        <div style={{ display: 'flex', gap: 12, width: 420 }}>
          <button onClick={() => setStep('setup')} style={{
            flex: 1, height: 40, borderRadius: 'var(--radius-md)',
            background: 'var(--color-surface)', border: '1px solid var(--color-border)',
            color: 'var(--text-secondary)', fontSize: 14, cursor: 'pointer',
          }}>{t('onboarding.back')}</button>
          <button onClick={handleSubmit} disabled={saving} style={{
            flex: 2, height: 40, borderRadius: 'var(--radius-md)',
            background: saving ? 'var(--color-border)' : 'var(--color-primary)',
            color: '#fff', fontSize: 14, fontWeight: 600,
            border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
          }}>{saving ? t('settings.saving') : t('onboarding.getStarted')}</button>
        </div>
      </div>
    )
  }

  const canProceed = isCustom
    ? !!customName.trim() && !!customBaseURL.trim() && !!apiKey.trim() && (!!selectedModel.trim() || customModels.length > 0)
    : !!selectedVendor && !!selectedEndpoint && !!apiKey && !!selectedModel

  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center', padding: 40,
    }}>
      <div style={{
        width: 480, background: 'var(--color-card)',
        borderRadius: 'var(--radius-xl)', padding: 32,
        border: '1px solid var(--color-border)',
      }}>
        <h2 style={{ fontSize: 20, fontWeight: 600, color: 'var(--text-primary)', margin: '0 0 4px' }}>
          {t('onboarding.configureLLM')} {t('settings.title')}
        </h2>
        <p style={{ fontSize: 13, color: 'var(--text-secondary)', margin: '0 0 24px' }}>
          {t('onboarding.vendorHint')}
        </p>

        {/* Vendor */}
        <label style={{ display: 'block', marginBottom: 16 }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('settings.vendor')}</span>
          <select value={selectedVendor} onChange={e => {
            setSelectedVendor(e.target.value)
            setSelectedEndpoint('')
            setSelectedModel('')
            setApiKey('')
            setCustomModels([])
            setFetchedModels([])
          }} style={selectStyle}>
            <option value="">{t('onboarding.chooseVendor')}</option>
            {presets.map(p => <option key={p.id} value={p.id}>{p.displayName}</option>)}
            <option value={CUSTOM_ID} style={{ fontWeight: 600 }}>{t('onboarding.customProvider')}</option>
          </select>
        </label>

        {isCustom ? (
          <>
            {/* Custom Provider Name */}
            <label style={{ display: 'block', marginBottom: 16 }}>
              <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('onboarding.providerName')}</span>
              <input
                value={customName}
                onChange={e => setCustomName(e.target.value)}
                placeholder={t('onboarding.providerPlaceholder')}
                style={inputStyle}
              />
            </label>

            {/* Protocol */}
            <label style={{ display: 'block', marginBottom: 16 }}>
              <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('onboarding.protocol')}</span>
              <select value={customProtocol} onChange={e => setCustomProtocol(e.target.value)} style={selectStyle}>
                {CUSTOM_PROTOCOLS.map(p => <option key={p} value={p}>{p}</option>)}
              </select>
            </label>

            {/* Base URL */}
            <label style={{ display: 'block', marginBottom: 16 }}>
              <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('settings.baseUrl')}</span>
              <input
                value={customBaseURL}
                onChange={e => setCustomBaseURL(e.target.value)}
                placeholder="https://api.example.com/v1"
                style={inputStyle}
              />
            </label>

            {/* API Key */}
            <label style={{ display: 'block', marginBottom: 16 }}>
              <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('settings.apiKey')}</span>
              <input
                type="password"
                value={apiKey}
                onChange={e => setApiKey(e.target.value)}
                placeholder={t('settings.apiKeyPlaceholder')}
                style={inputStyle}
              />
            </label>

            {/* Fetch Models button + Model */}
            <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
              <button
                onClick={handleFetchModels}
                disabled={fetchingModels || !customBaseURL.trim() || !apiKey.trim()}
                style={{
                  padding: '0 12px', height: 36, borderRadius: 'var(--radius-md)',
                  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                  color: 'var(--text-primary)', fontSize: 12, cursor: 'pointer',
                  whiteSpace: 'nowrap', flexShrink: 0,
                }}
              >{fetchingModels ? t('onboarding.fetchingModels') : t('onboarding.fetchModels')}</button>
              <div style={{ flex: 1 }}>
                {customModels.length > 0 ? (
                  <select value={selectedModel} onChange={e => setSelectedModel(e.target.value)} style={selectStyle}>
                    <option value="">{t('onboarding.chooseModel')}</option>
                    {customModels.map(m => <option key={m} value={m}>{m}</option>)}
                  </select>
                ) : (
                  <input
                    value={selectedModel}
                    onChange={e => setSelectedModel(e.target.value)}
                    placeholder={t('onboarding.modelPlaceholder')}
                    style={inputStyle}
                  />
                )}
              </div>
            </div>
          </>
        ) : (
          <>
            {/* Endpoint */}
            {currentPreset && (
              <label style={{ display: 'block', marginBottom: 16 }}>
                <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('settings.endpoint')}</span>
                <select value={selectedEndpoint} onChange={e => {
                  setSelectedEndpoint(e.target.value)
                  setSelectedModel('')
                  setFetchedModels([])
                }} style={selectStyle}>
                  <option value="">{t('onboarding.chooseEndpoint')}</option>
                  {currentPreset.endpoints.map(ep => (
                    <option key={ep.id} value={ep.id}>{ep.displayName || ep.id}</option>
                  ))}
                </select>
              </label>
            )}

            {/* API Key */}
            {selectedEndpoint && (
              <label style={{ display: 'block', marginBottom: 16 }}>
                <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>{t('settings.apiKey')}</span>
                <input
                  type="password"
                  value={apiKey}
                  onChange={e => setApiKey(e.target.value)}
                  placeholder={t('settings.apiKeyPlaceholder')}
                  style={inputStyle}
                />
              </label>
            )}

            {/* Model */}
            {showModels && (
              <label style={{ display: 'block', marginBottom: 16 }}>
                <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>
                  {t('settings.model')}{autoFetching ? ' (...)' : ''}
                </span>
                <select value={selectedModel} onChange={e => setSelectedModel(e.target.value)} style={selectStyle}>
                  <option value="">{t('onboarding.chooseModel')}</option>
                  {models.map(m => <option key={m} value={m}>{m}</option>)}
                </select>
              </label>
            )}
          </>
        )}

        {error && (
          <div style={{ color: 'var(--color-error)', fontSize: 12, marginBottom: 12 }}>{error}</div>
        )}

        <button
          onClick={() => setStep('mode')}
          disabled={saving || !canProceed}
          style={{
            width: '100%', padding: '10px 0', borderRadius: 'var(--radius-md)',
            background: (saving || !canProceed)
              ? 'var(--color-surface)' : 'var(--color-primary)',
            color: '#fff', border: 'none', cursor: 'pointer',
            fontSize: 14, fontWeight: 500,
            display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
          }}
        >
          {t('onboarding.next')}
          <ChevronRight size={16} />
        </button>
      </div>
    </div>
  )
}

const selectStyle: React.CSSProperties = {
  width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-bg)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', fontSize: 13, outline: 'none',
}

const inputStyle: React.CSSProperties = {
  width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-bg)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', fontSize: 13, outline: 'none',
}
