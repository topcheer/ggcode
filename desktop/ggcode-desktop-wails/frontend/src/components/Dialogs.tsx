import React, { useEffect, useState } from 'react'
import { X, ExternalLink, RefreshCw, Check, Download } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime'
import { useTranslation } from '../i18n'

function openLink(url: string) {
  BrowserOpenURL(url)
}

export function AboutDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [version, setVersion] = useState('dev')
  const [checking, setChecking] = useState(false)
  const [updateResult, setUpdateResult] = useState<{
    hasUpdate: boolean
    latestVersion: string
    error: string
  } | null>(null)

  useEffect(() => {
    App.GetVersion().then(v => setVersion(v || 'dev')).catch(() => {})
  }, [])

  const handleCheckUpdate = async () => {
    setChecking(true)
    setUpdateResult(null)
    try {
      const result = await App.CheckForUpdates()
      if (result.error) {
        setUpdateResult({ hasUpdate: false, latestVersion: '', error: result.error as string })
      } else {
        setUpdateResult({
          hasUpdate: result.has_update as boolean,
          latestVersion: (result.latest_version as string) || '',
          error: '',
        })
      }
    } catch (e: any) {
      setUpdateResult({ hasUpdate: false, latestVersion: '', error: e.message || String(e) })
    } finally {
      setChecking(false)
    }
  }

  return (
    <div style={{
      position: 'fixed', inset: 0,
      background: 'rgba(0,0,0,0.5)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 1000,
    }} onClick={onClose}>
      <div style={{
        width: 420, background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
        border: '1px solid var(--color-border)',
        display: 'flex', flexDirection: 'column',
        boxShadow: '0 16px 48px rgba(0,0,0,0.5)',
        overflow: 'hidden',
      }} onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '16px 20px',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <span style={{ fontWeight: 700, fontSize: 15, color: 'var(--text-primary)' }}>
            {t('about.title')}
          </span>
          <button onClick={onClose} style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--text-tertiary)', padding: 4, borderRadius: 'var(--radius-sm)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div style={{
          padding: '24px 28px',
          display: 'flex', flexDirection: 'column', gap: 16,
          alignItems: 'center',
        }}>
          {/* Logo */}
          <img src={new URL('../assets/images/app-icon.png', import.meta.url).href}
            alt="GGCode"
            style={{ width: 56, height: 56, borderRadius: 14 }}
          />

          <div style={{ textAlign: 'center' }}>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--text-primary)' }}>{t('dialogs.productName')}</div>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-secondary)', marginTop: 4 }}>
              {version !== 'dev' ? `v${version}` : 'dev'}
            </div>
          </div>

          <div style={{ fontSize: 12, color: 'var(--text-secondary)', textAlign: 'center', lineHeight: 1.5 }}>
            {t('about.description')}
          </div>

          {/* Links */}
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', justifyContent: 'center' }}>
            {[
              { label: t('dialogs.github'), url: 'https://github.com/topcheer/ggcode' },
              { label: t('dialogs.releases'), url: 'https://github.com/topcheer/ggcode/releases' },
              { label: t('dialogs.issues'), url: 'https://github.com/topcheer/ggcode/issues' },
              { label: t('dialogs.discord'), url: 'https://discord.gg/F2v4mJmfG' },
            ].map(link => (
              <button key={link.label} onClick={() => openLink(link.url)} style={{
                background: 'none', border: 'none', cursor: 'pointer',
                fontSize: 12, color: 'var(--color-info)',
                display: 'flex', alignItems: 'center', gap: 4,
                padding: '4px 8px', borderRadius: 'var(--radius-sm)',
              }}>
                {link.label} <ExternalLink size={10} />
              </button>
            ))}
          </div>

          {/* Check for updates */}
          <div style={{
            width: '100%', padding: '12px 16px',
            borderRadius: 'var(--radius-md)',
            background: 'var(--color-surface)',
            display: 'flex', flexDirection: 'column', gap: 8,
            alignItems: 'center',
          }}>
            {updateResult && !updateResult.error && (
              <div style={{
                fontSize: 12, textAlign: 'center',
                color: updateResult.hasUpdate ? 'var(--color-success)' : 'var(--text-secondary)',
              }}>
                {updateResult.hasUpdate
                  ? <>{t('dialogs.newVersion')}{updateResult.latestVersion} <button onClick={() => openLink('https://github.com/topcheer/ggcode/releases')} style={{ background: 'none', border: 'none', color: 'var(--color-info)', cursor: 'pointer', fontSize: 12, textDecoration: 'underline' }}><Download size={11} /> {t('dialogs.download')}</button></>
                  : <><Check size={13} style={{ verticalAlign: 'middle' }} /> {t('dialogs.upToDate')}{version})</>
                }
              </div>
            )}
            {updateResult && updateResult.error && (
              <div style={{ fontSize: 12, color: 'var(--color-error)', textAlign: 'center' }}>
                {updateResult.error}
              </div>
            )}
            <button onClick={handleCheckUpdate} disabled={checking} style={{
              background: checking ? 'var(--color-surface)' : 'var(--color-primary)',
              color: checking ? 'var(--text-tertiary)' : '#fff',
              border: '1px solid var(--color-border)',
              borderRadius: 'var(--radius-md)',
              padding: '6px 16px',
              fontSize: 12, fontWeight: 600, cursor: checking ? 'not-allowed' : 'pointer',
              display: 'flex', alignItems: 'center', gap: 6,
            }}>
              <RefreshCw size={13} style={{ animation: checking ? 'spin 1s linear infinite' : 'none' }} />
              {checking ? t('dialogs.checking') : t('dialogs.checkUpdate')}
            </button>
          </div>

          <span style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
            MIT License · © 2025 GG AI Studio
          </span>
        </div>
      </div>

      <style>{`
        @keyframes spin {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  )
}

export function UpdateNotification({ onClose }: { onClose: () => void }) {
  // The backend will push an event to show this dialog when an update is found.
  return null
}
