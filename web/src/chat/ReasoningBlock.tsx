import { useEffect, useRef, useState } from "react";

/** 思考块(§4.5):浅紫 reasoning token 底;流式展开,完成后收起为一行摘要 */
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

  return (
    <div className="rounded-xl border border-[rgba(124,58,237,.25)] bg-[rgba(124,58,237,.08)]">
      <button
        className="text-ui-xs flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-trajectory-reasoning)]"
        onClick={() => setOpen(!open)}
      >
        <span>💭 思考过程</span>
        {streaming && <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--color-trajectory-reasoning)]" />}
        <span className="ml-auto">{open ? "收起" : "展开"}</span>
      </button>
      {open && (
        <div
          ref={bodyRef}
          className="text-ui-sm max-h-56 overflow-y-auto px-3 pb-2 text-wrap-phrase whitespace-pre-wrap text-[var(--color-trajectory-reasoning)] opacity-80"
        >
          {text}
        </div>
      )}
    </div>
  );
}
