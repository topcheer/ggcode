import { useEffect, useState } from "react";

type Controls = { model?: string; mode?: string; reasoning_effort?: string };

const MODES = ["supervised", "bypass"] as const;
const EFFORTS = ["low", "medium", "high"] as const;
// effort 低→高彩色渐进 chip:灰→蓝→橙(高消耗警示)
const EFFORT_STYLES: Record<(typeof EFFORTS)[number], { idle: string; active: string }> = {
  low: { idle: "text-foreground-subtle", active: "bg-[var(--color-success-surface)] text-[var(--color-success-surface-fg)]" },
  medium: { idle: "text-foreground-subtle", active: "bg-[var(--color-info-surface)] text-[var(--color-info-surface-fg)]" },
  high: { idle: "text-foreground-subtle", active: "bg-[var(--color-warning-surface)] text-[var(--color-warning-surface-fg)]" },
};

/** chat-controls 全局单例 store:头部(模型+effort)与输入框下(mode toggle)
 *  共享同一份快照与 busy,消除多组件各开 20s 轮询的竞态(切态回显抖动根因) */
let snapState: Controls = {};
let snapBusy = false;
const snapSubs = new Set<(c: Controls, b: boolean) => void>();
let pollTimer: number | undefined;
let refCount = 0;

const notify = () => snapSubs.forEach((f) => f(snapState, snapBusy));

function poll() {
  fetch("/api/chat/controls")
    .then((r) => (r.ok ? r.json() : {}))
    .then((d: Controls) => {
      snapState = d;
      notify();
    })
    .catch(() => {});
}

/** useChatControls:订阅共享快照;首个订阅者开 20s 轮询,末个退订 */
function useChatControls() {
  const [, force] = useState(0);
  useEffect(() => {
    const sub = (c: Controls, b: boolean) => {
      snapState = c;
      snapBusy = b;
      force((n) => n + 1);
    };
    snapSubs.add(sub);
    force((n) => n + 1);
    refCount++;
    if (refCount === 1) {
      poll();
      pollTimer = window.setInterval(poll, 20000);
    }
    return () => {
      snapSubs.delete(sub);
      refCount--;
      if (refCount === 0 && pollTimer) {
        window.clearInterval(pollTimer);
        pollTimer = undefined;
      }
    };
  }, []);

  /** submitCommand:提交 slash;busy 在 finally 可靠复位(防切一次后全控件锁死) */
  const submitCommand = (text: string) => {
    snapBusy = true;
    notify();
    const done = () => {
      snapBusy = false;
      notify();
    };
    return fetch("/api/chat/commands", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    }).then((r) => {
      if (!r.ok) {
        // 409/400:后端拒绝(命令层校验失败),读错误体弹给 UI
        return r.text().then((t) => {
          done();
          throw new Error(t || `command rejected (${r.status})`);
        });
      }
      done();
    }).catch((e) => {
      done();
      throw e;
    });
  };

  return { ctrl: snapState, busy: snapBusy, submitCommand };
}

/** ChatControls(头部):模型下拉 + effort 分段胶囊条 */
export default function ChatControls() {
  const { ctrl, busy, submitCommand } = useChatControls();
  const [groups, setGroups] = useState<{ vendor: string; endpoint: string; models: string[] }[]>([]);
  const [active, setActive] = useState<{ vendor?: string; endpoint?: string }>({});
  const [err, setErr] = useState("");

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

  const run = (text: string) => {
    setErr("");
    submitCommand(text)
      .then(() => setTimeout(poll, 600))
      .catch((e) => setErr(String(e.message || e)));
  };

  const select = "text-ui-xs rounded-lg border border-border bg-card px-2 py-1 text-foreground disabled:opacity-50";
  const activeModels = groups.find((g) => g.vendor === active.vendor && g.endpoint === active.endpoint)?.models ?? [];
  const effort = ctrl.reasoning_effort || "medium";

  return (
    <div className="flex items-center gap-2">
      {err && <span className="text-ui-xs text-[var(--color-destructive-surface-fg)]">{err}</span>}
      <select className={select} value={ctrl.model ?? ""} disabled={busy} title="切换模型" onChange={(e) => run(`/model ${e.target.value}`)}>
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
      {/* effort 彩色 chip 组:低→高 灰→蓝→橙 渐进,当前档着色填充 */}
      <div className="text-ui-xs flex items-center gap-1" title="推理力度">
        {EFFORTS.map((e) => {
          const on = effort === e;
          const st = EFFORT_STYLES[e];
          return (
            <button
              key={e}
              type="button"
              disabled={busy}
              onClick={() => run(`/effort ${e}`)}
              className={`rounded-full border px-2 py-0.5 transition-colors disabled:opacity-50 ${
                on ? `${st.active} border-transparent font-medium` : `border-border ${st.idle} hover:text-foreground`
              }`}
            >
              {e}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/** ModeToggle(输入框下方):双态权限开关——点击拨到另一态;提交失败弹错误,
 *  成功后 600ms 快照确认(乐观不盲信,防"切了但没生效"死循环) */
export function ModeToggle() {
  const { ctrl, busy, submitCommand } = useChatControls();
  const mode = ctrl.mode || "supervised";
  const isBypass = mode === "bypass";
  const [err, setErr] = useState("");

  const flip = () => {
    const target = isBypass ? MODES[0] : MODES[1];
    setErr("");
    submitCommand(`/mode ${target}`)
      .then(() => setTimeout(poll, 600))
      .catch((e) => setErr(String(e.message || e)));
  };

  return (
    <div className="text-ui-xs flex items-center justify-center gap-2 px-4 pb-1.5" title="权限模式( supervised=监督执行 / bypass=跳过审批 )">
      {err && <span className="text-[var(--color-destructive-surface-fg)]">{err}</span>}
      <span className="text-foreground-subtlest">权限模式</span>
      <button
        type="button"
        disabled={busy}
        aria-pressed={isBypass}
        onClick={flip}
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
