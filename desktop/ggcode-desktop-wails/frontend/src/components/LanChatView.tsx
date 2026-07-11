import React, { useState, useEffect, useCallback, useRef } from 'react'
import * as App from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { marked } from 'marked'
import { LanChatParticipant, LanChatMessage } from '../types'
import { useTranslation } from '../i18n'

marked.setOptions({ gfm: true, breaks: true })

export function renderMarkdown(text: string): string {
  if (!text) return ''
  try {
    return marked.parse(text) as string
  } catch {
    return text.replace(/</g, '&lt;').replace(/>/g, '&gt;')
  }
}

interface Props {
  onUnreadChange?: (count: number) => void
}

// --- Room types ---
// Room key: "broadcast" | "dm:<nodeID>:<role>"
interface ChatRoom {
  messages: LanChatMessage[]
  unread: number
}

export interface ContactEntry {
  node_id: string
  label: string        // human_nick or agent_nick
  nick: string         // for @mention
  to_role: string      // "human" | "agent"
  team: string         // team name for grouping
  workspace?: string   // e.g. "/Volumes/new/ggai/mdns"
  project_name?: string // e.g. "mdns"
  languages?: string[]
}

export function roomKeyForDM(nodeID: string, role: string): string {
  return `dm:${nodeID}:${role}`
}

/** Parse a room key into { nodeID, role } or null for broadcast. */
export function parseRoomKey(key: string): { nodeID: string; role: string } | null {
  if (key === 'broadcast') return null
  const parts = key.split(':') // ["dm", nodeID, role] but nodeID may contain ':'
  if (parts.length < 3) return null
  // nodeID is everything between first and last ':'
  const role = parts[parts.length - 1]
  const nodeID = parts.slice(1, -1).join(':')
  return { nodeID, role }
}

/** Determine which room a message belongs to. */
export function roomKeyForMessage(msg: LanChatMessage, selfNodeID: string): string {
  // DM to me
  if (msg.to_node_id === selfNodeID && msg.to_node_id !== '') {
    return roomKeyForDM(msg.from_node_id, msg.from_role)
  }
  // DM from me to someone
  if (msg.from_node_id === selfNodeID && msg.to_node_id !== '') {
    // Need the recipient's role — but from_role is "human" (me)
    // The to_role tells us which role of the recipient
    return roomKeyForDM(msg.to_node_id, msg.to_role)
  }
  // Broadcast
  return 'broadcast'
}

/** Build contact entries from participants. */
export function buildContacts(participants: LanChatParticipant[], selfNodeID: string): ContactEntry[] {
  const contacts: ContactEntry[] = []
  const seen = new Set<string>() // dedup by `${nick}:${role}`
  for (const p of participants) {
    if (p.node_id === selfNodeID) continue
    const team = p.team || 'dev-team'
    if (p.human_nick) {
      const key = `${p.human_nick}:human`
      if (!seen.has(key)) {
        seen.add(key)
        contacts.push({ node_id: p.node_id, label: p.human_nick, nick: p.human_nick, to_role: 'human', team, workspace: p.workspace, project_name: p.project_name, languages: p.languages })
      }
    }
    if (p.agent_nick) {
      const key = `${p.agent_nick}:agent`
      if (!seen.has(key)) {
        seen.add(key)
        contacts.push({ node_id: p.node_id, label: `${p.agent_nick}`, nick: p.agent_nick, to_role: 'agent', team, workspace: p.workspace, project_name: p.project_name, languages: p.languages })
      }
    }
    if (!p.human_nick && !p.agent_nick) {
      contacts.push({ node_id: p.node_id, label: p.node_id.slice(0, 12), nick: '', to_role: 'human', team, workspace: p.workspace, project_name: p.project_name })
    }
  }
  // Sort alphabetically by label
  contacts.sort((a, b) => a.label.localeCompare(b.label))
  return contacts
}

export function LanChatView({ onUnreadChange }: Props) {
  const { t } = useTranslation()
  const [participants, setParticipants] = useState<LanChatParticipant[]>([])
  const [nick, setNick] = useState('')
  const [selfNodeID, setSelfNodeID] = useState('')
  const [selfTeam, setSelfTeam] = useState('')
  const [toggledTeams, setToggledTeams] = useState<Set<string>>(new Set())

  // Room state: all messages stored per-room
  const [rooms, setRooms] = useState<Map<string, ChatRoom>>(new Map([['broadcast', { messages: [], unread: 0 }]]))
  const [activeRoom, setActiveRoom] = useState('broadcast')

  const [inputText, setInputText] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Refs to avoid stale closures in event handlers
  const selfNodeIDRef = useRef('')
  const activeRoomRef = useRef('broadcast')
  selfNodeIDRef.current = selfNodeID
  activeRoomRef.current = activeRoom

  const contacts = buildContacts(participants, selfNodeID)

  // --- Helpers to manipulate rooms ---
  const addMessageToRoom = useCallback((roomKey: string, msg: LanChatMessage, isActive: boolean) => {
    setRooms(prev => {
      const next = new Map(prev)
      const room = next.get(roomKey) || { messages: [], unread: 0 }
      // Dedup: skip if message already present (race between initial load and event)
      if (room.messages.some(m => m.id === msg.id)) {
        return prev
      }
      const updated: ChatRoom = {
        messages: [...room.messages, msg],
        unread: isActive ? 0 : room.unread + 1,
      }
      next.set(roomKey, updated)
      return next
    })
  }, [])

  const totalUnread = Array.from(rooms.values()).reduce((sum, r) => sum + r.unread, 0)

  const updateUnread = useCallback(() => {
    onUnreadChange?.(totalUnread)
  }, [onUnreadChange, totalUnread])

  // --- Initial load + event listeners ---
  useEffect(() => {
    let mounted = true

    async function loadAll() {
      try {
        const [msgs, parts, self] = await Promise.all([
          App.LanChatMessages(),
          App.LanChatParticipants(),
          App.LanChatSelf(),
        ])
        if (mounted) {
          setParticipants((parts as any) || [])
          const s = self as any
          const myID = s?.node_id || ''
          setSelfNodeID(myID)
          setNick(s?.human_nick || s?.agent_nick || '')
          setSelfTeam(s?.team || 'dev-team')

          // Distribute initial messages into rooms
          const msgArr = (msgs as any) || []
          const roomMap = new Map<string, ChatRoom>([['broadcast', { messages: [], unread: 0 }]])
          for (const msg of msgArr) {
            const key = roomKeyForMessage(msg, myID)
            const room = roomMap.get(key) || { messages: [], unread: 0 }
            room.messages.push(msg)
            roomMap.set(key, room)
          }
          setRooms(roomMap)
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
      const myID = selfNodeIDRef.current
      const currentRoom = activeRoomRef.current
      // Determine target room using refs (not stale state)
      let roomKey = 'broadcast'
      if (msg.to_node_id && msg.to_node_id === myID) {
        roomKey = roomKeyForDM(msg.from_node_id, msg.from_role)
      } else if (msg.from_node_id === myID && msg.to_node_id) {
        roomKey = roomKeyForDM(msg.to_node_id, msg.to_role)
      }
      const isActive = roomKey === currentRoom
      addMessageToRoom(roomKey, msg as LanChatMessage, isActive)
      setTimeout(() => {
        messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
      }, 50)
    })

    const offReceipt = EventsOn('lanchat:receipt', (r: any) => {
      const labels: Record<string, string> = {
        'delivered': 'delivered',
        'pending': 'pending approval',
        'approved': 'approved',
        'processing': 'agent running',
        'completed': 'completed',
        'rejected': `rejected${r.reason ? ': ' + r.reason : ''}`,
      }
      const label = labels[r.status] || r.status
      // Use the receipt's from_nick and from_role so the UI shows who
      // acknowledged the message instead of "unknown".
      const fromNick = r.from_nick || 'peer'
      const fromRole = r.from_role || 'human'
      const sysMsg: LanChatMessage = {
        id: `receipt-${r.message_id}-${Date.now()}`,
        from_node_id: r.from_node_id || 'system',
        from_role: fromRole,
        from_nick: fromNick,
        to_node_id: '',
        to_role: '',
        content: `[${label}]`,
        timestamp: Date.now(),
      }
      // Route receipt to the correct DM room based on FromNodeID (the remote peer
      // that reported the receipt). If the original message was a broadcast, route
      // to the broadcast room.
      const myID = selfNodeIDRef.current
      let roomKey = 'broadcast'
      if (r.from_node_id && r.from_node_id !== myID) {
        // Receipt from a remote peer — this is a DM acknowledgement.
        // Use to_role if available (newer receipts), otherwise default to 'human'.
        const role = r.to_role || 'human'
        roomKey = roomKeyForDM(r.from_node_id, role)
      }
      addMessageToRoom(roomKey, sysMsg, activeRoomRef.current === roomKey)
    })

    const offAddP = EventsOn('lanchat:participant_added', refreshParticipants)
    const offRemoveP = EventsOn('lanchat:participant_removed', refreshParticipants)

    const offNickChange = EventsOn('lanchat:nick_change', (d: any) => {
      const oldNick = d.old_nick || '(unknown)'
      const newNick = d.new_nick || '(unknown)'
      const sysMsg: LanChatMessage = {
        id: `nick-${d.node_id}-${Date.now()}`,
        from_node_id: 'system',
        from_role: 'system',
        from_nick: '',
        to_node_id: '',
        to_role: '',
        content: `${oldNick} is now known as ${newNick}`,
        timestamp: Date.now(),
      }
      addMessageToRoom('broadcast', sysMsg, activeRoomRef.current === 'broadcast')
      refreshParticipants()
    })

    // Refresh self identity when session changes (nick/role/team may differ per session)
    const offIdentityUpdate = EventsOn('lanchat:identity_updated', async () => {
      try {
        const [s, parts] = await Promise.all([
          App.LanChatSelf(),
          App.LanChatParticipants(),
        ])
        if (mounted) {
          const self = s as any
          const id = self?.node_id || ''
          setSelfNodeID(id)
          setNick(self?.human_nick || self?.agent_nick || '')
          setSelfTeam(self?.team || 'dev-team')
          setParticipants((parts as any) || [])
        }
      } catch {}
    })

    // Retry participants load
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
      offNickChange()
      offIdentityUpdate()
    }
  }, [])

  // Track totalUnread for NavRail badge
  useEffect(() => {
    onUnreadChange?.(totalUnread)
  }, [totalUnread, onUnreadChange])

  // Clear unread when switching to a room
  const switchRoom = useCallback((key: string) => {
    setActiveRoom(key)
    setRooms(prev => {
      const next = new Map(prev)
      const room = next.get(key)
      if (room) {
        next.set(key, { ...room, unread: 0 })
      }
      return next
    })
    setTimeout(() => {
      messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    }, 50)
  }, [])

  // --- Actions ---
  const handleSend = useCallback(async () => {
    if (!inputText.trim()) return
    const text = inputText

    let toNodeID = ''
    let toRole = ''
    let targetRoomKey = 'broadcast'
    let sendContent = text

    if (activeRoom === 'broadcast') {
      const mentionMatch = text.match(/^@(\S+)\s+(.*)/)
      if (mentionMatch) {
        const mentionedNick = mentionMatch[1]
        const content = mentionMatch[2]
        const found = contacts.find(c => c.nick === mentionedNick)
        if (found) {
          toNodeID = found.node_id
          toRole = found.to_role
          targetRoomKey = roomKeyForDM(found.node_id, found.to_role)
          sendContent = content
        }
      }
    } else {
      const info = parseRoomKey(activeRoom)
      if (info) {
        toNodeID = info.nodeID
        toRole = info.role
        targetRoomKey = activeRoom
      }
    }

    // Local echo FIRST — always show immediately, don't wait for network
    const echoMsg: LanChatMessage = {
      id: `local-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      from_node_id: selfNodeID,
      from_role: 'human',
      from_nick: nick,
      to_node_id: toNodeID,
      to_role: toRole,
      content: sendContent,
      timestamp: Date.now(),
    }
    addMessageToRoom(targetRoomKey, echoMsg, true)
    setTimeout(() => {
      messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    }, 50)

    setInputText('')

    // Then send to backend
    try {
      await App.LanChatSend(sendContent, toNodeID, toRole, false)
    } catch (e) {
      console.error('Send failed:', e)
    }
  }, [inputText, activeRoom, contacts, selfNodeID, nick, addMessageToRoom])

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

  const activeMessages = rooms.get(activeRoom)?.messages || []
  const activeLabel = activeRoom === 'broadcast'
    ? t('lanchat.groupChat')
    : (() => {
        const info = parseRoomKey(activeRoom)
        if (!info) return t('lanchat.chat')
        const c = contacts.find(c => c.node_id === info!.nodeID && c.to_role === info!.role)
        return c ? `${c.label}${c.to_role === 'agent' ? t('lanchat.agentSuffix') : ''}` : t('lanchat.chat')
      })()

  return (
    <div style={{ display: 'flex', height: '100%', minHeight: 0 }}>
      {/* --- Left sidebar: contact list --- */}
      <div style={{
        width: '200px',
        minWidth: '200px',
        borderRight: '1px solid rgba(255,255,255,0.15)',
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          padding: '8px 12px',
          borderBottom: '1px solid var(--border-color)',
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          flexShrink: 0,
        }}>
          <span style={{ fontSize: '13px', fontWeight: 600, color: 'var(--text-primary)' }}>LAN Chat</span>
          <button
            onClick={handleNickChange}
            style={{
              padding: '1px 6px',
              fontSize: '11px',
              border: '1px solid var(--border-color)',
              borderRadius: '3px',
              background: 'var(--bg-secondary)',
              color: 'var(--text-secondary)',
              cursor: 'pointer',
              marginLeft: 'auto',
            }}
          >
            {nick || 'unnamed'}
          </button>
        </div>

        {/* Contact list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {/* Group chat */}
          <ContactRow
            label="# Group Chat"
            active={activeRoom === 'broadcast'}
            unread={rooms.get('broadcast')?.unread || 0}
            onClick={() => switchRoom('broadcast')}
          />

          {/* Direct Messages — grouped by team */}
          {contacts.length === 0 ? (
            <div style={{ padding: '8px 12px', fontSize: '12px', color: 'var(--text-tertiary)' }}>
              No contacts online
            </div>
          ) : (() => {
            // Group contacts by team
            const teamMap = new Map<string, ContactEntry[]>()
            for (const c of contacts) {
              const arr = teamMap.get(c.team) || []
              arr.push(c)
              teamMap.set(c.team, arr)
            }
            // Sort teams: self team first, then alphabetical
            const teams = Array.from(teamMap.keys()).sort((a, b) => {
              if (a === selfTeam) return -1
              if (b === selfTeam) return 1
              return a.localeCompare(b)
            })
            return teams.map(team => {
              const teamContacts = teamMap.get(team)!
              const isMyTeam = team === selfTeam
              const defaultCollapsed = !isMyTeam
              const effectiveCollapsed = toggledTeams.has(team) ? !defaultCollapsed : defaultCollapsed
              return (
                <div key={team}>
                  <div
                    onClick={() => {
                      setToggledTeams(prev => {
                        const next = new Set(prev)
                        if (next.has(team)) next.delete(team)
                        else next.add(team)
                        return next
                      })
                    }}
                    style={{
                      padding: '4px 12px',
                      fontSize: '11px',
                      color: 'var(--text-tertiary)',
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                      cursor: 'pointer',
                      userSelect: 'none',
                      display: 'flex',
                      alignItems: 'center',
                      gap: '4px',
                    }}
                  >
                    <span style={{ fontSize: '9px', opacity: 0.6 }}>{effectiveCollapsed ? '▶' : '▼'}</span>
                    {team} ({teamContacts.length})
                  </div>
                  {!effectiveCollapsed && teamContacts.map(c => {
                    const key = roomKeyForDM(c.node_id, c.to_role)
                    return (
                      <ContactRow
                        key={key}
                        label={c.label}
                        badge={c.to_role === 'agent' ? 'agent' : undefined}
                        subtitle={c.project_name || (c.workspace ? c.workspace.split('/').pop() : undefined)}
                        active={activeRoom === key}
                        unread={rooms.get(key)?.unread || 0}
                        onClick={() => switchRoom(key)}
                      />
                    )
                  })}
                </div>
              )
            })
          })()}
        </div>
      </div>

      {/* --- Right: chat area --- */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {/* Chat header */}
        <div style={{
          padding: '8px 16px',
          borderBottom: '1px solid var(--border-color)',
          display: 'flex',
          alignItems: 'center',
          flexShrink: 0,
        }}>
          <span style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>
            {activeLabel}
          </span>
        </div>

        {/* Messages */}
        <div role="log" aria-live="polite" aria-label={t('common.lanChatMessages')} style={{ flex: 1, overflowY: 'auto', padding: '8px 16px', minHeight: 0 }}>
          {activeMessages.length === 0 ? (
            <div style={{ textAlign: 'center', color: 'var(--text-tertiary)', padding: '40px 0', fontSize: '13px' }}>
              {activeRoom === 'broadcast'
                ? t('lanchat.noMessagesBroadcast')
                : t('lanchat.noMessagesDM')}
            </div>
          ) : (
            activeMessages.map((msg, i) => {
              const isSelf = msg.from_node_id === selfNodeID
              const fromNick = msg.from_nick || 'unknown'
              return (
                <div
                  key={msg.id || i}
                  style={{
                    marginBottom: '8px',
                    display: 'flex',
                    flexDirection: isSelf ? 'row-reverse' : 'row',
                  }}
                >
                  <div
                    className="markdown-body"
                    style={{
                      maxWidth: '80%',
                      padding: '6px 12px',
                      borderRadius: '8px',
                      background: isSelf ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                      color: isSelf ? '#fff' : 'var(--text-primary)',
                      fontSize: '13px',
                      lineHeight: '1.4',
                    }}
                  >
                    {!isSelf && (
                      <div style={{ fontSize: '11px', fontWeight: 600, color: 'var(--text-tertiary)', marginBottom: '2px' }}>
                        {fromNick} {msg.from_role === 'agent' && <span style={{ color: 'var(--color-primary)' }}>agent</span>}
                      </div>
                    )}
                    <div dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }} />
                  </div>
                </div>
              )
            })
          )}
          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-color)', flexShrink: 0, display: 'flex', gap: '8px' }}>
          <input
            ref={inputRef}
            type="text"
            value={inputText}
            onChange={e => setInputText(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                handleSend()
              }
            }}
            placeholder={activeRoom === 'broadcast' ? t('lanchat.broadcastPlaceholder') : t('lanchat.dmPlaceholder').replace('{name}', activeLabel)}
            style={{
              flex: 1,
              padding: '6px 10px',
              fontSize: '13px',
              border: '1px solid var(--border-color)',
              borderRadius: '6px',
              background: 'var(--bg-secondary)',
              color: 'var(--text-primary)',
              outline: 'none',
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
              cursor: 'pointer',
            }}
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}

// --- Contact row sub-component ---
function ContactRow({
  label,
  badge,
  active,
  unread,
  subtitle,
  onClick,
}: {
  label: string
  badge?: string
  active: boolean
  unread: number
  subtitle?: string
  onClick: () => void
}) {
  return (
    <div
      onClick={onClick}
      style={{
        padding: '6px 12px',
        display: 'flex',
        alignItems: 'center',
        gap: '6px',
        cursor: 'pointer',
        background: active ? 'var(--bg-tertiary)' : 'transparent',
        borderLeft: active ? '2px solid var(--color-primary)' : '2px solid transparent',
      }}
    >
      <div style={{ flex: 1, overflow: 'hidden' }}>
        <div style={{
          fontSize: '13px',
          color: active ? 'var(--text-primary)' : 'var(--text-secondary)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}>
          {label}
        </div>
        {subtitle && (
          <div style={{
            fontSize: '10px',
            color: 'var(--text-tertiary)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}>
            {subtitle}
          </div>
        )}
      </div>
      {badge && (
        <span style={{
          fontSize: '10px',
          padding: '1px 4px',
          borderRadius: '3px',
          background: 'var(--bg-secondary)',
          color: 'var(--color-primary)',
        }}>
          {badge}
        </span>
      )}
      {unread > 0 && (
        <span style={{
          minWidth: '16px',
          height: '16px',
          borderRadius: '8px',
          background: '#e53e3e',
          color: '#fff',
          fontSize: '10px',
          fontWeight: 600,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '0 4px',
        }}>
          {unread > 99 ? '99+' : unread}
        </span>
      )}
    </div>
  )
}
