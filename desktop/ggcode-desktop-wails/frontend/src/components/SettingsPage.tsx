import React, { useState, useEffect, useCallback } from 'react'
import { ArrowLeft, Eye, EyeOff } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface Props {
  onBack: () => void
}

type SettingsSection = 'provider' | 'behavior' | 'impersonation' | 'subagents' | 'swarm' | 'a2a' | 'harness' | 'stream'

interface FullConfig {
  vendor: string
  endpoint: string
  model: string
  apiKeySet: boolean
  language: string
  extraPrompt: string
  defaultMode: string
  maxIterations: number
  probeContext: boolean
  impersonatePreset: string
  impersonateCustomVersion: string
  impersonateCustomHeaders: Record<string, string>
  subAgentMaxConcurrent: number
  subAgentTimeout: string
  subAgentShowOutput: boolean
  swarmMaxTeammates: number
  swarmTimeout: string
  swarmInboxSize: number
  a2aDisabled: boolean
  a2aPort: number
  a2aHost: string
  a2aApiKey: string
  a2aLanDiscovery: boolean
  harnessAutoRun: string
  harnessAutoInit: boolean
  streamEncoder: string
  streamFPS: number
  knightEnabled: boolean
  knightTrustLevel: string
  workDir: string
  needsSetup: boolean
}

const sections: { id: SettingsSection; label: string }[] = [
  { id: 'provider', label: 'LLM Provider' },
  { id: 'behavior', label: 'Behavior' },
  { id: 'impersonation', label: 'Impersonation' },
  { id: 'subagents', label: 'Sub-Agents' },
  { id: 'swarm', label: 'Swarm' },
  { id: 'a2a', label: 'A2A Server' },
  { id: 'harness', label: 'Harness' },
  { id: 'stream', label: 'Video Stream' },
]

export function SettingsPage({ onBack }: Props) {
  const [section, setSection] = useState<SettingsSection>('provider')
  const [cfg, setCfg] = useState<FullConfig | null>(null)
  const [vendors, setVendors] = useState<string[]>([])
  const [endpoints, setEndpoints] = useState<{ key: string; displayName: string }[]>([])
  const [models, setModels] = useState<string[]>([])
  const [apiKey, setApiKey] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [saving, setSaving] = useState(false)

  // Local edits (buffered, applied on save)
  const [edits, setEdits] = useState<Record<string, any>>({})

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const c = await App.GetConfig() as any as FullConfig
        if (cancelled) return
        setCfg(c)
        const v = await App.GetVendors()
        if (cancelled) return
        setVendors(v as string[])
        // Load endpoints for current vendor
        if (c.vendor) {
          const eps = await App.GetEndpoints(c.vendor)
          if (cancelled) return
          setEndpoints((eps as any[]) || [])
          if (c.endpoint) {
            const ms = await App.GetModels(c.vendor, c.endpoint)
            if (cancelled) return
            setModels((ms as string[]) || [])
          }
        }
      } catch {}
    }
    load()
    return () => { cancelled = true }
  }, [])

  const update = useCallback((key: string, value: any) => {
    setEdits(prev => ({ ...prev, [key]: value }))
  }, [])

  const handleVendorChange = useCallback(async (vendor: string) => {
    update('vendor', vendor)
    const eps = await App.GetEndpoints(vendor) as any[]
    setEndpoints(eps || [])
    setModels([])
    update('endpoint', '')
    update('model', '')
  }, [update])

  const handleEndpointChange = useCallback(async (endpoint: string) => {
    const vendor = edits.vendor ?? cfg?.vendor ?? ''
    update('endpoint', endpoint)
    const ms = await App.GetModels(vendor, endpoint) as string[]
    setModels(ms || [])
    update('model', '')
  }, [cfg, edits, update])

  const save = useCallback(async () => {
    setSaving(true)
    try {
      const values: Record<string, any> = { ...edits }
      // Convert numbers
      for (const k of ['maxIterations', 'subAgentMaxConcurrent', 'swarmMaxTeammates', 'swarmInboxSize', 'a2aPort', 'streamFPS']) {
        if (values[k] !== undefined) values[k] = Number(values[k])
      }
      await App.UpdateConfig(values)
      if (apiKey) {
        const vendor = values.vendor ?? cfg?.vendor ?? ''
        const endpoint = values.endpoint ?? cfg?.endpoint ?? ''
        await App.SaveAPIKey(vendor, endpoint, apiKey)
        setApiKey('')
      }
      setEdits({})
      // Reload
      const c = await App.GetConfig() as any as FullConfig
      setCfg(c)
    } catch (e) {
      console.error('Save failed:', e)
    } finally {
      setSaving(false)
    }
  }, [edits, apiKey, cfg])

  const v = (key: string, fallback: any = '') => edits[key] ?? (cfg as any)?.[key] ?? fallback

  return (
    <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        {/* Settings nav sidebar */}
        <div style={{
          width: 200, background: 'var(--color-nav)',
          borderRight: '1px solid var(--color-border)',
          padding: '12px 0',
          flexShrink: 0,
        }}>
          <button onClick={onBack} style={backBtnStyle}>
            <ArrowLeft size={14} /> <span style={{ marginLeft: 4 }}>Back</span>
          </button>
          <div style={{ marginTop: 8 }}>
            {sections.map(s => (
              <button key={s.id} onClick={() => setSection(s.id)} style={{
                display: 'block', width: '100%', textAlign: 'left',
                padding: '6px 16px', border: 'none', cursor: 'pointer',
                background: section === s.id ? 'var(--color-primary)' : 'transparent',
                color: section === s.id ? '#fff' : 'var(--text-secondary)',
                fontSize: 13,
              }}>
                {s.label}
              </button>
            ))}
          </div>
        </div>

        {/* Settings content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px', maxWidth: 600 }}>
          {!cfg ? <div style={{ color: 'var(--text-tertiary)' }}>Loading...</div> : (
            <>
              {/* Provider */}
              {section === 'provider' && (
                <>
                  <h3 style={sectionTitle}>LLM Provider</h3>
                  <FieldRow label="Vendor">
                    <select value={v('vendor')} onChange={e => handleVendorChange(e.target.value)} style={selectStyle}>
                      <option value="">Choose vendor...</option>
                      {vendors.map(vn => <option key={vn} value={vn}>{vn}</option>)}
                    </select>
                  </FieldRow>
                  <FieldRow label="Endpoint">
                    <select value={v('endpoint')} onChange={e => handleEndpointChange(e.target.value)} style={selectStyle}>
                      <option value="">Choose endpoint...</option>
                      {endpoints.map(ep => <option key={ep.key} value={ep.key}>{ep.displayName || ep.key}</option>)}
                    </select>
                  </FieldRow>
                  <FieldRow label="Model">
                    <select value={v('model')} onChange={e => update('model', e.target.value)} style={selectStyle}>
                      <option value="">Choose model...</option>
                      {models.map(m => <option key={m} value={m}>{m}</option>)}
                    </select>
                  </FieldRow>
                  <FieldRow label="API Key">
                    <div style={{ display: 'flex', gap: 4 }}>
                      <input
                        type={showKey ? 'text' : 'password'}
                        value={apiKey}
                        onChange={e => setApiKey(e.target.value)}
                        placeholder={cfg?.apiKeySet ? '•••••••• (saved)' : 'Enter API key...'}
                        style={{ ...inputStyle, flex: 1 }}
                      />
                      <button onClick={() => setShowKey(p => !p)} style={eyeBtnStyle}>
                        {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
                      </button>
                    </div>
                  </FieldRow>
                  <FieldRow label="Language">
                    <select value={v('language', 'en')} onChange={e => update('language', e.target.value)} style={selectStyle}>
                      <option value="en">English</option>
                      <option value="zh">中文</option>
                      <option value="ja">日本語</option>
                    </select>
                  </FieldRow>
                </>
              )}

              {/* Behavior */}
              {section === 'behavior' && (
                <>
                  <h3 style={sectionTitle}>Behavior</h3>
                  <FieldRow label="Default Mode">
                    <select value={v('defaultMode', 'auto')} onChange={e => update('defaultMode', e.target.value)} style={selectStyle}>
                      <option value="auto">Auto (auto-approve safe tools)</option>
                      <option value="confirm">Confirm (ask every time)</option>
                      <option value="allow">Allow (approve everything)</option>
                    </select>
                  </FieldRow>
                  <FieldRow label="Max Iterations">
                    <input type="number" value={v('maxIterations', 30)} onChange={e => update('maxIterations', e.target.value)} style={inputStyle} />
                  </FieldRow>
                  <FieldRow label="Extra Prompt">
                    <textarea value={v('extraPrompt', '')} onChange={e => update('extraPrompt', e.target.value)}
                      placeholder="Additional system prompt instructions..."
                      style={{ ...inputStyle, minHeight: 80, resize: 'vertical' }} />
                  </FieldRow>
                  <FieldRow label="Probe Context">
                    <Toggle checked={v('probeContext', false)} onChange={c => update('probeContext', c)} />
                  </FieldRow>
                </>
              )}

              {/* Impersonation */}
              {section === 'impersonation' && (
                <>
                  <h3 style={sectionTitle}>Impersonation</h3>
                  <p style={{ fontSize: 12, color: 'var(--text-tertiary)', margin: '0 0 16px' }}>
                    Some providers require specific headers or user-agent to work.
                  </p>
                  <FieldRow label="Preset">
                    <select value={v('impersonatePreset', '')} onChange={e => update('impersonatePreset', e.target.value)} style={selectStyle}>
                      <option value="">None</option>
                      <option value="chrome-110">Chrome 110</option>
                      <option value="chrome-120">Chrome 120</option>
                      <option value="safari">Safari</option>
                      <option value="claude-web">Claude Web</option>
                      <option value="custom">Custom</option>
                    </select>
                  </FieldRow>
                  {v('impersonatePreset') === 'custom' && (
                    <>
                      <FieldRow label="Custom Version">
                        <input value={v('impersonateCustomVersion', '')} onChange={e => update('impersonateCustomVersion', e.target.value)}
                          placeholder="e.g. 1.0.0" style={inputStyle} />
                      </FieldRow>
                    </>
                  )}
                </>
              )}

              {/* Sub-Agents */}
              {section === 'subagents' && (
                <>
                  <h3 style={sectionTitle}>Sub-Agents</h3>
                  <FieldRow label="Max Concurrent">
                    <input type="number" value={v('subAgentMaxConcurrent', 3)} onChange={e => update('subAgentMaxConcurrent', e.target.value)} style={inputStyle} min={1} max={20} />
                  </FieldRow>
                  <FieldRow label="Show Output">
                    <Toggle checked={v('subAgentShowOutput', true)} onChange={c => update('subAgentShowOutput', c)} />
                  </FieldRow>
                </>
              )}

              {/* Swarm */}
              {section === 'swarm' && (
                <>
                  <h3 style={sectionTitle}>Swarm / Teams</h3>
                  <FieldRow label="Max Teammates">
                    <input type="number" value={v('swarmMaxTeammates', 5)} onChange={e => update('swarmMaxTeammates', e.target.value)} style={inputStyle} min={1} max={20} />
                  </FieldRow>
                  <FieldRow label="Inbox Size">
                    <input type="number" value={v('swarmInboxSize', 32)} onChange={e => update('swarmInboxSize', e.target.value)} style={inputStyle} min={8} max={256} />
                  </FieldRow>
                </>
              )}

              {/* A2A */}
              {section === 'a2a' && (
                <>
                  <h3 style={sectionTitle}>A2A Server</h3>
                  <FieldRow label="Enabled">
                    <Toggle checked={!v('a2aDisabled', false)} onChange={c => update('a2aDisabled', !c)} />
                  </FieldRow>
                  {!v('a2aDisabled', false) && (
                    <>
                      <FieldRow label="Port">
                        <input type="number" value={v('a2aPort', 0)} onChange={e => update('a2aPort', e.target.value)} style={inputStyle} />
                        <span style={{ fontSize: 11, color: 'var(--text-tertiary)', marginLeft: 8 }}>0 = auto</span>
                      </FieldRow>
                      <FieldRow label="Host">
                        <input value={v('a2aHost', '127.0.0.1')} onChange={e => update('a2aHost', e.target.value)} style={inputStyle} />
                      </FieldRow>
                      <FieldRow label="API Key">
                        <input type="password" value={v('a2aApiKey', '')} onChange={e => update('a2aApiKey', e.target.value)}
                          placeholder="Leave empty for no auth" style={inputStyle} />
                      </FieldRow>
                      <FieldRow label="LAN Discovery">
                        <Toggle checked={v('a2aLanDiscovery', false)} onChange={c => update('a2aLanDiscovery', c)} />
                      </FieldRow>
                    </>
                  )}
                </>
              )}

              {/* Harness */}
              {section === 'harness' && (
                <>
                  <h3 style={sectionTitle}>Harness</h3>
                  <FieldRow label="Auto Run">
                    <select value={v('harnessAutoRun', 'off')} onChange={e => update('harnessAutoRun', e.target.value)} style={selectStyle}>
                      <option value="off">Off (manual)</option>
                      <option value="suggest">Suggest (prompt user)</option>
                      <option value="on">On (auto route)</option>
                      <option value="strict">Strict (worktree isolation)</option>
                    </select>
                  </FieldRow>
                  <FieldRow label="Auto Init">
                    <Toggle checked={v('harnessAutoInit', false)} onChange={c => update('harnessAutoInit', c)} />
                    <span style={{ fontSize: 11, color: 'var(--text-tertiary)', marginLeft: 8 }}>Auto-create harness.yaml</span>
                  </FieldRow>
                </>
              )}

              {/* Stream */}
              {section === 'stream' && (
                <>
                  <h3 style={sectionTitle}>Video Stream</h3>
                  <FieldRow label="Hardware Encoder">
                    <select value={v('streamEncoder', 'auto')} onChange={e => update('streamEncoder', e.target.value)} style={selectStyle}>
                      <option value="auto">Auto</option>
                      <option value="software">Software (libx264)</option>
                      <option value="h264_videotoolbox">VideoToolbox (macOS)</option>
                      <option value="h264_nvenc">NVENC (NVIDIA)</option>
                    </select>
                  </FieldRow>
                  <FieldRow label="FPS">
                    <input type="number" value={v('streamFPS', 30)} onChange={e => update('streamFPS', e.target.value)} style={inputStyle} min={1} max={60} />
                  </FieldRow>
                </>
              )}

              {/* Save button */}
              {Object.keys(edits).length > 0 && (
                <div style={{ marginTop: 24, display: 'flex', gap: 8 }}>
                  <button onClick={save} disabled={saving} style={{
                    padding: '8px 20px', borderRadius: 'var(--radius-md)',
                    background: 'var(--color-primary)', color: '#fff',
                    border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500,
                  }}>
                    {saving ? 'Saving...' : 'Save Changes'}
                  </button>
                  <button onClick={() => setEdits({})} style={{
                    padding: '8px 20px', borderRadius: 'var(--radius-md)',
                    background: 'var(--color-surface)', color: 'var(--text-secondary)',
                    border: 'none', cursor: 'pointer', fontSize: 13,
                  }}>
                    Cancel
                  </button>
                </div>
              )}
            </>
          )}
        </div>
      </div>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', marginBottom: 16 }}>
      <span style={{ width: 140, color: 'var(--text-secondary)', fontSize: 13, flexShrink: 0, paddingTop: 6 }}>{label}</span>
      <div style={{ flex: 1 }}>{children}</div>
    </div>
  )
}

function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button onClick={() => onChange(!checked)} style={{
      width: 36, height: 20, borderRadius: 10, border: 'none', cursor: 'pointer',
      background: checked ? 'var(--color-primary)' : 'var(--color-surface)',
      position: 'relative', transition: 'background 0.15s',
    }}>
      <div style={{
        width: 16, height: 16, borderRadius: 8, background: '#fff',
        position: 'absolute', top: 2, left: checked ? 18 : 2,
        transition: 'left 0.15s',
      }} />
    </button>
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
  color: 'var(--text-primary)', fontSize: 13, outline: 'none',
  fontFamily: 'inherit',
}

const backBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--text-secondary)',
  cursor: 'pointer', display: 'flex', alignItems: 'center',
  padding: '6px 16px', fontSize: 13,
}

const eyeBtnStyle: React.CSSProperties = {
  background: 'var(--color-surface)', border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-md)', cursor: 'pointer',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  width: 36, height: 36, color: 'var(--text-tertiary)', flexShrink: 0,
}
