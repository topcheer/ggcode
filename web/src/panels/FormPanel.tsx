import { useEffect, useState } from "react";
import type { FormData_, PanelDirective } from "./types";
import { submitCommand } from "./types";
import { adaptIMAdapter, imFormToConfig } from "./adapters";

/** FormPanel:字段表单(secret 打码);双模式提交——HTTP 回写(IM 配置系列)或命令回填 */
export default function FormPanel({ panel, onClose }: { panel: PanelDirective; onClose: () => void }) {
  const [data, setData] = useState<FormData_ | null>(null);
  const [err, setErr] = useState("");
  const [vals, setVals] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    fetch(panel.dataUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`数据拉取失败 (${r.status})`))))
      .then((raw: unknown) => {
        // 异形端点归一:IM 适配器平面 config → fields + PUT 回写
        const d = panel.dataUrl.startsWith("/api/im/adapters/")
          ? adaptIMAdapter(raw as Record<string, unknown>, panel.dataUrl)
          : (raw as FormData_);
        setData(d);
        // 字段初值注入(编辑态而非空白表单)
        const init: Record<string, string> = {};
        for (const f of d.fields ?? []) if (f.value !== undefined) init[f.name] = f.value;
        setVals(init);
      })
      .catch((e: Error) => setErr(e.message || String(e)));
  }, [panel.dataUrl]);

  const fields = data?.fields ?? [];

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const s = data?.submit;
      if (s?.method && s.url) {
        // HTTP 回写模式:表单值 → config 对象 PUT 回端点
        const body = panel.dataUrl.startsWith("/api/im/adapters/")
          ? imFormToConfig(vals, fields)
          : vals;
        const r = await fetch(s.url, {
          method: s.method,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (!r.ok) throw new Error((await r.text()) || `保存失败 (${r.status})`);
      } else {
        // 命令回填模式:字段值拼参提交
        const cmd = s?.command ?? panel.cmdPrefix;
        if (!cmd) return;
        const arg = fields.filter((f) => vals[f.name]).map((f) => vals[f.name]).join(" ");
        await submitCommand(arg ? `${cmd} ${arg}` : cmd);
      }
      onClose();
    } catch (e) {
      setErr((e as Error).message || String(e));
      setBusy(false);
    }
  };

  if (err && !fields.length) return <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{err}</div>;
  if (!data) return <div className="p-4 text-ui-sm text-foreground-subtle">加载中…</div>;

  return (
    <div className="flex max-h-[70vh] flex-col gap-3 overflow-y-auto">
      {err && <div className="rounded-xl bg-[var(--color-danger-surface)] p-3 text-ui-sm">{err}</div>}
      {fields.length === 0 && (
        <div className="p-3 text-ui-sm text-foreground-subtle">该表单数据端点未提供字段</div>
      )}
      {fields.map((f) => (
        <label key={f.name} className="flex flex-col gap-1">
          <span className="text-ui-sm font-medium">
            {f.label}
            {f.help && <span className="ml-2 text-ui-xs text-foreground-subtle">{f.help}</span>}
          </span>
          {f.type === "checkbox" ? (
            <input
              type="checkbox"
              className="size-4"
              checked={vals[f.name] === "true"}
              onChange={(e) => setVals((v) => ({ ...v, [f.name]: e.target.checked ? "true" : "false" }))}
            />
          ) : f.type === "textarea" ? (
            <textarea
              rows={4}
              placeholder={f.placeholder ?? ""}
              className="rounded-lg border border-border bg-surface px-3 py-2 font-mono text-ui-sm outline-none focus:border-accent"
              value={vals[f.name] ?? ""}
              onChange={(e) => setVals((v) => ({ ...v, [f.name]: e.target.value }))}
            />
          ) : (
            <input
              type={f.secret ? "password" : (f.type ?? "text")}
              placeholder={f.placeholder ?? ""}
              className="rounded-lg border border-border bg-surface px-3 py-2 text-ui-sm outline-none focus:border-accent"
              value={vals[f.name] ?? ""}
              onChange={(e) => setVals((v) => ({ ...v, [f.name]: e.target.value }))}
            />
          )}
        </label>
      ))}
      {fields.length > 0 && (
        <button
          className="w-fit rounded-lg bg-accent px-4 py-1.5 text-ui-sm text-accent-fg disabled:opacity-50"
          disabled={busy}
          onClick={() => void submit()}
        >
          保存
        </button>
      )}
    </div>
  );
}
