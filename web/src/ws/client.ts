// WS 客户端 — 契约对齐 internal/webui/server_websocket.go（已核）
// 下行: text_delta|reasoning_delta|tool_call_chunk|tool_call|tool_result|system|done{usage?}|error{error}|user_ack{text}
// 上行: {type:"user_message", text, images?, files?}
// 服务端 30s ping(浏览器自动 pong), 90s 读超时, busy 时回 error

export type StreamMsg =
  | { type: "text_delta"; text: string }
  | { type: "reasoning_delta"; text: string }
  | { type: "tool_call_chunk"; id: string; name: string; arguments: string }
  | { type: "tool_call"; id: string; name: string; arguments: string }
  | { type: "tool_result"; name: string; result: string; is_error: boolean }
  | { type: "system"; text: string }
  | { type: "done"; usage?: { input_tokens: number; output_tokens: number } }
  | { type: "error"; error: string }
  | { type: "user_ack"; text: string };

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
    const ws = new WebSocket(this.url);
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

  close() {
    this.closedByUser = true;
    if (this.timer) clearTimeout(this.timer);
    this.ws?.close();
  }
}
