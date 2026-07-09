import { describe, expect, it } from 'vitest'
import {
  renderMarkdown,
  roomKeyForDM,
  parseRoomKey,
  roomKeyForMessage,
  buildContacts,
} from './LanChatView'
import type { LanChatParticipant, LanChatMessage } from '../types'

// --- renderMarkdown ---

describe('renderMarkdown', () => {
  it('returns empty string for empty input', () => {
    expect(renderMarkdown('')).toBe('')
  })

  it('renders bold text', () => {
    const result = renderMarkdown('**hello**')
    expect(result).toContain('<strong>hello</strong>')
  })

  it('renders code blocks', () => {
    const result = renderMarkdown('`code`')
    expect(result).toContain('<code>code</code>')
  })

  it('renders links', () => {
    const result = renderMarkdown('[example](https://example.com)')
    expect(result).toContain('<a href="https://example.com">')
  })

  it('renders lists', () => {
    const result = renderMarkdown('- item1\n- item2')
    expect(result).toContain('<li>item1</li>')
    expect(result).toContain('<li>item2</li>')
  })
})

// --- roomKeyForDM ---

describe('roomKeyForDM', () => {
  it('generates correct key format', () => {
    expect(roomKeyForDM('node-123', 'human')).toBe('dm:node-123:human')
  })

  it('handles agent role', () => {
    expect(roomKeyForDM('node-456', 'agent')).toBe('dm:node-456:agent')
  })

  it('handles nodeIDs with colons (IPv6)', () => {
    expect(roomKeyForDM('fe80::1', 'human')).toBe('dm:fe80::1:human')
  })
})

// --- parseRoomKey ---

describe('parseRoomKey', () => {
  it('parses standard DM key', () => {
    expect(parseRoomKey('dm:node-123:human')).toEqual({ nodeID: 'node-123', role: 'human' })
  })

  it('returns null for broadcast', () => {
    expect(parseRoomKey('broadcast')).toBeNull()
  })

  it('handles nodeID with colons', () => {
    expect(parseRoomKey('dm:fe80::1:agent')).toEqual({ nodeID: 'fe80::1', role: 'agent' })
  })

  it('returns null for malformed key', () => {
    expect(parseRoomKey('invalid')).toBeNull()
  })

  it('parses non-dm key with 3 segments (does not check dm prefix)', () => {
    // parseRoomKey splits by ':' and doesn't validate the "dm" prefix
    expect(parseRoomKey('group:test:greeting')).toEqual({ nodeID: 'test', role: 'greeting' })
  })
})

// --- roomKeyForMessage ---

function makeMsg(overrides: Partial<LanChatMessage> = {}): LanChatMessage {
  return {
    id: 'msg-1',
    from_node_id: 'node-A',
    from_role: 'human',
    from_nick: 'alice',
    to_node_id: '',
    to_role: '',
    content: 'hello',
    timestamp: 0,
    ...overrides,
  }
}

describe('roomKeyForMessage', () => {
  it('routes DM sent to me by from_node_id', () => {
    const msg = makeMsg({ from_node_id: 'node-B', to_node_id: 'me' })
    expect(roomKeyForMessage(msg, 'me')).toBe('dm:node-B:human')
  })

  it('routes DM sent by me to to_node_id with to_role', () => {
    const msg = makeMsg({ from_node_id: 'me', to_node_id: 'node-C', to_role: 'agent' })
    expect(roomKeyForMessage(msg, 'me')).toBe('dm:node-C:agent')
  })

  it('routes broadcast messages to broadcast room', () => {
    const msg = makeMsg({ from_node_id: 'node-B', to_node_id: '' })
    expect(roomKeyForMessage(msg, 'me')).toBe('broadcast')
  })

  it('routes my own broadcast as broadcast', () => {
    const msg = makeMsg({ from_node_id: 'me', to_node_id: '' })
    expect(roomKeyForMessage(msg, 'me')).toBe('broadcast')
  })
})

// --- buildContacts ---

function makeParticipant(overrides: Partial<LanChatParticipant> = {}): LanChatParticipant {
  return {
    node_id: 'node-1',
    human_nick: '',
    agent_nick: '',
    mode: 'tui',
    endpoint: '',
    role: 'developer',
    team: 'dev-team',
    online: true,
    last_seen: 0,
    ...overrides,
  }
}

describe('buildContacts', () => {
  it('excludes self node', () => {
    const participants = [
      makeParticipant({ node_id: 'me', human_nick: 'self' }),
      makeParticipant({ node_id: 'other', human_nick: 'alice' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts).toHaveLength(1)
    expect(contacts[0].label).toBe('alice')
  })

  it('creates both human and agent entries for same participant', () => {
    const participants = [
      makeParticipant({ node_id: 'node-1', human_nick: 'bob', agent_nick: 'bob_agent' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts).toHaveLength(2)
    expect(contacts[0].label).toBe('bob')
    expect(contacts[0].to_role).toBe('human')
    expect(contacts[1].label).toBe('bob_agent')
    expect(contacts[1].to_role).toBe('agent')
  })

  it('deduplicates participants with same nick and role', () => {
    const participants = [
      makeParticipant({ node_id: 'node-1', human_nick: 'alice' }),
      makeParticipant({ node_id: 'node-2', human_nick: 'alice' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts).toHaveLength(1)
    expect(contacts[0].label).toBe('alice')
  })

  it('extracts team from participant, defaults to dev-team', () => {
    const participants = [
      makeParticipant({ node_id: 'node-1', human_nick: 'a', team: 'platform' }),
      makeParticipant({ node_id: 'node-2', human_nick: 'b', team: '' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts[0].team).toBe('platform')
    expect(contacts[1].team).toBe('dev-team')
  })

  it('sorts contacts alphabetically by label', () => {
    const participants = [
      makeParticipant({ node_id: 'node-1', human_nick: 'charlie' }),
      makeParticipant({ node_id: 'node-2', human_nick: 'alice' }),
      makeParticipant({ node_id: 'node-3', human_nick: 'bob' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts.map(c => c.label)).toEqual(['alice', 'bob', 'charlie'])
  })

  it('handles participant with no nicks (falls back to node_id)', () => {
    const participants = [
      makeParticipant({ node_id: 'very-long-node-id-1234567890', human_nick: '', agent_nick: '' }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts).toHaveLength(1)
    expect(contacts[0].label).toBe('very-long-no') // first 12 chars of 'very-long-node-id-1234567890'
    expect(contacts[0].to_role).toBe('human')
  })

  it('returns empty array when only self is present', () => {
    const participants = [makeParticipant({ node_id: 'me', human_nick: 'self' })]
    expect(buildContacts(participants, 'me')).toHaveLength(0)
  })

  it('passes through workspace and project_name', () => {
    const participants = [
      makeParticipant({
        node_id: 'node-1',
        human_nick: 'alice',
        workspace: '/home/alice/project',
        project_name: 'awesome-project',
        languages: ['go', 'typescript'],
      }),
    ]
    const contacts = buildContacts(participants, 'me')
    expect(contacts[0].workspace).toBe('/home/alice/project')
    expect(contacts[0].project_name).toBe('awesome-project')
    expect(contacts[0].languages).toEqual(['go', 'typescript'])
  })
})
