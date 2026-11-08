import { useEffect, useState } from "react";
import { api } from "@/api/client";

type CronJob = {
  id: string;
  cron_expr: string;
  prompt: string;
  recurring: boolean;
  paused: boolean;
};

/** CronPage(§4.5 P6-10):当前 workspace 定时任务只读卡——写操作走 TUI/CLI */
export default function CronPage() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<CronJob[]>("/api/cron/jobs")
      .then((d) => {
        setJobs(d);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  return (
    <div className="mx-auto max-w-[860px] p-8">
      <h2 className="text-ui-lg mb-1 font-semibold">定时任务</h2>
      <p className="text-ui-sm text-foreground-subtle mb-4">只读视图 · 新建/暂停/删除请使用 TUI 或 CLI</p>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <ul className="flex flex-col gap-2">
        {jobs.map((j) => (
          <li key={j.id} className="rounded-xl border border-border bg-card p-3">
            <div className="mb-1 flex items-center gap-2">
              <span className="text-ui-xs rounded-md bg-[var(--color-tag,#2a2a34)] px-2 py-0.5 font-[family-name:var(--font-mono)] text-foreground-subtle">
                {j.cron_expr}
              </span>
              {j.paused && (
                <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-warning-surface)", color: "var(--color-warning-surface-fg)" }}>
                  已暂停
                </span>
              )}
              {!j.recurring && (
                <span className="text-ui-xs text-foreground-subtlest">单次</span>
              )}
              <span className="text-ui-xs ml-auto truncate font-[family-name:var(--font-mono)] text-foreground-subtlest">
                {j.id}
              </span>
            </div>
            <p className="text-ui-sm text-wrap-phrase text-foreground-subtle">{j.prompt}</p>
          </li>
        ))}
        {jobs.length === 0 && !err && <p className="text-ui-sm text-foreground-subtlest">(当前 workspace 无定时任务)</p>}
      </ul>
    </div>
  );
}
