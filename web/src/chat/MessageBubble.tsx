import { memo } from "react";
import { useChat, type UIMessage } from "@/store/chat";
import ToolCallCard from "./ToolCallCard";
import ReasoningBlock from "./ReasoningBlock";

/** 消息气泡:user 极淡底(§2)/assistant 全宽 markdown/工具卡内嵌 */
export default function MessageBubble({ msg }: { msg: UIMessage }) {
  if (msg.role === "system") {
    return (
      <div className="text-ui-xs rounded-lg bg-[var(--color-surface)] px-3 py-2 text-center text-foreground-subtle">
        {/* 策略师响应走 system 通道:markdown 渲染(派修件——裸 {msg.text} 丢语法结构) */}
        <MarkdownLite text={msg.text} />
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

/** 流式 markdown 增量块渲染(streamdown 同类方案,§4.1):
 * 分块按空行切分,```/~~~ fence 吞噬为不可分区域(继承旧 chatStreamBlocks 契约);
 * memo 稳定块缓存——文本未变的块不重刷,流式期间仅尾块重渲染,零闪烁。 */

function splitMarkdownBlocks(text: string): string[] {
  const blocks: string[] = [];
  const lines = text.split("\n");
  let cur: string[] = [];
  let fence: string | null = null; // 当前未闭合的 fence 标记(``` 或 ~~~)
  for (const line of lines) {
    const m = line.match(/^\s*(```|~~~)/);
    if (m) {
      if (fence === null) fence = m[1];
      else if (fence === m[1]) fence = null;
    }
    if (fence !== null) {
      cur.push(line);
      continue;
    }
    if (line.trim() === "" && cur.length > 0) {
      blocks.push(cur.join("\n"));
      cur = [];
    } else if (line.trim() !== "") {
      cur.push(line);
    }
  }
  if (cur.length > 0) blocks.push(cur.join("\n"));
  return blocks;
}

function MarkdownLite({ text }: { text: string }) {
  const blocks = splitMarkdownBlocks(text);
  return (
    <div className="text-ui-base flex flex-col gap-2 leading-relaxed">
      {blocks.map((b, i) => (
        <MarkdownBlock key={i} text={b} isLast={i === blocks.length - 1} />
      ))}
      <StreamCursor />
    </div>
  );
}

/** 稳定块:memo 下 text 不变则跳过重渲染(旧契约 chatStreamBlocks[reused].text === blocks[reused] 的 React 对应物) */
const MarkdownBlock = memo(function MarkdownBlock({ text, isLast }: { text: string; isLast: boolean }) {
  if (/^(```|~~~)/m.test(text)) {
    // fence 块(可能流式中未闭合)
    return (
      <pre className="text-ui-sm overflow-x-auto rounded-xl border border-border bg-[var(--color-input)] p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap">
        {text.replace(/^```\w*\n?/, "").replace(/```$/, "")}
      </pre>
    );
  }
  const lines = text.split("\n");
  return (
    <div className="flex flex-col gap-1">
      {lines.map((line, i) => (
        <MarkdownLine key={i} line={line} />
      ))}
      {isLast && null}
    </div>
  );
});

/** 行内 markdown:粗体/斜体/行内代码/链接(轻量实现,禁 dangerouslySetInnerHTML) */
function MarkdownLine({ line }: { line: string }) {
  const heading = line.match(/^(#{1,6})\s+(.*)/);
  if (heading) {
    const lvl = heading[1].length;
    const cls = lvl <= 2 ? "text-ui-lg font-semibold" : lvl <= 4 ? "text-ui-base font-semibold" : "text-ui-sm font-semibold";
    return <p className={cls}>{inline(heading[2])}</p>;
  }
  const li = line.match(/^\s*[-*]\s+(.*)/);
  if (li) {
    return (
      <p className="flex gap-2">
        <span className="text-brand">•</span>
        <span className="min-w-0">{inline(li[1])}</span>
      </p>
    );
  }
  return <p className="whitespace-pre-wrap">{inline(line)}</p>;
}

function inline(s: string): React.ReactNode[] {
  // 顺序:行内代码 `x` → 粗体 **x** → 斜体 *x* → 链接 [t](u)
  const parts: React.ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*)|(\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(s)) !== null) {
    if (m.index > last) parts.push(s.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("`")) {
      parts.push(
        <code key={k++} className="rounded-md bg-[var(--color-tag,#2a2a34)] px-1.5 py-0.5 font-[family-name:var(--font-mono)] text-[0.92em]">
          {tok.slice(1, -1)}
        </code>,
      );
    } else if (tok.startsWith("**")) {
      parts.push(<strong key={k++}>{tok.slice(2, -2)}</strong>);
    } else if (tok.startsWith("[")) {
      const lm = tok.match(/\[([^\]]+)\]\(([^)]+)\)/)!;
      parts.push(
        <a key={k++} href={lm[2]} target="_blank" rel="noreferrer" className="text-brand underline decoration-dashed underline-offset-2">
          {lm[1]}
        </a>,
      );
    } else {
      parts.push(<em key={k++}>{tok.slice(1, -1)}</em>);
    }
    last = m.index + tok.length;
  }
  if (last < s.length) parts.push(s.slice(last));
  return parts;
}

function StreamCursor() {
  const busy = useChat((s) => s.busy);
  const streaming = useChat((s) => s.messages.some((m) => m.streaming));
  if (!busy && !streaming) return null;
  return <span className="ml-0.5 inline-block h-4 w-2 animate-pulse bg-brand align-text-bottom" />;
}
