import { create } from "zustand";
import type { AskUserRequest } from "@/ws/client";

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
  name: string; // 原始工具名(run_command / mcp__srv__tool)
  argsText: string; // 累积的 arguments JSON 字符串
  displayName?: string; // 服务端展示名(tool.DescribeTool,与 TUI/desktop 同源)
  detail?: string; // 一行目标(文件/命令/pattern)
  result?: string; // 原始结果文本
  resultSummary?: string; // 结果摘要(tool.DescribeToolResult 或前端推导),折叠态徽章
  resultPayload?: string; // 按模式渲染的载荷(缺省回退 result)
  payloadMode?: string; // text | json | task_list | cron_list ...
  isError?: boolean;
  done: boolean;
  startedAt: number;
  endedAt?: number;
};

type HistoryBlock = {
  type: string;
  text?: string;
  reasoning?: string;
  tool_name?: string;
  tool_id?: string;
  input?: unknown;
  display_name?: string;
  detail?: string;
  output?: string;
  is_error?: boolean;
  summary?: string;
  payload?: string;
  payload_mode?: string;
};

/** 服务端展示字段 → 卡片;缺省时用 prettify 兜底(历史/旧事件) */
function applyToolDisplay(t: ToolInvocation, b: { display_name?: string; detail?: string; tool_name?: string }) {
  if (b.display_name) t.displayName = b.display_name;
  if (b.detail) t.detail = b.detail;
}

function applyToolResult(t: ToolInvocation, b: { output?: string; is_error?: boolean; summary?: string; payload?: string; payload_mode?: string }) {
  t.result = b.output ?? "";
  t.isError = b.is_error ?? false;
  t.resultSummary = b.summary || undefined;
  t.resultPayload = b.payload || undefined;
  t.payloadMode = b.payload_mode || undefined;
  t.done = true;
}

type ConnState = "connecting" | "open" | "closed";

/** 待处理的 ask-type gate (#5865):工具审批或 ask_user 问卷,键=id,按到达顺序 */
export type Interaction = {
  id: string;
  kind: "approval" | "ask_user";
  tool_name?: string;
  input?: string;
  request?: AskUserRequest;
};

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
  queueItems: string[]; // 插入消息队列镜像(append/dequeue/clear/list 四事件同步)
  lastError: string | null;
  subagentStreams: Record<string, SubagentStream>; // §6.1 二层:活跃/完结子代理流
  todos: TodoItem[]; // agent loop 运行时实时推送聚合(todo_write tool_result 解析)
  cronEvents: { text: string; at: string }[]; // [cron] 前缀系统消息归集(右栏 Cron tab)
  interactions: Interaction[]; // 待应答的审批/问卷(interaction 事件,resolved 时移除)
  loadHistory: () => Promise<void>;
  syncQueue: (items: string[]) => void; // 会话切换/重连后重拉队列快照
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
  queueItems: [],
  lastError: null,
  subagentStreams: {},
  todos: [],
  cronEvents: [],
  interactions: [],

  setConn: (conn) => set({ conn }),

  // TUI session 镜像(原实现语义,已核 server_websocket.go:42-47):
  // /api/chat/history 返回当前 chatBridge/agent 会话全量,前端镜像它而非独立会话。
  loadHistory: async () => {
    try {
      const res = await fetch("/api/chat/history");
      if (!res.ok) return;
      const data = (await res.json()) as Array<{ role: Role; content: HistoryBlock[] }>;
      const msgs: UIMessage[] = [];
      // tool_result 位于下一条 user 角色消息(provider 格式):按 tool_id 回填到发起调用的卡片
      const toolById = new Map<string, ToolInvocation>();
      for (const m of data) {
        const toolResults = m.content.filter((b) => b.type === "tool_result");
        const rest = m.content.filter((b) => b.type !== "tool_result");
        for (const b of toolResults) {
          const t = b.tool_id ? toolById.get(b.tool_id) : undefined;
          // 无 id(旧会话)时退回"最后一个未完成的同名调用"
          const fallback = !t
            ? [...toolById.values()].reverse().find((x) => x.name === b.tool_name && !x.done)
            : undefined;
          const target = t ?? fallback;
          if (target) applyToolResult(target, b);
        }
        if (m.role === "user") {
          // 只有真正带文本的 user 消息才是用户气泡(纯 tool_result 的 user 消息不渲染)
          const text = rest.map((b) => b.text ?? "").join("");
          if (text.trim()) msgs.push({ id: nid(), role: "user", text, reasoning: "", tools: [] });
          continue;
        }
        // assistant:文本/思考聚为一个气泡,tool_use 聚为卡片
        const a: UIMessage = { id: nid(), role: "assistant", text: "", reasoning: "", tools: [] };
        for (const b of rest) {
          if (b.type === "text") a.text += (a.text ? "\n\n" : "") + (b.text ?? "");
          else if (b.type === "thinking" || b.type === "redacted_thinking") a.reasoning += b.reasoning ?? "";
          else if (b.type === "tool_use") {
            const t: ToolInvocation = {
              id: b.tool_id || nid(), name: b.tool_name ?? "",
              argsText: typeof b.input === "string" ? b.input : JSON.stringify(b.input ?? {}),
              done: true, startedAt: 0,
            };
            applyToolDisplay(t, b);
            a.tools.push(t);
            if (b.tool_id) toolById.set(b.tool_id, t);
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

  reset: () => set({ messages: [], busy: false, lastError: null, interactions: [] }),

  syncQueue: (items) => set({ queueItems: items }),

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
          else {
            t.argsText = m.arguments; // done 事件带完整参数
            if (m.display_name) t.displayName = m.display_name;
            if (m.detail) t.detail = m.detail;
          }
          return { messages: msgs };
        }
        case "tool_result": {
          const msgs = [...st.messages];
          // tool_result 只带 name,匹配最后一个同名未完成调用
          // 优先按 id 精确匹配(并行同名调用不串);无 id(旧事件)退回最后一个同名未完成调用
          let hit: ToolInvocation | undefined;
          for (let i = msgs.length - 1; i >= 0 && !hit; i--) {
            hit = m.id
              ? msgs[i].tools.find((x) => x.id === m.id)
              : msgs[i].tools.find((x) => x.name === m.name && !x.done);
          }
          if (hit) {
            if (m.display_name) hit.displayName = m.display_name;
            if (m.detail) hit.detail = m.detail;
            applyToolResult(hit, m);
            hit.endedAt = Date.now();
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
          // [cron] 前缀 → 归集到右栏 Cron tab(最多留 50 条)
          let cronEvents = st.cronEvents;
          if (m.text.startsWith("[cron] ")) {
            cronEvents = [{ text: m.text.slice(7), at: new Date().toLocaleTimeString() }, ...st.cronEvents].slice(0, 50);
          }
          return { messages: msgs, cronEvents };
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
        case "interaction": {
          // 审批/问卷到达(含连接时回放):同 id 去重,保持到达顺序
          if (st.interactions.some((i) => i.id === m.id)) return {};
          return {
            interactions: [
              ...st.interactions,
              { id: m.id, kind: m.kind, tool_name: m.tool_name, input: m.input, request: m.request },
            ],
          };
        }
        case "interaction_resolved":
          // 任一前端(TUI 键盘/IM/移动端/本页)已应答 → 清除
          return { interactions: st.interactions.filter((i) => i.id !== m.id) };
        case "interaction_ack":
          return {};
        case "queue": {
          // 插入消息队列镜像:TUI 排队全链三时机事件+连接时 list 快照
          switch (m.event) {
            case "append":
              return { queueItems: [...st.queueItems, m.text ?? ""] };
            case "dequeue": {
              // 按文本移除首条匹配(队列无 id 语义;重复文本罕见可接受)
              const idx = st.queueItems.indexOf(m.text ?? "");
              if (idx < 0) return {};
              const next = [...st.queueItems];
              next.splice(idx, 1);
              return { queueItems: next };
            }
            case "clear":
              return { queueItems: [] };
            case "list":
              return { queueItems: m.items ?? [] };
            default:
              return {};
          }
        }
      }
    }),
}));
