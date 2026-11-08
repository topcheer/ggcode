import { useEffect, useState } from "react";
import { configApi } from "@/api/client";
import VendorsPage from "@/pages/VendorsPage";
import CronPage from "@/pages/CronPage";
import CheckpointsPage from "@/pages/CheckpointsPage";
import SessionsPage from "@/pages/SessionsPage";
import KnightPage from "@/pages/KnightPage";
import MCPPage from "@/pages/MCPPage";
import IMPage from "@/pages/IMPage";
import A2APage from "@/pages/A2APage";

/** SettingsShell:十域 tab 导航;每域专属页(Config/Vendors/MCP/IM/A2A/Sessions/Knight/Cron/Checkpoints) */
const TABS = [
  { id: "config", label: "配置", comp: ConfigPage },
  { id: "general", label: "常规", comp: ConfigPage }, // general 复用配置编辑形态
  { id: "vendors", label: "供应商", comp: VendorsPage },
  { id: "mcp", label: "MCP", comp: MCPPage },
  { id: "im", label: "IM", comp: IMPage },
  { id: "a2a", label: "A2A", comp: A2APage },
  { id: "sessions", label: "会话", comp: SessionsPage },
  { id: "knight", label: "Knight", comp: KnightPage },
  { id: "cron", label: "定时任务", comp: CronPage },
  { id: "checkpoints", label: "检查点", comp: CheckpointsPage },
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
