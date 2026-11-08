import { useEffect, useRef, useState } from "react";

type Controls = { model?: string; mode?: string; reasoning_effort?: string };

const MODES = ["supervised", "bypass"];
const EFFORTS = ["minimal", "low", "medium", "high"];

/** ChatControls(队列2):聊天头部三能力——模型/effort/mode 下拉;
 *  提交走 /api/chat/commands(TUI slash 全链),反馈系统消息经 WS 回流 */
export default function ChatControls() {
  const [ctrl, setCtrl] = useState<Controls>({});
  const [models, setModels] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  const load = () => {
    fetch("/api/chat/controls")
      .then((r) => (r.ok ? r.json() : {}))
      .then((d) => setCtrl(d))
      .catch(() => {});
    // 模型可选列表:config 域 vendors→endpoints→models 扁平
    fetch("/api/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg) => {
        const list: string[] = [];
        for (const v of cfg?.vendors ?? []) {
          for (const e of v?.endpoints ?? []) for (const m of e?.models ?? []) list.push(m);
        }
        setModels(list);
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
        {models.filter((m) => m !== ctrl.model).map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
      <select
        className={select}
        value={ctrl.reasoning_effort || ""}
        disabled={busy}
        title="推理力度"
        onChange={(e) => {
          // effort 走 TUI cycle 语义:选定目标值提交 /effort 不存在直设命令——
          // 循环至目标(medium 起步的 cycle 步数)由命令层完成,这里提交相对切换
          const order = EFFORTS;
          const cur = order.indexOf(ctrl.reasoning_effort || "medium");
          const next = order.indexOf(e.target.value);
          const steps = (next - cur + order.length) % order.length || 1;
          let remaining = steps;
          const tick = () => {
            if (remaining-- > 0) {
              submit("/effort");
              setTimeout(tick, 400);
            }
          };
          tick();
        }}
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
