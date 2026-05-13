import { Binding } from '../bindings/github.com/topcheer/ggcode/desktop';

// Re-export Wails bindings for convenience
export const wails = Binding;

export interface ContentBlock {
  type: 'text' | 'tool_use' | 'tool_result' | 'reasoning' | 'image';
  text?: string;
  tool_id?: string;
  tool_name?: string;
  input?: Record<string, unknown>;
  output?: string;
  is_error?: boolean;
  images?: { mime: string; base64: string }[];
}

export interface Message {
  role: 'user' | 'assistant' | 'system';
  content: ContentBlock[];
}

export interface TokenUsage {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface VendorInfo {
  name: string;
  displayName: string;
  endpoints: EndpointInfo[];
}

export interface EndpointInfo {
  name: string;
  displayName: string;
  models: string[];
  selectedModel: string;
  protocol: string;
}

export interface ActiveProviderInfo {
  vendor: string;
  endpoint: string;
  model: string;
}

export type StreamEventType =
  | 'text'
  | 'tool_call_chunk'
  | 'tool_call_done'
  | 'tool_result'
  | 'done'
  | 'error'
  | 'reasoning'
  | 'system';

export interface StreamEvent {
  type: StreamEventType;
  text?: string;
  tool?: {
    id: string;
    name: string;
    input?: Record<string, unknown>;
    input_json?: string;
  };
  result?: string;
  is_error?: boolean;
  usage?: TokenUsage;
  error?: string;
}
