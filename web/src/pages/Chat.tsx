import { useEffect, useRef } from "react";
import { useChat } from "@/store/chat";
import { ChatWS } from "@/ws/client";
import MessageList from "@/chat/MessageList";
import ChatControls, { ModeToggle } from "@/chat/ChatControls";
import Composer from "@/chat/Composer";
import WelcomeScreen from "@/pages/WelcomeScreen";

/** Chat 页:TUI session 镜像(WS 增量+/api/chat/history 初始全量);ws=/api/chat/ws 同源提供 */
export default function Chat() {
  const messages = useChat((s) => s.messages);
  const conn = useChat((s) => s.conn);
  const lastError = useChat((s) => s.lastError);
  const queueItems = useChat((s) => s.queueItems);
  const applyMsg = useChat((s) => s.applyMsg);
  const setConn = useChat((s) => s.setConn);
  const loadHistory = useChat((s) => s.loadHistory);
  const pushOptimistic = useChat((s) => s.pushOptimistic);
  const wsRef = useRef<ChatWS | null>(null);
  const historyLoaded = useRef(false);

  useEffect(() => {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new ChatWS(`${proto}://${location.host}/api/chat/ws`, {
      onMsg: applyMsg,
      onState: (s) => {
        setConn(s);
        // 连接建立后拉一次当前 TUI session 全量(镜像语义);重连时仅在首次未成时补拉
        if (s === "open" && !historyLoaded.current) {
          historyLoaded.current = true;
          void loadHistory();
        }
      },
    });
    wsRef.current = ws;
    return () => ws.close();
    // applyMsg/setConn/loadHistory 来自 zustand 稳定引用
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const send = (text: string, images?: string[]) => {
    pushOptimistic(text);
    wsRef.current?.sendUser(text, images);
  };

  const hasMessages = messages.length > 0;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-end gap-2 border-b border-border px-4 py-1.5">
        <ChatControls />
      </div>
      {lastError && (
        <div className="text-ui-sm border-b border-[var(--color-destructive-surface)] bg-[var(--color-destructive-surface)] px-6 py-2 text-center text-[var(--color-destructive-surface-fg)]">
          {lastError}
        </div>
      )}
      {conn !== "open" && (
        <div className="text-ui-xs bg-[var(--color-warning-surface)] px-6 py-1.5 text-center text-[var(--color-warning-surface-fg)]">
          连接断开,自动重连中…(已输入消息将在恢复后补发)
        </div>
      )}
      {hasMessages ? (
        <>
          <div className="min-h-0 flex-1">
            <MessageList />
          </div>
          {queueItems.length > 0 && (
            <div className="border-t border-border px-4 py-1.5 text-ui-xs text-foreground-subtle">
              排队中 {queueItems.length} 条(本轮结束后依次发送)
              <div className="mt-0.5 truncate text-foreground-subtlest">{queueItems[queueItems.length - 1]}</div>
            </div>
          )}
          <Composer onSend={send} />
          <ModeToggle />
        </>
      ) : (
        <>
          <div className="min-h-0 flex-1">
            <WelcomeScreen embedded onSend={send} />
          </div>
        </>
      )}
    </div>
  );
}
