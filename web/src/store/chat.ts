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

/** todo 面板项(运行时实时聚合,键=todo id) */
export type TodoItem = {
  id: string;
  content: string;
  status: "pending" | "in_progress" | "done";
};

/** 子代理实时流(P 二层一等键=SubAgentID,与桌面面板语义一致) */
export type SubagentStream = {
  id: string;
  text: string;
  reasoning: string;
  done: boolean;
  result?: string;
  isError?: boolean;
};

type ChatStore = {
  messages: UIMessage[];
  conn: ConnState;
  busy: boolean; // agent 处理中(§ WS busy error)
  lastError: string | null;
  subagentStreams: Record<string, SubagentStream>; // §6.1 二层:活跃/完结子代理流
  todos: TodoItem[]; // agent loop 运行时实时推送聚合(todo_write tool_result 解析)
  loadHistory: () => Promise<void>;
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
  subagentStreams: {},
  todos: [],

  setConn: (conn) => set({ conn }),

  // TUI session 镜像(原实现语义,已核 server_websocket.go:42-47):
  // /api/chat/history 返回当前 chatBridge/agent 会话全量,前端镜像它而非独立会话。
  loadHistory: async () => {
    try {
      const res = await fetch("/api/chat/history");
      if (!res.ok) return;
      const data = (await res.json()) as Array<{
        role: Role;
        content: Array<{
          type: string;
          text?: string;
          tool_name?: string;
          tool_id?: string;
          input?: unknown;
          output?: string;
          is_error?: boolean;
        }>;
      }>;
      const msgs: UIMessage[] = [];
      for (const m of data) {
        if (m.role === "user") {
          const text = m.content.map((b) => b.text ?? "").join("");
          msgs.push({ id: nid(), role: "user", text, reasoning: "", tools: [] });
          continue;
        }
        // assistant:文本/思考聚为一个气泡,tool_use+紧随 tool_result 聚为卡片
        const a: UIMessage = { id: nid(), role: "assistant", text: "", reasoning: "", tools: [] };
        for (const b of m.content) {
          if (b.type === "text") a.text += (a.text ? "\n\n" : "") + (b.text ?? "");
          else if (b.type === "tool_use")
            a.tools.push({
              id: b.tool_id ?? nid(), name: b.tool_name ?? "",
              argsText: typeof b.input === "string" ? b.input : JSON.stringify(b.input ?? {}),
              done: true, startedAt: 0,
            });
          else if (b.type === "tool_result" && a.tools.length > 0) {
            const t = a.tools[a.tools.length - 1];
            if (!t.result) {
              t.result = b.output ?? "";
              t.isError = b.is_error ?? false;
            }
          }
        }
        if (a.text || a.reasoning || a.tools.length) msgs.push(a);
      }
      set({ messages: msgs });
    } catch {
      // 镜像拉取失败静默(WS 增量仍可用)
    }
  },

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
          // todo 面板聚合:todo_write 的结果即最新全量清单(实时推送形态)
          let todos = st.todos;
          if (m.name === "todo_write" && !m.is_error) {
            try {
              const parsed = JSON.parse(m.result);
              const arr = Array.isArray(parsed) ? parsed : parsed?.todos;
              if (Array.isArray(arr)) {
                todos = arr
                  .filter((x) => x && typeof x.id === "string" && typeof x.content === "string")
                  .map((x) => ({
                    id: x.id,
                    content: x.content,
                    status: (x.status === "done" || x.status === "in_progress" ? x.status : "pending") as TodoItem["status"],
                  }));
              }
            } catch {
              /* 非 JSON 结果保持旧态 */
            }
          }
          return { messages: msgs, todos };
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
        // §6.1 二层一等流(SubAgentID 键;后端 emit 后自动激活,零事件不渲染)
        case "subagent_delta": {
          const prev = st.subagentStreams[m.id] ?? { id: m.id, text: "", reasoning: "", done: false };
          const cur = { ...prev };
          if (m.kind === "reasoning") cur.reasoning += m.text;
          else cur.text += m.text;
          return { subagentStreams: { ...st.subagentStreams, [m.id]: cur } };
        }
        case "subagent_done": {
          const prev = st.subagentStreams[m.id] ?? { id: m.id, text: "", reasoning: "", done: false };
          return {
            subagentStreams: {
              ...st.subagentStreams,
              [m.id]: { ...prev, done: true, result: m.result, isError: m.is_error },
            },
          };
        }
      }
    }),
}));
