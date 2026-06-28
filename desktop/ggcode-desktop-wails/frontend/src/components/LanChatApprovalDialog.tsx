import React, { useState, useEffect } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import * as App from '../../wailsjs/go/main/App'
import type { LanChatPendingApproval } from '../types'

function safeMarkdown(text: string): string {
  // Minimal markdown safety — reuses the same marked() pipeline as ChatView
  try {
    // @ts-ignore - marked is loaded globally
    return window.marked ? window.marked.parse(text) : text
  } catch {
    return text
  }
}

export function LanChatApprovalDialog() {
  const [approvals, setApprovals] = useState<LanChatPendingApproval[]>([])

  useEffect(() => {
    let mounted = true
    const refresh = async () => {
      try {
        const pending = await App.LanChatPendingApprovals() as any
        if (mounted && pending) setApprovals(pending)
      } catch {}
    }
    refresh()
    const off = EventsOn('lanchat:approval_request', refresh)
    const offCancel = EventsOn('lanchat:approval_cancelled', refresh)
    return () => { mounted = false; off(); offCancel() }
  }, [])

  // Nothing pending — render nothing
  if (approvals.length === 0) return null

  const removeOne = (id: string) => {
    setApprovals(prev => prev.filter(p => p.message.id !== id))
  }

  const handleApprove = async (id: string) => {
    try { await App.LanChatApprove(id) } catch {}
    removeOne(id)
  }

  const handleAlwaysApprove = async (fromNick: string, id: string) => {
    try {
      await App.LanChatSetApprovalPolicy(fromNick, 'always')
      await App.LanChatApprove(id)
    } catch {}
    removeOne(id)
  }

  const handleReject = async (id: string) => {
    try { await App.LanChatReject(id, '') } catch {}
    removeOne(id)
  }

  return (
    <div style={{
      position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
      background: 'rgba(0,0,0,0.4)', zIndex: 9998,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
    }}>
      <div style={{
        background: 'var(--bg-secondary)',
        borderRadius: '12px',
        padding: '20px 24px',
        maxWidth: '600px', width: '90%', maxHeight: '80vh',
        overflowY: 'auto',
        border: '1px solid var(--border-color)',
        boxShadow: '0 8px 32px rgba(0,0,0,0.3)',
      }}>
        {approvals.map((p, idx) => (
          <div key={p.message.id} style={{
            paddingBottom: idx < approvals.length - 1 ? '16px' : 0,
            marginBottom: idx < approvals.length - 1 ? '16px' : 0,
            borderBottom: idx < approvals.length - 1 ? '1px solid var(--border-color)' : 'none',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '12px' }}>
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--color-primary)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/>
              </svg>
              <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>
                LAN Chat Agent Request
              </span>
              <span style={{ fontSize: '13px', color: 'var(--text-secondary)' }}>
                from <span style={{ fontWeight: 500 }}>{p.message.from_nick}</span>
              </span>
            </div>

            <div className="markdown-body" style={{
              fontSize: '13px', lineHeight: '1.5',
              padding: '12px 16px', borderRadius: '8px',
              background: 'var(--bg-tertiary)',
              marginBottom: '14px',
              maxHeight: '300px', overflowY: 'auto',
            }} dangerouslySetInnerHTML={{ __html: safeMarkdown(p.message.content) }} />

            <div style={{ display: 'flex', gap: '8px' }}>
              <button
                onClick={() => handleApprove(p.message.id)}
                style={{
                  padding: '6px 16px', fontSize: '13px', fontWeight: 500,
                  border: 'none', borderRadius: '6px',
                  background: 'var(--color-primary)', color: '#fff', cursor: 'pointer',
                }}
              >
                Approve
              </button>
              <button
                onClick={() => handleAlwaysApprove(p.message.from_nick, p.message.id)}
                style={{
                  padding: '6px 16px', fontSize: '13px', fontWeight: 500,
                  border: 'none', borderRadius: '6px',
                  background: '#2f855a', color: '#fff', cursor: 'pointer',
                }}
              >
                Always Approve
              </button>
              <button
                onClick={() => handleReject(p.message.id)}
                style={{
                  padding: '6px 16px', fontSize: '13px', fontWeight: 500,
                  border: '1px solid var(--border-color)', borderRadius: '6px',
                  background: 'transparent', color: 'var(--text-secondary)', cursor: 'pointer',
                }}
              >
                Reject
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
