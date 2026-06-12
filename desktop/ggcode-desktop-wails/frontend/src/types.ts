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

export type ViewMode = "chat" | "settings" | "im" | "files" | "mcp" | "debug"

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
