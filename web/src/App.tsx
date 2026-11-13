import { useCallback, useEffect, useRef, useState } from "react";
import Chat from "@/pages/Chat";
import Settings from "@/pages/Settings";
import CommandPalette, { type PaletteAction } from "@/pages/CommandPalette";
import ShareModal from "@/pages/ShareModal";
import TunnelModal from "@/pages/TunnelModal";
import { useChat } from "@/store/chat";
import { usePane } from "@/store/pane";

type Theme = "dark" | "light" | "system";
type View = "chat" | "settings";

function applyTheme(t: Theme) {
  localStorage.setItem("ggcode-theme", t);
  const effective =
    t === "system"
      ? matchMedia("(prefers-color-scheme: light)").matches
        ? "light"
        : "dark"
      : t;
  document.documentElement.classList.remove("dark", "light");
  document.documentElement.classList.add(effective);
}

function currentTheme(): Theme {
  return (localStorage.getItem("ggcode-theme") as Theme) || "dark";
}

const THEME_LABEL: Record<Theme, string> = { dark: "暗色", light: "亮色", system: "跟随系统" };

export default function App() {
  const [theme, setThemeState] = useState<Theme>(currentTheme());
  const [view, setView] = useState<View>("chat");
  const [paletteOpen, setPaletteOpen] = useState(false);
  const resetChat = useChat((s) => s.reset);
  const loadHistory = useChat((s) => s.loadHistory);
  // 右 Pane(P5-6):Spawn 详情推入右栏并排阅读(§6.1)
  const paneSpawn = usePane((s) => s.active);
  const closePane = usePane((s) => s.close);
  // §6.1 二层:活跃子代理流(未完结的在前)
  const streams = useChat((s) => s.subagentStreams);
  const activeStreams = Object.entries(streams).filter(([, s]) => !s.done);
  const todos = useChat((s) => s.todos);
  const cronEvents = useChat((s) => s.cronEvents);
  const [actTab, setActTab] = useState<"todos" | "subs" | "cron">("todos");
  // 移动端抽屉(P5-7):窄屏左栏抽屉化
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [paneOpen, setPaneOpen] = useState(false); // 移动端右栏 overlay(lg 以下)
  // 桌面(lg+)右栏常驻可隐藏:panePinned=false 时收起,主区占满(与移动 overlay 语义互不干扰)。
  const [panePinned, setPanePinned] = useState(() => localStorage.getItem("ggcode-pane-pinned") !== "0");
  const [shareOpen, setShareOpen] = useState(false); // /share 二维码弹层
  const [tunnelOpen, setTunnelOpen] = useState(false); // /et 公网隧道弹层
  // ⌘K 会话切换数据源:最近 sessions(P4-2),面板打开时拉取
  // 会话列表(左栏,同桌面逻辑:本 workspace 全量,锁定态由 locked_by_pid 标示)
  const [sessions, setSessions] = useState<Array<{ id: string; title?: string; updated_at?: string; locked_by_pid?: number; locked_by_self?: boolean }>>([]);
  const [currentSessionId, setCurrentSessionId] = useState<string>("");
  const [switching, setSwitching] = useState(false);

  // ⌘K 全局快捷键(§2 键盘映射表)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const cycleTheme = () => {
    const next: Theme = theme === "dark" ? "light" : theme === "light" ? "system" : "dark";
    applyTheme(next);
    setThemeState(next);
  };

  // 字号档(P5-5):初始应用持久化值;调档 UI 在设置→常规(GeneralPage)
  useEffect(() => {
    document.documentElement.style.fontSize = localStorage.getItem("ggcode-font-size") || "14px";
  }, []);

  const paletteActions: PaletteAction[] = [
    { id: "go-chat", label: "打开会话", hint: "视图", run: () => setView("chat") },
    { id: "go-settings", label: "打开设置", hint: "视图", run: () => setView("settings") },
    { id: "theme", label: `切换主题 (当前:${THEME_LABEL[currentTheme()]})`, hint: "亦在 设置→常规", run: cycleTheme },
    { id: "new-chat", label: "新建会话(清空当前聊天视图)", hint: "⌘N", run: resetChat },
    // 历史会话切换(P4-2):点击后回 chat 视图并重拉该 session 镜像
    ...sessions.slice(0, 8).map((s) => ({
      id: `session-${s.id}`,
      label: `切换到会话: ${s.title || s.id.slice(0, 12)}`,
      hint: "历史会话",
      run: () => {
        setView("chat");
        void loadHistory();
      },
    })),
  ];

  // ⌘K 面板打开时拉最近会话(P4-2 数据源)
  useEffect(() => {
    if (!paletteOpen) return;
    fetch("/api/sessions")
      .then((r) => (r.ok ? r.json() : []))
      .then((d: Array<{ id: string; title?: string; updated_at?: string }>) => setSessions(Array.isArray(d) ? d : []))
      .catch(() => setSessions([]));
  }, [paletteOpen]);

  // 会话列表:打开面板或每 30s 拉取(当前 workspace 全量,含 current 标记)
  const loadSessions = useCallback(() => {
    fetch("/api/sessions")
      .then((r) => (r.ok ? r.json() : { groups: [] }))
      .then((d) => {
        const groups = Array.isArray(d?.groups) ? d.groups : [];
        // 只显示当前 workspace 的会话(同桌面逻辑;后端 groups 已标记 current,
        // 缺标记时回退 current_workspace 匹配)
        const cur =
          groups.find((g: { current?: boolean }) => g.current) ??
          groups.find((g: { workspace?: string }) => g.workspace === d?.current_workspace);
        const rows = [...(cur?.sessions ?? [])];
        // 锁定的会话排到最后(活会话优先,锁定态不打断浏览)
        rows.sort((a: { locked_by_pid?: number; updated_at?: string }, b: { locked_by_pid?: number; updated_at?: string }) => {
          const la = a.locked_by_pid ? 1 : 0;
          const lb = b.locked_by_pid ? 1 : 0;
          if (la !== lb) return la - lb;
          return (b.updated_at ?? "").localeCompare(a.updated_at ?? "");
        });
        setSessions(rows);
        if (cur?.sessions?.length) setCurrentSessionId(cur.sessions[0].id);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    loadSessions();
    const t = setInterval(loadSessions, 30000);
    return () => clearInterval(t);
  }, [loadSessions]);

  // 切换请求:busy(锁定)时后端返回 409,前端以禁用态呈现锁定
  const switchTimerRef = useRef<number | undefined>(undefined);
  const switchSession = useCallback(
    (id: string) => {
      if (id === currentSessionId || switching) return;
      setSwitching(true);
      fetch("/api/sessions/switch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      })
        .then((r) => {
          if (r.ok) {
            setCurrentSessionId(id);
            // 后端切换经 program msg 队列异步落地:800ms+2.5s 双保险重拉
            // 聊天历史与队列快照(A→B 快切时清旧定时器,避免脏触发)
            window.clearTimeout(switchTimerRef.current);
            const recheck = (delay: number) => {
              switchTimerRef.current = window.setTimeout(() => {
                useChat.getState().loadHistory();
                fetch("/api/chat/queue")
                  .then((r2) => (r2.ok ? r2.json() : { items: [] }))
                  .then((d) => useChat.getState().syncQueue(d.items ?? []))
                  .catch(() => {});
              }, delay);
            };
            recheck(800);
            recheck(2500);
          }
          loadSessions();
        })
        .catch(() => {})
        .finally(() => setSwitching(false));
    },
    [currentSessionId, switching, loadSessions]
  );

  // ⌘N 新会话
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        resetChat();
        setView("chat");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [resetChat]);

  return (
    <div className="flex h-full bg-background text-foreground">
      {/* 三栏骨架:左会话栏 + 主区(Chat/Settings 切换) + 右 Pane(P1.5) */}
      <aside className={`fixed inset-y-0 left-0 z-40 w-[232px] shrink-0 flex-col border-r border-border bg-sidebar transition-transform md:static md:flex ${drawerOpen ? "flex" : "hidden"}`}>
        {drawerOpen && <div className="fixed inset-0 z-[-1] bg-black/40 md:hidden" onClick={() => setDrawerOpen(false)} />}
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-5">
          <img src="/icon.png" alt="ggcode" className="h-9 w-9 rounded-[10px]" />
          <div>
            <h1 className="text-ui-lg font-bold tracking-tight">
              gg<span className="text-brand">code</span>
            </h1>
            <p className="text-ui-xs mt-0.5 text-foreground-subtlest">AI coding workspace</p>
          </div>
        </div>
        <nav className="flex flex-1 flex-col gap-0.5 overflow-y-auto p-2">
          <button
            className={
              "rounded-lg px-3 py-2 text-left text-ui-sm transition-colors " +
              (view === "chat"
                ? "bg-selected font-medium text-foreground"
                : "text-foreground-subtle hover:bg-hover hover:text-foreground")
            }
            onClick={() => setView("chat")}
          >
            会话
          </button>
          <button
            className={
              "rounded-lg px-3 py-2 text-left text-ui-sm transition-colors " +
              (view === "settings"
                ? "bg-selected font-medium text-foreground"
                : "text-foreground-subtle hover:bg-hover hover:text-foreground")
            }
            onClick={() => setView("settings")}
          >
            ⚙ 设置
          </button>
          {/* 双隧道入口:📱 mobile 共享(二维码) / ⇄ et 公网隧道(可点 URL) */}
          <button
            className="rounded-lg px-3 py-2 text-left text-ui-sm text-foreground-subtle transition-colors hover:bg-hover hover:text-foreground"
            onClick={() => setShareOpen(true)}
            title="共享到 Mobile App(扫码)"
          >
            📱 共享
          </button>
          <button
            className="rounded-lg px-3 py-2 text-left text-ui-sm text-foreground-subtle transition-colors hover:bg-hover hover:text-foreground"
            onClick={() => setTunnelOpen(true)}
            title="et 公网隧道"
          >
            ⇄ 隧道
          </button>
        </nav>
        {/* 会话列表(同桌面逻辑:本 workspace 全量;busy 会话锁定不可点) */}
        <div className="border-t border-border flex-1 overflow-y-auto p-2">
          <div className="text-ui-xs px-3 py-1.5 text-foreground-subtlest">会话</div>
          {sessions.map((s) => {
            const current = s.id === currentSessionId;
            // locked_by_self=锁定者是本实例自己(自己打开的会话),不算“被锁”
            const locked = !!s.locked_by_pid && !s.locked_by_self;
            return (
              <button
                key={s.id}
                disabled={current || switching || locked}
                title={current ? "当前会话" : locked ? `被另一实例锁定 (PID ${s.locked_by_pid})` : switching ? "切换中…" : "切换到此会话"}
                className={
                  "w-full truncate rounded-lg px-3 py-1.5 text-left text-ui-xs transition-colors " +
                  (current
                    ? "bg-selected font-medium text-foreground"
                    : locked
                      ? "cursor-not-allowed text-foreground-subtlest opacity-60"
                      : switching
                        ? "cursor-wait text-foreground-subtlest"
                        : "text-foreground-subtle hover:bg-hover hover:text-foreground")
                }
                onClick={() => switchSession(s.id)}
              >
                {locked ? "🔒 " : ""}{s.title || s.id.slice(0, 16)}
              </button>
            );
          })}
          {sessions.length === 0 && <div className="px-3 py-1.5 text-ui-xs text-foreground-subtlest">(暂无会话)</div>}
        </div>
        <div className="border-t border-border px-4 py-3 text-ui-xs text-foreground-subtlest">
          WebUI {view === "chat" ? "P1 · 聊天" : "P2 · 设置"} · 建设中
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-hidden">
        <div className="flex items-center gap-2 border-b border-border px-3 py-2 md:hidden">
          <button
            className="text-ui-sm rounded-lg px-2 py-1 text-foreground-subtle hover:bg-hover"
            onClick={() => setDrawerOpen(true)}
            title="打开菜单"
          >
            ☰
          </button>
          <img src="/icon.png" alt="" className="h-5 w-5 rounded-md" />
          <span className="text-ui-sm font-semibold">ggcode</span>
          {/* 移动端 ⌘K 等价入口(pointer: coarse 无组合键,点按唤起命令面板) */}
          <button
            className="text-ui-sm ml-auto rounded-lg px-2 py-1 text-foreground-subtle hover:bg-hover md:hidden"
            onClick={() => setPaletteOpen(true)}
            title="命令面板"
          >
            ⌘
          </button>
          {/* 右栏:移动=打开 overlay;桌面=切换常驻(pin/unpin,localStorage 持久) */}
          <button
            className="text-ui-sm rounded-lg px-2 py-1 text-foreground-subtle hover:bg-hover lg:hidden"
            onClick={() => setPaneOpen(true)}
            title="活动面板"
          >
            ☰
          </button>
          <button
            className="text-ui-sm hidden rounded-lg px-2 py-1 text-foreground-subtle hover:bg-hover lg:inline-block"
            onClick={() => {
              setPanePinned((v) => {
                localStorage.setItem("ggcode-pane-pinned", v ? "0" : "1");
                return !v;
              });
            }}
            title={panePinned ? "隐藏右侧栏" : "显示右侧栏"}
          >
            {panePinned ? "⬗" : "☰ 面板"}
          </button>
        </div>
        {view === "chat" ? <Chat /> : <Settings onBack={() => setView("chat")} />}
      </main>
      {paneOpen && <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setPaneOpen(false)} />}
      <aside className={`fixed inset-y-0 right-0 z-50 w-[92vw] max-w-[420px] flex-col border-l border-border bg-sidebar transition-transform lg:static lg:z-auto lg:w-[380px] lg:max-w-none ${panePinned ? "lg:flex" : "lg:hidden"} ${paneOpen ? "flex" : "hidden"}`} style={paneSpawn || activeStreams.length > 0 || todos.length > 0 || cronEvents.length > 0 ? { width: "380px" } : undefined}>
        {/* 活动面板:任务清单/子代理/Cron 三 tab 独立区域(零事件 tab 灰显) */}
        {(todos.length > 0 || activeStreams.length > 0 || cronEvents.length > 0) && (
          <div className="flex min-h-0 flex-col border-b border-border">
            <div className="text-ui-xs flex shrink-0 border-b border-border">
              {([
                { id: "todos", label: `任务 ${todos.length}`, on: todos.length > 0 },
                { id: "subs", label: `子代理 ${activeStreams.length}`, on: activeStreams.length > 0 },
                { id: "cron", label: `Cron ${cronEvents.length}`, on: cronEvents.length > 0 },
              ] as const).map((t) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setActTab(t.id)}
                  className={`flex-1 border-b-2 px-2 py-2 transition-colors ${
                    actTab === t.id ? "border-brand text-foreground" : t.on ? "border-transparent text-foreground-subtle hover:text-foreground" : "border-transparent text-foreground-subtlest"
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
              {actTab === "todos" && (
                todos.length === 0 ? (
                  <div className="px-4 py-3 text-ui-xs text-foreground-subtlest">暂无任务清单(agent 运行 todo_write 时出现)</div>
                ) : (
                  <>
                    <div className="px-4 py-2 text-ui-xs text-foreground-subtlest">
                      进度 {todos.filter((t) => t.status === "done").length}/{todos.length}
                    </div>
                    {todos.map((t) => (
                      <div key={t.id} className="flex items-start gap-2 border-t border-border px-4 py-1.5">
                        <span
                          className="mt-1 inline-block h-2 w-2 shrink-0 rounded-full"
                          style={{
                            background:
                              t.status === "done" ? "var(--color-success)" : t.status === "in_progress" ? "var(--color-trajectory-tool-call)" : "var(--color-foreground-subtlest)",
                          }}
                          title={t.status}
                        />
                        <span
                          className={`text-ui-xs text-wrap-phrase ${t.status === "done" ? "text-foreground-subtlest line-through" : t.status === "in_progress" ? "text-foreground" : "text-foreground-subtle"}`}
                        >
                          {t.content}
                        </span>
                      </div>
                    ))}
                  </>
                )
              )}
              {actTab === "subs" && (
                activeStreams.length === 0 ? (
                  <div className="px-4 py-3 text-ui-xs text-foreground-subtlest">无活跃子代理(spawn_agent 运行时实时流在此)</div>
                ) : (
                  activeStreams.map(([id, s]) => (
                    <div key={id} className="border-t border-border px-4 py-2 first:border-t-0">
                      <div className="mb-1 flex items-center gap-2">
                        <span
                          className="inline-block h-2 w-2 rounded-full"
                          style={{ background: s.done ? (s.isError ? "var(--color-destructive)" : "var(--color-success)") : "var(--color-trajectory-tool-call)" }}
                        />
                        <span className="text-ui-xs font-[family-name:var(--font-mono)] text-foreground-subtle">{id.slice(0, 12)}</span>
                        <span className="text-ui-xs ml-auto text-foreground-subtlest">{s.done ? (s.isError ? "失败" : "完成") : "运行中"}</span>
                      </div>
                      <pre className="text-ui-xs max-h-32 overflow-auto font-[family-name:var(--font-mono)] whitespace-pre-wrap text-foreground-subtle">
                        {s.text || s.reasoning || "…"}
                      </pre>
                    </div>
                  ))
                )
              )}
              {actTab === "cron" && (
                cronEvents.length === 0 ? (
                  <div className="px-4 py-3 text-ui-xs text-foreground-subtlest">暂无 Cron 触发事件(定时任务到点时在此归集)</div>
                ) : (
                  cronEvents.map((e, i) => (
                    <div key={`${i}-${e.at}`} className="flex items-start gap-2 border-t border-border px-4 py-1.5 first:border-t-0">
                      <span className="mt-0.5 inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--color-trajectory-tool-call)" }} />
                      <span className="text-ui-xs font-[family-name:var(--font-mono)] text-foreground-subtlest">{e.at}</span>
                      <span className="text-ui-xs text-wrap-phrase text-foreground-subtle">{e.text}</span>
                    </div>
                  ))
                )
              )}
            </div>
          </div>
        )}
        {paneSpawn ? (
          <div className="flex h-full flex-col">
            <div className="flex items-center gap-2 border-b border-border px-4 py-3">
              <span
                className="inline-block h-2.5 w-2.5 rounded-full"
                style={{ background: paneSpawn.isError ? "var(--color-destructive)" : paneSpawn.done ? "var(--color-success)" : "var(--color-trajectory-tool-call)" }}
              />
              <h3 className="text-ui-sm flex-1 truncate font-medium" style={{ color: "var(--color-agent-node-fg)" }}>
                Spawn {paneSpawn.name}
              </h3>
              <button
                className="text-ui-xs rounded px-2 py-0.5 text-foreground-subtlest hover:bg-hover"
                onClick={closePane}
                title="关闭"
              >
                ✕
              </button>
            </div>
            <pre className="text-ui-xs flex-1 overflow-auto p-4 font-[family-name:var(--font-mono)] whitespace-pre-wrap text-foreground-subtle">
              {paneSpawn.result || paneSpawn.argsText || "(运行中…结果就绪后自动更新)"}
            </pre>
          </div>
        ) : null}
      </aside>
      {paletteOpen && (
        <CommandPalette actions={paletteActions} onClose={() => setPaletteOpen(false)} />
      )}
      {shareOpen && <ShareModal onClose={() => setShareOpen(false)} />}
      {tunnelOpen && <TunnelModal onClose={() => setTunnelOpen(false)} />}
    </div>
  );
}
