import { create } from 'zustand';
import type {
  Message,
  VendorInfo,
  ActiveProviderInfo,
  StreamEvent,
} from './types';

interface ChatState {
  // Messages
  messages: Message[];
  isStreaming: boolean;

  // Provider
  vendors: VendorInfo[];
  activeProvider: ActiveProviderInfo;

  // Token usage
  totalInputTokens: number;
  totalOutputTokens: number;

  // Collapsed state for tool blocks (tool_id → collapsed)
  collapsedTools: Record<string, boolean>;

  // Reasoning visibility (message index → visible)
  reasoningVisible: Record<number, boolean>;

  // Actions
  addMessage: (msg: Message) => void;
  appendToLastAssistant: (event: StreamEvent) => void;
  setStreaming: (v: boolean) => void;
  setVendors: (v: VendorInfo[]) => void;
  setActiveProvider: (v: ActiveProviderInfo) => void;
  toggleToolCollapsed: (toolId: string) => void;
  toggleReasoning: (msgIndex: number) => void;
  resetTokens: () => void;
  clearMessages: () => void;
}

export const useChatStore = create<ChatState>((set, get) => ({
  messages: [],
  isStreaming: false,
  vendors: [],
  activeProvider: { vendor: '', endpoint: '', model: '' },
  totalInputTokens: 0,
  totalOutputTokens: 0,
  collapsedTools: {},
  reasoningVisible: {},

  addMessage: (msg) =>
    set((s) => ({ messages: [...s.messages, msg] })),

  appendToLastAssistant: (event) =>
    set((s) => {
      const msgs = [...s.messages];
      const last = msgs[msgs.length - 1];
      if (!last || last.role !== 'assistant') return s;

      const updated = { ...last, content: [...last.content] };

      switch (event.type) {
        case 'text': {
          // Append to last text block or create new one
          const lastBlock = updated.content[updated.content.length - 1];
          if (lastBlock?.type === 'text') {
            updated.content[updated.content.length - 1] = {
              ...lastBlock,
              text: (lastBlock.text || '') + (event.text || ''),
            };
          } else {
            updated.content.push({ type: 'text', text: event.text || '' });
          }
          break;
        }
        case 'reasoning': {
          const lastBlock = updated.content[updated.content.length - 1];
          if (lastBlock?.type === 'reasoning') {
            updated.content[updated.content.length - 1] = {
              ...lastBlock,
              text: (lastBlock.text || '') + (event.text || ''),
            };
          } else {
            updated.content.push({ type: 'reasoning', text: event.text || '' });
          }
          break;
        }
        case 'tool_call_done': {
          if (event.tool) {
            updated.content.push({
              type: 'tool_use',
              tool_id: event.tool.id,
              tool_name: event.tool.name,
              input: event.tool.input,
            });
          }
          break;
        }
        case 'tool_result': {
          if (event.tool) {
            updated.content.push({
              type: 'tool_result',
              tool_id: event.tool.id,
              tool_name: event.tool.name,
              output: event.result || '',
              is_error: event.is_error,
            });
          }
          break;
        }
        case 'done': {
          if (event.usage) {
            return {
              ...s,
              messages: [...msgs.slice(0, -1), updated],
              isStreaming: false,
              totalInputTokens: s.totalInputTokens + (event.usage.input_tokens || 0),
              totalOutputTokens: s.totalOutputTokens + (event.usage.output_tokens || 0),
            };
          }
          return { ...s, messages: [...msgs.slice(0, -1), updated], isStreaming: false };
        }
        case 'error': {
          return { ...s, isStreaming: false };
        }
      }

      return { messages: [...msgs.slice(0, -1), updated] };
    }),

  setStreaming: (v) => set({ isStreaming: v }),
  setVendors: (v) => set({ vendors: v }),
  setActiveProvider: (v) => set({ activeProvider: v }),

  toggleToolCollapsed: (toolId) =>
    set((s) => ({
      collapsedTools: {
        ...s.collapsedTools,
        [toolId]: !s.collapsedTools[toolId],
      },
    })),

  toggleReasoning: (msgIndex) =>
    set((s) => ({
      reasoningVisible: {
        ...s.reasoningVisible,
        [msgIndex]: !s.reasoningVisible[msgIndex],
      },
    })),

  resetTokens: () => set({ totalInputTokens: 0, totalOutputTokens: 0 }),
  clearMessages: () => set({ messages: [], totalInputTokens: 0, totalOutputTokens: 0 }),
}));
