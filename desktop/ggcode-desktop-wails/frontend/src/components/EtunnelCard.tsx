import React, { useEffect, useState } from 'react'
import { Globe, Play, Square, Save } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { main } from '../../wailsjs/go/models'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { useTranslation } from '../i18n'

type EtunnelState = main.EtunnelState

const EMPTY: EtunnelState = new main.EtunnelState()

// #5574: public-URL tunnel card. Push-pull state: EtunnelStatus() pull on
// mount + "etunnel:state" EventsEmit push when activity flips (poller in
// et_ctl.go relays remote disconnects so the dot falls back without a
// manual refresh).
export function EtunnelCard() {
  const { t } = useTranslation()
  const [state, setState] = useState<EtunnelState>(EMPTY)
  const [subdomain, setSubdomain] = useState('')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    App.EtunnelStatus()
      .then((s: EtunnelState) => setState(s ?? EMPTY))
      .catch(() => {})
    EventsOn('etunnel:state', (ev: any) => {
      setState((prev) => ({ ...prev, active: !!ev?.active, url: ev?.url || '' }))
    })
    return () => {
      EventsOff('etunnel:state')
    }
  }, [])

  const run = (fn: () => Promise<main.EtunnelState>) => {
    setBusy(true)
    fn()
      .then((s: main.EtunnelState) => setState(s ?? EMPTY))
      .catch((e: any) => setState((prev) => ({ ...prev, message: String(e) })))
      .finally(() => setBusy(false))
  }

  const labelStyle: React.CSSProperties = {
    display: 'block',
    fontSize: 12,
    opacity: 0.7,
    marginBottom: 4,
  }
  const inputStyle: React.CSSProperties = {
    width: '100%',
    boxSizing: 'border-box',
    padding: '8px 10px',
    borderRadius: 8,
    border: '1px solid rgba(128,128,128,0.35)',
    background: 'rgba(128,128,128,0.08)',
    marginBottom: 12,
  }
  const btnStyle = (primary: boolean): React.CSSProperties => ({
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '8px 14px',
    borderRadius: 8,
    border: 'none',
    cursor: busy ? 'wait' : 'pointer',
    opacity: busy ? 0.6 : 1,
    background: primary ? 'var(--accent, #4f8cff)' : 'rgba(128,128,128,0.15)',
    color: primary ? '#fff' : 'inherit',
  })

  return (
    <div style={{ maxWidth: 560 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
        <Globe size={16} />
        <span
          data-testid="etunnel-dot"
          style={{
            width: 10,
            height: 10,
            borderRadius: '50%',
            background: state.active ? '#34c759' : 'rgba(128,128,128,0.6)',
            display: 'inline-block',
          }}
        />
        <span style={{ fontSize: 13 }}>
          {state.active ? t('settings.etunnelActive') : t('settings.etunnelInactive')}
        </span>
      </div>

      {state.url && (
        <p data-testid="etunnel-url" style={{ fontSize: 13, wordBreak: 'break-all', margin: '0 0 12px' }}>
          <a href={state.url} target="_blank" rel="noreferrer">
            {state.url}
          </a>
        </p>
      )}

      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <button style={btnStyle(true)} disabled={busy || state.active} onClick={() => run(() => App.EtunnelStart())}>
          <Play size={14} /> {t('settings.etunnelStart')}
        </button>
        <button style={btnStyle(false)} disabled={busy || !state.active} onClick={() => run(() => App.EtunnelStop())}>
          <Square size={14} /> {t('settings.etunnelStop')}
        </button>
      </div>

      {state.message && (
        <p style={{ fontSize: 12, opacity: 0.75, whiteSpace: 'pre-wrap', margin: '0 0 16px' }}>{state.message}</p>
      )}

      <div style={{ borderTop: '1px solid rgba(128,128,128,0.25)', paddingTop: 16, marginTop: 8 }}>
        <label style={labelStyle}>{t('settings.etunnelSubdomain')}</label>
        <input
          style={inputStyle}
          value={subdomain}
          onChange={(e) => setSubdomain(e.target.value)}
          placeholder="my-team"
        />
        <label style={labelStyle}>{t('settings.etunnelToken')}</label>
        <input
          style={inputStyle}
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          placeholder="et_xxx"
        />
        <button
          style={btnStyle(false)}
          disabled={busy}
          onClick={() =>
            run(() => App.EtunnelSetup(subdomain.trim(), token.trim()))
          }
        >
          <Save size={14} /> {t('settings.etunnelSave')}
        </button>
        <p style={{ fontSize: 12, opacity: 0.6, margin: '12px 0 0' }}>{t('settings.etunnelHint')}</p>
      </div>
    </div>
  )
}
