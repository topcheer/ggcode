import { useEffect, useMemo, useRef, useState } from "react";
import type { ToolInvocation } from "@/store/chat";
import { usePane } from "@/store/pane";
import { kindVisual, parseArgs, resultBadge, toolKind, toolTarget, toolTitle, prettifyToolName, mcpTitle, str, truncate, defaultExpanded } from "./toolPresent";
import ToolArgsView from "./ToolArgsView";
import ToolResultView from "./ToolResultView";

/**
 * ToolCallCard:工具调用的核心体验单元。
 * 头部(始终可见):状态 → 类别图标 → 展示名(服务端 tool.DescribeTool)→ 目标 → 结果徽章 → 耗时 → 展开箭头
 * 正文(展开):按类别的参数视图 + 按类别的结果视图;"原始"切换查看 JSON/原文
 * 默认展开规则见 toolPresent.defaultExpanded:错误/编辑/todo/小结果直接展开,大输出折叠(徽章给结论)
 */
export default function ToolCallCard({ tool }: { tool: ToolInvocation }) {
  const kind = toolKind(tool.name);
  const visual = kindVisual(kind);
  const openPane = usePane((s) => s.open);
  const [expanded, setExpanded] = useState<boolean>(() => defaultExpanded(tool));
  const [rawMode, setRawMode] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const running = !tool.done;

  // 运行中:实时秒数
  useEffect(() => {
    if (!running) return;
    const tick = () => setElapsed(Math.max(0, Math.round((Date.now() - tool.startedAt) / 1000)));
    tick();
    const t = setInterval(tick, 1000);
    return () => clearInterval(t);
  }, [running, tool.startedAt]);

  const durMs = tool.done && tool.endedAt && tool.startedAt ? tool.endedAt - tool.startedAt : null;
  // 流式期间挂载时还是"执行中",结果到达后按规则自动展开一次(用户手动折叠后不再打扰)
  const autoOpened = useRef(false);
  const manual = useRef(false);
  useEffect(() => {
    if (!tool.done || autoOpened.current || manual.current) return;
    if (defaultExpanded(tool)) {
      autoOpened.current = true;
      setExpanded(true);
    }
  }, [tool.done, tool.result]);
  const toggle = () => {
    manual.current = true;
    setExpanded((v) => !v);
  };
  const title = useMemo(() => toolTitle(tool), [tool]);
  const target = useMemo(() => toolTarget(tool), [tool]);
  const badge = useMemo(() => resultBadge(tool), [tool]);
  const sub = kind === "run" && tool.displayName ? prettifyToolName(tool.name) : kind === "mcp" ? mcpTitle(tool.name) ?? null : null;

  // 子代理委派:一行入口,点击进右侧 Pane(保留既有体验)
  if (kind === "delegate" && (tool.name === "spawn_agent" || tool.name === "delegate" || tool.name === "use_namedagent" || tool.name === "teammate_spawn")) {
    const a = parseArgs(tool.argsText);
    const spawnName = str(a.name) || truncate(str(a.task) || str(a.prompt) || "agent", 40);
    return (
      <div className="overflow-hidden rounded-xl border border-[var(--color-agent-node)] bg-card">
        <button
          className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-[var(--color-hover)]"
          onClick={() => openPane(tool)}
        >
          <span style={{ color: visual.color }}>{visual.icon}</span>
          <span className="text-ui-sm font-medium" style={{ color: "var(--color-agent-node-fg)" }}>
            {spawnName}
          </span>
          <span className="text-ui-xs text-foreground-subtlest">{tool.name === "spawn_agent" ? "子代理" : tool.name === "teammate_spawn" ? "队友" : "子代理"}</span>
          {running && <span className="text-ui-xs text-foreground-subtlest">推理中…</span>}
          {tool.done && !tool.isError && <span className="text-ui-xs text-foreground-subtlest">已完成 → 点击查看报告</span>}
          {tool.isError && <span className="text-ui-xs text-[var(--color-destructive-surface-fg)]">失败</span>}
          <span className="ml-auto text-ui-xs text-foreground-subtlest">⤢ 并排查看</span>
        </button>
      </div>
    );
  }

  const hasBody = !!tool.argsText || !!tool.result;
  const expandable = hasBody;

  return (
    <div
      className="overflow-hidden rounded-xl border bg-card"
      style={{ borderColor: tool.isError ? "var(--color-destructive)" : "var(--color-border)" }}
    >
      <div
        className={"flex items-center gap-2 px-3 py-2 text-left " + (expandable ? "cursor-pointer select-none transition-colors hover:bg-[var(--color-hover)]" : "")}
        onClick={() => expandable && toggle()}
        title={sub ?? undefined}
      >
        {running ? (
          <span className="inline-block h-2.5 w-2.5 animate-spin rounded-full border-2 border-[var(--color-trajectory-tool-call)] border-t-transparent" />
        ) : (
          <span
            className="inline-block h-2.5 w-2.5 rounded-full"
            style={{ background: tool.isError ? "var(--color-destructive)" : "var(--color-success)" }}
          />
        )}
        <span className="shrink-0 text-ui-sm" style={{ color: tool.isError ? "var(--color-destructive-surface-fg)" : visual.color }} aria-hidden>
          {visual.icon}
        </span>
        <span className={"min-w-0 truncate text-ui-sm " + (kind === "run" ? "font-medium" : "font-medium text-foreground")} title={title}>
          {kind === "run" && sub ? (
            <>
              {title} <span className="font-normal text-foreground-subtlest">· {sub}</span>
            </>
          ) : (
            title
          )}
        </span>
        {target && target !== title && (
          <span className="min-w-0 max-w-[45%] truncate font-[family-name:var(--font-mono)] text-ui-xs text-foreground-subtlest" title={target}>
            {target}
          </span>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-2 text-ui-xs text-foreground-subtlest">
          {running && elapsed > 0 && <span className="font-[family-name:var(--font-mono)]">{fmtDur(elapsed)}</span>}
          {!running && durMs !== null && durMs >= 1000 && <span className="font-[family-name:var(--font-mono)]">{fmtDur(Math.round(durMs / 1000))}</span>}
          {badge && (
            <span
              className="max-w-[40vw] truncate rounded-md px-1.5 py-0.5"
              style={{
                background: tool.isError ? "var(--color-destructive-surface)" : "var(--color-tag)",
                color: tool.isError ? "var(--color-destructive-surface-fg)" : "var(--color-tag-fg)",
              }}
              title={badge}
            >
              {badge}
            </span>
          )}
          {expandable && <span>{expanded ? "▾" : "▸"}</span>}
        </span>
      </div>

      {expanded && (
        <div className="border-t border-border">
          {rawMode ? (
            <div className="flex flex-col">
              {tool.argsText && (
                <pre className="max-h-64 overflow-auto bg-[var(--color-input)] p-3 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap break-all text-foreground-subtle">
                  {pretty(tool.argsText)}
                </pre>
              )}
              {tool.result && (
                <pre className="max-h-80 overflow-auto p-3 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap break-words text-foreground-subtle">
                  {tool.result}
                </pre>
              )}
            </div>
          ) : (
            <>
              {tool.argsText && <ToolArgsView tool={tool} />}
              {tool.result && <div className="border-t border-border"><ToolResultView tool={tool} /></div>}
              {!tool.result && running && <div className="px-3 py-2 text-ui-xs text-foreground-subtlest">执行中…</div>}
            </>
          )}
          <div className="flex items-center gap-2 border-t border-border px-3 py-1 text-ui-xs text-foreground-subtlest">
            <span className="font-[family-name:var(--font-mono)]">{tool.name}</span>
            <button
              className="ml-auto rounded-md border border-border px-1.5 py-0.5 hover:bg-[var(--color-hover)]"
              onClick={(e) => {
                e.stopPropagation();
                setRawMode((v) => !v);
              }}
            >
              {rawMode ? "结构化视图" : "原始 JSON"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return s > 0 ? `${m}m ${s}s` : `${m}m`;
}

function pretty(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
