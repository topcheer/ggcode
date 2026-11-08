import { useEffect, useState } from "react";
import { api } from "@/api/client";

type MCPRuntime = {
  connected: boolean;
  pending: boolean;
  disabled: boolean;
  error?: string;
  tools?: string[];
};

type MCPEntry = {
  name: string;
  type?: string;
  command?: string;
  url?: string;
  runtime?: MCPRuntime;
};

/** MCPPage(P2 细化):每服务器卡——连接态灯+传输形态+工具清单+错误面 */
export default function MCPPage() {
  const [entries, setEntries] = useState<MCPEntry[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<MCPEntry[]>("/api/mcp")
      .then((d) => {
        setEntries(Array.isArray(d) ? d : []);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  const dot = (e: MCPEntry) => {
    if (!e.runtime) return { bg: "var(--color-foreground-subtlest)", label: "无运行态" };
    if (e.runtime.disabled) return { bg: "var(--color-warning)", label: "已禁用" };
    if (e.runtime.pending) return { bg: "var(--color-trajectory-tool-call)", label: "连接中" };
    if (e.runtime.connected) return { bg: "var(--color-success)", label: "已连接" };
    return { bg: "var(--color-destructive)", label: "未连接" };
  };

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <h2 className="text-ui-lg mb-4 font-semibold">MCP 服务器</h2>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <div className="flex flex-col gap-2">
        {entries.map((e) => {
          const d = dot(e);
          return (
            <div key={e.name} className="rounded-xl border border-border bg-card px-3 py-2">
              <div className="flex items-center gap-2">
                <span className="inline-block h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: d.bg }} title={d.label} />
                <span className="text-ui-sm font-medium text-foreground">{e.name}</span>
                {e.type && <span className="text-ui-xs rounded-md bg-[var(--color-surface)] px-2 py-0.5 text-foreground-subtlest">{e.type}</span>}
                <span className="text-ui-xs ml-auto text-foreground-subtlest">{d.label}</span>
              </div>
              {(e.command || e.url) && (
                <p className="text-ui-xs mt-1 truncate font-[family-name:var(--font-mono)] text-foreground-subtlest">
                  {e.command || e.url}
                </p>
              )}
              {e.runtime?.error && (
                <p className="text-ui-xs mt-1 text-[var(--color-destructive-surface-fg)]">{e.runtime.error}</p>
              )}
              {e.runtime?.tools && e.runtime.tools.length > 0 && (
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {e.runtime.tools.slice(0, 12).map((t) => (
                    <span key={t} className="text-ui-xs rounded bg-[var(--color-surface)] px-1.5 py-0.5 font-[family-name:var(--font-mono)] text-foreground-subtle">
                      {t}
                    </span>
                  ))}
                  {e.runtime.tools.length > 12 && (
                    <span className="text-ui-xs px-1.5 py-0.5 text-foreground-subtlest">+{e.runtime.tools.length - 12}</span>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {entries.length === 0 && !err && <p className="text-ui-sm text-foreground-subtlest">(未配置 MCP 服务器)</p>}
      </div>
    </div>
  );
}
