import { useEffect, useState } from "react";
import { api } from "@/api/client";

type SessionSummary = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  msg_count: number;
  vendor?: string;
  endpoint?: string;
  model?: string;
};

type WorkspaceGroup = {
  workspace: string;
  short_name: string;
  count: number;
  sessions: SessionSummary[];
  current: boolean;
};

/** SessionsPage(P2 细化):workspace 分组表——列 id/标题/条数/模型/时间,最近优先 */
export default function SessionsPage() {
  const [groups, setGroups] = useState<WorkspaceGroup[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [openWs, setOpenWs] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ groups: WorkspaceGroup[] }>("/api/sessions")
      .then((d) => {
        setGroups(d.groups || []);
        setErr(null);
        // 默认展开当前 workspace
        const cur = (d.groups || []).find((g) => g.current);
        if (cur) setOpenWs(cur.workspace);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <h2 className="text-ui-lg mb-4 font-semibold">会话</h2>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <div className="flex flex-col gap-3">
        {groups.map((g) => (
          <div key={g.workspace} className="overflow-hidden rounded-xl border border-border bg-card">
            <button
              className="text-ui-sm flex w-full items-center gap-2 px-4 py-2.5 text-left transition-colors hover:bg-[var(--color-hover)]"
              onClick={() => setOpenWs(openWs === g.workspace ? null : g.workspace)}
            >
              <span className="font-medium text-foreground">{g.short_name}</span>
              <span className="text-ui-xs rounded-md bg-[var(--color-surface)] px-2 py-0.5 text-foreground-subtlest">
                {g.count}
              </span>
              {g.current && (
                <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-success-surface)", color: "var(--color-success-surface-fg)" }}>
                  当前
                </span>
              )}
              <span className="text-ui-xs ml-auto text-foreground-subtlest">{openWs === g.workspace ? "▾" : "▸"}</span>
            </button>
            {openWs === g.workspace && (
              <table className="w-full text-left">
                <thead>
                  <tr className="text-ui-xs border-t border-border text-foreground-subtlest">
                    <th className="px-4 py-1.5 font-normal">标题 / ID</th>
                    <th className="px-4 py-1.5 font-normal">条数</th>
                    <th className="px-4 py-1.5 font-normal">模型</th>
                    <th className="px-4 py-1.5 font-normal">更新时间</th>
                  </tr>
                </thead>
                <tbody>
                  {[...g.sessions]
                    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
                    .map((s) => (
                      <tr key={s.id} className="text-ui-xs border-t border-border">
                        <td className="max-w-[280px] truncate px-4 py-1.5 font-[family-name:var(--font-mono)] text-foreground">
                          {s.title || s.id.slice(0, 16)}
                        </td>
                        <td className="px-4 py-1.5 text-foreground-subtle">{s.msg_count}</td>
                        <td className="px-4 py-1.5 text-foreground-subtle">{s.model || "—"}</td>
                        <td className="px-4 py-1.5 text-foreground-subtlest">
                          {s.updated_at.replace("T", " ").slice(0, 19)}
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            )}
          </div>
        ))}
        {groups.length === 0 && !err && <p className="text-ui-sm text-foreground-subtlest">(无会话记录)</p>}
      </div>
    </div>
  );
}
