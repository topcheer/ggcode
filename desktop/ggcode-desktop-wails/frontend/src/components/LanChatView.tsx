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

interface ChatRoom {
  messages: LanChatMessage[]
  unread: number
}

export interface ContactEntry {
  node_id: string
  label: string
  nick: string
  to_role: string
  team: string
  workspace?: string
  project_name?: string
  languages?: string[]
}

export function roomKeyForDM(nodeID: string, role: string): string {
  return `dm:${nodeID}:${role}`
}

export function parseRoomKey(key: string): { nodeID: string; role: string } | null {
  if (key === 'broadcast') return null
  const parts = key.split(':')
  if (parts.length < 3) return null
  const role = parts[parts.length - 1]
  const nodeID = parts.slice(1, -1).join(':')
  return { nodeID, role }
}

export function roomKeyForMessage(msg: LanChatMessage, selfNodeID: string): string {
  if (msg.to_node_id === selfNodeID && msg.to_node_id !== '') {
    return roomKeyForDM(msg.from_node_id, msg.from_role)
  }
  if (msg.from_node_id === selfNodeID && msg.to_node_id !== '') {
    return roomKeyForDM(msg.to_node_id, msg.to_role)
  }
  return 'broadcast'
}

export function buildContacts(participants: LanChatParticipant[], selfNodeID: string): ContactEntry[] {
  const contacts: ContactEntry[] = []
  const seen = new Set<string>()
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

  const [rooms, setRooms] = useState<Map<string, ChatRoom>>(new Map([['broadcast', { messages: [], unread: 0 }]]))
  const [activeRoom, setActiveRoom] = useState('broadcast')

  const [inputText, setInputText] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const selfNodeIDRef = useRef('')
  const activeRoomRef = useRef('broadcast')
  selfNodeIDRef.current = selfNodeID
  activeRoomRef.current = activeRoom

  const contacts = buildContacts(participants, selfNodeID)

  const addMessageToRoom = useCallback((roomKey: string, msg: LanChatMessage, isActive: boolean) => {
    setRooms(prev => {
      const next = new Map(prev)
      const room = next.get(roomKey) || { messages: [], unread: 0 }
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
      const myID = selfNodeIDRef.current
      let roomKey = 'broadcast'
      if (r.from_node_id && r.from_node_id !== myID) {
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
      // #5625: no-change renames (A -> A) are broadcast noise from old-bin
      // peers re-asserting identity - never render them.
      if (oldNick !== newNick) {
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
      }
      refreshParticipants()
    })

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

  useEffect(() => {
    onUnreadChange?.(totalUnread)
  }, [totalUnread, onUnreadChange])

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
      {}
      <div style={{
        width: '200px',
        minWidth: '200px',
        borderRight: '1px solid rgba(255,255,255,0.15)',
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {}
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

        {}
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 0' }}>
          {}
          <ContactRow
            label="# Group Chat"
            active={activeRoom === 'broadcast'}
            unread={rooms.get('broadcast')?.unread || 0}
            onClick={() => switchRoom('broadcast')}
          />

          {}
          {contacts.length === 0 ? (
            <div style={{ padding: '8px 12px', fontSize: '12px', color: 'var(--text-tertiary)' }}>
              No contacts online
            </div>
          ) : (() => {
            const teamMap = new Map<string, ContactEntry[]>()
            for (const c of contacts) {
              const arr = teamMap.get(c.team) || []
              arr.push(c)
              teamMap.set(c.team, arr)
            }
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

      {}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {}
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

        {}
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

        {}
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
