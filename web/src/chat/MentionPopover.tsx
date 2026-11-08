import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/api/client";

/** @mention 弹层(§4.3):输入 @ 触发;三类节点彩色 token(§3 mention 色族) */
type MentionKind = "file" | "skill" | "agent";

const KIND_META: Record<MentionKind, { label: string; cssVar: string }> = {
  file: { label: "文件", cssVar: "--color-file-node-fg" },
  skill: { label: "技能", cssVar: "--color-skill-node-fg" },
  agent: { label: "Agent", cssVar: "--color-agent-node-fg" },
};

export default function MentionPopover({
  query,
  anchorRect,
  onPick,
}: {
  query: string; // @ 后的过滤词
  anchorRect: DOMRect | null;
  onPick: (text: string) => void;
}) {
  const [kind, setKind] = useState<MentionKind>("file");
  const inputRef = useRef<HTMLInputElement>(null);
  // file 类:接 /api/workspace/files 真 fuzzy(P4-1);防抖 150ms
  const [fileHits, setFileHits] = useState<string[]>([]);

  useEffect(() => {
    if (kind !== "file") {
      setFileHits([]);
      return;
    }
    const h = setTimeout(() => {
      api
        .get<string[]>(`/api/workspace/files?q=${encodeURIComponent(query)}`)
        .then((d) => setFileHits(d))
        .catch(() => setFileHits([]));
    }, 150);
    return () => clearTimeout(h);
  }, [query, kind]);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const suggestions = useMemo(
    () => [
      { kind: "file" as const, text: "@file:", hint: "引用工作区文件路径" },
      { kind: "skill" as const, text: "@skill:", hint: "调用已安装技能" },
      { kind: "agent" as const, text: "@agent:", hint: "委派 named agent" },
    ].filter((s) => query === "" || s.kind.startsWith(query) || KIND_META[s.kind].label.includes(query)),
    [query],
  );

  if (!anchorRect) return null;

  return (
    <div
      className="fixed z-50 w-80 rounded-xl border border-border bg-popover p-2 shadow-xl"
      style={{ left: anchorRect.left, top: anchorRect.top - 130 }}
    >
      <div className="mb-1.5 flex gap-1">
        {(Object.keys(KIND_META) as MentionKind[]).map((k) => (
          <button
            key={k}
            className="text-ui-xs rounded-md px-2 py-1 transition-colors"
            style={{
              background: `var(--color-${k}-node)`,
              color: `var(${KIND_META[k].cssVar})`,
              opacity: kind === k ? 1 : 0.55,
            }}
            onClick={() => setKind(k)}
          >
            {KIND_META[k].label}
          </button>
        ))}
      </div>
      <input
        ref={inputRef}
        className="text-ui-sm mb-1.5 w-full rounded-lg border border-border bg-input px-2 py-1.5 text-foreground focus:outline-none"
        placeholder={`输入${KIND_META[kind].label}名或路径…`}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            const v = inputRef.current?.value.trim();
            if (v) {
              onPick(`@${kind}:${v} `);
            }
          } else if (e.key === "Escape") {
            onPick("");
          }
        }}
      />
      <ul className="flex max-h-40 flex-col gap-0.5 overflow-y-auto">
        {kind === "file" &&
          fileHits.map((f) => (
            <li key={f}>
              <button
                className="text-ui-xs w-full truncate rounded-md px-2 py-1.5 text-left font-[family-name:var(--font-mono)] hover:bg-hover"
                onClick={() => onPick(`@file:${f} `)}
              >
                <span style={{ color: `var(${KIND_META.file.cssVar})` }}>@file:{f}</span>
              </button>
            </li>
          ))}
        {!(kind === "file" && fileHits.length > 0) &&
          suggestions.map((s) => (
          <li key={s.kind}>
            <button
              className="text-ui-xs w-full rounded-md px-2 py-1.5 text-left hover:bg-hover"
              onClick={() => onPick(s.text)}
            >
              <span style={{ color: `var(${KIND_META[s.kind].cssVar})` }}>{s.text}</span>
              <span className="ml-2 text-foreground-subtlest">{s.hint}</span>
            </button>
          </li>
        ))}
      </ul>
      <p className="text-ui-xs mt-1 px-2 text-foreground-subtlest">Enter 插入 · Esc 关闭</p>
    </div>
  );
}
