import { useEffect, useRef, useState } from "react";
import { useChat } from "@/store/chat";
import MentionPopover from "@/chat/MentionPopover";

/** Composer(§4.3):发光活跃态+⌘⏎发送+Esc 中断面;P1.5 加 @mention 弹层 */
export default function Composer({
  onSend,
}: {
  onSend: (text: string, images?: string[]) => void;
}) {
  const [value, setValue] = useState("");
  const [focused, setFocused] = useState(false);
  const [pastedImages, setPastedImages] = useState<string[]>([]); // dataURL 缩略图预览(P4-4)
  const busy = useChat((s) => s.busy);
  const conn = useChat((s) => s.conn);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const [mention, setMention] = useState<{ query: string; at: number; rect: DOMRect | null } | null>(null);

  // 自适应高度
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
  }, [value]);

  // 发光呼吸(仅 focus 且非 busy)
  useEffect(() => {
    const el = taRef.current?.closest("[data-composer]") as HTMLElement | null;
    if (!el) return;
    el.style.animation = focused && !busy ? "composer-breathe 2.4s ease-in-out infinite" : "";
    el.style.boxShadow = focused || busy ? "" : "var(--shadow-composer-idle)";
  }, [focused, busy]);

  const submit = () => {
    const text = value.trim();
    // #5300: 断线不再禁发——sendUser 非 OPEN 入队、onopen 补发(client.ts §4.4)
    if ((!text && pastedImages.length === 0) || busy) return;
    onSend(text, pastedImages.length ? pastedImages : undefined);
    setValue("");
    setPastedImages([]);
  };

  // 截图/图片粘贴(P4-4):读为 dataURL 缩略图,发送时附 images
  const onPaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    e.preventDefault();
    for (const f of files.slice(0, 4)) {
      const rd = new FileReader();
      rd.onload = () => setPastedImages((prev) => [...prev, String(rd.result)]);
      rd.readAsDataURL(f);
    }
  };

  return (
    <div className="border-t border-border bg-background px-6 py-4">
      <div
        data-composer
        className="mx-auto max-w-[860px] rounded-2xl border border-border bg-card p-3 transition-shadow"
      >
        <textarea
          ref={taRef}
          className="text-ui-base w-full resize-none bg-transparent text-wrap-phrase text-foreground placeholder:text-foreground-subtlest focus:outline-none"
          placeholder="输入消息…(⌘⏎ 发送 / Shift+⏎ 换行)"
          rows={2}
          value={value}
          onPaste={onPaste}
          onChange={(e) => {
            setValue(e.target.value);
            // @ 触发 mention 弹层(§4.3)
            const upto = e.target.value.slice(0, e.target.selectionStart ?? 0);
            const at = upto.lastIndexOf("@");
            if (at >= 0 && !upto.slice(at).includes(" ")) {
              setMention({ query: upto.slice(at + 1), at, rect: taRef.current?.getBoundingClientRect() ?? null });
            } else {
              setMention(null);
            }
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              submit();
            }
          }}
        />
        {pastedImages.length > 0 && (
          <div className="mb-2 flex gap-2">
            {pastedImages.map((src, i) => (
              <div key={i} className="relative">
                <img src={src} alt="paste" className="h-16 w-16 rounded-lg border border-border object-cover" />
                <button
                  className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full border border-border bg-popover text-ui-xs text-foreground-subtle"
                  onClick={() => setPastedImages((p) => p.filter((_, j) => j !== i))}
                  title="移除"
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        )}
        {mention && (
          <MentionPopover
            query={mention.query}
            anchorRect={mention.rect}
            onPick={(text) => {
              setMention(null);
              if (!text) return;
              // #5299: 替换触发性 @+query,而非末尾 append(曾必产 @@file: 畸形 token)
              const m = mention;
              setValue((v) => {
                if (!m) return v + text;
                return v.slice(0, m.at) + text + v.slice(m.at + 1 + m.query.length);
              });
              const pos = (m?.at ?? value.length) + text.length;
              taRef.current?.focus();
              requestAnimationFrame(() => taRef.current?.setSelectionRange(pos, pos));
            }}
          />
        )}
        <div className="flex items-center justify-between pt-1">
          <span className="text-ui-xs text-foreground-subtlest">
            {conn !== "open" ? "离线中,消息将在重连后补发" : busy ? "生成中…(等待完成)" : "⌘⏎ 发送"}
          </span>
          <button
            className="text-ui-sm rounded-lg bg-brand px-4 py-1.5 font-medium text-white transition-opacity disabled:opacity-40"
            disabled={!value.trim() || busy}
            onClick={submit}
          >
            发送
          </button>
        </div>
      </div>
    </div>
  );
}
