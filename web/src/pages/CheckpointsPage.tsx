import { useEffect, useState } from "react";
import { api } from "@/api/client";

type Checkpoint = {
  id: string;
  file_path: string;
  tool_call: string;
  timestamp: string;
  run_id?: string;
};

/** CheckpointsPage(§4.5 P6-9):编辑检查点只读时间线——回滚走 TUI 面板 */
export default function CheckpointsPage() {
  const [items, setItems] = useState<Checkpoint[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Checkpoint[]>("/api/checkpoints")
      .then((d) => {
        setItems(d);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  return (
    <div className="mx-auto max-w-[860px] p-8">
      <h2 className="text-ui-lg mb-1 font-semibold">编辑检查点</h2>
      <p className="text-ui-sm text-foreground-subtle mb-4">只读时间线 · 回滚请使用 TUI 检查点面板</p>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <ul className="flex flex-col gap-1.5">
        {items.map((c) => (
          <li key={c.id} className="rounded-xl border border-border bg-card px-3 py-2">
            <div className="flex items-center gap-2">
              <span className="text-ui-xs truncate font-[family-name:var(--font-mono)] text-foreground">
                {c.file_path}
              </span>
              <span className="text-ui-xs ml-auto shrink-0 text-foreground-subtlest">
                {c.timestamp.replace("T", " ").slice(0, 19)}
              </span>
            </div>
            <div className="mt-0.5 flex items-center gap-2">
              <span className="text-ui-xs text-foreground-subtlest">{c.tool_call}</span>
              {c.run_id && (
                <span className="text-ui-xs truncate font-[family-name:var(--font-mono)] text-foreground-subtlest">
                  run:{c.run_id.slice(0, 10)}
                </span>
              )}
            </div>
          </li>
        ))}
        {items.length === 0 && !err && <p className="text-ui-sm text-foreground-subtlest">(无检查点记录)</p>}
      </ul>
    </div>
  );
}
