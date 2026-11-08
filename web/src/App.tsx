import { useEffect, useState } from "react";
import Chat from "@/pages/Chat";
import Settings from "@/pages/Settings";
import CommandPalette, { type PaletteAction } from "@/pages/CommandPalette";
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
  // ⌘K 会话切换数据源:最近 sessions(P4-2),面板打开时拉取
  const [sessions, setSessions] = useState<Array<{ id: string; title?: string; updated_at?: string }>>([]);

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

  // 字号档循环(P5-5):调整 text-ui-* 基准,持久化 localStorage
  const FONT_SIZES = ["12px", "13px", "14px", "16px"] as const;
  const [fontSize, setFontSizeState] = useState<string>(() => localStorage.getItem("ggcode-font-size") || "14px");
  useEffect(() => {
    document.documentElement.style.fontSize = fontSize;
  }, [fontSize]);
  const cycleFontSize = () => {
    const idx = FONT_SIZES.indexOf(fontSize as (typeof FONT_SIZES)[number]);
    const next = FONT_SIZES[(idx + 1) % FONT_SIZES.length];
    localStorage.setItem("ggcode-font-size", next);
    setFontSizeState(next);
  };

  const paletteActions: PaletteAction[] = [
    { id: "go-chat", label: "打开会话", hint: "视图", run: () => setView("chat") },
    { id: "go-settings", label: "打开设置", hint: "视图", run: () => setView("settings") },
    { id: "theme", label: `切换主题 (当前:${THEME_LABEL[theme]})`, hint: "dark→light→system", run: cycleTheme },
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
      <aside className="hidden w-[232px] shrink-0 flex-col border-r border-border bg-sidebar md:flex">
        <div className="border-b border-border px-4 py-5">
          <h1 className="text-ui-lg font-bold tracking-tight">
            gg<span className="text-brand">code</span>
          </h1>
          <p className="text-ui-xs mt-0.5 text-foreground-subtlest">AI coding workspace</p>
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
          <button
            className="rounded-lg px-3 py-2 text-left text-ui-sm text-foreground-subtle transition-colors hover:bg-hover hover:text-foreground"
            onClick={cycleTheme}
            title="切换主题 (dark → light → system)"
          >
            主题: {THEME_LABEL[theme]}
          </button>
          <button
            className="rounded-lg px-3 py-2 text-left text-ui-sm text-foreground-subtle transition-colors hover:bg-hover hover:text-foreground"
            onClick={cycleFontSize}
            title="字号 (12 → 13 → 14 → 16)"
          >
            字号: {fontSize.replace("px", "")}
          </button>
        </nav>
        <div className="border-t border-border px-4 py-3 text-ui-xs text-foreground-subtlest">
          WebUI {view === "chat" ? "P1 · 聊天" : "P2 · 设置"} · 建设中
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-hidden">
        {view === "chat" ? <Chat /> : <Settings onBack={() => setView("chat")} />}
      </main>
      <aside className="hidden w-[380px] shrink-0 flex-col border-l border-border bg-sidebar lg:block" style={paneSpawn ? { width: "380px" } : undefined}>
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
    </div>
  );
}
