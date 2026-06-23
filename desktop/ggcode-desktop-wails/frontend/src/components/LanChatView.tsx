import React, { useState, useEffect, useCallback } from 'react'
import { LanChatParticipant, LanChatMessage, LanChatPendingApproval } from '../types'

interface Props {
  onUnreadChange?: (count: number) => void
}

export function LanChatView({ onUnreadChange }: Props) {
  const [messages, setMessages] = useState<LanChatMessage[]>([])
  const [participants, setParticipants] = useState<LanChatParticipant[]>([])
  const [pending, setPending] = useState<LanChatPendingApproval[]>([])
  const [input, setInput] = useState('')
  const [self, setSelf] = useState<LanChatParticipant | null>(null)
  const [nickEdit, setNickEdit] = useState('')
  const [showNickEdit, setShowNickEdit] = useState(false)
  const [unread, setUnread] = useState(0)

  const poll = useCallback(async () => {
    try {
      const msgs = await (window as any).ggcode.LanChatMessages()
      setMessages(msgs || [])
      const parts = await (window as any).ggcode.LanChatParticipants()
      setParticipants(parts || [])
      const pend = await (window as any).ggcode.LanChatPendingApprovals()
      setPending(pend || [])
      const s = await (window as any).ggcode.LanChatSelf()
      setSelf(s)
    } catch {
      // LAN chat not available
    }
  }, [])

  useEffect(() => {
    poll()
    const interval = setInterval(poll, 3000)
    return () => clearInterval(interval)
  }, [poll])

  useEffect(() => {
    onUnreadChange?.(unread)
  }, [unread, onUnreadChange])

  const handleSend = async () => {
    const text = input.trim()
    if (!text) return

    if (text.startsWith('/nick ')) {
      const nick = text.slice(6).trim()
      if (nick) {
        await (window as any).ggcode.LanChatSetNick(nick)
        setSelf(prev => prev ? { ...prev, human_nick: nick, agent_nick: nick + '_agent' } : prev)
      }
      setInput('')
      return
    }

    // Parse @mention
    if (text.startsWith('@')) {
      const spaceIdx = text.indexOf(' ')
      if (spaceIdx > 0) {
        const mention = text.slice(1, spaceIdx)
        const content = text.slice(spaceIdx + 1).trim()
        const target = participants.find(p => p.human_nick === mention || p.agent_nick === mention)
        if (target) {
          const role = mention.endsWith('_agent') ? 'agent' : 'human'
          await (window as any).ggcode.LanChatSend(content, target.node_id, role)
        }
      }
    } else {
      // Broadcast
      await (window as any).ggcode.LanChatSend(text, '', '')
    }
    setInput('')
    setUnread(0)
  }

  const handleApprove = async (id: string) => {
    await (window as any).ggcode.LanChatApprove(id)
    setPending(pending.filter(p => p.message.id !== id))
  }

  const handleReject = async (id: string) => {
    await (window as any).ggcode.LanChatReject(id, 'rejected')
    setPending(pending.filter(p => p.message.id !== id))
  }

  const handleSaveNick = async () => {
    if (nickEdit.trim()) {
      await (window as any).ggcode.LanChatSetNick(nickEdit.trim())
      setSelf(prev => prev ? { ...prev, human_nick: nickEdit.trim(), agent_nick: nickEdit.trim() + '_agent' } : prev)
    }
    setShowNickEdit(false)
  }

  return (
    <div style={{ display: 'flex', height: '100%', background: 'var(--color-bg)' }}>
      {/* Sidebar: participants */}
      <div style={{ width: 200, borderRight: '1px solid var(--color-border)', overflowY: 'auto', padding: 8 }}>
        <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 8, color: 'var(--text-secondary)' }}>
          Online ({participants.filter(p => p.online).length})
        </div>
        {self && (
          <div style={{ marginBottom: 12, padding: 8, background: 'var(--color-surface)', borderRadius: 8 }}>
            <div style={{ fontSize: 13, fontWeight: 600 }}>
              👤 {self.human_nick} <span style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>(you)</span>
            </div>
            <div style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>🤖 {self.agent_nick}</div>
            <button
              onClick={() => { setNickEdit(self.human_nick); setShowNickEdit(!showNickEdit) }}
              style={{ marginTop: 4, fontSize: 11, color: 'var(--color-primary)', cursor: 'pointer', background: 'none', border: 'none' }}
            >
              Edit nickname
            </button>
            {showNickEdit && (
              <div style={{ marginTop: 4, display: 'flex', gap: 4 }}>
                <input
                  value={nickEdit}
                  onChange={e => setNickEdit(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && handleSaveNick()}
                  style={{ fontSize: 12, flex: 1, padding: '2px 4px' }}
                  autoFocus
                />
                <button onClick={handleSaveNick} style={{ fontSize: 11 }}>OK</button>
              </div>
            )}
          </div>
        )}
        {participants.filter(p => p.node_id !== self?.node_id).map(p => (
          <div key={p.node_id} style={{ marginBottom: 6, padding: 6, fontSize: 12 }}>
            <div style={{ fontWeight: 500, color: p.online ? 'var(--text-primary)' : 'var(--text-tertiary)' }}>
              {p.online ? '●' : '○'} 👤 {p.human_nick}
            </div>
            <div style={{ color: 'var(--text-tertiary)' }}>🤖 {p.agent_nick}</div>
          </div>
        ))}
      </div>

      {/* Main: messages + input */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        {/* Messages */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
          {messages.length === 0 && (
            <div style={{ textAlign: 'center', color: 'var(--text-tertiary)', marginTop: 40 }}>
              No messages yet. Start chatting!
            </div>
          )}
          {messages.map(msg => (
            <div
              key={msg.id}
              style={{
                marginBottom: 8,
                display: 'flex',
                justifyContent: msg.from_node_id === self?.node_id ? 'flex-end' : 'flex-start',
              }}
            >
              <div
                style={{
                  maxWidth: '70%',
                  padding: '8px 14px',
                  borderRadius: 12,
                  background: msg.from_node_id === self?.node_id
                    ? 'var(--color-primary)'
                    : msg.from_role === 'agent'
                      ? 'var(--color-surface-elevated)'
                      : 'var(--color-surface)',
                  color: msg.from_node_id === self?.node_id ? '#fff' : 'var(--text-primary)',
                }}
              >
                {msg.from_node_id !== self?.node_id && (
                  <div style={{ fontSize: 11, fontWeight: 600, marginBottom: 2, opacity: 0.8 }}>
                    {msg.from_role === 'agent' ? '🤖' : '👤'} {msg.from_nick}
                    {!msg.to_node_id ? '' : msg.to_role === 'agent' ? ' → agent' : ' → you'}
                  </div>
                )}
                <div style={{ fontSize: 14 }}>{msg.content}</div>
                {msg.attachments && msg.attachments.length > 0 && (
                  <div style={{ marginTop: 4, fontSize: 12, opacity: 0.7 }}>
                    {msg.attachments.map(a => (
                      <div key={a.id}>📎 {a.name} ({Math.round(a.size / 1024)}KB)</div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>

        {/* Pending approvals */}
        {pending.length > 0 && (
          <div style={{ padding: 8, background: 'var(--color-surface)', borderTop: '1px solid var(--color-border)' }}>
            {pending.map(p => (
              <div key={p.message.id} style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: 8, borderRadius: 8,
                background: 'rgba(251, 191, 36, 0.1)', marginBottom: 4,
              }}>
                <div style={{ flex: 1, fontSize: 13 }}>
                  <strong>📨 {p.message.from_nick}</strong> → your agent:
                  <div style={{ marginTop: 2 }}>{p.message.content}</div>
                </div>
                <button
                  onClick={() => handleApprove(p.message.id)}
                  style={{ padding: '4px 12px', background: '#22c55e', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 12 }}
                >
                  Approve
                </button>
                <button
                  onClick={() => handleReject(p.message.id)}
                  style={{ padding: '4px 12px', background: '#ef4444', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontSize: 12 }}
                >
                  Reject
                </button>
              </div>
            ))}
          </div>
        )}

        {/* Input */}
        <div style={{ padding: 12, borderTop: '1px solid var(--color-border)', display: 'flex', gap: 8 }}>
          <input
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleSend()}
            placeholder="Type a message... (@mention for direct, /nick to rename)"
            style={{ flex: 1, padding: '8px 12px', fontSize: 14, borderRadius: 8, border: '1px solid var(--color-border)', background: 'var(--color-surface)' }}
          />
          <button
            onClick={handleSend}
            style={{ padding: '8px 20px', background: 'var(--color-primary)', color: '#fff', border: 'none', borderRadius: 8, cursor: 'pointer', fontSize: 14 }}
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}
