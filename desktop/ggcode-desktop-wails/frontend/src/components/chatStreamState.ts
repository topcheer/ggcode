export type ChatRole = 'user' | 'assistant' | 'reasoning' | 'tool' | 'error' | 'system'

export interface StreamChatMessage {
  id: string
  role: ChatRole
  content: string
  streaming?: boolean
  timestamp?: number
  source?: string
  responseDuration?: number // seconds, set when assistant streaming completes
}

export interface DesktopTextEvent {
  turn_id?: string
  message_id?: string
  seq?: number
  content: string
}

export interface DesktopDoneEvent {
  turn_id?: string
  message_id?: string
}

export interface DesktopUserMessageEvent {
  turn_id?: string
  message_id?: string
  text: string
  source?: string
}

export interface ApplyChunkResult<T extends StreamChatMessage> {
  messages: T[]
  streamingMessageID: string | null
}

export function parseStreamData<T>(raw: unknown): T | null {
  if (raw == null) return null
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw) as T
    } catch {
      return null
    }
  }
  if (typeof raw === 'object') return raw as T
  return null
}

export function closeOpenAssistantStreams<T extends StreamChatMessage>(messages: T[]): T[] {
  const now = Date.now()
  return messages.map(m => {
    if ((m.role === 'assistant' || m.role === 'reasoning') && m.streaming) {
      const dur = m.timestamp ? Math.max(1, Math.round((now - m.timestamp) / 1000)) : undefined
      return { ...m, streaming: false, responseDuration: dur }
    }
    return m
  })
}

export function appendAssistantChunk<T extends StreamChatMessage>(
  messages: T[],
  streamingMessageID: string | null,
  content: string,
  nextID: () => string,
  now: () => number = () => Date.now(),
  explicitMessageID?: string,
): ApplyChunkResult<T> {
  const out = [...messages]
  const messageID = explicitMessageID || streamingMessageID
  const idx = messageID ? out.findIndex(m => m.id === messageID) : -1
  if (idx >= 0 && out[idx].role === 'assistant') {
    out[idx] = { ...out[idx], content: out[idx].content + content, streaming: true }
    return { messages: out, streamingMessageID: out[idx].id }
  }

  const id = explicitMessageID || nextID()
  out.push({
    id,
    role: 'assistant',
    content,
    streaming: true,
    timestamp: now(),
  } as T)
  return { messages: out, streamingMessageID: id }
}

export function appendReasoningChunk<T extends StreamChatMessage>(
  messages: T[],
  content: string,
  nextID: () => string,
  now: () => number = () => Date.now(),
  explicitMessageID?: string,
): T[] {
  const out = [...messages]
  if (explicitMessageID) {
    const idx = out.findIndex(m => m.id === explicitMessageID)
    if (idx >= 0 && out[idx].role === 'reasoning') {
      out[idx] = { ...out[idx], content: out[idx].content + content, streaming: true }
      return out
    }
    out.push({
      id: explicitMessageID,
      role: 'reasoning',
      content,
      streaming: true,
      timestamp: now(),
    } as T)
    return out
  }
  for (let i = out.length - 1; i >= 0; i--) {
    if (out[i].role === 'reasoning' && out[i].streaming) {
      out[i] = { ...out[i], content: out[i].content + content, streaming: true }
      return out
    }
  }
  out.push({
    id: nextID(),
    role: 'reasoning',
    content,
    streaming: true,
    timestamp: now(),
  } as T)
  return out
}

export function appendUserMessage<T extends StreamChatMessage>(
  messages: T[],
  message: T,
): T[] {
  const closed = closeOpenAssistantStreams(messages)
  const idx = closed.findIndex(m => m.id === message.id)
  if (idx >= 0) {
    const out = [...closed]
    out[idx] = { ...out[idx], ...message }
    return out
  }
  return [...closed, message]
}

export function finishAssistantMessage<T extends StreamChatMessage>(
  messages: T[],
  messageID?: string,
): T[] {
  if (!messageID) return closeOpenAssistantStreams(messages)
  const now = Date.now()
  return messages.map(m => {
    if (m.id === messageID && (m.role === 'assistant' || m.role === 'reasoning')) {
      const dur = m.timestamp ? Math.max(1, Math.round((now - m.timestamp) / 1000)) : undefined
      return { ...m, streaming: false, responseDuration: dur }
    }
    return m
  })
}

export function finishAssistantRun<T extends StreamChatMessage>(messages: T[]): T[] {
  return closeOpenAssistantStreams(messages)
}
