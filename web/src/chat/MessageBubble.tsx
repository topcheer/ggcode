import { useChat, type UIMessage } from "@/store/chat";
import ToolCallCard from "./ToolCallCard";
import ReasoningBlock from "./ReasoningBlock";

/** 消息气泡:user 极淡底(§2)/assistant 全宽 markdown/工具卡内嵌 */
export default function MessageBubble({ msg }: { msg: UIMessage }) {
  if (msg.role === "system") {
    return (
      <div className="text-ui-xs rounded-lg bg-[var(--color-surface)] px-3 py-2 text-center text-foreground-subtle">
        {msg.text}
      </div>
    );
  }

  if (msg.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="text-ui-base text-wrap-phrase max-w-[85%] rounded-2xl bg-[var(--color-user-message-bubble)] px-4 py-2.5 whitespace-pre-wrap">
          {msg.text}
        </div>
      </div>
    );
  }

  return (
    <div className="text-wrap-phrase flex flex-col gap-3" style={{ animation: "message-enter 120ms ease-out" }}>
      {msg.reasoning && <ReasoningBlock text={msg.reasoning} streaming={msg.streaming} />}

      {msg.tools.map((t) => (
        <ToolCallCard key={t.id || t.name + t.startedAt} tool={t} />
      ))}

      {msg.text && (
        <MarkdownLite text={msg.text} />
      )}

      {msg.streaming && !msg.text && !msg.reasoning && msg.tools.length === 0 && (
        <div className="text-ui-sm flex items-center gap-2 text-foreground-subtle">
          <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-brand border-t-transparent" />
          思考中…
        </div>
      )}

      {msg.usage && (
        <div className="text-ui-xs text-foreground-subtlest">
          ↑{msg.usage.input_tokens} ↓{msg.usage.output_tokens} tokens
        </div>
      )}
    </div>
  );
}

/** P1 轻量 markdown:段落+行内代码+代码块;P1.5 换 streamdown 同类流式渲染器 */
function MarkdownLite({ text }: { text: string }) {
  const parts = text.split(/(```[\s\S]*?```)/g);
  return (
    <div className="text-ui-base flex flex-col gap-2 leading-relaxed">
      {parts.map((p, i) =>
        p.startsWith("```") ? (
          <pre
            key={i}
            className="text-ui-sm overflow-x-auto rounded-xl border border-border bg-[var(--color-input)] p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap"
          >
            {p.replace(/^```\w*\n?/, "").replace(/```$/, "")}
          </pre>
        ) : (
          <p key={i} className="whitespace-pre-wrap">
            {p}
          </p>
        ),
      )}
      {/* 流式尾光标 */}
      <StreamCursor />
    </div>
  );
}

function StreamCursor() {
  const busy = useChat((s) => s.busy);
  const streaming = useChat((s) => s.messages.some((m) => m.streaming));
  if (!busy && !streaming) return null;
  return <span className="ml-0.5 inline-block h-4 w-2 animate-pulse bg-brand align-text-bottom" />;
}
