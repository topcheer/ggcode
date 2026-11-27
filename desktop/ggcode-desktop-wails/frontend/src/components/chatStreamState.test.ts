import { describe, expect, it } from 'vitest'
import {
  appendAssistantChunk,
  appendBufferedChunks,
  appendReasoningChunk,
  appendUserMessage,
  finishAssistantMessage,
  finishAssistantRun,
  parseStreamData,
  type StreamChatMessage,
} from './chatStreamState'

function idFactory() {
  let n = 0
  return () => `m-${++n}`
}

const now = () => 123

describe('desktop stream payload parser', () => {
  it('accepts JSON string payloads from runtime events', () => {
    expect(parseStreamData<{ content: string }>('{"content":"hello"}')).toEqual({
      content: 'hello',
    })
  })

  it('accepts object payloads returned by Wails JSON marshalling', () => {
    const payload = { content: 'hello' }
    expect(parseStreamData<{ content: string }>(payload)).toBe(payload)
  })

  it('rejects invalid JSON string payloads', () => {
    expect(parseStreamData('{bad json')).toBeNull()
  })
})

describe('desktop chat stream state', () => {
  it('merges chunks by explicit backend message_id even without current streaming ref', () => {
    const nextID = idFactory()
    let messages: StreamChatMessage[] = []

    let result = appendAssistantChunk(messages, null, 'Hello', nextID, now, 'assistant-turn-1')
    messages = result.messages
    result = appendAssistantChunk(messages, null, ' again', nextID, now, 'assistant-turn-1')
    messages = result.messages
    result = appendAssistantChunk(messages, null, ' help?', nextID, now, 'assistant-turn-1')
    messages = result.messages

    expect(messages).toHaveLength(1)
    expect(messages[0]).toMatchObject({
      id: 'assistant-turn-1',
      role: 'assistant',
      content: 'Hello again help?',
      streaming: true,
    })
  })

  it('keeps separate turns in separate assistant bubbles by message_id', () => {
    const nextID = idFactory()
    let result = appendAssistantChunk([], null, 'First', nextID, now, 'assistant-turn-1')
    result = appendAssistantChunk(result.messages, null, 'Second', nextID, now, 'assistant-turn-2')

    expect(result.messages).toHaveLength(2)
    expect(result.messages[0]).toMatchObject({ id: 'assistant-turn-1', content: 'First' })
    expect(result.messages[1]).toMatchObject({ id: 'assistant-turn-2', content: 'Second' })
  })

  it('finishes only the targeted assistant message by message_id', () => {
    const messages: StreamChatMessage[] = [
      { id: 'assistant-turn-1', role: 'assistant', content: 'one', streaming: true },
      { id: 'assistant-turn-2', role: 'assistant', content: 'two', streaming: true },
    ]

    const finished = finishAssistantMessage(messages, 'assistant-turn-1')

    expect(finished[0].streaming).toBe(false)
    expect(finished[1].streaming).toBe(true)
  })

  it('merges multiple assistant text chunks into the same current message', () => {
    const nextID = idFactory()
    let messages: StreamChatMessage[] = []
    let streamingID: string | null = null

    let result = appendAssistantChunk(messages, streamingID, 'Hi!', nextID, now)
    messages = result.messages
    streamingID = result.streamingMessageID

    result = appendAssistantChunk(messages, streamingID, ' What would', nextID, now)
    messages = result.messages
    streamingID = result.streamingMessageID

    result = appendAssistantChunk(messages, streamingID, ' you like?', nextID, now)
    messages = result.messages
    streamingID = result.streamingMessageID

    expect(messages).toHaveLength(1)
    expect(messages[0]).toMatchObject({
      id: 'm-1',
      role: 'assistant',
      content: 'Hi! What would you like?',
      streaming: true,
    })
    expect(streamingID).toBe('m-1')
  })

  it('does not append a new assistant turn to an older stale streaming assistant', () => {
    const nextID = idFactory()
    let messages: StreamChatMessage[] = [
      { id: 'old', role: 'assistant', content: 'old partial', streaming: true },
    ]

    messages = appendUserMessage(messages, {
      id: 'user-1',
      role: 'user',
      content: 'hello',
      streaming: false,
    })

    const result = appendAssistantChunk(messages, null, 'new answer', nextID, now)
    messages = result.messages

    expect(messages[0]).toMatchObject({ id: 'old', content: 'old partial', streaming: false })
    expect(messages[2]).toMatchObject({ role: 'assistant', content: 'new answer', streaming: true })
    expect(result.streamingMessageID).toBe('m-1')
  })

  it('closes assistant and reasoning streams when a run finishes', () => {
    const messages: StreamChatMessage[] = [
      { id: 'a', role: 'assistant', content: 'answer', streaming: true },
      { id: 'r', role: 'reasoning', content: 'thought', streaming: true },
      { id: 't', role: 'tool', content: 'tool running', streaming: true },
    ]

    const finished = finishAssistantRun(messages)

    expect(finished[0].streaming).toBe(false)
    expect(finished[1].streaming).toBe(false)
    expect(finished[2].streaming).toBe(true)
  })

  it('merges reasoning chunks into the latest reasoning stream', () => {
    const nextID = idFactory()
    let messages: StreamChatMessage[] = appendReasoningChunk([], 'thinking', nextID, now)
    messages = appendReasoningChunk(messages, ' more', nextID, now)

    expect(messages).toHaveLength(1)
    expect(messages[0]).toMatchObject({
      id: 'm-1',
      role: 'reasoning',
      content: 'thinking more',
      streaming: true,
    })
  })
})

describe('appendBufferedChunks (stream batching)', () => {
  it('applies 50 text deltas as one message update on a single assistant message', () => {
    const r = appendBufferedChunks<StreamChatMessage>([], null, Array.from({ length: 50 }, (_, i) => ({ type: 'text' as const, content: `${i},` })), idFactory(), now)
    expect(r.messages).toHaveLength(1)
    expect(r.messages[0].role).toBe('assistant')
    expect(r.messages[0].streaming).toBe(true)
    expect(r.messages[0].content.endsWith('48,49,')).toBe(true)
    expect(r.messages[0].content.startsWith('0,1,2,')).toBe(true)
    expect(r.streamingMessageID).toBe(r.messages[0].id)
  })

  it('keeps interleaved reasoning and text on their own streaming messages in order', () => {
    const r = appendBufferedChunks<StreamChatMessage>([], null, [
      { type: 'reasoning', content: 'think ' },
      { type: 'reasoning', content: 'more' },
      { type: 'text', content: 'answer' },
      { type: 'text', content: '!' },
    ], idFactory(), now)
    expect(r.messages.map(m => m.role)).toEqual(['reasoning', 'assistant'])
    expect(r.messages[0].content).toBe('think more')
    expect(r.messages[1].content).toBe('answer!')
  })

  it('continues the message identified by streamingMessageID and honours explicit ids', () => {
    const first = appendBufferedChunks<StreamChatMessage>([], null, [{ type: 'text', content: 'a' }], idFactory(), now)
    const second = appendBufferedChunks(first.messages, first.streamingMessageID, [{ type: 'text', content: 'b' }], idFactory(), now)
    expect(second.messages).toHaveLength(1)
    expect(second.messages[0].content).toBe('ab')
    const explicit = appendBufferedChunks(second.messages, second.streamingMessageID, [{ type: 'text', content: 'c', messageID: 'srv-1' }], idFactory(), now)
    expect(explicit.messages.map(m => m.id)).toEqual([second.messages[0].id, 'srv-1'])
    expect(explicit.streamingMessageID).toBe('srv-1')
  })

  it('returns the input untouched for an empty batch', () => {
    const msgs: StreamChatMessage[] = [{ id: 'x', role: 'assistant', content: 'hi' }]
    const r = appendBufferedChunks(msgs, 'x', [], idFactory(), now)
    expect(r.messages).toBe(msgs)
    expect(r.streamingMessageID).toBe('x')
  })
})
