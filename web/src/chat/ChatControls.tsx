import { useEffect, useRef, useState } from "react";

type Controls = { model?: string; mode?: string; reasoning_effort?: string };

const MODES = ["supervised", "bypass"];
const EFFORTS = ["low", "medium", "high"];

/** ChatControls(队列2):聊天头部三能力——模型/effort/mode 下拉;
 *  提交走 /api/chat/commands(TUI slash 全链),反馈系统消息经 WS 回流 */
export default function ChatControls() {
  const [ctrl, setCtrl] = useState<Controls>({});
  const [groups, setGroups] = useState<{ vendor: string; endpoint: string; models: string[] }[]>([]);
  const [busy, setBusy] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  const load = () => {
    fetch("/api/chat/controls")
      .then((r) => (r.ok ? r.json() : {}))
      .then((d) => setCtrl(d))
      .catch(() => {});
      // 模型可选列表:provider(vendor)→endpoint 两级分组,models 保留层级
    fetch("/api/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg: { vendors?: Record<string, { endpoints?: Record<string, { models?: string[] }> }> } | null) => {
        const gs: { vendor: string; endpoint: string; models: string[] }[] = [];
        for (const [vname, v] of Object.entries(cfg?.vendors ?? {})) {
          for (const [ename, e] of Object.entries(v?.endpoints ?? {})) {
            const ms = [...new Set(e?.models ?? [])];
            if (ms.length) gs.push({ vendor: vname, endpoint: ename, models: ms });
          }
        }
        setGroups(gs);
      })
      .catch(() => {});
  };

  useEffect(() => {
    load();
    timer.current = window.setInterval(load, 20000);
    return () => window.clearInterval(timer.current);
  }, []);

  const submit = (text: string) => {
    setBusy(true);
    fetch("/api/chat/commands", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    })
      .then(() => setTimeout(load, 600))
      .catch(() => {})
      .finally(() => setBusy(false));
  };

  const select = "text-ui-xs rounded-lg border border-border bg-card px-2 py-1 text-foreground disabled:opacity-50";

  return (
    <div className="flex items-center gap-1.5">
      <select
        className={select}
        value={ctrl.model ?? ""}
        disabled={busy}
        title="切换模型"
        onChange={(e) => submit(`/model ${e.target.value}`)}
      >
        <option value="" disabled>
          {ctrl.model || "模型"}
        </option>
        {groups.map((g) => {
          const ms = g.models.filter((m) => m !== ctrl.model);
          if (!ms.length) return null;
          return (
            <optgroup key={`${g.vendor}/${g.endpoint}`} label={`${g.vendor} · ${g.endpoint}`}>
              {ms.map((m) => (
                <option key={`${g.vendor}/${g.endpoint}/${m}`} value={m}>
                  {m}
                </option>
              ))}
            </optgroup>
          );
        })}
      </select>
      <select
        className={select}
        value={ctrl.reasoning_effort || ""}
        disabled={busy}
        title="推理力度"
        onChange={(e) => submit(`/effort ${e.target.value}`)}
      >
        <option value="" disabled>
          {ctrl.reasoning_effort || "effort"}
        </option>
        {EFFORTS.map((e) => (
          <option key={e} value={e}>
            {e}
          </option>
        ))}
      </select>
      <select
        className={select}
        value={ctrl.mode || ""}
        disabled={busy}
        title="权限模式"
        onChange={(e) => submit(`/mode ${e.target.value}`)}
      >
        <option value="" disabled>
          {ctrl.mode || "mode"}
        </option>
        {MODES.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
    </div>
  );
}
