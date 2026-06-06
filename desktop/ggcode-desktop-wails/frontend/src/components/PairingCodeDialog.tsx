import React from 'react'
import { Link2 } from 'lucide-react'
import { useTranslation } from '../i18n'

export interface PairingRequest {
  adapter: string
  platform: string
  code: string
  kind?: string
}

export function PairingCodeDialog({ request, onClose }: { request: PairingRequest; onClose: () => void }) {
  const { t } = useTranslation()
  const isRebind = request.kind === 'rebind'
  const title = isRebind ? 'IM Rebind Required' : t('pairing.title')
  const body = isRebind
    ? `This bot is already bound to another channel. Enter this 4-digit code in the new ${request.platform} channel to switch the binding.`
    : `A ${request.platform} channel is requesting to bind this workspace. Enter this 4-digit code in that channel to complete pairing.`

  const codeDigits = request.code.split('').join('   ')

  return (
    <div style={{
      position: 'fixed', inset: 0,
      background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 1000,
    }}>
      <div style={{
        background: 'var(--color-surface)',
        borderRadius: 'var(--radius-lg)',
        border: '1px solid var(--color-border)',
        width: 460,
        maxWidth: '92vw',
        boxShadow: '0 20px 60px rgba(0,0,0,0.4)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden',
      }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '16px 20px',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <Link2 size={18} style={{ color: 'var(--color-primary)', flexShrink: 0 }} />
          <span style={{ fontWeight: 700, fontSize: 15, color: 'var(--text-primary)' }}>
            {title}
          </span>
        </div>

        <div style={{ padding: '16px 20px 8px', display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 6, alignSelf: 'flex-start',
            padding: '4px 10px', borderRadius: 'var(--radius-md)',
            background: 'rgba(99,102,241,0.15)', color: '#818cf8',
            fontWeight: 600, fontSize: 13,
          }}>
            {request.adapter} [{request.platform}]
          </div>
          <div style={{ color: 'var(--text-secondary)', fontSize: 13, lineHeight: 1.6 }}>
            {body}
          </div>
          <div style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
            Enter this code in your IM channel:
          </div>
          <div style={{
            alignSelf: 'stretch',
            padding: '18px 20px',
            borderRadius: 'var(--radius-lg)',
            background: 'linear-gradient(135deg, rgba(37,99,235,0.9), rgba(79,70,229,0.9))',
            color: '#fff',
            fontSize: 28,
            fontWeight: 800,
            letterSpacing: '0.08em',
            textAlign: 'center',
            fontFamily: 'var(--font-mono)',
          }}>
            {codeDigits}
          </div>
        </div>

        <div style={{
          display: 'flex', justifyContent: 'flex-end',
          padding: '8px 20px 18px',
        }}>
          <button
            onClick={onClose}
            style={{
              padding: '8px 18px',
              borderRadius: 'var(--radius-md)',
              background: 'var(--color-card)',
              color: 'var(--text-secondary)',
              border: '1px solid var(--color-border)',
              cursor: 'pointer',
              fontWeight: 600,
              fontSize: 13,
            }}
          >
            {t('pairing.close')}
          </button>
        </div>
      </div>
    </div>
  )
}
