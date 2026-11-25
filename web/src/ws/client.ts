// WS 客户端 — 契约对齐 internal/webui/server_websocket.go（已核）
// 下行: text_delta|reasoning_delta|tool_call_chunk|tool_call|tool_result|system|done{usage?}|error{error}|user_ack{text}
//       interaction{kind,id,tool_name?,input?,request?} | interaction_resolved{kind,id,decision} | interaction_ack{id}  (#5865 审批/问卷)
// 上行: {type:"user_message", text, images?, files?}
//       {type:"interaction_response", id, decision, answers?}  （应答待处理的审批/问卷）
// 服务端 30s ping(浏览器自动 pong), 90s 读超时, busy 时回 error

export type StreamMsg =
  | { type: "text_delta"; text: string }
  | { type: "reasoning_delta"; text: string }
  | { type: "tool_call_chunk"; id: string; name: string; arguments: string }
  // done 事件附带服务端展示名/目标(tool.DescribeTool),卡片永不渲染裸工具名
  | { type: "tool_call"; id: string; name: string; arguments: string; display_name?: string; detail?: string }
  // 结果按 id 精确关联;summary/payload/payload_mode 为结果展示形态(tool.DescribeToolResult)
  | { type: "tool_result"; id?: string; name: string; result: string; is_error: boolean; display_name?: string; detail?: string; summary?: string; payload?: string; payload_mode?: string }
  | { type: "system"; text: string }
  | { type: "done"; usage?: { input_tokens: number; output_tokens: number } }
  | { type: "error"; error: string }
  | { type: "user_ack"; text: string }
  // 二层契约(§6.1 实时流):后端 emit 后前端自动激活;零事件=完全降级不变
  | { type: "subagent_delta"; id: string; kind: "text" | "reasoning"; text: string }
  | { type: "subagent_done"; id: string; result: string; is_error: boolean }
  // 插入消息队列三事件(append/dequeue/clear)+连接时 list 快照——镜像 TUI 排队区
  | { type: "queue"; event: "append" | "dequeue" | "clear" | "list"; text?: string; items?: string[]; count?: number }
  // ask-type gates (#5865): approval | ask_user, replayed on connect
  | { type: "interaction"; kind: "approval" | "ask_user"; id: string; tool_name?: string; input?: string; request?: AskUserRequest }
  | { type: "interaction_resolved"; kind: "approval" | "ask_user"; id: string; decision?: string }
  | { type: "interaction_ack"; id: string };

export type AskUserChoice = { id: string; label: string };
export type AskUserQuestion = {
  id: string; title: string; prompt: string; kind: "single" | "multi" | "text";
  choices?: AskUserChoice[]; allow_freeform?: boolean; placeholder?: string;
};
export type AskUserRequest = { title?: string; questions: AskUserQuestion[] };
export type InteractionAnswer = { question_id: string; choice_ids?: string[]; freeform_text?: string };

type Handlers = {
  onMsg: (m: StreamMsg) => void;
  onState: (s: "connecting" | "open" | "closed") => void;
};

export class ChatWS {
  private ws: WebSocket | null = null;
  private retry = 0;
  private queue: string[] = []; // 断线补发队列(§4.4 乐观 UI)
  private closedByUser = false;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private url: string, private h: Handlers) {
    this.connect();
  }

  private connect() {
    this.h.onState("connecting");
    // WS 握手鉴权(auth.go:WebSocket 路由接受 query token;浏览器 WS API 无法自定义
    // Authorization 头)。token 由 server_static.go authScript 在 #token= 首访时写入
    // sessionStorage——此处消费,缺失则裸连(服务器未启用鉴权时也可用)
    const tok = sessionStorage.getItem("ggcode-ws-token");
    const url =
      this.url +
      (tok ? (this.url.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(tok) : "");
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      this.h.onState("open");
      // 补发断线期间积压消息
      while (this.queue.length) {
        ws.send(this.queue.shift()!);
      }
    };
    ws.onmessage = (ev) => {
      try {
        this.h.onMsg(JSON.parse(ev.data) as StreamMsg);
      } catch {
        /* 非 JSON 帧忽略 */
      }
    };
    ws.onclose = () => {
      this.h.onState("closed");
      if (this.closedByUser) return;
      // 指数退避重连: 1s/2s/4s/8s 上限 8s(§7 验收:≤3s 恢复靠首次快速重试)
      const delay = Math.min(1000 * 2 ** this.retry++, 8000);
      this.timer = setTimeout(() => this.connect(), delay);
    };
    ws.onerror = () => ws.close();
  }

  sendUser(text: string, images?: unknown[], files?: unknown[]) {
    const payload = JSON.stringify({ type: "user_message", text, images, files });
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(payload);
    else this.queue.push(payload);
  }

  /** 应答待处理的审批/问卷 (#5865)。approval: allow|always_allow|deny;ask_user: submitted|cancelled */
  sendInteractionResponse(id: string, decision: string, answers?: InteractionAnswer[]) {
    const payload = JSON.stringify({ type: "interaction_response", id, decision, answers });
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(payload);
  }

  close() {
    this.closedByUser = true;
    if (this.timer) clearTimeout(this.timer);
    this.ws?.close();
  }
}
