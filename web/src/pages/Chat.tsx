import { useEffect, useRef } from "react";
import { useChat } from "@/store/chat";
import { ChatWS } from "@/ws/client";
import MessageList from "@/chat/MessageList";
import Composer from "@/chat/Composer";
import WelcomeScreen from "@/pages/WelcomeScreen";

/** Chat 页:WS 接线+乐观 UI+错误横幅;ws=/api/chat/ws 由 vite proxy/embed 同源提供 */
export default function Chat() {
  const messages = useChat((s) => s.messages);
  const conn = useChat((s) => s.conn);
  const lastError = useChat((s) => s.lastError);
  const applyMsg = useChat((s) => s.applyMsg);
  const setConn = useChat((s) => s.setConn);
  const pushOptimistic = useChat((s) => s.pushOptimistic);
  const wsRef = useRef<ChatWS | null>(null);

  useEffect(() => {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new ChatWS(`${proto}://${location.host}/api/chat/ws`, {
      onMsg: applyMsg,
      onState: setConn,
    });
    wsRef.current = ws;
    return () => ws.close();
    // applyMsg/setConn 来自 zustand 稳定引用
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const send = (text: string) => {
    pushOptimistic(text);
    wsRef.current?.sendUser(text);
  };

  const hasMessages = messages.length > 0;

  return (
    <div className="flex h-full flex-col">
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
          <Composer onSend={send} />
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
