import { useEffect, useState } from "react";
import { api } from "@/api/client";

type IMRuntime = {
  adapter: string;
  platform: string;
  healthy: boolean;
  status: string;
  last_error?: string;
  muted?: boolean;
  disabled?: boolean;
};

/** IMPage(P2 细化):适配器健康灯卡——平台/状态/静音禁用徽章+错误面 */
export default function IMPage() {
  const [adapters, setAdapters] = useState<IMRuntime[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<IMRuntime[]>("/api/im/status")
      .then((d) => {
        setAdapters(Array.isArray(d) ? d : []);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <h2 className="text-ui-lg mb-4 font-semibold">IM 适配器</h2>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <div className="flex flex-col gap-2">
        {adapters.map((a) => (
          <div key={a.adapter + a.platform} className="rounded-xl border border-border bg-card px-3 py-2">
            <div className="flex items-center gap-2">
              <span
                className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
                style={{ background: a.disabled ? "var(--color-warning)" : a.healthy ? "var(--color-success)" : "var(--color-destructive)" }}
                title={a.disabled ? "已禁用" : a.healthy ? "健康" : "异常"}
              />
              <span className="text-ui-sm font-medium text-foreground">{a.adapter}</span>
              <span className="text-ui-xs rounded-md bg-[var(--color-surface)] px-2 py-0.5 text-foreground-subtlest">{a.platform}</span>
              {a.muted && (
                <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-warning-surface)", color: "var(--color-warning-surface-fg)" }}>
                  静音
                </span>
              )}
              {a.disabled && (
                <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-destructive-surface)", color: "var(--color-destructive-surface-fg)" }}>
                  禁用
                </span>
              )}
              <span className="text-ui-xs ml-auto text-foreground-subtlest">{a.status}</span>
            </div>
            {a.last_error && (
              <p className="text-ui-xs mt-1 text-[var(--color-destructive-surface-fg)]">{a.last_error}</p>
            )}
          </div>
        ))}
        {adapters.length === 0 && !err && <p className="text-ui-sm text-foreground-subtlest">(无绑定适配器)</p>}
      </div>
    </div>
  );
}
