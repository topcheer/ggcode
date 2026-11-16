import { useEffect, useState } from "react";
import type { ListDetailData, PanelDirective } from "./types";
import { submitCommand } from "./types";
import { adaptListDetail } from "./adapters";

type Item = { id: string; title: string; subtitle?: string; badges?: string[] };

/** ListDetailPanel:左列表右详情双栏;行内动作回填命令(sessions resume 等) */
export default function ListDetailPanel({ panel, onClose }: { panel: PanelDirective; onClose: () => void }) {
  const [data, setData] = useState<ListDetailData | null>(null);
  const [err, setErr] = useState("");
  const [sel, setSel] = useState(0);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    fetch(panel.dataUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`数据拉取失败 (${r.status})`))))
      // 异形端点归一:cron 顶层数组/skills 双键/mcp 嵌套 → {items}
      .then((raw: unknown) => setData(adaptListDetail(raw, panel.dataUrl)))
      .catch((e: Error) => setErr(e.message || String(e)));
  }, [panel.dataUrl]);

  const items: Item[] = (data?.items ?? []) as Item[];

  const act = async (item: Item) => {
    const cmd = data?.submit?.command ?? panel.cmdPrefix;
    if (!cmd || busy) return;
    setBusy(true);
    try {
      await submitCommand(`${cmd} ${item.id}`);
      onClose();
    } catch (e) {
      setErr((e as Error).message || String(e));
      setBusy(false);
    }
  };

  if (err) return <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{err}</div>;
  if (!data) return <div className="p-4 text-ui-sm text-foreground-subtle">加载中…</div>;

  return (
    <div className="flex max-h-[70vh] gap-3">
      <div className="flex w-1/2 flex-col gap-1 overflow-y-auto pr-1">
        {items.length === 0 && <div className="p-3 text-ui-sm text-foreground-subtle">暂无条目</div>}
        {items.map((it, i) => (
          <button
            key={it.id ?? i}
            className={`rounded-xl border p-2.5 text-left ${
              sel === i ? "border-accent bg-accent-surface" : "border-border bg-surface hover:bg-surface-hover"
            }`}
            onMouseEnter={() => setSel(i)}
            onClick={() => setSel(i)}
          >
            <div className="truncate text-ui-sm font-medium">{it.title ?? it.id}</div>
            {it.subtitle && <div className="truncate text-ui-xs text-foreground-subtle">{it.subtitle}</div>}
          </button>
        ))}
      </div>
      <div className="flex w-1/2 flex-col gap-2 overflow-y-auto rounded-xl border border-border bg-surface p-3">
        {items[sel] ? (
          <>
            <div className="text-ui-base font-semibold">{items[sel].title ?? items[sel].id}</div>
            {items[sel].subtitle && <div className="text-ui-sm text-foreground-subtle">{items[sel].subtitle}</div>}
            {items[sel].badges?.map((b) => (
              <span key={b} className="w-fit rounded-full bg-surface-hover px-2 py-0.5 text-ui-xs text-foreground-subtle">{b}</span>
            ))}
            {(data?.submit?.command ?? panel.cmdPrefix) && (
              <button
                className="mt-auto w-fit rounded-lg bg-accent px-3 py-1.5 text-ui-sm text-accent-fg disabled:opacity-50"
                disabled={busy}
                onClick={() => void act(items[sel])}
              >
                {panel.cmdPrefix === "/resume" ? "切换到此会话" : "选择"}
              </button>
            )}
          </>
        ) : (
          <div className="text-ui-sm text-foreground-subtle">—</div>
        )}
      </div>
    </div>
  );
}
