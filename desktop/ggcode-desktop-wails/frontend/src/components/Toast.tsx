import React, { useEffect } from 'react'
import { CheckCircle2, AlertCircle, Info, X } from 'lucide-react'

export type ToastType = 'success' | 'error' | 'info'

export interface ToastMessage {
  id: number
  type: ToastType
  message: string
}

interface ToastContainerProps {
  toasts: ToastMessage[]
  onDismiss: (id: number) => void
}

const MAX_VISIBLE = 4

const toastConfig: Record<ToastType, {
  icon: React.ReactNode
  color: string
  borderColor: string
  bgColor: string
  progressColor: string
}> = {
  success: {
    icon: <CheckCircle2 size={16} />,
    color: 'var(--color-success)',
    borderColor: 'color-mix(in srgb, var(--color-success) 35%, transparent)',
    bgColor: 'color-mix(in srgb, var(--color-success) 12%, var(--color-card))',
    progressColor: 'var(--color-success)',
  },
  error: {
    icon: <AlertCircle size={16} />,
    color: 'var(--color-error)',
    borderColor: 'color-mix(in srgb, var(--color-error) 35%, transparent)',
    bgColor: 'color-mix(in srgb, var(--color-error) 12%, var(--color-card))',
    progressColor: 'var(--color-error)',
  },
  info: {
    icon: <Info size={16} />,
    color: 'var(--color-info)',
    borderColor: 'color-mix(in srgb, var(--color-info) 35%, transparent)',
    bgColor: 'color-mix(in srgb, var(--color-info) 12%, var(--color-card))',
    progressColor: 'var(--color-info)',
  },
}

function ToastItem({ toast, onDismiss }: { toast: ToastMessage; onDismiss: (id: number) => void }) {
  const duration = toast.type === 'error' ? 6000 : 3500
  const config = toastConfig[toast.type]

  useEffect(() => {
    const id = window.setTimeout(() => onDismiss(toast.id), duration)
    return () => window.clearTimeout(id)
  }, [toast.id, duration, onDismiss])

  return (
    <div
      role="status"
      aria-live="polite"
      className="toast-enter"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        maxWidth: 420,
        minWidth: 280,
        padding: '10px 12px',
        borderRadius: 'var(--radius-md)',
        border: `1px solid ${config.borderColor}`,
        background: config.bgColor,
        backdropFilter: 'blur(8px)',
        boxShadow: '0 8px 24px rgba(0, 0, 0, 0.3)',
        fontSize: 13,
        lineHeight: 1.45,
        color: 'var(--text-primary)',
        overflow: 'hidden',
        position: 'relative',
      }}
    >
      <span style={{ color: config.color, display: 'flex', flexShrink: 0 }}>{config.icon}</span>
      <span style={{ flex: 1 }}>{toast.message}</span>
      <button
        type="button"
        aria-label="Dismiss notification"
        onClick={() => onDismiss(toast.id)}
        style={{
          width: 22,
          height: 22,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: 'none',
          borderRadius: '50%',
          background: 'rgba(255, 255, 255, 0.08)',
          color: 'var(--text-tertiary)',
          cursor: 'pointer',
          flexShrink: 0,
        }}
      >
        <X size={13} />
      </button>
      {/* Auto-dismiss progress bar */}
      <div
        style={{
          position: 'absolute',
          bottom: 0,
          left: 0,
          right: 0,
          height: 2,
          background: 'transparent',
        }}
      >
        <div
          className="toast-progress"
          style={{
            height: '100%',
            background: config.progressColor,
            opacity: 0.6,
            animationName: 'toastProgressShrink',
            animationDuration: `${duration}ms`,
            animationTimingFunction: 'linear',
            animationFillMode: 'forwards',
          }}
        />
      </div>
    </div>
  )
}

export function ToastContainer({ toasts, onDismiss }: ToastContainerProps) {
  if (toasts.length === 0) return null
  const visible = toasts.slice(-MAX_VISIBLE)

  return (
    <div
      style={{
        position: 'fixed',
        right: 20,
        bottom: 44,
        zIndex: 10000,
        display: 'flex',
        flexDirection: 'column-reverse',
        gap: 8,
        pointerEvents: 'none',
      }}
    >
      {visible.map(toast => (
        <div key={toast.id} style={{ pointerEvents: 'auto' }}>
          <ToastItem toast={toast} onDismiss={onDismiss} />
        </div>
      ))}
    </div>
  )
}

/** Backward-compatible single-toast wrapper (deprecated, use ToastContainer) */
export function Toast({ toast, onClose }: { toast: ToastMessage | null; onClose: () => void }) {
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(onClose, toast.type === 'error' ? 6000 : 3500)
    return () => window.clearTimeout(id)
  }, [toast, onClose])

  if (!toast) return null

  return (
    <ToastContainer
      toasts={[toast]}
      onDismiss={() => onClose()}
    />
  )
}
