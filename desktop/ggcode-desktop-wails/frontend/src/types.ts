export interface ConfigData {
  vendor: string
  endpoint: string
  model: string
  mode: string
  language: string
  theme: string
}

export interface FileInfo {
  name: string
  isDir: boolean
  size: number
  modified: number
}

export interface Session {
  id: string
  title: string
  workspace: string
  createdAt: number
  updatedAt: number
}

export interface Message {
  id: string
  role: "user" | "assistant" | "system"
  content: string
  timestamp: number
  toolCalls?: ToolCall[]
}

export interface ToolCall {
  id: string
  name: string
  status: "running" | "success" | "error"
  input?: string
  output?: string
}

export interface EndpointInfo {
  key: string
  displayName: string
}

export type ViewMode = "chat" | "settings" | "im" | "files" | "mcp" | "debug" | "lanchat"

export interface LanChatParticipant {
  node_id: string
  human_nick: string
  agent_nick: string
  mode: string
  endpoint: string
  online: boolean
  last_seen: number
  workspace?: string
  project_name?: string
  languages?: string[]
  frameworks?: string[]
  has_git?: boolean
  has_tests?: boolean
}

export interface LanChatMessage {
  id: string
  from_node_id: string
  from_role: string
  from_nick: string
  to_node_id: string
  to_role: string
  content: string
  attachments?: { id: string; name: string; size: number; mime_type: string; url: string }[]
  timestamp: number
}

export interface LanChatPendingApproval {
  message: LanChatMessage
  received: string
}

export interface StatusBarData {
  vendor: string
  model: string
  mode: string
  contextUsed: number
  contextTotal: number
  usagePercent: number
  remainingPercent: number
  inputTokens: number
  outputTokens: number
  cacheRead: number
  cacheWrite: number
  cacheHit: number
  status: string
}
