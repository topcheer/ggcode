import React, { useState, useEffect, useCallback } from 'react'
import { ArrowLeft, Eye, EyeOff } from 'lucide-react'
import { EndpointInfo } from '../types'

interface ConfigSnapshot {
  vendor: string
  endpoint: string
  model: string
  defaultMode: string
  language: string
  extraPrompt: string
}

interface Props {
  onBack: () => void
}

// Wails Go bindings
declare global {
  interface Window {
    go: {
      main: {
        App: {
          GetConfig: () => Promise<ConfigSnapshot>
          GetVendors: () => Promise<string[]>
          GetEndpoints: (vendor: string) => Promise<EndpointInfo[]>
          GetModels: (vendor: string, endpoint: string) => Promise<string[]>
          SaveConfig: (values: Record<string, string>) => Promise<void>
        }
      }
    }
  }
}

export function SettingsPage({ onBack }: Props) {
  const [cfg, setCfg] = useState<ConfigSnapshot>({
    vendor: '', endpoint: '', model: '', defaultMode: 'code', language: 'en', extraPrompt: '',
  })
  const [vendors, setVendors] = useState<string[]>([])
  const [endpoints, setEndpoints] = useState<EndpointInfo[]>([])
  const [models, setModels] = useState<string[]>([])
  const [showKey, setShowKey] = useState(false)
  const [activeNav, setActiveNav] = useState('Provider')
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  // Load config
  useEffect(() => {
    window.go.main.App.GetConfig()
      .then(c => setCfg(c || { vendor: '', endpoint: '', model: '', defaultMode: 'code', language: 'en', extraPrompt: '' }))
      .catch(() => {})
    window.go.main.App.GetVendors()
      .then(v => setVendors(v || []))
      .catch(() => {})
  }, [])

  // Load endpoints when vendor changes
  useEffect(() => {
    if (cfg.vendor) {
      window.go.main.App.GetEndpoints(cfg.vendor)
        .then(eps => setEndpoints(eps || []))
        .catch(() => setEndpoints([]))
    }
  }, [cfg.vendor])

  // Load models when endpoint changes
  useEffect(() => {
    if (cfg.vendor && cfg.endpoint) {
      window.go.main.App.GetModels(cfg.vendor, cfg.endpoint)
        .then(m => setModels(m || []))
        .catch(() => setModels([]))
    }
  }, [cfg.vendor, cfg.endpoint])

  const save = async () => {
    setSaving(true)
    try {
      await window.go.main.App.SaveConfig({
        vendor: cfg.vendor,
        endpoint: cfg.endpoint,
        model: cfg.model,
        defaultMode: cfg.defaultMode,
        language: cfg.language,
        extraPrompt: cfg.extraPrompt,
      })
      setDirty(false)
    } catch (e) {
      console.error('Save failed:', e)
    } finally {
      setSaving(false)
    }
  }

  const navItems = ['Provider', 'Permissions', 'Appearance', 'Language', 'MCP Servers', 'Advanced']

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Settings nav */}
      <div style={{
        width: 200, background: 'var(--color-nav)',
        padding: 'var(--spacing-lg) 0',
        display: 'flex', flexDirection: 'column', gap: 2,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 var(--spacing-lg) var(--spacing-md)' }}>
          <button onClick={onBack} style={backBtnStyle}><BackArrow /></button>
          <span style={{ fontWeight: 600, fontSize: 14 }}>Settings</span>
        </div>
        {navItems.map(item => (
          <button
            key={item}
            onClick={() => setActiveNav(item)}
            style={{
              padding: 'var(--spacing-sm) var(--spacing-lg)',
              background: activeNav === item ? 'var(--color-card)' : 'transparent',
              border: 'none', textAlign: 'left', cursor: 'pointer',
              color: activeNav === item ? 'var(--text-primary)' : 'var(--text-secondary)',
              fontWeight: activeNav === item ? 500 : 400,
              fontSize: 13,
            }}
          >
            {item}
          </button>
        ))}
      </div>

      {/* Settings content */}
      <div style={{
        flex: 1, padding: 'var(--spacing-xl) 32px',
        display: 'flex', flexDirection: 'column', gap: 20,
        overflowY: 'auto',
      }}>
        <h2 style={{ fontSize: 18, fontWeight: 600 }}>Provider & Model</h2>

        <FieldRow label="Vendor">
          <select
            value={cfg.vendor}
            onChange={e => { setCfg(c => ({ ...c, vendor: e.target.value })); setDirty(true) }}
            style={selectStyle}
          >
            <option value="">Select vendor...</option>
            {vendors.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        </FieldRow>

        <FieldRow label="Endpoint">
          <select
            value={cfg.endpoint}
            onChange={e => { setCfg(c => ({ ...c, endpoint: e.target.value })); setDirty(true) }}
            style={selectStyle}
          >
            <option value="">Select endpoint...</option>
            {endpoints.map(ep => <option key={ep.key} value={ep.key}>{ep.displayName || ep.key}</option>)}
          </select>
        </FieldRow>

        <FieldRow label="API Key">
          <div style={{ display: 'flex', gap: 8, flex: 1 }}>
            <input
              type={showKey ? 'text' : 'password'}
              style={{ ...inputStyle, flex: 1 }}
              placeholder="Enter API key..."
            />
            <button onClick={() => setShowKey(!showKey)} style={iconBtnStyle}>
              {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
        </FieldRow>

        <FieldRow label="Model">
          <select
            value={cfg.model}
            onChange={e => { setCfg(c => ({ ...c, model: e.target.value })); setDirty(true) }}
            style={selectStyle}
          >
            <option value="">Select model...</option>
            {models.map(m => <option key={m} value={m}>{m}</option>)}
          </select>
        </FieldRow>

        <FieldRow label="Extra Prompt">
          <textarea
            value={cfg.extraPrompt}
            onChange={e => { setCfg(c => ({ ...c, extraPrompt: e.target.value })); setDirty(true) }}
            style={{ ...inputStyle, flex: 1, minHeight: 60, resize: 'vertical' }}
            placeholder="Always respond concisely..."
          />
        </FieldRow>

        <FieldRow label="Default Mode">
          <div style={{ display: 'flex', gap: 6 }}>
            {['code', 'agent'].map(m => (
              <button key={m} onClick={() => { setCfg(c => ({ ...c, defaultMode: m })); setDirty(true) }} style={{
                padding: '4px 12px', borderRadius: 'var(--radius-sm)',
                background: cfg.defaultMode === m ? 'var(--color-primary)' : 'var(--color-surface)',
                color: cfg.defaultMode === m ? '#fff' : 'var(--text-secondary)',
                border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 500,
                textTransform: 'capitalize',
              }}>
                {m}
              </button>
            ))}
          </div>
        </FieldRow>

        <div style={{ flex: 1 }} />

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onBack} style={{
            padding: '8px 20px', borderRadius: 'var(--radius-md)',
            border: '1px solid var(--color-border)',
            background: 'transparent', color: 'var(--text-secondary)',
            cursor: 'pointer', fontSize: 13,
          }}>
            Cancel
          </button>
          <button
            onClick={save}
            disabled={!dirty || saving}
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: dirty ? 'var(--color-primary)' : 'var(--color-surface)',
              color: dirty ? '#fff' : 'var(--text-tertiary)',
              border: 'none', cursor: dirty ? 'pointer' : 'default',
              fontSize: 13, fontWeight: 500, opacity: saving ? 0.7 : 1,
            }}
          >
            {saving ? 'Saving...' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
      <span style={{ width: 100, color: 'var(--text-secondary)', fontSize: 13, flexShrink: 0 }}>{label}</span>
      <div style={{ flex: 1, display: 'flex' }}>{children}</div>
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-card)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', outline: 'none', fontSize: 13,
  fontFamily: 'var(--font-mono)',
}

const selectStyle: React.CSSProperties = {
  ...inputStyle, width: '100%', appearance: 'none', cursor: 'pointer',
}

const iconBtnStyle: React.CSSProperties = {
  width: 36, height: 36, borderRadius: 'var(--radius-md)',
  background: 'var(--color-surface)', border: 'none',
  color: 'var(--text-secondary)', cursor: 'pointer',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}

function BackArrow() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M10 3L5 8L10 13" />
    </svg>
  )
}

const backBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--text-secondary)',
  cursor: 'pointer', display: 'flex', alignItems: 'center',
}
