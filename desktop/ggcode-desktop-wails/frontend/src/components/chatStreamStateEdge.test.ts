import { describe, expect, it } from 'vitest'
import {
  appendAssistantChunk,
  appendUserMessage,
  closeOpenAssistantStreams,
  finishAssistantMessage,
  type StreamChatMessage,
} from './chatStreamState'

function idFactory() {
  let n = 0
  return () => `m-${++n}`
}

const now = () => 1000

describe('closeOpenAssistantStreams', () => {
  it('closes streaming assistant and reasoning messages', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a', role: 'assistant', content: 'hello', streaming: true, timestamp: 500 },
      { id: 'r', role: 'reasoning', content: 'thinking', streaming: true, timestamp: 500 },
      { id: 'd', role: 'tool', content: 'ok', streaming: false },
    ]
    const result = closeOpenAssistantStreams(messages)
    expect(result[0].streaming).toBe(false)
    expect(result[1].streaming).toBe(false)
    expect(result[2].streaming).toBe(false) // already false
  })

  it('sets responseDuration based on timestamp', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a', role: 'assistant', content: 'hello', streaming: true, timestamp: 500 },
    ]
    const result = closeOpenAssistantStreams(messages)
    expect(result[0].responseDuration).toBeGreaterThanOrEqual(1)
  })

  it('handles messages without timestamp (no responseDuration)', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a', role: 'assistant', content: 'hello', streaming: true },
    ]
    const result = closeOpenAssistantStreams(messages)
    expect(result[0].streaming).toBe(false)
    expect(result[0].responseDuration).toBeUndefined()
  })

  it('does not modify tool or error messages', () => {
    const messages: StreamChatMessage[] = [
      { id: 't', role: 'tool', content: 'output', streaming: true },
      { id: 'e', role: 'error', content: 'err', streaming: true },
    ]
    const result = closeOpenAssistantStreams(messages)
    expect(result[0].streaming).toBe(true) // tool left alone
    expect(result[1].streaming).toBe(true) // error left alone
  })

  it('returns empty array as-is', () => {
    expect(closeOpenAssistantStreams([])).toEqual([])
  })
})

describe('appendUserMessage', () => {
  it('updates existing message by ID', () => {
    const messages: StreamChatMessage[] = [
      { id: 'u1', role: 'user', content: 'old', streaming: false },
    ]
    const result = appendUserMessage(messages, {
      id: 'u1',
      role: 'user',
      content: 'updated',
      streaming: false,
    })
    expect(result).toHaveLength(1)
    expect(result[0].content).toBe('updated')
  })

  it('appends new message when ID does not match', () => {
    const messages: StreamChatMessage[] = [
      { id: 'u1', role: 'user', content: 'first', streaming: false },
    ]
    const result = appendUserMessage(messages, {
      id: 'u2',
      role: 'user',
      content: 'second',
      streaming: false,
    })
    expect(result).toHaveLength(2)
    expect(result[1].content).toBe('second')
  })

  it('closes open assistant streams when appending user message', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a1', role: 'assistant', content: 'partial', streaming: true, timestamp: 500 },
    ]
    const result = appendUserMessage(messages, {
      id: 'u1',
      role: 'user',
      content: 'new question',
      streaming: false,
    })
    expect(result[0].streaming).toBe(false) // assistant closed
    expect(result[1].content).toBe('new question')
  })
})

describe('appendAssistantChunk edge cases', () => {
  it('handles empty content chunk', () => {
    const nextID = idFactory()
    const result = appendAssistantChunk<StreamChatMessage>([], null, '', nextID, now)
    expect(result.messages).toHaveLength(1)
    expect(result.messages[0].content).toBe('')
  })

  it('does not match assistant chunk to reasoning message with same explicit ID', () => {
    const nextID = idFactory()
    const messages: StreamChatMessage[] = [
      { id: 'shared', role: 'reasoning', content: 'thinking', streaming: true },
    ]
    const result = appendAssistantChunk(messages, null, 'answer', nextID, now, 'shared')
    // Should create a new assistant message, not append to reasoning
    expect(result.messages).toHaveLength(2)
    expect(result.messages[1].role).toBe('assistant')
  })

  it('preserves explicit message ID over generated one', () => {
    const nextID = idFactory()
    const result = appendAssistantChunk<StreamChatMessage>([], null, 'hello', nextID, now, 'custom-id')
    expect(result.messages[0].id).toBe('custom-id')
  })
})

describe('finishAssistantMessage edge cases', () => {
  it('does nothing when messageID is undefined (delegates to closeOpenAssistantStreams)', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a', role: 'assistant', content: 'hi', streaming: true },
    ]
    const result = finishAssistantMessage(messages)
    expect(result[0].streaming).toBe(false)
  })

  it('does not close other messages', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a1', role: 'assistant', content: 'one', streaming: true },
      { id: 'a2', role: 'assistant', content: 'two', streaming: true },
    ]
    const result = finishAssistantMessage(messages, 'a1')
    expect(result[0].streaming).toBe(false)
    expect(result[1].streaming).toBe(true)
  })
})
