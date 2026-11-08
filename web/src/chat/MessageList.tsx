import { useEffect, useRef, useState } from "react";
import { useChat, type UIMessage } from "@/store/chat";
import MessageBubble from "./MessageBubble";

/** 消息列表:跟随语义继承 TUI List.Follow(§4.5 贴底自动滚、离底不打扰) */
/** 消息列表:跟随语义继承 TUI List.Follow + 窗口裁剪继承 SetMaxItems(上限防内存累积) */
const MAX_RENDER = 200;

export default function MessageList() {
  const messages = useChat((s) => s.messages);
  const busy = useChat((s) => s.busy);
  const [windowStart, setWindowStart] = useState(0);
  // 超上限时裁剪渲染窗口(保留完整 messages 供导出/回放,仅渲染层裁剪)
  const effectiveStart = Math.max(windowStart, messages.length - MAX_RENDER);
  const visible = messages.slice(effectiveStart);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const followRef = useRef(true);

  // 贴底判定:滚动离开底部即暂停跟随,回到底部恢复
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const onScroll = () => {
      followRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  // 新消息时若处于跟随态则滚到底
  useEffect(() => {
    if (followRef.current) bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  return (
    <div ref={containerRef} className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[860px] px-6 py-8 flex flex-col gap-6">
        {effectiveStart > 0 && (
          <button
            className="text-ui-xs rounded-lg border border-border py-2 text-foreground-subtle hover:bg-hover"
            onClick={() => setWindowStart(Math.max(0, effectiveStart - MAX_RENDER))}
          >
            加载更早消息({effectiveStart} 条已折叠)
          </button>
        )}
        {visible.length === 0 && (
          <p className="text-ui-sm mt-24 text-center text-foreground-subtlest">
            发送第一条消息开始会话
          </p>
        )}
        {visible.map((m) => (
          <MessageBubble key={m.id} msg={m} />
        ))}
        {busy && (
          <div className="flex items-center gap-1.5 py-2" aria-label="thinking">
            {[0, 1, 2].map((i) => (
              <span
                key={i}
                className="inline-block h-1.5 w-1.5 rounded-full animate-pulse"
                style={{ background: "var(--color-foreground-subtle)", animationDelay: `${i * 0.18}s` }}
              />
            ))}
            <span className="text-ui-xs ml-1 text-foreground-subtlest">思考中…</span>
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

export type { UIMessage };
