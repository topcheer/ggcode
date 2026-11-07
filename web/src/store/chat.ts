import { create } from "zustand";

// 消息模型对齐 provider.ContentBlock / server_websocket.go 线上格式(已核)
export type Role = "user" | "assistant" | "system";

export type UIMessage = {
  id: string;
  role: Role;
  text: string; // 已聚合的 markdown 文本
  reasoning: string; // 已聚合思考文本
  tools: ToolInvocation[];
  streaming?: boolean;
  error?: string;
  usage?: { input_tokens: number; output_tokens: number };
};

export type ToolInvocation = {
  id: string;
  name: string;
  argsText: string; // 累积的 arguments JSON 字符串
  result?: string;
  isError?: boolean;
  done: boolean;
  startedAt: number;
  endedAt?: number;
};

type ConnState = "connecting" | "open" | "closed";

type ChatStore = {
  messages: UIMessage[];
  conn: ConnState;
  busy: boolean; // agent 处理中(§ WS busy error)
  lastError: string | null;
  applyMsg: (m: import("@/ws/client").StreamMsg) => void;
  pushOptimistic: (text: string) => void;
  setConn: (s: ConnState) => void;
  reset: () => void;
};

let seq = 0;
const nid = () => `m${Date.now()}-${seq++}`;

function lastAssistant(msgs: UIMessage[]): UIMessage {
  const last = msgs[msgs.length - 1];
  if (last && last.role === "assistant" && last.streaming) return last;
  return {
    id: nid(), role: "assistant", text: "", reasoning: "", tools: [], streaming: true,
  };
}

export const useChat = create<ChatStore>((set) => ({
  messages: [],
  conn: "closed",
  busy: false,
  lastError: null,

  setConn: (conn) => set({ conn }),

  // 乐观 UI(§4.4):用户消息立即上屏
  pushOptimistic: (text) =>
    set((st) => ({
      messages: [...st.messages, { id: nid(), role: "user", text, reasoning: "", tools: [] }],
      busy: true,
      lastError: null,
    })),

  reset: () => set({ messages: [], busy: false, lastError: null }),

  applyMsg: (m) =>
    set((st) => {
      switch (m.type) {
        case "user_ack":
          // ack 到达:若乐观消息文本一致则标记成功(简化:不做 diff,ack 即成功)
          return {};
        case "text_delta": {
          const msgs = [...st.messages];
          const a = lastAssistant(msgs);
          if (!msgs.includes(a)) msgs.push(a);
          a.text += m.text;
          return { messages: msgs };
        }
        case "reasoning_delta": {
          const msgs = [...st.messages];
          const a = lastAssistant(msgs);
          if (!msgs.includes(a)) msgs.push(a);
          a.reasoning += m.text;
          return { messages: msgs };
        }
        case "tool_call_chunk":
        case "tool_call": {
          const msgs = [...st.messages];
          const a = lastAssistant(msgs);
          if (!msgs.includes(a)) msgs.push(a);
          let t = a.tools.find((x) => x.id === m.id);
          if (!t) {
            t = { id: m.id, name: m.name, argsText: "", done: false, startedAt: Date.now() };
            a.tools.push(t);
          }
          if (m.type === "tool_call_chunk") t.argsText += m.arguments;
          else t.argsText = m.arguments; // done 事件带完整参数
          return { messages: msgs };
        }
        case "tool_result": {
          const msgs = [...st.messages];
          // tool_result 只带 name,匹配最后一个同名未完成调用
          for (let i = msgs.length - 1; i >= 0; i--) {
            const t = msgs[i].tools.find((x) => x.name === m.name && !x.done);
            if (t) {
              t.result = m.result;
              t.isError = m.is_error;
              t.done = true;
              t.endedAt = Date.now();
              break;
            }
          }
          return { messages: msgs };
        }
        case "done": {
          const msgs = [...st.messages];
          const last = msgs[msgs.length - 1];
          if (last && last.role === "assistant") {
            last.streaming = false;
            last.usage = m.usage;
          }
          return { messages: msgs, busy: false };
        }
        case "error":
          return { lastError: m.error, busy: false };
        case "system": {
          const msgs = [...st.messages];
          msgs.push({ id: nid(), role: "system", text: m.text, reasoning: "", tools: [] });
          return { messages: msgs };
        }
      }
    }),
}));
