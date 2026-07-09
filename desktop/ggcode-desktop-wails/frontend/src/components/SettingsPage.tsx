import React, { useState, useEffect, useCallback } from 'react'
import { ArrowLeft, Eye, EyeOff, Plus, Zap, RefreshCw, Check, Server, Radio, PanelRight, Terminal, Share2, Info, Shield, FolderOpen, Code2 } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { EventsEmit } from '../../wailsjs/runtime/runtime'
import { useTranslation, type Locale } from '../i18n'
import { ViewMode } from '../types'

interface Props {
  onBack: () => void
  onNavigate?: (view: ViewMode) => void
  onOpenContext?: () => void
  onOpenShare?: () => void
  onOpenAbout?: () => void
  showToast?: (type: 'success' | 'error' | 'info', message: string) => void
}

type SettingsTab = 'provider' | 'agent' | 'impersonation' | 'addEndpoint' | 'integrations' | 'diagnostics' | 'lsp'

interface ImpersonationPreset {
  id: string
  displayName: string
  defaultVersion: string
  extraHeaders?: Record<string, string>
}

export function SettingsPage({ onBack, onNavigate, onOpenContext, onOpenShare, onOpenAbout, showToast }: Props) {
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

  // Endpoint limits
  const [contextWindow, setContextWindow] = useState('')
  const [maxTokens, setMaxTokens] = useState('')

  // Anthropic OAuth
  const [oauthConnected, setOauthConnected] = useState(false)
  const [oauthBusy, setOauthBusy] = useState(false)

  // Impersonation state
  const [presets, setPresets] = useState<ImpersonationPreset[]>([])
  const [selectedPreset, setSelectedPreset] = useState('none')
  const [impVersion, setImpVersion] = useState('')

  // Load initial data
  const [lspStatus, setLspStatus] = useState<{ id: string; display_name: string; available: boolean; binary: string; install_hint: string; override: boolean; can_install: boolean; install_options: { id: string; label: string; binary: string; recommended: boolean; scope: string }[] }[]>([])
  const [installing, setInstalling] = useState<string | null>(null)
  const [installResult, setInstallResult] = useState<{ lang: string; success: boolean; output: string } | null>(null)

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

        // Auto-refresh models from API (online discovery)
        if (resolved?.vendorId && resolved?.endpointId) {
          try {
            const onlineModels = await App.FetchModels(resolved.vendorId, resolved.endpointId, '', '') as string[]
            if (!cancelled && onlineModels && onlineModels.length > 0) {
              setModels(onlineModels)
              setModelsSource('dynamic')
            }
          } catch {
            // Online discovery failed — keep static/resolved list as fallback
          }
        }

        const ps = await App.GetImpersonationPresets()
        if (cancelled) return
        setPresets(ps as ImpersonationPreset[])
      } catch (e: any) {
        showToast?.('error', `Failed to load settings: ${e?.message || e}`)
      }
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
    try {
      const eps = await App.GetEndpoints(vendor) as any[]
      setEndpoints(eps || [])
    } catch (e: any) {
      showToast?.('error', `Failed to load endpoints: ${e?.message || e}`)
    }
  }, [showToast])

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
        setContextWindow(details.contextWindow ? String(details.contextWindow) : '')
        setMaxTokens(details.maxTokens ? String(details.maxTokens) : '')
        if (details.models && details.models.length > 0) {
          setModels(details.models)
        }
      }
    } catch (e: any) {
      showToast?.('error', `Failed to load endpoint details: ${e?.message || e}`)
    }

    // Load OAuth status if this is an Anthropic OAuth endpoint
    if (currentVendor === 'anthropic' && endpoint === 'oauth') {
      try {
        const connected = await App.GetAnthropicOAuthStatus() as any
        setOauthConnected(!!connected)
      } catch { setOauthConnected(false) }
    } else {
      setOauthConnected(false)
    }

    // Also load static models as fallback
    try {
      const ms = await App.GetModels(currentVendor, endpoint) as string[]
      if (ms && ms.length > 0) {
        setModels(ms)
      }
    } catch (e: any) {
      showToast?.('error', `Failed to load static models: ${e?.message || e}`)
    }

    // Auto-refresh models from API (online discovery)
    // Only fetch if API key is available; fall back to static list on failure
    try {
      const ms = await App.FetchModels(currentVendor, endpoint, '', '') as string[]
      if (ms && ms.length > 0) {
        setModels(ms)
        setModelsSource('dynamic')
      }
    } catch {
      // Online discovery failed — keep static list as fallback
    }
  }, [currentVendor, showToast])

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
      const message = e?.message || 'Failed to fetch models'
      setModelsError(message)
      showToast?.('error', `Failed to fetch models: ${message}`)
    } finally {
      setModelsLoading(false)
    }
  }, [currentVendor, currentEndpoint, apiKey, showToast])

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
      setLocale(language)
      showToast?.('success', 'Settings saved')
      EventsEmit('config:updated')
      setTimeout(() => setSaved(false), 2000)
    } catch (e: any) {
      showToast?.('error', `Failed to save settings: ${e?.message || e}`)
      console.error('Save failed:', e)
    } finally {
      setSaving(false)
    }
  }, [currentVendor, currentEndpoint, apiKey, language, defaultMode, resolvedBaseURL, contextWindow, maxTokens, setLocale, showToast])

  // Save endpoint limits
  const saveEndpointLimits = useCallback(async () => {
    if (!currentVendor || !currentEndpoint) return
    const cw = contextWindow ? parseInt(contextWindow, 10) : 0
    const mt = maxTokens ? parseInt(maxTokens, 10) : 0
    if (Number.isNaN(cw) || Number.isNaN(mt)) {
      showToast?.('error', 'Context window and max tokens must be numbers')
      return
    }
    try {
      await App.SetEndpointLimits(currentVendor, currentEndpoint, cw, mt)
      showToast?.('success', 'Endpoint limits saved')
      EventsEmit('config:updated')
    } catch (e: any) {
      showToast?.('error', `Failed to save limits: ${e?.message || e}`)
    }
  }, [currentVendor, currentEndpoint, contextWindow, maxTokens, showToast])

  // Anthropic OAuth login
  const handleOAuthLogin = useCallback(async () => {
    setOauthBusy(true)
    try {
      await App.StartAnthropicOAuth()
      // Now wait for the callback in background
      await App.CompleteAnthropicOAuth()
      setOauthConnected(true)
      showToast?.('success', 'Anthropic OAuth login successful')
      EventsEmit('config:updated')
    } catch (e: any) {
      showToast?.('error', `OAuth login failed: ${e?.message || e}`)
    } finally {
      setOauthBusy(false)
    }
  }, [showToast])

  // Anthropic OAuth logout
  const handleOAuthLogout = useCallback(async () => {
    try {
      await App.LogoutAnthropicOAuth()
      setOauthConnected(false)
      showToast?.('success', 'Anthropic OAuth logged out')
      EventsEmit('config:updated')
    } catch (e: any) {
      showToast?.('error', `OAuth logout failed: ${e?.message || e}`)
    }
  }, [showToast])

  const applyImpersonation = useCallback(async () => {
    setSaving(true)
    try {
      await App.ApplyImpersonation(selectedPreset, impVersion, {} as Record<string, string>)
      showToast?.('success', 'Impersonation settings applied')
      EventsEmit('config:updated')
    } catch (e: any) {
      showToast?.('error', `Failed to apply impersonation: ${e?.message || e}`)
      console.error('Apply failed:', e)
    } finally {
      setSaving(false)
    }
  }, [selectedPreset, impVersion, showToast])

  const openView = useCallback((view: ViewMode) => {
    onNavigate?.(view)
  }, [onNavigate])

  const modeInfo = getModeInfo(defaultMode)

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'provider', label: t('settings.title') },
    { id: 'agent', label: 'Agent & Safety' },
    { id: 'integrations', label: 'Integrations' },
    { id: 'diagnostics', label: 'Diagnostics' },
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

            {/* Anthropic OAuth: login/logout when vendor=anthropic and endpoint=oauth */}
            {currentVendor === 'anthropic' && currentEndpoint === 'oauth' && (
              <FieldRow label="Anthropic OAuth">
                <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                  <span style={{
                    fontSize: 12,
                    color: oauthConnected ? 'var(--color-success)' : 'var(--text-tertiary)',
                    display: 'flex', alignItems: 'center', gap: 6,
                  }}>
                    <span style={{
                      width: 8, height: 8, borderRadius: 4,
                      background: oauthConnected ? 'var(--color-success)' : 'var(--text-tertiary)',
                      display: 'inline-block',
                    }} />
                    {oauthConnected ? 'Connected' : 'Not connected'}
                  </span>
                  {!oauthConnected ? (
                    <button onClick={handleOAuthLogin} disabled={oauthBusy}
                      style={{ ...primaryBtnStyle, padding: '4px 12px', fontSize: 12 }}>
                      {oauthBusy ? 'Logging in...' : 'Login'}
                    </button>
                  ) : (
                    <button onClick={handleOAuthLogout}
                      style={{ ...iconBtnStyle, padding: '4px 12px', fontSize: 12, color: 'var(--color-danger)' }}>
                      Logout
                    </button>
                  )}
                </div>
              </FieldRow>
            )}

            {/* Context Window & Max Tokens */}
            <FieldRow label="Context Window & Max Tokens">
              <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <input
                  type="number"
                  value={contextWindow}
                  onChange={e => setContextWindow(e.target.value)}
                  placeholder="auto"
                  style={{ ...inputStyle, flex: 1, fontFamily: 'var(--font-mono)', fontSize: 12 }}
                />
                <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>tokens</span>
                <span style={{ fontSize: 11, color: 'var(--text-tertiary)', margin: '0 4px' }}>/</span>
                <input
                  type="number"
                  value={maxTokens}
                  onChange={e => setMaxTokens(e.target.value)}
                  placeholder="auto"
                  style={{ ...inputStyle, flex: 1, fontFamily: 'var(--font-mono)', fontSize: 12 }}
                />
                <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>max out</span>
                <button onClick={saveEndpointLimits} disabled={!currentVendor || !currentEndpoint}
                  title="Save limits" style={iconBtnStyle}>
                  <Check size={14} />
                </button>
              </div>
              <span style={{ display: 'block', marginTop: 4, fontSize: 11, color: 'var(--text-tertiary)' }}>
                Set to 0 or leave empty for auto-detection from model specs.
              </span>
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

        {/* Agent & Safety Tab */}
        {tab === 'agent' && (
          <>
            <h3 style={sectionTitle}>Agent & Safety</h3>
            <p style={hintStyle}>
              Review how much autonomy the desktop agent currently has. This summarizes existing behavior without changing advanced tool policies.
            </p>

            <div style={{ ...summaryCardStyle, borderColor: modeInfo.border, background: modeInfo.background }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
                <span style={{ width: 34, height: 34, borderRadius: 'var(--radius-md)', display: 'flex', alignItems: 'center', justifyContent: 'center', background: modeInfo.iconBackground, color: modeInfo.color }}>
                  <Shield size={18} />
                </span>
                <div>
                  <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--text-primary)' }}>{modeInfo.title}</div>
                  <div style={{ fontSize: 12, color: modeInfo.color }}>{defaultMode || 'supervised'}</div>
                </div>
              </div>
              <p style={{ margin: 0, fontSize: 12, lineHeight: 1.6, color: 'var(--text-secondary)' }}>{modeInfo.description}</p>
            </div>

            <FieldRow label="Permission mode">
              <select value={defaultMode} onChange={e => setDefaultMode(e.target.value)} style={selectStyle}>
                <option value="supervised">Supervised (confirm each tool)</option>
                <option value="auto">Auto (safe tools only)</option>
                <option value="plan">Plan (read-only)</option>
                <option value="bypass">Bypass (auto-approve most tools)</option>
                <option value="autopilot">Autopilot (high autonomy)</option>
              </select>
              <span style={{ display: 'block', marginTop: 6, fontSize: 11, color: 'var(--text-tertiary)' }}>
                Save to persist this default mode. Runtime mode changes still follow the app's normal mode handling.
              </span>
            </FieldRow>

            <h3 style={{ ...sectionTitle, marginTop: 24 }}>Workspace & file access</h3>
            <p style={hintStyle}>
              File tools operate within the current workspace and configured allowed directories. Advanced allowed-directory editing is intentionally not exposed here yet to avoid changing safety boundaries accidentally.
            </p>
            <FeatureGrid>
              <FeatureCard
                icon={<FolderOpen size={18} />}
                title="File Browser"
                description="Browse files in the current workspace using the existing desktop file browser."
                action="Open Files"
                onClick={() => openView('files')}
              />
              <FeatureCard
                icon={<PanelRight size={18} />}
                title="Context Usage"
                description="Inspect active context usage and token/cache totals before running long tasks."
                action="Open Context"
                onClick={onOpenContext}
              />
            </FeatureGrid>

            <button onClick={save} disabled={saving || !currentVendor || !currentEndpoint}
              style={{ ...primaryBtnStyle, display: 'flex', alignItems: 'center', gap: 6 }}>
              {saved ? <><Check size={14} /> {t('settings.saved')}</> : saving ? t('settings.saving') : 'Save agent settings'}
            </button>
          </>
        )}

        {/* Integrations Tab */}
        {tab === 'integrations' && (
          <>
            <h3 style={sectionTitle}>Integrations</h3>
            <p style={hintStyle}>
              Manage existing desktop features from one place. These shortcuts open the current dedicated screens without changing any backend configuration semantics.
            </p>
            <FeatureGrid>
              <FeatureCard
                icon={<Server size={18} />}
                title="MCP Servers"
                description="Configure Model Context Protocol servers and tools."
                action="Manage MCP"
                onClick={() => openView('mcp')}
              />
              <FeatureCard
                icon={<Radio size={18} />}
                title="IM Adapters"
                description="Connect Telegram, Slack, Feishu, DingTalk and other chat adapters."
                action="Manage IM"
                onClick={() => openView('im')}
              />
              <FeatureCard
                icon={<Share2 size={18} />}
                title="Mobile Share"
                description="Share the active desktop session with mobile clients through the relay."
                action="Open Share"
                onClick={onOpenShare}
              />
              <FeatureCard
                icon={<PanelRight size={18} />}
                title="Context Panel"
                description="Inspect context usage, token totals, and cache statistics for the active session."
                action="Open Context"
                onClick={onOpenContext}
              />
              <FeatureCard
                icon={<Code2 size={18} />}
                title="Language Servers"
                description="View detected LSP servers for Go, Rust, TypeScript, Python and more."
                action="View Status"
                onClick={() => {
                  App.GetLSPStatus().then((res: any) => {
                    setLspStatus(res.languages || [])
                    setTab('lsp')
                  })
                }}
              />
            </FeatureGrid>
          </>
        )}

        {/* LSP Tab */}
        {tab === 'lsp' && (
          <>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <button onClick={() => setTab('integrations')} style={backBtnStyle}>
                <ArrowLeft size={14} /> Integrations
              </button>
            </div>
            <h3 style={sectionTitle}>Language Servers</h3>
            <p style={hintStyle}>
              Language Server Protocol (LSP) servers provide the agent with code intelligence:
              go-to-definition, find references, hover info, diagnostics, and more. Servers are
              auto-detected from your PATH and workspace files. You can override binary paths in
              the config file under <code style={{ fontSize: 11, fontFamily: 'var(--font-mono)' }}>lsp_servers</code>.
            </p>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
              {lspStatus.length === 0 && (
                <div style={{ ...cardStyleObj, padding: 24, textAlign: 'center', color: 'var(--text-tertiary)' }}>
                  No language servers detected in this workspace.
                </div>
              )}
              {lspStatus.map((lang) => (
                <div key={lang.id} style={{
                  ...cardStyleObj,
                  padding: '12px 16px',
                }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <div style={{
                        width: 8, height: 8, borderRadius: '50%',
                        background: lang.available ? 'var(--color-success)' : 'var(--color-error)',
                        flexShrink: 0,
                      }} />
                      <div>
                        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--text-primary)' }}>
                          {lang.display_name}
                          {lang.override && (
                            <span style={{ marginLeft: 6, fontSize: 10, color: 'var(--color-primary)', fontWeight: 400 }}>
                              (configured)
                            </span>
                          )}
                        </div>
                        <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--text-tertiary)' }}>
                          {lang.available ? lang.binary : 'Not found'}
                        </div>
                      </div>
                    </div>
                    {lang.available && (
                      <Check size={16} style={{ color: 'var(--color-success)' }} />
                    )}
                  </div>
                  {/* Install buttons for unavailable servers */}
                  {!lang.available && lang.can_install && lang.install_options && lang.install_options.length > 0 && (
                    <div style={{ display: 'flex', gap: 6, marginTop: 10, flexWrap: 'wrap', alignItems: 'center' }}>
                      <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>Install:</span>
                      {lang.install_options.map((opt) => {
                        const scopeColors: Record<string, string> = {
                          user: 'var(--color-success)',
                          global: 'var(--color-primary)',
                          project: 'var(--text-tertiary)',
                        }
                        const scopeColor = scopeColors[opt.scope] || 'var(--text-tertiary)'
                        return (
                          <button
                            key={opt.id}
                            disabled={installing === `${lang.id}:${opt.id}`}
                            onClick={() => {
                              setInstalling(`${lang.id}:${opt.id}`)
                              setInstallResult(null)
                              App.InstallLSPServer(lang.id, opt.id).then((res: any) => {
                                setInstalling(null)
                                setInstallResult({ lang: lang.id, success: res.success, output: res.output })
                                if (res.success) {
                                  // Refresh status
                                  App.GetLSPStatus().then((r: any) => setLspStatus(r.languages || []))
                                }
                              })
                            }}
                            style={{
                              padding: '4px 10px', borderRadius: 'var(--radius-sm)',
                              background: opt.recommended ? 'var(--color-primary)' : 'var(--color-surface)',
                              color: opt.recommended ? '#fff' : 'var(--text-secondary)',
                              border: `1px solid ${opt.recommended ? 'var(--color-primary)' : 'var(--color-border)'}`,
                              cursor: installing ? 'wait' : 'pointer', fontSize: 11,
                              display: 'inline-flex', alignItems: 'center', gap: 4,
                              opacity: installing === `${lang.id}:${opt.id}` ? 0.6 : 1,
                            }}
                          >
                            {installing === `${lang.id}:${opt.id}` && <RefreshCw size={10} style={{ animation: 'spin 1s linear infinite' }} />}
                            {opt.label}
                            <span style={{
                              fontSize: 8, padding: '1px 4px', borderRadius: 3,
                              background: opt.recommended ? 'rgba(255,255,255,0.2)' : `color-mix(in srgb, ${scopeColor} 15%, transparent)`,
                              color: opt.recommended ? 'rgba(255,255,255,0.8)' : scopeColor,
                              textTransform: 'uppercase', fontWeight: 600,
                            }}>
                              {opt.scope}
                            </span>
                          </button>
                        )
                      })}
                    </div>
                  )}
                  {!lang.available && !lang.can_install && lang.install_hint && (
                    <div style={{ fontSize: 10, fontFamily: 'var(--font-mono)', color: 'var(--text-tertiary)',
                      marginTop: 8, padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                      background: 'var(--color-surface)', border: '1px solid var(--color-border)',
                    }}>
                      {lang.install_hint}
                    </div>
                  )}
                  {/* Install result */}
                  {installResult && installResult.lang === lang.id && (
                    <div style={{
                      marginTop: 8, padding: '8px 10px', borderRadius: 'var(--radius-sm)',
                      background: installResult.success ? 'color-mix(in srgb, var(--color-success) 10%, transparent)' : 'color-mix(in srgb, var(--color-error) 10%, transparent)',
                      border: `1px solid ${installResult.success ? 'var(--color-success)' : 'var(--color-error)'}`,
                      fontSize: 10, fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)',
                      maxHeight: 120, overflow: 'auto', whiteSpace: 'pre-wrap',
                    }}>
                      {installResult.output}
                    </div>
                  )}
                </div>
              ))}
            </div>
            <button
              onClick={() => {
                App.GetLSPStatus().then((res: any) => setLspStatus(res.languages || []))
              }}
              style={{ ...secondaryBtnStyle, marginTop: 16 }}
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </>
        )}

        {/* Diagnostics Tab */}
        {tab === 'diagnostics' && (
          <>
            <h3 style={sectionTitle}>Diagnostics</h3>
            <p style={hintStyle}>
              Tools for troubleshooting and support. These open existing desktop panels and dialogs.
            </p>
            <FeatureGrid>
              <FeatureCard
                icon={<Terminal size={18} />}
                title="Debug Console"
                description="View runtime logs and recent desktop/backend diagnostic events."
                action="Open Console"
                onClick={() => openView('debug')}
              />
              <FeatureCard
                icon={<Info size={18} />}
                title="About & Updates"
                description="Check the app version, update status, release notes, and support links."
                action="Open About"
                onClick={onOpenAbout}
              />
            </FeatureGrid>
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
          <AddEndpointForm vendors={vendors} currentVendor={currentVendor} showToast={showToast} onDone={() => {
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
function AddEndpointForm({ vendors, currentVendor, onDone, showToast }: {
  vendors: string[], currentVendor: string, onDone: () => void, showToast?: (type: 'success' | 'error' | 'info', message: string) => void
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
      const message = e.message || e
      setStatus('Failed: ' + message)
      showToast?.('error', `Endpoint test failed: ${message}`)
    }
  }, [protocol, baseURL, epApiKey, showToast])

  const save = useCallback(async () => {
    if (!name || !baseURL || !vendor) return
    setSaving(true)
    try {
      await App.AddCustomEndpoint(vendor, name, protocol, baseURL, epApiKey)
      showToast?.('success', 'Endpoint added')
      onDone()
    } catch (e: any) {
      const message = e.message || e
      setStatus('Error: ' + message)
      showToast?.('error', `Failed to add endpoint: ${message}`)
    } finally {
      setSaving(false)
    }
  }, [vendor, name, protocol, baseURL, epApiKey, onDone, showToast])

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
          <option value="gemini">Google Gemini</option>
          <option value="copilot">GitHub Copilot</option>
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

function FeatureGrid({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 }}>
      {children}
    </div>
  )
}

function FeatureCard({ icon, title, description, action, onClick }: {
  icon: React.ReactNode
  title: string
  description: string
  action: string
  onClick?: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        textAlign: 'left',
        padding: 14,
        borderRadius: 'var(--radius-lg)',
        border: '1px solid var(--color-border)',
        background: 'var(--color-card)',
        color: 'var(--text-primary)',
        cursor: onClick ? 'pointer' : 'default',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        minHeight: 148,
      }}
    >
      <span style={{ width: 32, height: 32, borderRadius: 'var(--radius-md)', background: 'rgba(59,130,246,0.14)', color: 'var(--color-primary)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        {icon}
      </span>
      <span style={{ fontSize: 14, fontWeight: 600 }}>{title}</span>
      <span style={{ fontSize: 12, lineHeight: 1.5, color: 'var(--text-tertiary)', flex: 1 }}>{description}</span>
      <span style={{ fontSize: 12, color: 'var(--color-primary)', fontWeight: 600 }}>{action}</span>
    </button>
  )
}

const sectionTitle: React.CSSProperties = {
  fontSize: 16, fontWeight: 600, color: 'var(--text-primary)',
  margin: '0 0 16px', paddingBottom: 8,
  borderBottom: '1px solid var(--color-border)',
}

const hintStyle: React.CSSProperties = {
  fontSize: 12,
  color: 'var(--text-tertiary)',
  lineHeight: 1.6,
  margin: '0 0 16px',
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

function getModeInfo(mode: string) {
  switch (mode) {
    case 'plan':
      return {
        title: 'Read-only planning mode',
        description: 'The agent can inspect files and search the workspace, but write actions and commands are blocked. This is the safest mode for exploration.',
        color: 'var(--color-primary)',
        border: 'rgba(59, 130, 246, 0.35)',
        background: 'rgba(59, 130, 246, 0.08)',
        iconBackground: 'rgba(59, 130, 246, 0.16)',
      }
    case 'auto':
      return {
        title: 'Auto mode',
        description: 'Safe operations can proceed automatically while dangerous actions are denied or require escalation according to policy.',
        color: 'var(--color-success)',
        border: 'rgba(34, 197, 94, 0.35)',
        background: 'rgba(34, 197, 94, 0.08)',
        iconBackground: 'rgba(34, 197, 94, 0.16)',
      }
    case 'bypass':
      return {
        title: 'Bypass mode',
        description: 'Most operations are allowed with fewer prompts. Use this only in trusted workspaces where you are comfortable with faster execution.',
        color: 'var(--color-warning)',
        border: 'rgba(245, 158, 11, 0.38)',
        background: 'rgba(245, 158, 11, 0.09)',
        iconBackground: 'rgba(245, 158, 11, 0.16)',
      }
    case 'autopilot':
      return {
        title: 'Autopilot mode',
        description: 'The agent has high autonomy and can continue through many steps automatically. Review workspace state before using it on important code.',
        color: 'var(--color-error)',
        border: 'rgba(239, 68, 68, 0.38)',
        background: 'rgba(239, 68, 68, 0.1)',
        iconBackground: 'rgba(239, 68, 68, 0.18)',
      }
    default:
      return {
        title: 'Supervised mode',
        description: 'The agent asks before unspecified tool actions. This is a balanced default for normal desktop use.',
        color: 'var(--text-secondary)',
        border: 'var(--color-border)',
        background: 'var(--color-card)',
        iconBackground: 'rgba(148, 163, 184, 0.14)',
      }
  }
}

const summaryCardStyle: React.CSSProperties = {
  border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-lg)',
  padding: 14,
  marginBottom: 18,
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

const cardStyleObj: React.CSSProperties = {
  background: 'var(--color-surface)',
  border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-md)',
}
