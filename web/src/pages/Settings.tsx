import { useEffect, useState } from "react";
import { configApi } from "@/api/client";
import VendorsPage from "@/pages/VendorsPage";
import CronPage from "@/pages/CronPage";
import SessionsPage from "@/pages/SessionsPage";
import MCPPage from "@/pages/MCPPage";
import IMPage from "@/pages/IMPage";
import A2APage from "@/pages/A2APage";

/** SettingsShell:八域 tab 导航;每域专属页(Config/Vendors/MCP/IM/A2A/Sessions/Cron) */
const TABS = [
  { id: "config", label: "配置", comp: ConfigPage },
  { id: "general", label: "常规", comp: GeneralPage },
  { id: "vendors", label: "供应商", comp: VendorsPage },
  { id: "mcp", label: "MCP", comp: MCPPage },
  { id: "im", label: "IM", comp: IMPage },
  { id: "a2a", label: "A2A", comp: A2APage },
  { id: "sessions", label: "会话", comp: SessionsPage },
  { id: "cron", label: "定时任务", comp: CronPage },
] as const;

export default function Settings({ onBack }: { onBack: () => void }) {
  const [tab, setTab] = useState<string>("config");
  const Active = TABS.find((t) => t.id === tab)?.comp ?? ConfigPage;
  return (
    <div className="flex h-full">
      <nav className="w-52 shrink-0 border-r border-border bg-sidebar p-3">
        <button
          className="text-ui-sm mb-3 rounded-lg px-3 py-1.5 text-foreground-subtle hover:bg-hover hover:text-foreground"
          onClick={onBack}
        >
          ← 返回会话
        </button>
        {TABS.map((t) => (
          <button
            key={t.id}
            className={
              "text-ui-sm mb-0.5 block w-full rounded-lg px-3 py-2 text-left transition-colors " +
              (tab === t.id
                ? "bg-selected font-medium text-foreground"
                : "text-foreground-subtle hover:bg-hover hover:text-foreground")
            }
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>
      <div className="min-w-0 flex-1 overflow-y-auto">
        <Active />
      </div>
    </div>
  );
}

/** ConfigPage:P2 代表页——GET /api/config 展示+PUT 保存(通用 KV 编辑) */
function ConfigPage() {
  const [cfg, setCfg] = useState<string>("");
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    configApi
      .get()
      .then((data) => {
        setCfg(JSON.stringify(data, null, 2));
        setLoading(false);
      })
      .catch((e: Error) => {
        setErr(e.message);
        setLoading(false);
      });
  }, []);

  const save = () => {
    try {
      const parsed = JSON.parse(cfg) as Record<string, unknown>;
      configApi
        .put(parsed)
        .then((res: { applied?: string[]; ignored?: string[] }) => {
          setSaved(true);
          // #5298: 白名单外字段(vendors/mcp_servers/im/a2a 及脱敏键)服务端忽略,回显给用户
          setNote(res.ignored?.length ? `已应用 ${res.applied?.length ?? 0} 项;仅字段级页可改而被忽略: ${res.ignored.join("、")}` : null);
          setTimeout(() => setSaved(false), 1500);
        })
        .catch((e: Error) => setErr(e.message));
    } catch {
      setErr("JSON 格式错误");
    }
  };

  if (loading) return <CenterNote>加载中…</CenterNote>;
  return (
    <div className="mx-auto max-w-[860px] p-8">
      <h2 className="text-ui-lg mb-1 font-semibold">配置</h2>
      <p className="text-ui-sm mb-4 text-foreground-subtle">
        当前配置(通用 JSON 编辑;字段级表单随 P2 迭代细化)
      </p>
      {err && (
        <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
          {err}
        </div>
      )}
      <textarea
        className="text-ui-sm h-96 w-full resize-none rounded-xl border border-border bg-input p-3 font-[family-name:var(--font-mono)] text-foreground focus:outline-none"
        value={cfg}
        onChange={(e) => setCfg(e.target.value)}
        spellCheck={false}
      />
      <div className="mt-3 flex items-center gap-3">
        <button className="rounded-lg bg-brand px-4 py-1.5 text-ui-sm font-medium text-white" onClick={save}>
          保存
        </button>
        {saved && <span className="text-ui-sm text-[var(--color-success)]">已保存</span>}
        {note && <span className="text-ui-xs text-foreground-subtle">{note}</span>}
      </div>
    </div>
  );
}

function CenterNote({ children }: { children: React.ReactNode }) {
  return <p className="text-ui-sm flex h-full items-center justify-center text-foreground-subtle">{children}</p>;
}

const THEMES = [
  { id: "dark", label: "暗色" },
  { id: "light", label: "亮色" },
  { id: "system", label: "跟随系统" },
];
const FONT_SIZES = ["12px", "13px", "14px", "16px"];
const MODES = ["supervised", "bypass"];
const EFFORTS = ["low", "medium", "high"];

function applyTheme(t: string) {
  const effective =
    t === "system"
      ? matchMedia("(prefers-color-scheme: light").matches
        ? "light"
        : "dark"
      : t;
  document.documentElement.classList.remove("dark", "light");
  document.documentElement.classList.add(effective);
  localStorage.setItem("ggcode-theme", t);
}

const segBtn = (active: boolean) =>
  "text-ui-xs rounded-lg px-3 py-1.5 transition-colors " +
  (active ? "bg-selected font-medium text-foreground" : "text-foreground-subtle hover:bg-hover hover:text-foreground");

/** GeneralPage(常规):外观(主题/字号)+ 模型选择/推理力度/权限模式 +
 * 当前 endpoint 模型配置(context window/max output tokens)。与聊天头部控制并存。 */
function GeneralPage() {
  const [theme, setTheme] = useState(() => localStorage.getItem("ggcode-theme") || "dark");
  const [fontSize, setFontSize] = useState(() => localStorage.getItem("ggcode-font-size") || "14px");
  // 聊天运行时控制(与聊天头部同源 /api/chat/commands slash 链)
  const [ctrl, setCtrl] = useState<{ model?: string; mode?: string; reasoning_effort?: string }>({});
  const [models, setModels] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  // 当前 endpoint 模型配置
  const [active, setActive] = useState<{ vendor: string; endpoint: string } | null>(null);
  const [cw, setCw] = useState("");
  const [mt, setMt] = useState("");
  const [savingCfg, setSavingCfg] = useState(false);

  const load = () => {
    fetch("/api/chat/controls")
      .then((r) => (r.ok ? r.json() : {}))
      .then((d) => setCtrl(d))
      .catch(() => {});
    fetch("/api/config")
      .then((r) => (r.ok ? r.json() : null))
      .then((cfg: any) => {
        if (!cfg) return;
        const list: string[] = [];
        for (const v of Object.values<any>(cfg?.vendors ?? {})) {
          for (const e of Object.values<any>(v?.endpoints ?? {})) for (const m of (e as any)?.models ?? []) list.push(m);
        }
        setModels([...new Set(list)]);
        const vendor = cfg?.vendor, endpoint = cfg?.endpoint;
        if (vendor && endpoint) {
          setActive({ vendor, endpoint });
          const ep = cfg?.vendors?.[vendor]?.endpoints?.[endpoint];
          if (ep) {
            setCw(ep.context_window ? String(ep.context_window) : "");
            setMt(ep.max_tokens ? String(ep.max_tokens) : "");
          }
        }
      })
      .catch(() => {});
  };
  useEffect(() => { load(); }, []);

  const submitCmd = (text: string, after?: () => void) => {
    setBusy(true);
    fetch("/api/chat/commands", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    })
      .then(() => setTimeout(load, 600))
      .then(() => { after?.(); })
      .catch(() => {})
      .finally(() => setBusy(false));
  };

  const saveEndpointCfg = () => {
    if (!active) return;
    const body: Record<string, number> = {};
    if (cw !== "") body.context_window = parseInt(cw, 10);
    if (mt !== "") body.max_tokens = parseInt(mt, 10);
    setSavingCfg(true);
    setNote(null);
    fetch(`/api/vendors/${encodeURIComponent(active.vendor)}/endpoints/${encodeURIComponent(active.endpoint)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
      .then(async (r) => {
        if (!r.ok) throw new Error((await r.json().catch(() => ({})))?.error || `HTTP ${r.status}`);
        setNote("已保存");
        setTimeout(() => setNote(null), 2000);
      })
      .catch((e: Error) => setNote(`保存失败: ${e.message}`))
      .finally(() => setSavingCfg(false));
  };

  return (
    <div className="mx-auto max-w-[860px] space-y-8 p-8">
      <section>
        <h2 className="text-ui-lg mb-1 font-semibold">常规</h2>
        <p className="text-ui-sm mb-4 text-foreground-subtle">外观与模型运行时偏好(与聊天界面控制并存)</p>

        <div className="space-y-5">
          <div>
            <div className="text-ui-sm mb-1.5 text-foreground-subtle">主题</div>
            <div className="inline-flex gap-1 rounded-xl border border-border bg-card p-1">
              {THEMES.map((t) => (
                <button key={t.id} className={segBtn(theme === t.id)} onClick={() => { applyTheme(t.id); setTheme(t.id); }}>
                  {t.label}
                </button>
              ))}
            </div>
          </div>
          <div>
            <div className="text-ui-sm mb-1.5 text-foreground-subtle">字号</div>
            <div className="inline-flex gap-1 rounded-xl border border-border bg-card p-1">
              {FONT_SIZES.map((f) => (
                <button key={f} className={segBtn(fontSize === f)} onClick={() => { document.documentElement.style.fontSize = f; localStorage.setItem("ggcode-font-size", f); setFontSize(f); }}>
                  {f.replace("px", "")}
                </button>
              ))}
            </div>
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div>
              <div className="text-ui-sm mb-1.5 text-foreground-subtle">模型</div>
              <select
                className="text-ui-sm w-full rounded-lg border border-border bg-card px-2 py-1.5 text-foreground disabled:opacity-50"
                value={ctrl.model ?? ""}
                disabled={busy}
                onChange={(e) => submitCmd(`/model ${e.target.value}`)}
              >
                <option value="" disabled>{ctrl.model || "模型"}</option>
                {models.filter((m) => m !== ctrl.model).map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </div>
            <div>
              <div className="text-ui-sm mb-1.5 text-foreground-subtle">推理力度</div>
              <div className="inline-flex gap-1 rounded-xl border border-border bg-card p-1">
                {EFFORTS.map((e) => (
                  <button key={e} className={segBtn(ctrl.reasoning_effort === e)} disabled={busy} onClick={() => submitCmd(`/effort ${e}`)}>
                    {e}
                  </button>
                ))}
              </div>
            </div>
            <div>
              <div className="text-ui-sm mb-1.5 text-foreground-subtle">权限模式</div>
              <div className="inline-flex gap-1 rounded-xl border border-border bg-card p-1">
                {MODES.map((m) => (
                  <button key={m} className={segBtn(ctrl.mode === m)} disabled={busy} onClick={() => submitCmd(`/mode ${m}`)}>
                    {m}
                  </button>
                ))}
              </div>
            </div>
          </div>

          {active && (
            <div className="rounded-xl border border-border bg-card p-4">
              <div className="text-ui-sm mb-2 font-medium">当前端点模型配置</div>
              <div className="text-ui-xs mb-3 text-foreground-subtlest">{active.vendor} · {active.endpoint}</div>
              <div className="grid grid-cols-2 gap-4">
                <label className="block">
                  <span className="text-ui-xs mb-1 block text-foreground-subtle">Context window</span>
                  <input
                    className="text-ui-sm w-full rounded-lg border border-border bg-input px-2 py-1.5 text-foreground"
                    type="number" min={0} placeholder="留空保持不变"
                    value={cw} onChange={(e) => setCw(e.target.value)}
                  />
                </label>
                <label className="block">
                  <span className="text-ui-xs mb-1 block text-foreground-subtle">Max output tokens</span>
                  <input
                    className="text-ui-sm w-full rounded-lg border border-border bg-input px-2 py-1.5 text-foreground"
                    type="number" min={0} placeholder="留空保持不变"
                    value={mt} onChange={(e) => setMt(e.target.value)}
                  />
                </label>
              </div>
              <div className="mt-3 flex items-center gap-3">
                <button
                  className="rounded-lg bg-brand px-4 py-1.5 text-ui-sm font-medium text-white disabled:opacity-40"
                  disabled={savingCfg}
                  onClick={saveEndpointCfg}
                >
                  保存配置
                </button>
                {note && <span className="text-ui-xs text-foreground-subtle">{note}</span>}
              </div>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
