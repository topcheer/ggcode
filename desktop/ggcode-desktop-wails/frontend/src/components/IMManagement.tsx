import React, { useState } from 'react'
import { Search, Plus, X } from 'lucide-react'

interface IMAdapter {
  id: string
  name: string
  icon: string
  enabled: boolean
  connected: boolean
  fields: { label: string; value: string; secret?: boolean }[]
  targets: string[]
  sttEnabled: boolean
  streamEnabled: boolean
}

const mockAdapters: IMAdapter[] = [
  {
    id: 'wecom', name: 'WeChat Work', icon: '🏢', enabled: true, connected: true,
    fields: [
      { label: 'Corp ID', value: 'ww1234567890abcdef' },
      { label: 'Agent ID', value: '1000002' },
      { label: 'Secret', value: 'sk-xxxxxxxxxxxxxxxx', secret: true },
      { label: 'Token URL', value: 'qyapi.weixin.qq.com' },
    ],
    targets: ['Dev Team Group', 'Alerts Channel'],
    sttEnabled: false, streamEnabled: true,
  },
  {
    id: 'dingtalk', name: 'DingTalk', icon: '🔔', enabled: false, connected: false,
    fields: [
      { label: 'App Key', value: '' },
      { label: 'App Secret', value: '', secret: true },
    ],
    targets: [],
    sttEnabled: false, streamEnabled: false,
  },
  {
    id: 'feishu', name: 'Feishu', icon: '🐦', enabled: false, connected: false,
    fields: [{ label: 'App ID', value: '' }, { label: 'App Secret', value: '', secret: true }],
    targets: [], sttEnabled: false, streamEnabled: false,
  },
  {
    id: 'qq', name: 'QQ', icon: '🐧', enabled: true, connected: true,
    fields: [{ label: 'App ID', value: '102012345' }, { label: 'Token', value: 'xxxx', secret: true }],
    targets: ['Dev Group'],
    sttEnabled: false, streamEnabled: true,
  },
  {
    id: 'discord', name: 'Discord', icon: '🎮', enabled: false, connected: false,
    fields: [{ label: 'Bot Token', value: '', secret: true }],
    targets: [], sttEnabled: false, streamEnabled: false,
  },
  {
    id: 'telegram', name: 'Telegram', icon: '✈', enabled: false, connected: false,
    fields: [{ label: 'Bot Token', value: '', secret: true }],
    targets: [], sttEnabled: false, streamEnabled: false,
  },
  {
    id: 'whatsapp', name: 'WhatsApp', icon: '📱', enabled: false, connected: false,
    fields: [{ label: 'Phone', value: '' }, { label: 'API Key', value: '', secret: true }],
    targets: [], sttEnabled: false, streamEnabled: false,
  },
  {
    id: 'twitch', name: 'Twitch', icon: '📺', enabled: false, connected: false,
    fields: [{ label: 'Channel', value: '' }, { label: 'OAuth', value: '', secret: true }],
    targets: [], sttEnabled: false, streamEnabled: false,
  },
]

export function IMManagement({ onBack }: { onBack: () => void }) {
  const [adapters, setAdapters] = useState(mockAdapters)
  const [activeId, setActiveId] = useState('wecom')

  const active = adapters.find(a => a.id === activeId)!
  const setActive = (updater: (a: IMAdapter) => IMAdapter) => {
    setAdapters(prev => prev.map(a => a.id === activeId ? updater(a) : a))
  }

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Adapter nav */}
      <div style={{
        width: 200, background: 'var(--color-nav)',
        padding: 'var(--spacing-lg) 0',
        display: 'flex', flexDirection: 'column', gap: 2,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 var(--spacing-lg) var(--spacing-md)' }}>
          <button onClick={onBack} style={backBtnStyle}><BackArrow /></button>
          <span style={{ fontWeight: 600, fontSize: 14 }}>IM Adapters</span>
        </div>
        {adapters.map(a => (
          <button key={a.id} onClick={() => setActiveId(a.id)} style={{
            padding: 'var(--spacing-sm) var(--spacing-lg)',
            background: a.id === activeId ? 'var(--color-card)' : 'transparent',
            border: 'none', textAlign: 'left', cursor: 'pointer',
            display: 'flex', gap: 8, alignItems: 'center',
          }}>
            <span style={{ fontSize: 14 }}>{a.icon}</span>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <span style={{
                fontSize: 12, fontWeight: a.id === activeId ? 500 : 400,
                color: a.id === activeId ? 'var(--text-primary)' : 'var(--text-secondary)',
              }}>{a.name}</span>
              {a.enabled && (
                <span style={{
                  fontSize: 10,
                  color: a.connected ? 'var(--color-success)' : 'var(--color-error)',
                }}>{a.connected ? '● Connected' : '○ Disconnected'}</span>
              )}
            </div>
          </button>
        ))}
      </div>

      {/* Adapter config */}
      <div style={{
        flex: 1, padding: 'var(--spacing-xl) 32px',
        display: 'flex', flexDirection: 'column', gap: 16,
        overflowY: 'auto',
      }}>
        <h2 style={{ fontSize: 18, fontWeight: 600 }}>{active.icon} {active.name}</h2>

        {/* Enabled toggle */}
        <ToggleRow label="Enabled" value={active.enabled} onChange={v => setActive(a => ({ ...a, enabled: v }))} />

        {/* Config fields */}
        {active.fields.map((f, i) => (
          <FieldRow key={i} label={f.label}>
            <input
              type={f.secret ? 'password' : 'text'}
              value={f.value}
              onChange={e => {
                const newFields = [...active.fields]
                newFields[i] = { ...f, value: e.target.value }
                setActive(a => ({ ...a, fields: newFields }))
              }}
              placeholder={f.secret ? '••••••••' : `Enter ${f.label}...`}
              style={inputStyle}
            />
          </FieldRow>
        ))}

        {/* Test connection */}
        <div style={{ display: 'flex', gap: 8 }}>
          <button style={{
            padding: '6px 14px', borderRadius: 'var(--radius-md)',
            border: '1px solid var(--color-primary)', background: 'transparent',
            color: 'var(--color-info)', cursor: 'pointer', fontSize: 12,
          }}>Test Connection</button>
          {active.connected && (
            <span style={{ color: 'var(--color-success)', fontSize: 12, alignSelf: 'center' }}>
              ✓ Connected
            </span>
          )}
        </div>

        {/* Targets */}
        <h3 style={{ fontSize: 14, fontWeight: 600, marginTop: 8 }}>Targets</h3>
        {active.targets.map((t, i) => (
          <div key={i} style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: 'var(--spacing-sm) var(--spacing-md)',
            borderRadius: 'var(--radius-md)', background: 'var(--color-bg)',
          }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--color-info)' }}>#</span>
            <span style={{ fontSize: 12 }}>{t}</span>
            <div style={{ flex: 1 }} />
            <button onClick={() => setActive(a => ({ ...a, targets: a.targets.filter((_, j) => j !== i) }))}
              style={{ background: 'none', border: 'none', color: 'var(--color-error)', cursor: 'pointer', fontSize: 11 }}>
              Remove
            </button>
          </div>
        ))}
        <button onClick={() => setActive(a => ({ ...a, targets: [...a.targets, 'New target'] }))}
          style={{
            padding: 'var(--spacing-sm) var(--spacing-md)', borderRadius: 'var(--radius-md)',
            border: '1px solid var(--color-border)', background: 'transparent',
            color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 12,
            display: 'flex', gap: 8, alignItems: 'center',
          }}>
          <Plus size={14} /> Add target
        </button>

        {/* STT / Stream toggles */}
        <ToggleRow label="STT / TTS" value={active.sttEnabled} onChange={v => setActive(a => ({ ...a, sttEnabled: v }))} />
        <ToggleRow label="Stream Output" value={active.streamEnabled} onChange={v => setActive(a => ({ ...a, streamEnabled: v }))} />
      </div>
    </div>
  )
}

function ToggleRow({ label, value, onChange }: { label: string; value: boolean; onChange: (v: boolean) => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      <span style={{ fontSize: 13, color: 'var(--text-primary)' }}>{label}</span>
      <div style={{ flex: 1 }} />
      <button onClick={() => onChange(!value)} style={{
        width: 40, height: 22, borderRadius: 11,
        background: value ? 'var(--color-success)' : 'var(--color-surface)',
        border: 'none', cursor: 'pointer', position: 'relative',
        transition: 'background 0.15s',
      }}>
        <div style={{
          width: 18, height: 18, borderRadius: 9,
          background: '#fff', position: 'absolute', top: 2,
          left: value ? 20 : 2,
          transition: 'left 0.15s',
        }} />
      </button>
    </div>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
      <span style={{ width: 100, color: 'var(--text-secondary)', fontSize: 13, flexShrink: 0 }}>{label}</span>
      <div style={{ flex: 1 }}>{children}</div>
    </div>
  )
}

function BackArrow() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M10 3L5 8L10 13" />
    </svg>
  )
}

const inputStyle: React.CSSProperties = {
  width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
  background: 'var(--color-card)', border: '1px solid var(--color-border)',
  color: 'var(--text-primary)', outline: 'none', fontSize: 12,
  fontFamily: 'var(--font-mono)',
}

const backBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--text-secondary)',
  cursor: 'pointer', display: 'flex', alignItems: 'center',
}
