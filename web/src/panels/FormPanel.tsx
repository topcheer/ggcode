import { useEffect, useState } from "react";
import type { FormData_, PanelDirective } from "./types";
import { submitCommand } from "./types";

/** FormPanel:字段表单(secret 打码);提交回填命令(IM 配置系列) */
export default function FormPanel({ panel, onClose }: { panel: PanelDirective; onClose: () => void }) {
  const [data, setData] = useState<FormData_ | null>(null);
  const [err, setErr] = useState("");
  const [vals, setVals] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    fetch(panel.dataUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`数据拉取失败 (${r.status})`))))
      .then((d: FormData_) => setData(d))
      .catch((e: Error) => setErr(e.message || String(e)));
  }, [panel.dataUrl]);

  const fields = data?.fields ?? [];

  const submit = async () => {
    const cmd = data?.submit?.command ?? panel.cmdPrefix;
    if (!cmd || busy) return;
    const arg = fields
      .filter((f) => vals[f.name])
      .map((f) => vals[f.name])
      .join(" ");
    setBusy(true);
    try {
      await submitCommand(arg ? `${cmd} ${arg}` : cmd);
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
        <div className="p-3 text-ui-sm text-foreground-subtle">该适配器表单数据端点待接入(P4 数据面)</div>
      )}
      {fields.map((f) => (
        <label key={f.name} className="flex flex-col gap-1">
          <span className="text-ui-sm font-medium">
            {f.label}
            {f.help && <span className="ml-2 text-ui-xs text-foreground-subtle">{f.help}</span>}
          </span>
          <input
            type={f.secret ? "password" : (f.type ?? "text")}
            placeholder={f.placeholder ?? ""}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-ui-sm outline-none focus:border-accent"
            value={vals[f.name] ?? ""}
            onChange={(e) => setVals((v) => ({ ...v, [f.name]: e.target.value }))}
          />
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
