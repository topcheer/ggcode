import { useEffect, useState } from "react";
import { api } from "@/api/client";

type A2AStatus = {
  disabled: boolean;
  port: number;
  host: string;
  max_tasks: number;
  task_timeout?: string;
  auth: {
    has_api_key: boolean;
    has_api_keys: boolean;
    api_key_count: number;
  };
};

/** A2APage(P2 细化):服务状态卡——开关/监听面/并发预算/鉴权层摘要 */
export default function A2APage() {
  const [st, setSt] = useState<A2AStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<A2AStatus>("/api/a2a")
      .then((d) => {
        setSt(d);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  if (err) {
    return (
      <div className="mx-auto max-w-[960px] p-8">
        <div className="text-ui-sm rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">{err}</div>
      </div>
    );
  }
  if (!st) return <div className="p-8 text-ui-sm text-foreground-subtlest">加载中…</div>;

  const Row = ({ k, v }: { k: string; v: string }) => (
    <div className="flex items-center gap-3 border-t border-border px-4 py-2 first:border-t-0">
      <span className="text-ui-xs w-28 shrink-0 text-foreground-subtlest">{k}</span>
      <span className="text-ui-sm truncate text-foreground">{v}</span>
    </div>
  );

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <div className="mb-4 flex items-center gap-2">
        <h2 className="text-ui-lg font-semibold">A2A 远程代理</h2>
        <span
          className="text-ui-xs rounded-full px-2 py-0.5"
          style={{
            background: st.disabled ? "var(--color-surface)" : "var(--color-success-surface)",
            color: st.disabled ? "var(--color-foreground-subtlest)" : "var(--color-success-surface-fg)",
          }}
        >
          {st.disabled ? "已禁用" : "运行中"}
        </span>
      </div>
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        <Row k="监听" v={st.disabled ? "—" : `${st.host}:${st.port}`} />
        <Row k="并发上限" v={String(st.max_tasks)} />
        {st.task_timeout && <Row k="任务超时" v={st.task_timeout} />}
        <Row
          k="鉴权"
          v={
            st.auth.has_api_key || st.auth.has_api_keys
              ? `API Key（${st.auth.api_key_count} 把）`
              : "未配置（匿名拒绝由服务端执行）"
          }
        />
      </div>
    </div>
  );
}
