import React, { useEffect } from 'react'
import { X, Command, CornerDownLeft, ArrowUp, ArrowDown, Search, PanelLeft, PanelRight, Settings, Plus, Share2, UserCog } from 'lucide-react'
import { useTranslation, type TranslationKey } from '../i18n'

interface ShortcutGroup {
  titleKey: TranslationKey
  shortcuts: { keys: string[]; labelKey: TranslationKey; icon?: React.ComponentType<{ size?: number; style?: React.CSSProperties }> }[]
}

const SHORTCUT_GROUPS: ShortcutGroup[] = [
  {
    titleKey: 'kb.global',
    shortcuts: [
      { keys: ['⌘', 'K'], labelKey: 'kb.cmdPalette', icon: Command },
      { keys: ['⌘', 'B'], labelKey: 'kb.toggleSidebar', icon: PanelLeft },
      { keys: ['⌘', '.'], labelKey: 'kb.toggleContext', icon: PanelRight },
      { keys: ['⌘', ','], labelKey: 'kb.openSettings', icon: Settings },
      { keys: ['?'], labelKey: 'kb.showShortcuts', icon: Search },
      { keys: ['Esc'], labelKey: 'kb.closeDialog' },
    ],
  },
  {
    titleKey: 'kb.chat',
    shortcuts: [
      { keys: ['⌘', 'N'], labelKey: 'kb.newSession', icon: Plus },
      { keys: ['⌘', '⇧', 'S'], labelKey: 'kb.shareSession', icon: Share2 },
      { keys: ['⌘', 'G'], labelKey: 'kb.toggleIdentity', icon: UserCog },
      { keys: ['⌘', 'F'], labelKey: 'kb.searchInChat', icon: Search },
      { keys: ['↵'], labelKey: 'kb.sendMessage', icon: CornerDownLeft },
      { keys: ['⇧', '↵'], labelKey: 'kb.newline' },
    ],
  },
  {
    titleKey: 'kb.navigation',
    shortcuts: [
      { keys: ['↑', '↓'], labelKey: 'kb.navigateList' },
      { keys: ['j', 'k'], labelKey: 'kb.vimNav' },
      { keys: ['↵'], labelKey: 'kb.selectItem' },
    ],
  },
]

function KeyCap({ children }: { children: React.ReactNode }) {
  return (
    <kbd style={{
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      minWidth: 22, height: 22, padding: '0 6px',
      borderRadius: 'var(--radius-sm)',
      background: 'var(--color-surface)',
      border: '1px solid var(--color-border)',
      fontSize: 11, fontWeight: 600, color: 'var(--text-secondary)',
      fontFamily: 'ui-monospace, "SF Mono", monospace',
    }}>{children}</kbd>
  )
}

export function KeyboardShortcuts({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [onClose])

  return (
    <>
      <div onClick={onClose} style={{
        position: 'absolute', inset: 0, zIndex: 199,
        background: 'rgba(0,0,0,0.4)',
      }} />
      <div style={{
        position: 'absolute', top: '50%', left: '50%',
        transform: 'translate(-50%, -50%)',
        width: 480, maxHeight: '70vh',
        background: 'var(--color-card)', borderRadius: 'var(--radius-xl)',
        border: '1px solid var(--color-border)',
        boxShadow: '0 16px 48px rgba(0,0,0,0.5)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden', zIndex: 200,
      }}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: 'var(--spacing-md) var(--spacing-lg)',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <h2 style={{ margin: 0, fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>
            {t('kb.title')}
          </h2>
          <button onClick={onClose} style={{
            background: 'transparent', border: 'none', cursor: 'pointer',
            color: 'var(--text-tertiary)', padding: 4, display: 'flex',
          }}>
            <X size={16} />
          </button>
        </div>

        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--spacing-sm) 0' }}>
          {SHORTCUT_GROUPS.map(group => (
            <div key={group.titleKey as string}>
              <div style={{
                padding: '8px var(--spacing-lg) 4px',
                fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)',
                textTransform: 'uppercase', letterSpacing: '0.05em',
              }}>{t(group.titleKey)}</div>
              {group.shortcuts.map(sc => {
                const Icon = sc.icon
                return (
                  <div key={sc.labelKey as string} style={{
                    display: 'flex', alignItems: 'center',
                    padding: '6px var(--spacing-lg)',
                  }}>
                    {Icon && <Icon size={14} style={{ marginRight: 8, color: 'var(--text-tertiary)', flexShrink: 0 }} />}
                    <span style={{
                      fontSize: 13, color: 'var(--text-secondary)',
                      flex: 1,
                    }}>{t(sc.labelKey)}</span>
                    <div style={{ display: 'flex', gap: 4 }}>
                      {sc.keys.map((k, i) => <KeyCap key={i}>{k}</KeyCap>)}
                    </div>
                  </div>
                )
              })}
            </div>
          ))}
        </div>

        {/* Footer */}
        <div style={{
          padding: 'var(--spacing-sm) var(--spacing-lg)',
          borderTop: '1px solid var(--color-border)',
          fontSize: 11, color: 'var(--text-tertiary)',
          textAlign: 'center',
        }}>
          {t('kb.footer')}
        </div>
      </div>
    </>
  )
}
