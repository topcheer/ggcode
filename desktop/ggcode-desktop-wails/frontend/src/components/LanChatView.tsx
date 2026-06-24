import React, { useState, useEffect, useCallback, useRef } from 'react'
import { LanChatParticipant, LanChatMessage, LanChatPendingApproval } from '../types'
import { EventsOn } from '../../wailsjs/runtime/runtime'

interface Props {
  onUnreadChange?: (count: number) => void
}

export function LanChatView({ onUnreadChange }: Props) {
  const [messages, setMessages] = useState<LanChatMessage[]>([])
  const [participants, setParticipants] = useState<LanChatParticipant[]>([])
  const [pendingApprovals, setPendingApprovals] = useState<LanChatPendingApproval[]>([])
  const [inputText, setInputText] = useState('')
  const [nick, setNick] = useState('')
  const [selectedParticipant, setSelectedParticipant] = useState<string>('')
  const [sendAsAgent, setSendAsAgent] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const [hasInitiallyLoaded, setHasInitiallyLoaded] = useState(false)
  const [selfNodeID, setSelfNodeID] = useState('')

  // Track unread count for the NavRail badge.
  const unreadCount = useRef(0)
  const updateUnread = useCallback(() => {
    onUnreadChange?.(unreadCount.current)
  }, [onUnreadChange])

  // --- Initial load ---
  useEffect(() => {
    let mounted = true

    async function loadAll() {
      try {
        const msgs = await (window as any).LanChatMessages()
        const parts = await (window as any).LanChatParticipants()
        const pending = await (window as any).LanChatPendingApprovals()
        const myNick = await (window as any).LanChatNick()
        const self = await (window as any).LanChatSelf()
        if (mounted) {
          setMessages(msgs || [])
          setParticipants(parts || [])
          setPendingApprovals(pending || [])
          setNick(myNick || '')
          setSelfNodeID(self?.node_id || '')
          setHasInitiallyLoaded(true)
        }
      } catch (e) {
        console.error('LAN Chat initial load failed:', e)
      }
    }

    loadAll()

    return () => { mounted = false }
  }, [])

  // --- Real-time event listeners ---
  useEffect(() => {
    if (!hasInitiallyLoaded) return

    // Listen for new messages
    const offMessage = EventsOn('lanchat:message', (_msg: any) => {
      setMessages(prev => [...prev, _msg])
      unreadCount.current++
      updateUnread()
      setTimeout(() => {
        messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
      }, 50)
    })

    // Listen for receipt status updates
    const offReceipt = EventsOn('lanchat:receipt', (receipt: any) => {
      // Update message status in-place if we track it
      console.log('[LAN Chat] receipt:', receipt)
    })

    // Listen for participant changes
    const offAddParticipant = EventsOn('lanchat:participant_added', async () => {
      // Refresh participants from backend
      try {
        const parts = await (window as any).LanChatParticipants()
        setParticipants(parts || [])
      } catch {}
    })

    const offRemoveParticipant = EventsOn('lanchat:participant_removed', async () => {
      try {
        const parts = await (window as any).LanChatParticipants()
        setParticipants(parts || [])
      } catch {}
    })

    // Listen for approval requests (most important for agent interaction)
    const offApproval = EventsOn('lanchat:approval_request', async () => {
      try {
        const pending = await (window as any).LanChatPendingApprovals()
        setPendingApprovals(pending || [])
      } catch {}
    })

    return () => {
      offMessage()
      offReceipt()
      offAddParticipant()
      offRemoveParticipant()
      offApproval()
    }
  }, [hasInitiallyLoaded, updateUnread])

  // Scroll to bottom when messages change
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Reset unread when user interacts
  useEffect(() => {
    unreadCount.current = 0
    updateUnread()
  }, [inputText, selectedParticipant, updateUnread])

  // --- Actions ---
  const handleSend = useCallback(async () => {
    if (!inputText.trim()) return
    try {
      let target = selectedParticipant
      let toRole = 'human'

      // Parse @mention: "@nick message" sends DM to that nick's agent
      const mentionMatch = inputText.match(/^@(\S+)\s+(.*)/)
      if (mentionMatch) {
        const mentionedNick = mentionMatch[1]
        const content = mentionMatch[2]
        const found = participants.find(p =>
          p.human_nick === mentionedNick || p.agent_nick === mentionedNick
        )
        if (found) {
          target = found.node_id
          toRole = mentionedNick === found.agent_nick ? 'agent' : 'human'
          await (window as any).LanChatSend(content, target, toRole, sendAsAgent)
        } else {
          await (window as any).LanChatSend(inputText, '', '', sendAsAgent)
        }
      } else {
        await (window as any).LanChatSend(inputText, target, toRole, sendAsAgent)
      }
      setInputText('')
    } catch (e) {
      console.error('Send failed:', e)
    }
  }, [inputText, selectedParticipant, participants, sendAsAgent])

  const handleApprove = useCallback(async (messageId: string) => {
    try {
      await (window as any).LanChatApprove(messageId)
      setPendingApprovals(prev => prev.filter(p => p.message.id !== messageId))
    } catch (e) {
      console.error('Approve failed:', e)
    }
  }, [])

  const handleReject = useCallback(async (messageId: string, reason: string = '') => {
    try {
      await (window as any).LanChatReject(messageId, reason)
      setPendingApprovals(prev => prev.filter(p => p.message.id !== messageId))
    } catch (e) {
      console.error('Reject failed:', e)
    }
  }, [])

  const handleNickChange = useCallback(async () => {
    const newNick = prompt('Enter new nickname:', nick)
    if (!newNick || newNick === nick) return
    try {
      await (window as any).LanChatSetNick(newNick)
      setNick(newNick)
    } catch (e) {
      console.error('Nick change failed:', e)
    }
  }, [nick])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 16px', borderBottom: '1px solid var(--border-color)', flexShrink: 0 }}>
        <span style={{ fontSize: '15px', fontWeight: 600, color: 'var(--text-primary)' }}>LAN Chat</span>
        <button
          onClick={handleNickChange}
          style={{
            padding: '2px 8px',
            fontSize: '12px',
            border: '1px solid var(--border-color)',
            borderRadius: '4px',
            background: 'var(--bg-secondary)',
            color: 'var(--text-secondary)',
            cursor: 'pointer'
          }}
        >
          {nick || 'unnamed'}
        </button>
        <span style={{ fontSize: '12px', color: 'var(--text-tertiary)' }}>
          ({participants.filter(p => p.online).length} online)
        </span>
      </div>

      {/* Approval requests */}
      {pendingApprovals.length > 0 && (
        <div style={{ borderBottom: '1px solid var(--border-color)', padding: '8px 16px', flexShrink: 0 }}>
          {pendingApprovals.map(p => (
            <div
              key={p.message.id}
              style={{
                padding: '8px 12px',
                marginBottom: '4px',
                borderRadius: '6px',
                background: 'var(--bg-tertiary)',
                border: '1px solid var(--border-color)',
                fontSize: '13px'
              }}
            >
              <div style={{ marginBottom: '4px' }}>
                <span style={{ fontWeight: 600, color: 'var(--color-primary)' }}>@agent</span>
                <span style={{ color: 'var(--text-secondary)' }}> request from </span>
                <span style={{ fontWeight: 500 }}>{p.message.from_nick}</span>
              </div>
              <div style={{ color: 'var(--text-secondary)', marginBottom: '6px' }}>
                {p.message.content}
              </div>
              <div style={{ display: 'flex', gap: '8px' }}>
                <button
                  onClick={() => handleApprove(p.message.id)}
                  style={{ padding: '4px 12px', fontSize: '12px', border: 'none', borderRadius: '4px', background: 'var(--color-primary)', color: '#fff', cursor: 'pointer' }}
                >
                  Approve
                </button>
                <button
                  onClick={() => handleReject(p.message.id)}
                  style={{ padding: '4px 12px', fontSize: '12px', border: '1px solid var(--border-color)', borderRadius: '4px', background: 'transparent', color: 'var(--text-secondary)', cursor: 'pointer' }}
                >
                  Reject
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Messages */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '8px 16px', minHeight: 0 }}>
        {messages.length === 0 ? (
          <div style={{ textAlign: 'center', color: 'var(--text-tertiary)', padding: '40px 0', fontSize: '13px' }}>
            No messages yet. Start a conversation with other ggcode users on your network.
          </div>
        ) : (
          messages.map((msg, i) => {
            const isSelf = msg.from_node_id === selfNodeID
            const fromNick = msg.from_nick || 'unknown'
            return (
              <div
                key={msg.id || i}
                style={{
                  marginBottom: '8px',
                  display: 'flex',
                  flexDirection: isSelf ? 'row-reverse' : 'row'
                }}
              >
                <div style={{
                  maxWidth: '70%',
                  padding: '6px 12px',
                  borderRadius: '8px',
                  background: isSelf ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                  color: isSelf ? '#fff' : 'var(--text-primary)',
                  fontSize: '13px',
                  lineHeight: '1.4'
                }}>
                  {!isSelf && (
                    <div style={{ fontSize: '11px', fontWeight: 600, color: 'var(--text-tertiary)', marginBottom: '2px' }}>
                      {fromNick} {msg.from_role === 'agent' && <span style={{ color: 'var(--color-primary)' }}>agent</span>}
                    </div>
                  )}
                  {msg.content}
                </div>
              </div>
            )
          })
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Input */}
      <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-color)', flexShrink: 0 }}>
        <div style={{ display: 'flex', gap: '6px', marginBottom: '6px' }}>
          <select
            value={selectedParticipant}
            onChange={e => setSelectedParticipant(e.target.value)}
            style={{
              flex: 1,
              padding: '4px 8px',
              fontSize: '12px',
              border: '1px solid var(--border-color)',
              borderRadius: '4px',
              background: 'var(--bg-secondary)',
              color: 'var(--text-primary)'
            }}
          >
            <option value="">Broadcast</option>
            {participants.filter(p => p.node_id !== selfNodeID).map(p => (
              <option key={p.node_id} value={p.node_id}>
                {p.human_nick || p.agent_nick || p.node_id.slice(0, 12)}
              </option>
            ))}
          </select>
          <label style={{
            display: 'flex',
            alignItems: 'center',
            gap: '3px',
            fontSize: '12px',
            color: 'var(--text-secondary)',
            cursor: 'pointer',
            whiteSpace: 'nowrap'
          }}>
            <input
              type="checkbox"
              checked={sendAsAgent}
              onChange={e => setSendAsAgent(e.target.checked)}
              style={{ cursor: 'pointer' }}
            />
            as agent
          </label>
        </div>
        <div style={{ display: 'flex', gap: '8px' }}>
          <input
            type="text"
            value={inputText}
            onChange={e => setInputText(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                handleSend()
              }
            }}
            placeholder="Type a message... (@nick for DM)"
            style={{
              flex: 1,
              padding: '6px 10px',
              fontSize: '13px',
              border: '1px solid var(--border-color)',
              borderRadius: '6px',
              background: 'var(--bg-secondary)',
              color: 'var(--text-primary)',
              outline: 'none'
            }}
          />
          <button
            onClick={handleSend}
            style={{
              padding: '6px 16px',
              fontSize: '13px',
              border: 'none',
              borderRadius: '6px',
              background: 'var(--color-primary)',
              color: '#fff',
              cursor: 'pointer'
            }}
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}
