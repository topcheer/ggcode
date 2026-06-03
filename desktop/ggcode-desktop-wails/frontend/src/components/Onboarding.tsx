import React, { useState, useEffect } from 'react'
import { ChevronRight, FolderOpen } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface Props {
  onComplete: () => void
}

const MODES = [
  { id: 'supervised', label: 'Supervised', desc: 'Review and approve every tool call before execution', color: '#eab308' },
  { id: 'auto', label: 'Auto', desc: 'Auto-approve read-only tools, confirm destructive operations', color: '#22c55e' },
  { id: 'bypass', label: 'Bypass', desc: 'Auto-approve all tools, no confirmation needed', color: '#ef4444' },
  { id: 'autopilot', label: 'Autopilot', desc: 'Fully autonomous — agent runs without any confirmation', color: '#a855f7' },
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

export function Onboarding({ onComplete }: Props) {
  const [step, setStep] = useState<'workspace' | 'setup' | 'mode'>('workspace')
  const [workDir, setWorkDir] = useState('')

  // Onboard form state
  const [presets, setPresets] = useState<VendorPreset[]>([])
  const [selectedVendor, setSelectedVendor] = useState('')
  const [selectedEndpoint, setSelectedEndpoint] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [selectedMode, setSelectedMode] = useState('supervised')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const currentPreset = presets.find(p => p.id === selectedVendor)
  const currentEndpoint = currentPreset?.endpoints.find(e => e.id === selectedEndpoint)
  const models = currentEndpoint?.models || []
  const showModels = apiKey !== '' && models.length > 0

  useEffect(() => {
    App.GetVendorPresets().then(p => {
      setPresets((p as any[]) || [])
    }).catch(() => {})
  }, [])

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

  const handleSubmit = async () => {
    if (!selectedVendor || !selectedEndpoint || !apiKey || !selectedModel) {
      setError('Please fill in all fields')
      return
    }
    setSaving(true)
    setError('')
    try {
      await App.CompleteOnboard(selectedVendor, selectedEndpoint, selectedModel, apiKey)
      // Save permission mode
      try { await App.SaveDefaultMode(selectedMode) } catch {}
      onComplete()
    } catch (e: any) {
      setError(e?.message || 'Failed to save config')
    } finally {
      setSaving(false)
    }
  }

  // ─── Step 1: Workspace Selection ───
  if (step === 'workspace') {
    return (
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column',
        alignItems: 'center', justifyContent: 'center', gap: 24,
      }}>
        <h1 style={{ fontSize: 28, fontWeight: 700, color: 'var(--text-primary)', margin: 0 }}>
          Welcome to GGCode
        </h1>
        <p style={{ fontSize: 15, color: 'var(--text-secondary)', margin: 0 }}>
          Select a project directory to get started
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
          Choose Directory
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
          Choose Permission Mode
        </h2>
        <p style={{ color: 'var(--text-secondary)', fontSize: 13, margin: 0, maxWidth: 400, textAlign: 'center' }}>
          How much control do you want over tool execution?
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
                <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--text-primary)' }}>{m.label}</div>
                <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 2 }}>{m.desc}</div>
              </div>
            </button>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 12, width: 420 }}>
          <button onClick={() => setStep('setup')} style={{
            flex: 1, height: 40, borderRadius: 'var(--radius-md)',
            background: 'var(--color-surface)', border: '1px solid var(--color-border)',
            color: 'var(--text-secondary)', fontSize: 14, cursor: 'pointer',
          }}>Back</button>
          <button onClick={handleSubmit} disabled={saving} style={{
            flex: 2, height: 40, borderRadius: 'var(--radius-md)',
            background: saving ? 'var(--color-border)' : 'var(--color-primary)',
            color: '#fff', fontSize: 14, fontWeight: 600,
            border: 'none', cursor: saving ? 'not-allowed' : 'pointer',
          }}>{saving ? 'Saving...' : 'Start Using GGCode'}</button>
        </div>
      </div>
    )
  }

  // ─── Step 2: Setup (Vendor/Endpoint/API Key/Model) ───
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
          Configure LLM Provider
        </h2>
        <p style={{ fontSize: 13, color: 'var(--text-secondary)', margin: '0 0 24px' }}>
          Set up your AI provider to start chatting
        </p>

        {/* Vendor */}
        <label style={{ display: 'block', marginBottom: 16 }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>Vendor</span>
          <select value={selectedVendor} onChange={e => {
            setSelectedVendor(e.target.value)
            setSelectedEndpoint('')
            setSelectedModel('')
          }} style={selectStyle}>
            <option value="">Choose vendor...</option>
            {presets.map(p => <option key={p.id} value={p.id}>{p.displayName}</option>)}
          </select>
        </label>

        {/* Endpoint */}
        {currentPreset && (
          <label style={{ display: 'block', marginBottom: 16 }}>
            <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>Endpoint</span>
            <select value={selectedEndpoint} onChange={e => {
              setSelectedEndpoint(e.target.value)
              setSelectedModel('')
            }} style={selectStyle}>
              <option value="">Choose endpoint...</option>
              {currentPreset.endpoints.map(ep => (
                <option key={ep.id} value={ep.id}>{ep.displayName || ep.id}</option>
              ))}
            </select>
          </label>
        )}

        {/* API Key */}
        {selectedEndpoint && (
          <label style={{ display: 'block', marginBottom: 16 }}>
            <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>API Key</span>
            <input
              type="password"
              value={apiKey}
              onChange={e => setApiKey(e.target.value)}
              placeholder="Enter your API key..."
              style={inputStyle}
            />
          </label>
        )}

        {/* Model */}
        {showModels && (
          <label style={{ display: 'block', marginBottom: 16 }}>
            <span style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'block', marginBottom: 4 }}>Model</span>
            <select value={selectedModel} onChange={e => setSelectedModel(e.target.value)} style={selectStyle}>
              <option value="">Choose model...</option>
              {models.map(m => <option key={m} value={m}>{m}</option>)}
            </select>
          </label>
        )}

        {error && (
          <div style={{ color: 'var(--color-error)', fontSize: 12, marginBottom: 12 }}>{error}</div>
        )}

        {/* Submit → go to mode selection */}
        <button
          onClick={() => setStep('mode')}
          disabled={saving || !selectedVendor || !selectedEndpoint || !apiKey || !selectedModel}
          style={{
            width: '100%', padding: '10px 0', borderRadius: 'var(--radius-md)',
            background: (saving || !selectedVendor || !selectedEndpoint || !apiKey || !selectedModel)
              ? 'var(--color-surface)' : 'var(--color-primary)',
            color: '#fff', border: 'none', cursor: 'pointer',
            fontSize: 14, fontWeight: 500,
            display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
          }}
        >
          Next
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
