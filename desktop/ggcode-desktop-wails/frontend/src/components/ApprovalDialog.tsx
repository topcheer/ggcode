import React, { useState } from 'react'
import { ShieldAlert, XCircle, CheckCircle2, ShieldCheck } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

export interface ApprovalRequest {
  requestId: string
  toolName: string
  input: string
}

interface ApprovalDialogProps {
  request: ApprovalRequest
  onClose: () => void
}

function formatInput(input: string): string {
  try {
    const parsed = JSON.parse(input)
    return JSON.stringify(parsed, null, 2)
  } catch {
    return input
  }
}

export function ApprovalDialog({ request, onClose }: ApprovalDialogProps) {
  const [responding, setResponding] = useState(false)

  const handleRespond = async (decision: 'deny' | 'allow' | 'always_allow') => {
    if (responding) return
    setResponding(true)
    try {
      await App.RespondApproval(request.requestId, decision)
    } catch (e) {
      console.error('Approval response error:', e)
    }
    onClose()
  }

  const formattedInput = formatInput(request.input)

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
        width: 560,
        maxWidth: '90vw',
        maxHeight: '80vh',
        boxShadow: '0 20px 60px rgba(0,0,0,0.4)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '16px 20px',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <ShieldAlert size={20} style={{ color: 'var(--color-warning)', flexShrink: 0 }} />
          <span style={{ fontWeight: 700, fontSize: 15, color: 'var(--text-primary)' }}>
            Tool Approval Required
          </span>
        </div>

        {/* Tool name */}
        <div style={{ padding: '12px 20px 0' }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '4px 10px', borderRadius: 'var(--radius-md)',
            background: 'rgba(99,102,241,0.15)', color: '#818cf8',
            fontWeight: 600, fontSize: 13, fontFamily: 'var(--font-mono)',
          }}>
            {request.toolName}
          </div>
        </div>

        {/* Input display */}
        <div style={{
          flex: 1, minHeight: 0, margin: '12px 20px',
          padding: 12, borderRadius: 'var(--radius-md)',
          background: 'var(--color-card)', border: '1px solid var(--color-border)',
          overflow: 'auto', maxHeight: 320,
        }}>
          <pre style={{
            margin: 0, fontSize: 12, fontFamily: 'var(--font-mono)',
            color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
            wordBreak: 'break-word', lineHeight: 1.5,
          }}>
            {formattedInput}
          </pre>
        </div>

        {/* Buttons */}
        <div style={{
          display: 'flex', gap: 10, justifyContent: 'flex-end',
          padding: '0 20px 16px',
        }}>
          <button
            onClick={() => handleRespond('deny')}
            disabled={responding}
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: 'rgba(220,38,38,0.15)', color: '#f87171',
              border: '1px solid rgba(220,38,38,0.3)',
              cursor: responding ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 13,
              display: 'flex', alignItems: 'center', gap: 6,
              opacity: responding ? 0.5 : 1,
            }}
          >
            <XCircle size={15} /> Deny
          </button>
          <button
            onClick={() => handleRespond('allow')}
            disabled={responding}
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-primary)', color: '#fff',
              border: 'none',
              cursor: responding ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 13,
              display: 'flex', alignItems: 'center', gap: 6,
              opacity: responding ? 0.5 : 1,
            }}
          >
            <CheckCircle2 size={15} /> Allow
          </button>
          <button
            onClick={() => handleRespond('always_allow')}
            disabled={responding}
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: 'rgba(34,197,94,0.15)', color: '#4ade80',
              border: '1px solid rgba(34,197,94,0.3)',
              cursor: responding ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 13,
              display: 'flex', alignItems: 'center', gap: 6,
              opacity: responding ? 0.5 : 1,
            }}
          >
            <ShieldCheck size={15} /> Always Allow
          </button>
        </div>
      </div>
    </div>
  )
}
