import React, { useEffect } from 'react'
import { X } from 'lucide-react'

export type ToastType = 'success' | 'error' | 'info'

export interface ToastMessage {
  id: number
  type: ToastType
  message: string
}

interface ToastProps {
  toast: ToastMessage | null
  onClose: () => void
}

const toastColors: Record<ToastType, { border: string; background: string; color: string }> = {
  success: {
    border: 'rgba(34, 197, 94, 0.35)',
    background: 'rgba(22, 101, 52, 0.92)',
    color: '#dcfce7',
  },
  error: {
    border: 'rgba(239, 68, 68, 0.35)',
    background: 'rgba(127, 29, 29, 0.94)',
    color: '#fee2e2',
  },
  info: {
    border: 'rgba(59, 130, 246, 0.35)',
    background: 'rgba(30, 64, 175, 0.92)',
    color: '#dbeafe',
  },
}

export function Toast({ toast, onClose }: ToastProps) {
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(onClose, toast.type === 'error' ? 6000 : 3500)
    return () => window.clearTimeout(id)
  }, [toast, onClose])

  if (!toast) return null

  const colors = toastColors[toast.type]
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        position: 'fixed',
        right: 20,
        bottom: 44,
        zIndex: 10000,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        maxWidth: 420,
        padding: '10px 12px',
        borderRadius: 'var(--radius-md)',
        border: `1px solid ${colors.border}`,
        background: colors.background,
        color: colors.color,
        boxShadow: '0 12px 32px rgba(0, 0, 0, 0.35)',
        fontSize: 13,
        lineHeight: 1.45,
      }}
    >
      <span style={{ flex: 1 }}>{toast.message}</span>
      <button
        type="button"
        aria-label="Dismiss notification"
        onClick={onClose}
        style={{
          width: 22,
          height: 22,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: 'none',
          borderRadius: '50%',
          background: 'rgba(255, 255, 255, 0.12)',
          color: 'inherit',
          cursor: 'pointer',
          flexShrink: 0,
        }}
      >
        <X size={13} />
      </button>
    </div>
  )
}
