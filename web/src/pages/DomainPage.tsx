import { useCallback, useEffect, useState } from "react";
import { api, vendorsApi, mcpApi, imApi, a2aApi, sessionsApi, knightApi } from "@/api/client";

/** DomainPage:P2 六页共享取数框架——GET 渲染 JSON 树+域专属操作;类型细化随迭代 */
type Domain = "vendors" | "mcp" | "im" | "a2a" | "sessions" | "knight";

const FETCHERS: Record<Domain, () => Promise<unknown>> = {
  vendors: () => vendorsApi.list(),
  mcp: () => Promise.all([mcpApi.get(), mcpApi.status()]).then(([d, s]) => ({ config: d, status: s })),
  im: () => Promise.all([imApi.get(), imApi.status()]).then(([d, s]) => ({ config: d, status: s })),
  a2a: () => a2aApi.get(),
  sessions: () => sessionsApi.list(),
  knight: () => Promise.all([knightApi.get(), knightApi.skills()]).then(([d, s]) => ({ config: d, skills: s })),
};

const TITLES: Record<Domain, { title: string; note: string; actions?: { label: string; path: string; body?: unknown }[] }> = {
  vendors: { title: "供应商", note: "provider 端点/密钥管理" },
  mcp: { title: "MCP", note: "服务器配置与连接状态", actions: [{ label: "刷新状态", path: "/api/mcp/status" }] },
  im: {
    title: "IM",
    note: "IM 适配器绑定与启停",
    actions: [{ label: "查看适配器", path: "/api/im/adapters" }],
  },
  a2a: { title: "A2A", note: "agent 卡与委派配置", actions: [{ label: "Discover", path: "/api/a2a/discover" }] },
  sessions: { title: "会话", note: "历史会话列表" },
  knight: { title: "Knight", note: "技能治理", actions: [{ label: "技能列表", path: "/api/knight/skills" }] },
};

export default function DomainPage({ domain }: { domain: Domain }) {
  const [data, setData] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);

  const [actionNote, setActionNote] = useState<string | null>(null);

  const meta = TITLES[domain];
  const runAction = useCallback(
    (a: { label: string; path: string }) => {
      api
        .get(a.path)
        .then((d) => {
          setData(d);
          setActionNote(`${a.label} 完成`);
          setTimeout(() => setActionNote(null), 1500);
        })
        .catch((e: Error) => setErr(e.message));
    },
    [],
  );

  const load = useCallback(() => {
    setLoading(true);
    setErr(null);
    FETCHERS[domain]()
      .then((d) => {
        setData(d);
        setLoading(false);
      })
      .catch((e: Error) => {
        setErr(e.message);
        setLoading(false);
      });
  }, [domain]);

  useEffect(load, [load, reloadKey]);

  return (
    <div className="mx-auto max-w-[860px] p-8">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h2 className="text-ui-lg font-semibold">{meta.title}</h2>
          <p className="text-ui-sm text-foreground-subtle">{meta.note}</p>
        </div>
        <div className="flex items-center gap-2">
          {meta.actions?.map((a) => (
            <button
              key={a.path}
              className="text-ui-sm rounded-lg border border-border px-3 py-1.5 text-foreground-subtle hover:bg-hover hover:text-foreground"
              onClick={() => runAction(a)}
            >
              {a.label}
            </button>
          ))}
          <button
            className="text-ui-sm rounded-lg border border-border px-3 py-1.5 text-foreground-subtle hover:bg-hover hover:text-foreground"
            onClick={() => setReloadKey((k) => k + 1)}
          >
            刷新
          </button>
          {actionNote && <span className="text-ui-xs text-[var(--color-success)]">{actionNote}</span>}
        </div>
      </div>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      {loading ? (
        <p className="text-ui-sm text-foreground-subtle">加载中…</p>
      ) : (
        <JsonTree value={data} />
      )}
    </div>
  );
}

/** 可折叠 JSON 树:对象数组渲染为卡片列表,标量直接展示 */
export function JsonTree({ value }: { value: unknown }) {
  if (value === null || value === undefined)
    return <p className="text-ui-sm text-foreground-subtlest">(空)</p>;
  if (Array.isArray(value))
    return (
      <div className="flex flex-col gap-2">
        {value.length === 0 && <p className="text-ui-sm text-foreground-subtlest">(空列表)</p>}
        {value.map((v, i) => (
          <div key={i} className="rounded-xl border border-border bg-card p-3">
            <JsonTree value={v} />
          </div>
        ))}
      </div>
    );
  if (typeof value === "object")
    return (
      <div className="flex flex-col gap-1">
        {Object.entries(value as Record<string, unknown>).map(([k, v]) => (
          <div key={k} className="flex items-baseline gap-3 py-0.5">
            <span className="text-ui-sm w-56 shrink-0 truncate font-[family-name:var(--font-mono)] text-foreground-subtle">
              {k}
            </span>
            {typeof v === "object" && v !== null ? (
              <JsonTree value={v} />
            ) : (
              <span
                className="text-ui-sm min-w-0 flex-1 break-all font-[family-name:var(--font-mono)]"
                style={{
                  color:
                    v === true
                      ? "var(--color-success)"
                      : v === false
                        ? "var(--color-destructive)"
                        : "var(--color-foreground)",
                }}
              >
                {String(v)}
              </span>
            )}
          </div>
        ))}
      </div>
    );
  return <span className="text-ui-sm text-foreground">{String(value)}</span>;
}
