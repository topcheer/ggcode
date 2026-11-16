import { useEffect, useState } from "react";
import type { PanelDirective, SelectData, SelectOption } from "./types";
import { submitCommand } from "./types";
import { adaptSelect } from "./adapters";

/** SelectPanel:卡片选择器(分组/平铺),键盘 ↑↓+Enter,选中回填带参命令 */
export default function SelectPanel({ panel, onClose }: { panel: PanelDirective; onClose: () => void }) {
  const [data, setData] = useState<SelectData | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [sel, setSel] = useState(0);

  useEffect(() => {
    fetch(panel.dataUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`数据拉取失败 (${r.status})`))))
      .then((raw: unknown) => setData(adaptSelect(raw, panel.dataUrl, panel.command)))
      .catch((e: Error) => setErr(e.message || String(e)));
  }, [panel.dataUrl, panel.command]);

  const flat: SelectOption[] = data?.grouped
    ? (data.groups ?? []).flatMap((g: { label: string; options: SelectOption[] }) => g.options)
    : (data?.options ?? []);

  useEffect(() => {
    if (!data) return;
    const cur = flat.findIndex((o) => o.current);
    if (cur > 0) setSel(cur);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const choose = async (idx: number) => {
    const o = flat[idx];
    if (!o || busy) return;
    const argFrom = data?.submit?.argFrom === "id" ? o.id : o.label;
    setBusy(true);
    try {
      const s = data?.submit;
      if (s?.method && s.url) {
        // HTTP 回写模式(如 impersonate PUT):选中值写入 bodyField
        const r = await fetch(s.url, {
          method: s.method,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ [s.bodyField ?? "value"]: argFrom }),
        });
        if (!r.ok) throw new Error((await r.text()) || `保存失败 (${r.status})`);
      } else {
        // 命令回填模式:拼参提交走 TUI 命令链(校验/反馈单源)
        const cmd = `${s?.command ?? panel.cmdPrefix ?? panel.command} ${argFrom}`;
        await submitCommand(cmd);
      }
      onClose();
    } catch (e) {
      setErr((e as Error).message || String(e));
      setBusy(false);
    }
  };

  const onKey = (e: React.KeyboardEvent) => {
    if (flat.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSel((s) => Math.min(s + 1, flat.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSel((s) => Math.max(s - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      void choose(sel);
    }
  };

  const renderItem = (o: SelectOption, i: number) => (
    <button
      key={`${o.id}-${i}`}
      className={`w-full rounded-xl border p-3 text-left transition-colors ${
        sel === i
          ? "border-accent bg-accent-surface text-accent-surface-fg"
          : "border-border bg-surface hover:bg-surface-hover"
      } ${busy ? "opacity-50" : ""}`}
      onMouseEnter={() => setSel(i)}
      onClick={() => void choose(i)}
      disabled={busy}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-ui-base font-medium">{o.label}</span>
        {o.current && (
          <span className="rounded-full bg-accent-surface px-2 py-0.5 text-ui-xs text-accent-surface-fg">当前</span>
        )}
      </div>
      {o.badge && <div className="mt-0.5 text-ui-sm text-foreground-subtle">{o.badge}</div>}
    </button>
  );

  let gi = -1;
  return (
    <div
      className="flex max-h-[70vh] flex-col gap-3 overflow-y-auto outline-none"
      tabIndex={0}
      onKeyDown={onKey}
      autoFocus
    >
      {err && <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{err}</div>}
      {!err && data?.error && <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{data.error}</div>}
      {data?.context && (data.context.vendor || data.context.endpoint) && (
        <div className="text-ui-xs text-foreground-subtle">
          {[data.context.vendor, data.context.endpoint, data.context.protocol].filter(Boolean).join(" · ")}
        </div>
      )}
      {!data && !err && <div className="p-4 text-ui-sm text-foreground-subtle">加载中…</div>}
      {data?.grouped
        ? data.groups?.map((g, idx) => (
            <div key={idx} className="flex flex-col gap-1.5">
              <div className="text-ui-xs uppercase tracking-wide text-foreground-subtle">{g.label}</div>
              {g.options.map((o) => {
                gi++;
                return renderItem(o, gi);
              })}
            </div>
          ))
        : data?.options?.map((o) => {
            gi++;
            return renderItem(o, gi);
          })}
    </div>
  );
}
