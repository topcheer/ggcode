import React, { useState, useEffect } from 'react'
import { ArrowLeft, Eye, EyeOff } from 'lucide-react'
import { ConfigData, EndpointInfo } from '../types'

interface Props {
  onBack: () => void
}

export function SettingsPage({ onBack }: Props) {
  const [cfg, setCfg] = useState<ConfigData>({
    vendor: '', endpoint: '', model: '', mode: 'code', language: 'en', theme: 'dark',
  })
  const [vendors, setVendors] = useState<string[]>([])
  const [endpoints, setEndpoints] = useState<EndpointInfo[]>([])
  const [models, setModels] = useState<string[]>([])
  const [showKey, setShowKey] = useState(false)
  const [activeNav, setActiveNav] = useState('Provider')

  useEffect(() => {
    // Load config and vendors from Go backend
    // @ts-ignore
    window.go.main.App.GetConfig().then((c: ConfigData) => setCfg(c))
    // @ts-ignore
    window.go.main.App.GetVendors().then((v: string[]) => setVendors(v || []))
  }, [])

  useEffect(() => {
    if (cfg.vendor) {
      // @ts-ignore
      window.go.main.App.GetEndpoints(cfg.vendor).then((eps: EndpointInfo[]) => setEndpoints(eps || []))
    }
  }, [cfg.vendor])

  useEffect(() => {
    if (cfg.vendor && cfg.endpoint) {
      // @ts-ignore
      window.go.main.App.GetModels(cfg.vendor, cfg.endpoint).then((m: string[]) => setModels(m || []))
    }
  }, [cfg.vendor, cfg.endpoint])

  const navItems = ['Provider', 'Permissions', 'Appearance', 'Language', 'MCP Servers', 'Advanced']

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Settings nav */}
      <div style={{
        width: 200, background: 'var(--color-nav)',
        padding: 'var(--spacing-lg) 0',
        display: 'flex', flexDirection: 'column', gap: 2,
      }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '0 var(--spacing-lg) var(--spacing-md)',
        }}>
          <button onClick={onBack} style={{
            background: 'none', border: 'none',
            color: 'var(--text-secondary)', cursor: 'pointer',
            display: 'flex', alignItems: 'center',
          }}>
            <ArrowLeft size={16} />
          </button>
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

        {/* Vendor */}
        <FieldRow label="Vendor">
          <select
            value={cfg.vendor}
            onChange={e => setCfg(c => ({ ...c, vendor: e.target.value }))}
            style={selectStyle}
          >
            {vendors.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        </FieldRow>

        {/* Endpoint */}
        <FieldRow label="Endpoint">
          <select
            value={cfg.endpoint}
            onChange={e => setCfg(c => ({ ...c, endpoint: e.target.value }))}
            style={selectStyle}
          >
            {endpoints.map(ep => <option key={ep.id} value={ep.id}>{ep.name}</option>)}
          </select>
        </FieldRow>

        {/* API Key */}
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

        {/* Model */}
        <FieldRow label="Model">
          <select
            value={cfg.model}
            onChange={e => setCfg(c => ({ ...c, model: e.target.value }))}
            style={selectStyle}
          >
            {models.map(m => <option key={m} value={m}>{m}</option>)}
          </select>
        </FieldRow>

        {/* Extra Prompt */}
        <FieldRow label="Extra Prompt">
          <textarea
            style={{ ...inputStyle, flex: 1, minHeight: 60, resize: 'vertical' }}
            placeholder="Always respond concisely..."
          />
        </FieldRow>

        {/* Default Mode */}
        <FieldRow label="Default Mode">
          <div style={{ display: 'flex', gap: 6 }}>
            {['Code', 'Agent'].map(m => (
              <button key={m} style={{
                padding: '4px 12px', borderRadius: 'var(--radius-sm)',
                background: cfg.mode === m.toLowerCase() ? 'var(--color-primary)' : 'var(--color-surface)',
                color: cfg.mode === m.toLowerCase() ? '#fff' : 'var(--text-secondary)',
                border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 500,
              }}>
                {m}
              </button>
            ))}
          </div>
        </FieldRow>

        <div style={{ flex: 1 }} />

        {/* Buttons */}
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button onClick={onBack} style={{
            padding: '8px 20px', borderRadius: 'var(--radius-md)',
            border: '1px solid var(--color-border)',
            background: 'transparent', color: 'var(--text-secondary)',
            cursor: 'pointer', fontSize: 13,
          }}>
            Cancel
          </button>
          <button style={{
            padding: '8px 20px', borderRadius: 'var(--radius-md)',
            background: 'var(--color-primary)',
            color: '#fff', border: 'none', cursor: 'pointer',
            fontSize: 13, fontWeight: 500,
          }}>
            Save Changes
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
  ...inputStyle,
  width: '100%',
  appearance: 'none',
  cursor: 'pointer',
}

const iconBtnStyle: React.CSSProperties = {
  width: 36, height: 36, borderRadius: 'var(--radius-md)',
  background: 'var(--color-surface)', border: 'none',
  color: 'var(--text-secondary)', cursor: 'pointer',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}
