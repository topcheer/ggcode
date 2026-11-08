import { useEffect, useRef, useState } from "react";

type Controls = { model?: string; mode?: string; reasoning_effort?: string };

const MODES = ["supervised", "bypass"] as const;
const EFFORTS = ["low", "medium", "high"] as const;

/** useChatControls:三能力共用快照轮询+命令提交(20s 轮询;提交后 600ms 刷新) */
function useChatControls() {
  const [ctrl, setCtrl] = useState<Controls>({});
  const [busy, setBusy] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  const load = () => {
    fetch("/api/chat/controls")
      .then((r) => (r.ok ? r.json() : {}))
      .then((d) => setCtrl(d))
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

  return { ctrl, busy, submit };
}

/** ChatControls(头部):模型下拉 + effort 分段胶囊条;
 *  提交走 /api/chat/commands(TUI slash 全链),反馈系统消息经 WS 回流 */
export default function ChatControls() {
  const { ctrl, busy, submit } = useChatControls();
  const [groups, setGroups] = useState<{ vendor: string; endpoint: string; models: string[] }[]>([]);
  // 活动 provider·endpoint 取自 /api/config 顶层(/api/chat/controls 不含这两项)
  const [active, setActive] = useState<{ vendor?: string; endpoint?: string }>({});

  useEffect(() => {
    fetch("/api/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg: { vendor?: string; endpoint?: string; vendors?: Record<string, { endpoints?: Record<string, { models?: string[] }> }> } | null) => {
        setActive({ vendor: cfg?.vendor, endpoint: cfg?.endpoint });
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
  }, []);

  const select = "text-ui-xs rounded-lg border border-border bg-card px-2 py-1 text-foreground disabled:opacity-50";
  const activeModels = groups.find((g) => g.vendor === active.vendor && g.endpoint === active.endpoint)?.models ?? [];
  const effort = ctrl.reasoning_effort || "medium";

  return (
    <div className="flex items-center gap-2">
      <select className={select} value={ctrl.model ?? ""} disabled={busy} title="切换模型" onChange={(e) => submit(`/model ${e.target.value}`)}>
        <option value="" disabled>
          {ctrl.model || "模型"}
        </option>
        {/* 仅活动 provider·endpoint 的模型;切换 provider 请到设置-供应商 */}
        {activeModels
          .filter((m) => m !== ctrl.model)
          .map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
      </select>
      {/* effort 分段胶囊条(活力棒):三档单选,当前档反色高亮 */}
      <div className="text-ui-xs flex items-center rounded-full border border-border bg-card p-0.5" title="推理力度">
        {EFFORTS.map((e) => (
          <button
            key={e}
            type="button"
            disabled={busy}
            onClick={() => submit(`/effort ${e}`)}
            className={`rounded-full px-2 py-0.5 transition-colors disabled:opacity-50 ${
              effort === e ? "bg-foreground text-background" : "text-foreground-subtle hover:text-foreground"
            }`}
          >
            {e}
          </button>
        ))}
      </div>
    </div>
  );
}

/** ModeToggle(输入框下方):双态权限模式开关——track 左 supervised 右 bypass,
 *  点击拨到另一态提交 /mode;当前态在 track 内反色小字直读 */
export function ModeToggle() {
  const { ctrl, busy, submit } = useChatControls();
  const mode = ctrl.mode || "supervised";
  const isBypass = mode === "bypass";

  return (
    <div className="text-ui-xs flex items-center justify-center gap-2 px-4 pb-1.5" title="权限模式( supervised=监督执行 / bypass=跳过审批 )">
      <span className="text-foreground-subtlest">权限模式</span>
      <button
        type="button"
        disabled={busy}
        aria-pressed={isBypass}
        onClick={() => submit(`/mode ${isBypass ? MODES[0] : MODES[1]}`)}
        className={`relative h-5 w-10 rounded-full border transition-colors disabled:opacity-50 ${
          isBypass ? "border-[var(--color-warning-surface-fg)] bg-[var(--color-warning-surface)]" : "border-border bg-card"
        }`}
      >
        <span
          className={`absolute top-0.5 h-4 w-4 rounded-full bg-foreground shadow transition-all ${
            isBypass ? "left-[calc(100%-1.125rem)]" : "left-0.5"
          }`}
        />
      </button>
      <span className={isBypass ? "text-[var(--color-warning-surface-fg)]" : "text-foreground-subtle"}>{mode}</span>
    </div>
  );
}
