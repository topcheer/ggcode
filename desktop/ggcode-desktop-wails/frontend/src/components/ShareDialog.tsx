import React, { useState, useEffect } from 'react'
import { X, Copy, Smartphone, StopCircle, ExternalLink } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { useTranslation } from '../i18n'

interface ShareInfo {
  connectURL: string
  qrCodeBase64: string
}

export default function ShareDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [shareInfo, setShareInfo] = useState<ShareInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [connected, setConnected] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    // Auto-start sharing on mount
    startSharing()
  }, [])

  useEffect(() => {
    EventsOn('tunnel:connected', () => {
      setConnected(true)
      // Auto-close the dialog once a mobile client has connected.
      setTimeout(() => onClose(), 600)
    })
    EventsOn('tunnel:disconnected', () => {
      setConnected(false)
    })
    return () => {
      EventsOff('tunnel:connected')
      EventsOff('tunnel:disconnected')
    }
  }, [])

  const startSharing = async () => {
    setLoading(true)
    setError('')
    try {
      const info = await App.StartShare() as ShareInfo
      setShareInfo(info)
    } catch (e: any) {
      setError(e?.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  const stopSharing = async () => {
    try {
      await App.StopShare()
      setShareInfo(null)
      setConnected(false)
      onClose()
    } catch {}
  }

  const copyURL = async () => {
    if (!shareInfo?.connectURL) return
    try {
      await navigator.clipboard.writeText(shareInfo.connectURL)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {}
  }

  return (
    <div style={{
      position: 'fixed', inset: 0,
      background: 'rgba(0,0,0,0.5)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 1000,
    }} onClick={onClose}>
      <div style={{
        background: 'var(--color-surface)',
        borderRadius: 'var(--radius-lg)',
        padding: 24,
        width: 400,
        maxWidth: '90vw',
        boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
        display: 'flex', flexDirection: 'column', gap: 16,
      }} onClick={e => e.stopPropagation()}>

        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Smartphone size={20} style={{ color: 'var(--color-primary)' }} />
            <span style={{ fontWeight: 700, fontSize: 16, color: 'var(--text-primary)' }}>
              {t('share.title')}
            </span>
          </div>
          <button onClick={onClose} style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--text-tertiary)', padding: 4,
          }}><X size={18} /></button>
        </div>

        {/* QR Code */}
        {loading && (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--text-tertiary)' }}>
            {t('share.generating')}
          </div>
        )}

        {error && (
          <div style={{
            padding: 12, borderRadius: 'var(--radius-md)',
            background: 'rgba(220,38,38,0.1)', color: '#f87171', fontSize: 13,
          }}>
            {error}
          </div>
        )}

        {shareInfo?.qrCodeBase64 && (
          <div style={{ display: 'flex', justifyContent: 'center' }}>
            <img src={shareInfo.qrCodeBase64}
              alt="QR Code"
              style={{ width: 200, height: 200, borderRadius: 'var(--radius-md)' }}
            />
          </div>
        )}

        {/* Connection URL */}
        {shareInfo?.connectURL && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '8px 12px', borderRadius: 'var(--radius-md)',
            background: 'var(--color-card)', border: '1px solid var(--color-border)',
          }}>
            <input readOnly value={shareInfo.connectURL} style={{
              flex: 1, border: 'none', background: 'transparent',
              fontSize: 12, fontFamily: 'var(--font-mono)',
              color: 'var(--text-primary)', outline: 'none',
            }} onClick={e => (e.target as HTMLInputElement).select()} />
            <button onClick={copyURL} style={{
              padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-primary)', color: '#fff',
              border: 'none', cursor: 'pointer', fontSize: 11,
              display: 'flex', alignItems: 'center', gap: 4,
            }}><Copy size={12} /> {copied ? t('share.copied') : t('share.copy')}</button>
          </div>
        )}

        {/* Connection status */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '8px 12px', borderRadius: 'var(--radius-md)',
          background: connected ? 'rgba(34,197,94,0.1)' : 'rgba(255,255,255,0.05)',
          border: `1px solid ${connected ? 'rgba(34,197,94,0.3)' : 'var(--color-border)'}`,
        }}>
          <div style={{
            width: 8, height: 8, borderRadius: '50%',
            background: connected ? 'var(--color-success)' : 'var(--color-warning)',
            animation: 'pulse 2s infinite',
          }} />
          <span style={{ fontSize: 12, color: connected ? 'var(--color-success)' : 'var(--text-tertiary)' }}>
            {connected ? 'Mobile client connected' : t('share.generating')}
          </span>
        </div>

        {/* Download links */}
        <div style={{ display: 'flex', gap: 8, justifyContent: 'center' }}>
          <a href="https://apps.apple.com/app/ggcode-mobile/id6770855612" target="_blank" rel="noopener noreferrer" style={{
            display: 'flex', alignItems: 'center', gap: 4,
            fontSize: 11, color: 'var(--color-info)',
            textDecoration: 'none',
          }}><ExternalLink size={12} /> iOS (TestFlight)</a>
          <a href="https://play.google.com/apps/testing/gg.ai.ggcode.mobile" target="_blank" rel="noopener noreferrer" style={{
            display: 'flex', alignItems: 'center', gap: 4,
            fontSize: 11, color: 'var(--color-info)',
            textDecoration: 'none',
          }}><ExternalLink size={12} /> Android</a>
          <a href="https://discord.gg/F2v4mJmfG" target="_blank" rel="noopener noreferrer" style={{
            display: 'flex', alignItems: 'center', gap: 4,
            fontSize: 11, color: 'var(--color-info)',
            textDecoration: 'none',
          }}><ExternalLink size={12} /> Discord</a>
        </div>

        {/* Stop sharing */}
        {shareInfo && (
          <button onClick={stopSharing} style={{
            padding: '8px 16px', borderRadius: 'var(--radius-md)',
            background: 'rgba(220,38,38,0.1)', color: '#f87171',
            border: '1px solid rgba(220,38,38,0.3)',
            cursor: 'pointer', fontWeight: 600, fontSize: 13,
            display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
          }}><StopCircle size={16} /> {t('share.stopSharing')}</button>
        )}
      </div>
    </div>
  )
}
