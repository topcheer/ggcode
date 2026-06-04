import React, { useState, useEffect } from 'react'
import type { JSX } from 'react'
import { Plus, Trash2, Power, PowerOff, Volume2, VolumeX } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

// ── Types matching Go structs ──

interface IMAdapterInfo {
  name: string
  enabled: boolean
  muted: boolean
  platform: string
  transport: string
  command: string
  extra: Record<string, string>
  targets: string[]
  workspace: string
  isCurrent: boolean
}

interface IMPlatformField {
  key: string
  label: string
  placeholder: string
  secret?: boolean
}

interface IMPlatformMeta {
  id: string
  displayName: string
  fields: IMPlatformField[]
  qrAuth: boolean
}

// ── Brand SVG icons (24x24 viewBox, official brand colors) ──

const PLATFORM_ICONS: Record<string, { svg: JSX.Element; color: string }> = {
  qq: {
    color: '#12B7F5',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 13.24c-.18.53-.5 1.08-.94 1.5.23.16.36.38.36.62 0 .58-.72 1.06-1.6 1.06-.62 0-1.16-.24-1.42-.58H11c-.26.34-.8.58-1.42.58-.88 0-1.6-.48-1.6-1.06 0-.24.13-.46.36-.62-.44-.42-.76-.97-.94-1.5-.42.12-.82.16-1.12.06-.68-.22-.82-1.1-.32-1.96.32-.54.82-.94 1.32-1.06.02-.54.14-1.06.36-1.54-.64-.46-1.1-1.16-1.26-1.98C6.12 7.02 7.84 4 12 4s5.88 3.02 5.62 5.76c-.16.82-.62 1.52-1.26 1.98.22.48.34 1 .36 1.54.5.12 1 .52 1.32 1.06.5.86.36 1.74-.32 1.96-.3.1-.7.06-1.12-.06z"/></svg>,
  },
  telegram: {
    color: '#26A5E4',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.479.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"/></svg>,
  },
  discord: {
    color: '#5865F2',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189z"/></svg>,
  },
  feishu: {
    color: '#3370FF',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M3.977 5.743A3.978 3.978 0 017.955 1.765a3.978 3.978 0 013.978 3.978v3.978H7.955A3.978 3.978 0 013.977 5.743zm8.09 0V1.765h3.978a3.978 3.978 0 013.978 3.978 3.978 3.978 0 01-3.978 3.978h-3.978zm0 8.09h3.978a3.978 3.978 0 013.978 3.978 3.978 3.978 0 01-3.978 3.978 3.978 3.978 0 01-3.978-3.978v-3.978zm-4.112 4.077A4.077 4.077 0 013.878 13.833a4.077 4.077 0 014.077-4.077 4.077 4.077 0 014.077 4.077 4.077 4.077 0 01-4.077 4.077z"/></svg>,
  },
  dingtalk: {
    color: '#0082EF',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M12 0C5.373 0 0 5.373 0 12s5.373 12 12 12 12-5.373 12-12S18.627 0 12 0zm5.56 11.73l-1.12.84.28 1.36a.28.28 0 01-.42.3L12 12.38l-4.3 1.85a.28.28 0 01-.42-.3l.28-1.36-1.12-.84a.28.28 0 01.16-.5l1.43-.12.56-1.28a.28.28 0 01.52 0l.56 1.28 1.43.12a.28.28 0 01.16.5l-1.12.84.28 1.36a.28.28 0 01-.42.3L12 12.38l3.46 1.49a.28.28 0 01.37-.37l-.28-1.36 1.12-.84a.28.28 0 01-.16-.5l-1.43-.12-.56-1.28a.28.28 0 00-.52 0l-.56 1.28-1.43.12a.28.28 0 00-.16.5z"/></svg>,
  },
  slack: {
    color: '#4A154B',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zm1.271 0a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313zM8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zm0 1.271a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312zm10.122 2.521a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522V8.834zm-1.268 0a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.165 0a2.528 2.528 0 0 1 2.523 2.522v6.312zm-2.523 10.122a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.165 24a2.527 2.527 0 0 1-2.52-2.522v-2.522h2.52zm0-1.268a2.527 2.527 0 0 1-2.52-2.523 2.527 2.527 0 0 1 2.52-2.52h6.313A2.527 2.527 0 0 1 24 15.165a2.528 2.528 0 0 1-2.522 2.523h-6.313z"/></svg>,
  },
  wechat: {
    color: '#07C160',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M8.691 2.188C3.891 2.188 0 5.476 0 9.53c0 2.212 1.17 4.203 3.002 5.55a.59.59 0 0 1 .213.665l-.39 1.48c-.019.07-.048.141-.048.213 0 .163.13.295.29.295a.326.326 0 0 0 .167-.054l1.903-1.114a.864.864 0 0 1 .717-.098 10.16 10.16 0 0 0 2.837.403c.276 0 .543-.027.811-.05-.857-2.578.157-4.972 1.932-6.446 1.703-1.415 3.882-1.98 5.853-1.838-.576-3.583-4.196-6.348-8.596-6.348zM5.785 5.991c.642 0 1.162.529 1.162 1.18a1.17 1.17 0 0 1-1.162 1.178A1.17 1.17 0 0 1 4.623 7.17c0-.651.52-1.18 1.162-1.18zm5.813 0c.642 0 1.162.529 1.162 1.18a1.17 1.17 0 0 1-1.162 1.178 1.17 1.17 0 0 1-1.162-1.178c0-.651.52-1.18 1.162-1.18zm3.844 4.014c-3.898-.29-7.465 2.268-7.465 5.66 0 3.314 3.26 5.945 7.12 5.945a8.37 8.37 0 0 0 2.34-.333.67.67 0 0 1 .563.076l1.57.919a.274.274 0 0 0 .14.045c.133 0 .241-.108.241-.245 0-.06-.024-.118-.04-.176l-.323-1.224a.478.478 0 0 1 .176-.548C21.928 19.17 24 17.335 24 15.065c0-3.104-3.152-5.593-7.558-5.06zM14.7 13.2c.537 0 .973.442.973.988a.98.98 0 0 1-.973.985.98.98 0 0 1-.973-.985c0-.546.436-.988.973-.988zm4.865 0c.537 0 .973.442.973.988a.98.98 0 0 1-.973.985.98.98 0 0 1-.973-.985c0-.546.436-.988.973-.988z"/></svg>,
  },
  wecom: {
    color: '#07C160',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M5.455 7.101a1.4 1.4 0 1 1 0-2.8 1.4 1.4 0 0 1 0 2.8zm4.586 0a1.4 1.4 0 1 1 0-2.8 1.4 1.4 0 0 1 0 2.8zm-2.317 8.455A7.227 7.227 0 0 1 .505 8.327 7.227 7.227 0 0 1 7.724 1.1a7.227 7.227 0 0 1 7.218 7.227 7.227 7.227 0 0 1-5.473 6.988l-.476 1.354a.453.453 0 0 1-.749.187l-1.52-1.3zm7.369 2.17a1.2 1.2 0 1 1 0-2.4 1.2 1.2 0 0 1 0 2.4zm3.934 0a1.2 1.2 0 1 1 0-2.4 1.2 1.2 0 0 1 0 2.4zM17.063 24a6.2 6.2 0 0 1-6.193-6.207A6.2 6.2 0 0 1 17.063 11.6a6.2 6.2 0 0 1 6.192 6.193A6.2 6.2 0 0 1 18.1 23.84l-.389 1.104a.368.368 0 0 1-.61.153l-1.038-.944z"/></svg>,
  },
  whatsapp: {
    color: '#25D366',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413z"/></svg>,
  },
  mattermost: {
    color: '#0058CC',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M1.875 0C.839 0 0 .839 0 1.875v20.25C0 23.161.839 24 1.875 24h20.25C23.161 24 24 23.161 24 22.125V1.875C24 .839 23.161 0 22.125 0zm9.94 3.842a.198.198 0 01.22.079l.81 1.218.012.016 5.67 8.544c.324.492.408 1.092.24 1.656a2.16 2.16 0 01-1.14 1.32l-3.168 1.5a2.7 2.7 0 01-3.348-1.068L5.65 8.93a.72.72 0 01.18-.96l.204-.132a.72.72 0 01.96.192l4.56 6.564a1.26 1.26 0 001.56.492l2.46-1.164a.54.54 0 00.264-.708.54.54 0 00-.18-.24l-5.82-3.756a.3.3 0 01-.084-.396l.672-1.08a1.26 1.26 0 011.608-.444l2.748 1.164a.12.12 0 00.156-.06.12.12 0 00-.012-.12l-3-4.14z"/></svg>,
  },
  signal: {
    color: '#3A76F0',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M12 0a12 12 0 0 1 8.485 3.515A12 12 0 0 1 24 12a12 12 0 0 1-3.515 8.485A12 12 0 0 1 12 24a12 12 0 0 1-8.485-3.515A12 12 0 0 1 0 12 12 12 0 0 1 3.515 3.515 12 12 0 0 1 12 0zm0 1.8A10.2 10.2 0 0 0 4.8 5.76 10.2 10.2 0 0 0 1.8 12a10.2 10.2 0 0 0 2.52 6.72l-.564 1.644a1.8 1.8 0 0 0 2.298 2.298l1.644-.564A10.2 10.2 0 0 1 12 22.2a10.2 10.2 0 0 0 7.2-2.964A10.2 10.2 0 0 0 22.2 12a10.2 10.2 0 0 0-3.96-8.244A10.2 10.2 0 0 0 12 1.8zM8.64 6.276a1.2 1.2 0 0 1 .912.336l3.624 3.624a1.2 1.2 0 0 1 0 1.696l-.048.048 2.04 2.04a.48.48 0 0 1 0 .679l-.004.004a.48.48 0 0 1-.679 0l-2.04-2.04-.048.048a1.2 1.2 0 0 1-1.696 0L7.08 9.012a1.2 1.2 0 0 1 0-1.696l1.52-1.52a1.2 1.2 0 0 1 .04-.22v-.3z"/></svg>,
  },
  irc: {
    color: '#95959B',
    svg: <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d="M.676 4.318A.676.676 0 0 0 0 4.994v.012c0 .17.064.333.178.456L5.94 12 .178 18.538A.676.676 0 0 0 .676 19.682h4.31c.193 0 .377-.083.505-.228l4.458-5.063 4.458 5.063a.676.676 0 0 0 .505.228h4.31a.676.676 0 0 0 .498-1.144L14.958 12l5.864-6.538a.676.676 0 0 0-.498-1.144h-4.31a.676.676 0 0 0-.505.228l-4.458 5.063L6.49 4.546a.676.676 0 0 0-.505-.228z"/></svg>,
  },
}

function PlatformIcon({ platform, size = 20 }: { platform: string; size?: number }) {
  const icon = PLATFORM_ICONS[platform]
  if (!icon) return <div style={{ width: size, height: size, borderRadius: '50%', background: 'var(--color-border)' }} />
  return (
    <div style={{ width: size, height: size, color: icon.color, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
      {React.cloneElement(icon.svg, { width: size, height: size })}
    </div>
  )
}

// ── Component ──

export function IMManagement() {
  const [adapters, setAdapters] = useState<IMAdapterInfo[]>([])
  const [platforms, setPlatforms] = useState<IMPlatformMeta[]>([])
  const [showAdd, setShowAdd] = useState(false)
  const [editAdapter, setEditAdapter] = useState<string | null>(null)
  const [editFields, setEditFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')

  useEffect(() => {
    loadData()
  }, [])

  async function loadData() {
    try {
      console.log('[IM] Calling ListIMAdapters + GetIMPlatformRegistry...')
      const [adaptersResult, platformsResult] = await Promise.all([
        App.ListIMAdapters() as Promise<IMAdapterInfo[]>,
        App.GetIMPlatformRegistry() as Promise<IMPlatformMeta[]>,
      ])
      console.log('[IM] Adapters:', adaptersResult)
      console.log('[IM] Platforms:', platformsResult)
      setAdapters(adaptersResult || [])
      setPlatforms(platformsResult || [])
    } catch (e: any) {
      console.error('[IM] Error:', e)
      setError(e?.message || 'Failed to load IM config')
    }
  }

  // Find platform meta by ID
  function getPlatform(id: string): IMPlatformMeta | undefined {
    return platforms.find(p => p.id === id)
  }

  // ── Add adapter dialog ──
  if (showAdd) {
    return <AddAdapterDialog
      platforms={platforms}
      onAdd={async (name, platform, fields) => {
        try {
          const values: Record<string, string> = { platform, ...fields }
          await App.SaveIMAdapter(name, values)
          setShowAdd(false)
          loadData()
        } catch (e: any) {
          setError(e?.message || 'Failed to save adapter')
        }
      }}
      onCancel={() => setShowAdd(false)}
      error={error}
    />
  }

  // ── Edit adapter ──
  if (editAdapter) {
    const adapter = adapters.find(a => a.name === editAdapter)
    const platform = adapter ? getPlatform(adapter.platform) : undefined
    return <EditAdapterDialog
      adapter={adapter!}
      platform={platform}
      fields={editFields}
      setFields={setEditFields}
      onSave={async () => {
        try {
          const values: Record<string, string> = { platform: adapter!.platform, ...editFields }
          await App.SaveIMAdapter(adapter!.name, values)
          setEditAdapter(null)
          setEditFields({})
          loadData()
        } catch (e: any) {
          setError(e?.message || 'Failed to save')
        }
      }}
      onCancel={() => { setEditAdapter(null); setEditFields({}) }}
      error={error}
    />
  }

  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16, flex: 1, overflow: 'auto', minHeight: 0 }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <h3 style={{ margin: 0, color: 'var(--text-primary)' }}>IM Adapters</h3>
        <div style={{ flex: 1 }} />
        <button onClick={() => { setShowAdd(true); setError('') }} style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '6px 14px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontSize: 13,
        }}>
          <Plus size={14} /> Add Adapter
        </button>
      </div>

      {error && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error}</div>}

      {/* Adapter list — grouped by workspace */}
      {adapters.length === 0 ? (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 13, textAlign: 'center', padding: 40 }}>
          No IM adapters configured. Click "Add Adapter" to get started.
        </div>
      ) : (() => {
        // Group by isCurrent, then workspace, then unbound
        const current = adapters.filter(a => a.isCurrent)
        const other = adapters.filter(a => !a.isCurrent)
        return (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {current.length > 0 && (
              <div>
                <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-success)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
                  This Workspace ({current.length})
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  {current.map(adapter => <AdapterRow key={adapter.name} adapter={adapter} onReload={loadData} onEdit={(n: string, e: Record<string, string>) => { setEditFields(e); setEditAdapter(n); setError('') }} />)}
                </div>
              </div>
            )}
            {other.length > 0 && (
              <div>
                <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-tertiary)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
                  {current.length > 0 ? 'Other Adapters' : 'All Adapters'} ({other.length})
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6, opacity: 0.7 }}>
                  {other.map(adapter => <AdapterRow key={adapter.name} adapter={adapter} onReload={loadData} onEdit={(n: string, e: Record<string, string>) => { setEditFields(e); setEditAdapter(n); setError('') }} />)}
                </div>
              </div>
            )}
          </div>
        )
      })()}
    </div>
  )
}

// ── Add Adapter Dialog ──

function AddAdapterDialog({ platforms, onAdd, onCancel, error }: {
  platforms: IMPlatformMeta[]
  onAdd: (name: string, platform: string, fields: Record<string, string>) => void
  onCancel: () => void
  error: string
}) {
  const [selectedPlatform, setSelectedPlatform] = useState('')
  const [adapterName, setAdapterName] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [localError, setLocalError] = useState('')

  const platform = platforms.find(p => p.id === selectedPlatform)

  function handleAdd() {
    if (!selectedPlatform) { setLocalError('Select a platform'); return }
    if (!adapterName.trim()) { setLocalError('Enter an adapter name'); return }
    if (platform && !platform.qrAuth) {
      for (const f of platform.fields) {
        if (!fields[f.key]?.trim()) { setLocalError(`${f.label} is required`); return }
      }
    }
    onAdd(adapterName.trim(), selectedPlatform, fields)
  }

  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 480 }}>
      <h3 style={{ margin: 0 }}>Add IM Adapter</h3>

      {(error || localError) && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error || localError}</div>}

      {/* Platform select */}
      <label style={{ display: 'block' }}>
        <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>Platform</span>
        <select value={selectedPlatform} onChange={e => { setSelectedPlatform(e.target.value); setFields({}); setLocalError('') }} style={{
          width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-bg)', border: '1px solid var(--color-border)',
          color: 'var(--text-primary)', fontSize: 13, outline: 'none',
        }}>
          <option value="">Select platform...</option>
          {platforms.map(p => <option key={p.id} value={p.id}>{p.displayName}</option>)}
        </select>
      </label>

      {/* Adapter name */}
      <label style={{ display: 'block' }}>
        <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>Adapter Name</span>
        <input value={adapterName} onChange={e => setAdapterName(e.target.value)} placeholder="e.g. dingtalk-alerts, telegram-dev" style={{
          width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
          background: 'var(--color-bg)', border: '1px solid var(--color-border)',
          color: 'var(--text-primary)', fontSize: 13, outline: 'none',
        }} />
      </label>

      {/* Platform fields */}
      {platform?.qrAuth && (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 12, fontStyle: 'italic' }}>
          This platform uses QR code authentication. Save to start the pairing process.
        </div>
      )}
      {platform?.fields.map(f => (
        <label key={f.key} style={{ display: 'block' }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{f.label}</span>
          <input
            type={f.secret ? 'password' : 'text'}
            value={fields[f.key] || ''}
            onChange={e => setFields(prev => ({ ...prev, [f.key]: e.target.value }))}
            placeholder={f.placeholder}
            style={{
              width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontSize: 13, outline: 'none',
            }}
          />
        </label>
      ))}

      <div style={{ display: 'flex', gap: 12 }}>
        <button onClick={onCancel} style={{
          flex: 1, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-surface)', border: '1px solid var(--color-border)',
          color: 'var(--text-secondary)', cursor: 'pointer',
        }}>Cancel</button>
        <button onClick={handleAdd} style={{
          flex: 2, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontWeight: 600,
        }}>Add Adapter</button>
      </div>
    </div>
  )
}

// ── Edit Adapter Dialog ──

function EditAdapterDialog({ adapter, platform, fields, setFields, onSave, onCancel, error }: {
  adapter: IMAdapterInfo
  platform?: IMPlatformMeta
  fields: Record<string, string>
  setFields: (f: Record<string, string>) => void
  onSave: () => void
  onCancel: () => void
  error: string
}) {
  return (
    <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 480 }}>
      <h3 style={{ margin: 0 }}>Edit: {adapter.name}</h3>
      <div style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>
        {platform?.displayName || adapter.platform}
      </div>

      {error && <div style={{ color: 'var(--color-error)', fontSize: 12 }}>{error}</div>}

      {platform?.fields.map(f => (
        <label key={f.key} style={{ display: 'block' }}>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{f.label}</span>
          <input
            type={f.secret ? 'password' : 'text'}
            value={fields[f.key] || ''}
            onChange={e => setFields({ ...fields, [f.key]: e.target.value })}
            placeholder={f.placeholder}
            style={{
              width: '100%', height: 36, padding: '0 12px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-bg)', border: '1px solid var(--color-border)',
              color: 'var(--text-primary)', fontSize: 13, outline: 'none',
            }}
          />
        </label>
      ))}

      {!platform?.fields?.length && !platform?.qrAuth && (
        <div style={{ color: 'var(--text-tertiary)', fontSize: 12 }}>
          No configurable fields for this adapter.
        </div>
      )}

      <div style={{ display: 'flex', gap: 12 }}>
        <button onClick={onCancel} style={{
          flex: 1, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-surface)', border: '1px solid var(--color-border)',
          color: 'var(--text-secondary)', cursor: 'pointer',
        }}>Cancel</button>
        <button onClick={onSave} style={{
          flex: 2, height: 36, borderRadius: 'var(--radius-md)',
          background: 'var(--color-primary)', color: '#fff',
          border: 'none', cursor: 'pointer', fontWeight: 600,
        }}>Save</button>
      </div>
    </div>
  )
}

// ── Adapter Row ──

function AdapterRow({ adapter, onReload, onEdit }: {
  adapter: IMAdapterInfo
  onReload: () => void
  onEdit: (name: string, extra: Record<string, string>) => void
}) {
  return (
    <div style={{
      padding: '12px 16px', borderRadius: 'var(--radius-md)',
      background: adapter.isCurrent ? 'var(--color-card)' : 'var(--color-surface)',
      border: `1px solid ${adapter.isCurrent ? 'var(--color-success)' : 'var(--color-border)'}`,
      display: 'flex', alignItems: 'center', gap: 12,
      opacity: adapter.enabled ? 1 : 0.5,
    }}>
      {/* Brand icon */}
      <PlatformIcon platform={adapter.platform} size={24} />

      {/* Info */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--text-primary)', display: 'flex', alignItems: 'center', gap: 6 }}>
          {adapter.name}
          {adapter.isCurrent && <span style={{ fontSize: 10, color: 'var(--color-success)', fontWeight: 400 }}>active</span>}
        </div>
        <div style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
          {adapter.platform}
          {adapter.workspace && !adapter.isCurrent && ` · ${adapter.workspace.split('/').pop()}`}
        </div>
      </div>

      {/* Status badge */}
      <div style={{
        padding: '2px 8px', borderRadius: 'var(--radius-sm)', fontSize: 10, fontWeight: 600,
        background: adapter.enabled ? 'var(--color-success)' : 'var(--color-border)',
        color: adapter.enabled ? '#fff' : 'var(--text-tertiary)',
      }}>
        {adapter.enabled ? 'ON' : 'OFF'}
      </div>

      {/* Actions */}
      <button onClick={async () => {
        try { await App.SetIMAdapterEnabled(adapter.name, !adapter.enabled); onReload() } catch {}
      }} style={{
        padding: '4px 8px', borderRadius: 'var(--radius-sm)',
        border: 'none', cursor: 'pointer', fontSize: 11,
        background: adapter.enabled ? 'var(--color-warning)' : 'var(--color-success)',
        color: '#fff',
      }}>
        {adapter.enabled ? 'Disable' : 'Enable'}
      </button>

      <button onClick={() => {
        const fields: Record<string, string> = {}
        if (adapter.extra) for (const [k, v] of Object.entries(adapter.extra)) fields[k] = String(v)
        onEdit(adapter.name, fields)
      }} style={{
        padding: '4px 10px', borderRadius: 'var(--radius-sm)',
        border: '1px solid var(--color-border)', cursor: 'pointer',
        background: 'var(--color-surface)', color: 'var(--text-secondary)', fontSize: 11,
      }}>
        Edit
      </button>

      <button onClick={async () => {
        try {
          await App.MuteIMAdapter(adapter.name, !adapter.muted)
          onReload()
        } catch {}
      }} style={{
        padding: '4px 8px', borderRadius: 'var(--radius-sm)',
        border: 'none', cursor: 'pointer', fontSize: 11,
        background: adapter.muted ? 'rgba(63,185,80,0.15)' : 'rgba(210,153,34,0.15)',
        color: adapter.muted ? 'var(--color-success)' : '#D29922',
        display: 'flex', alignItems: 'center', gap: 3,
      }}>
        {adapter.muted ? <><Volume2 size={11} /> Unmute</> : <><VolumeX size={11} /> Mute</>}
      </button>

      <button onClick={async () => {
        if (!confirm(`Remove adapter "${adapter.name}"?`)) return
        try { await App.RemoveIMAdapter(adapter.name); onReload() } catch {}
      }} style={{
        padding: '4px 6px', borderRadius: 'var(--radius-sm)',
        border: 'none', cursor: 'pointer', background: 'transparent', color: 'var(--color-error)',
      }}>
        <Trash2 size={14} />
      </button>
    </div>
  )
}
