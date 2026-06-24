import React, { useState, useEffect, useCallback, useRef } from 'react'
import * as App from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { LanChatParticipant, LanChatMessage, LanChatPendingApproval } from '../types'

interface Props {
  onUnreadChange?: (count: number) => void
}

/** Target option for the dropdown / @mention — can address human or agent of a node. */
interface TargetOption {
  node_id: string
  label: string       // display label
  nick: string        // the nick to @mention
  to_role: string     // "human" | "agent"
}

/** Build target options from participants — includes both human and agent identities. */
function buildTargets(participants: LanChatParticipant[], selfNodeID: string): TargetOption[] {
  const targets: TargetOption[] = []
  for (const p of participants) {
    if (p.node_id === selfNodeID) continue
    if (p.human_nick) {
      targets.push({ node_id: p.node_id, label: p.human_nick, nick: p.human_nick, to_role: 'human' })
    }
    if (p.agent_nick) {
      targets.push({ node_id: p.node_id, label: `${p.agent_nick} (agent)`, nick: p.agent_nick, to_role: 'agent' })
    }
    // If no nicks at all, show node_id fallback
    if (!p.human_nick && !p.agent_nick) {
      targets.push({ node_id: p.node_id, label: p.node_id.slice(0, 12), nick: '', to_role: 'human' })
    }
  }
  return targets
}

export function LanChatView({ onUnreadChange }: Props) {
  const [messages, setMessages] = useState<LanChatMessage[]>([])
  const [participants, setParticipants] = useState<LanChatParticipant[]>([])
  const [pendingApprovals, setPendingApprovals] = useState<LanChatPendingApproval[]>([])
  const [inputText, setInputText] = useState('')
  const [nick, setNick] = useState('')
  const [selectedTarget, setSelectedTarget] = useState<string>('')  // "nodeID:role" or ""
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const [selfNodeID, setSelfNodeID] = useState('')

  // @mention autocomplete state
  const [mentionList, setMentionList] = useState<TargetOption[]>([])
  const [mentionIndex, setMentionIndex] = useState(0)
  const [mentionQuery, setMentionQuery] = useState('')
  const [showMentions, setShowMentions] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const unreadCount = useRef(0)
  const updateUnread = useCallback(() => {
    onUnreadChange?.(unreadCount.current)
  }, [onUnreadChange])

  const targets = buildTargets(participants, selfNodeID)

  // --- Initial load + event listeners ---
  useEffect(() => {
    let mounted = true

    async function loadAll() {
      try {
        const [msgs, parts, pending, self] = await Promise.all([
          App.LanChatMessages(),
          App.LanChatParticipants(),
          App.LanChatPendingApprovals(),
          App.LanChatSelf(),
        ])
        if (mounted) {
          setMessages((msgs as any) || [])
          setParticipants((parts as any) || [])
          setPendingApprovals((pending as any) || [])
          const s = self as any
          setSelfNodeID(s?.node_id || '')
          setNick(s?.human_nick || s?.agent_nick || '')
        }
      } catch (e) {
        console.error('LAN Chat initial load failed:', e)
      }
    }

    loadAll()

    const refreshParticipants = async () => {
      try {
        const parts = await App.LanChatParticipants()
        if (mounted) setParticipants((parts as any) || [])
      } catch {}
    }

    const offMessage = EventsOn('lanchat:message', (msg: any) => {
      setMessages(prev => [...prev, msg])
      unreadCount.current++
      updateUnread()
      setTimeout(() => {
        messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
      }, 50)
    })

    const offReceipt = EventsOn('lanchat:receipt', (_receipt: any) => {})

    const offAddP = EventsOn('lanchat:participant_added', refreshParticipants)
    const offRemoveP = EventsOn('lanchat:participant_removed', refreshParticipants)

    const offApproval = EventsOn('lanchat:approval_request', async () => {
      try {
        const pending = await App.LanChatPendingApprovals()
        if (mounted) setPendingApprovals((pending as any) || [])
      } catch {}
    })

    // Retry participants load — peers may not be discovered yet at initial load.
    const retry1 = setTimeout(refreshParticipants, 5000)
    const retry2 = setTimeout(refreshParticipants, 10000)

    return () => {
      mounted = false
      clearTimeout(retry1)
      clearTimeout(retry2)
      offMessage()
      offReceipt()
      offAddP()
      offRemoveP()
      offApproval()
    }
  }, [])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  useEffect(() => {
    unreadCount.current = 0
    updateUnread()
  }, [inputText, selectedTarget, updateUnread])

  // --- @mention autocomplete ---
  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value
    setInputText(val)

    // Detect @mention: text starts with @ or has space then @
    const atMatch = val.match(/(?:^|\s)@(\S*)$/)
    if (atMatch) {
      const query = atMatch[1].toLowerCase()
      const matched = targets.filter(t =>
        t.nick && t.nick.toLowerCase().includes(query)
      )
      if (matched.length > 0) {
        setMentionList(matched)
        setMentionQuery(atMatch[0]) // full match including @
        setMentionIndex(0)
        setShowMentions(true)
        return
      }
    }
    setShowMentions(false)
  }

  const selectMention = (target: TargetOption) => {
    // Replace the @query with @nick
    const newText = inputText.replace(
      /(?:^|\s)@(\S*)$/,
      (match, prefix) => {
        const sep = match.startsWith(' ') ? ' ' : ''
        return `${sep}@${target.nick} `
      }
    )
    setInputText(newText)
    setShowMentions(false)
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (showMentions) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setMentionIndex(i => (i + 1) % mentionList.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setMentionIndex(i => (i - 1 + mentionList.length) % mentionList.length)
        return
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault()
        selectMention(mentionList[mentionIndex])
        return
      }
      if (e.key === 'Escape') {
        setShowMentions(false)
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  // --- Actions ---
  const handleSend = useCallback(async () => {
    if (!inputText.trim()) return
    try {
      let nodeID = ''
      let toRole = 'human'

      if (selectedTarget) {
        const [id, role] = selectedTarget.split(':')
        nodeID = id
        toRole = role || 'human'
      }

      // Parse @mention: "@nick message"
      const mentionMatch = inputText.match(/^@(\S+)\s+(.*)/)
      if (mentionMatch) {
        const mentionedNick = mentionMatch[1]
        const content = mentionMatch[2]
        const found = targets.find(t => t.nick === mentionedNick)
        if (found) {
          await App.LanChatSend(content, found.node_id, found.to_role, false)
        } else {
          // Unknown nick, broadcast
          await App.LanChatSend(inputText, '', '', false)
        }
      } else {
        await App.LanChatSend(inputText, nodeID, toRole, false)
      }
      setInputText('')
    } catch (e) {
      console.error('Send failed:', e)
    }
  }, [inputText, selectedTarget, targets])

  const handleApprove = useCallback(async (messageId: string) => {
    try {
      await App.LanChatApprove(messageId)
      setPendingApprovals(prev => prev.filter(p => p.message.id !== messageId))
    } catch (e) {
      console.error('Approve failed:', e)
    }
  }, [])

  const handleReject = useCallback(async (messageId: string, reason: string = '') => {
    try {
      await App.LanChatReject(messageId, reason)
      setPendingApprovals(prev => prev.filter(p => p.message.id !== messageId))
    } catch (e) {
      console.error('Reject failed:', e)
    }
  }, [])

  const handleNickChange = useCallback(async () => {
    const newNick = prompt('Enter new nickname:', nick)
    if (!newNick || newNick === nick) return
    try {
      await App.LanChatSetNick(newNick)
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
          ({targets.length} contacts)
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

      {/* Input area */}
      <div style={{ position: 'relative', padding: '8px 16px', borderTop: '1px solid var(--border-color)', flexShrink: 0 }}>
        {/* @mention dropdown */}
        {showMentions && mentionList.length > 0 && (
          <div style={{
            position: 'absolute',
            bottom: '100%',
            left: '16px',
            right: '16px',
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border-color)',
            borderRadius: '6px',
            boxShadow: '0 -4px 12px rgba(0,0,0,0.15)',
            maxHeight: '200px',
            overflowY: 'auto',
            zIndex: 10,
          }}>
            {mentionList.map((t, i) => (
              <div
                key={`${t.node_id}:${t.to_role}`}
                onClick={() => selectMention(t)}
                style={{
                  padding: '6px 12px',
                  fontSize: '13px',
                  cursor: 'pointer',
                  background: i === mentionIndex ? 'var(--bg-tertiary)' : 'transparent',
                  color: 'var(--text-primary)',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                }}
              >
                <span>{t.label}</span>
                {t.to_role === 'agent' && (
                  <span style={{ fontSize: '11px', color: 'var(--color-primary)' }}>agent</span>
                )}
              </div>
            ))}
          </div>
        )}

        {/* Recipient selector */}
        <div style={{ display: 'flex', gap: '6px', marginBottom: '6px' }}>
          <select
            value={selectedTarget}
            onChange={e => setSelectedTarget(e.target.value)}
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
            <option value="">Broadcast to all</option>
            {targets.map(t => (
              <option key={`${t.node_id}:${t.to_role}`} value={`${t.node_id}:${t.to_role}`}>
                {t.label}
              </option>
            ))}
          </select>
        </div>

        {/* Text input */}
        <div style={{ display: 'flex', gap: '8px' }}>
          <input
            ref={inputRef}
            type="text"
            value={inputText}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            placeholder="Type a message... (@ to mention)"
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
