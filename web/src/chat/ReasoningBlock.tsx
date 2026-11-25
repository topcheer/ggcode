import { useEffect, useRef, useState } from "react";
import NavIcon from "@/components/NavIcon";

/**
 * 思考块(§4.5):以"旁注"形态存在——比正文与工具卡更轻,不抢注意力:
 * 无外框、仅一条细竖线做引导,标签与正文都用可读的中性弱色(深色主题下
 * 原先的 #7c3aed 紫字压在紫底上几乎不可辨)。流式期间展开并贴底,完成后收起为一行。
 */
export default function ReasoningBlock({ text, streaming }: { text: string; streaming?: boolean }) {
  const [open, setOpen] = useState(true);
  const bodyRef = useRef<HTMLDivElement>(null);

  // 流式期间自动贴底;完成后收起
  useEffect(() => {
    if (streaming && bodyRef.current) bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
  }, [text, streaming]);
  useEffect(() => {
    if (!streaming) setOpen(false);
  }, [streaming]);

  const lines = text.split("\n").length;

  return (
    <div className="border-l-2 border-[var(--color-border)] pl-3">
      <button
        type="button"
        className="text-ui-xs flex w-full items-center gap-1.5 py-0.5 text-left text-foreground-subtle transition-colors hover:text-foreground"
        onClick={() => setOpen(!open)}
        title={open ? "收起思考过程" : "展开思考过程"}
      >
        <NavIcon name="reasoning" className="h-3.5 w-3.5 shrink-0" />
        <span>思考过程</span>
        {streaming ? (
          <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-current opacity-70" />
        ) : (
          !open && <span className="font-[family-name:var(--font-mono)] opacity-80">{lines} 行</span>
        )}
        <span className="ml-auto select-none">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div
          ref={bodyRef}
          className="text-ui-sm max-h-56 overflow-y-auto py-1 text-wrap-phrase whitespace-pre-wrap leading-relaxed text-foreground-subtle"
        >
          {text}
        </div>
      )}
    </div>
  );
}
