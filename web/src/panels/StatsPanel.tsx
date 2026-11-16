import { useEffect, useState } from "react";
import type { PanelDirective, StatsData } from "./types";

/** StatsPanel:只读仪表卡(sections×metrics,趋势箭头) */
export default function StatsPanel({ panel }: { panel: PanelDirective; onClose: () => void }) {
  const [data, setData] = useState<StatsData | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    fetch(panel.dataUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`数据拉取失败 (${r.status})`))))
      .then((d: StatsData) => setData(d))
      .catch((e: Error) => setErr(e.message || String(e)));
  }, [panel.dataUrl]);

  if (err) return <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{err}</div>;
  if (!data) return <div className="p-4 text-ui-sm text-foreground-subtle">加载中…</div>;

  return (
    <div className="flex max-h-[70vh] flex-col gap-3 overflow-y-auto">
      {(data.sections ?? []).map((s, i) => (
        <div key={i} className="flex flex-col gap-2">
          <div className="text-ui-xs uppercase tracking-wide text-foreground-subtle">{s.title}</div>
          <div className="grid grid-cols-2 gap-2">
            {s.metrics.map((m, j) => (
              <div key={j} className="rounded-xl border border-border bg-surface p-3">
                <div className="text-ui-xs text-foreground-subtle">{m.label}</div>
                <div className="mt-0.5 flex items-baseline gap-1.5">
                  <span className="text-ui-lg font-semibold">{m.value}</span>
                  {m.trend && <span className="text-ui-xs text-foreground-subtle">{m.trend}</span>}
                </div>
              </div>
            ))}
          </div>
        </div>
      ))}
      {(data.sections ?? []).length === 0 && (
        <div className="p-3 text-ui-sm text-foreground-subtle">数据端点待接入(P4 数据面)</div>
      )}
    </div>
  );
}
