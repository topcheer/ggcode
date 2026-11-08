import { useState } from "react";
import type { ToolInvocation } from "@/store/chat";

/** ToolCallCard(§4.2 状态机):spinner→绿/红状态灯+耗时徽章;对齐 TUI 分类学的特判渲染 */
export default function ToolCallCard({ tool, onOpenPane }: { tool: ToolInvocation; onOpenPane?: (tool: ToolInvocation) => void }) {
  const [expanded, setExpanded] = useState(false);
  const dur = tool.endedAt ? tool.endedAt - tool.startedAt : null;

  // 特判:diff 类工具内嵌 diff 视图(TUI editdiff 同判);P1.5 随 tool_result_rich 结构化升级
  const isDiffTool = /^(edit_file|multi_edit_file|write_file|apply_patch|notebook_edit)$/.test(tool.name);

  // Spawn 条目形态(§6.1):子代理委派类工具渲染为独立入口——
  // 主聊天流只出现一行 "Spawn <name> →"(状态灯),点击展开推理/结果,
  // 可推入右侧 Pane 并排阅读(不污染主流——ZCode 关键体验)
  const spawnName = parseSpawnTarget(tool);
  if (spawnName !== null) {
    return (
      <div className="rounded-xl border border-[var(--color-agent-node)] bg-card overflow-hidden">
        <button
          className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-[var(--color-hover)]"
          onClick={() => (onOpenPane ? onOpenPane(tool) : setExpanded(!expanded))}
        >
          <StateLight tool={tool} />
          <span
            className="text-ui-sm font-medium"
            style={{ color: "var(--color-agent-node-fg)" }}
          >
            Spawn {spawnName}
          </span>
          {!tool.done && <span className="text-ui-xs text-foreground-subtlest">推理中…</span>}
          {tool.done && !tool.isError && <span className="text-ui-xs text-foreground-subtlest">已完成 → 点击查看报告</span>}
          {tool.isError && <span className="text-ui-xs text-[var(--color-destructive-surface-fg)]">失败</span>}
          <span className="ml-auto text-ui-xs text-foreground-subtlest">{onOpenPane ? "⤢" : expanded ? "▾" : "▸"}</span>
        </button>
        {expanded && !onOpenPane && tool.result && (
          <pre
            className="text-ui-xs max-h-72 overflow-auto border-t border-border p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap text-foreground-subtle"
          >
            {tool.result}
          </pre>
        )}
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-border bg-card overflow-hidden">
      <button
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-[var(--color-hover)]"
        onClick={() => setExpanded(!expanded)}
      >
        <StateLight tool={tool} />
        <span className="text-ui-sm font-medium text-foreground">{tool.name}</span>
        <ArgsPreview argsText={tool.argsText} />
        <span className="ml-auto flex items-center gap-2">
          {dur !== null && (
            <span className="text-ui-xs text-foreground-subtlest">{(dur / 1000).toFixed(1)}s</span>
          )}
          <span className="text-ui-xs text-foreground-subtlest">{expanded ? "▾" : "▸"}</span>
        </span>
      </button>
      {expanded && (
        <div className="border-t border-border">
          {tool.argsText && (
            <pre className="text-ui-xs max-h-48 overflow-auto bg-[var(--color-input)] p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap text-foreground-subtle">
              {safePretty(tool.argsText)}
            </pre>
          )}
          {tool.result && (
            <DiffAwareResult isDiffTool={isDiffTool} result={tool.result} isError={!!tool.isError} />
          )}
        </div>
      )}
      {/* isGit 彩色语义由 result 文本色 tokens 承担;P1.5 结构化 diff 视图待 tool_result_rich */}
    </div>
  );
}

function StateLight({ tool }: { tool: ToolInvocation }) {
  if (!tool.done)
    return <span className="inline-block h-2.5 w-2.5 animate-spin rounded-full border-2 border-[var(--color-trajectory-tool-call)] border-t-transparent" />;
  return (
    <span
      className="inline-block h-2.5 w-2.5 rounded-full"
      style={{ background: tool.isError ? "var(--color-destructive)" : "var(--color-success)" }}
    />
  );
}

function ArgsPreview({ argsText }: { argsText: string }) {
  try {
    const o = JSON.parse(argsText || "{}") as Record<string, unknown>;
    const hint = o.file_path || o.path || o.pattern || o.command || o.query || "";
    return hint ? (
      <span className="text-ui-xs truncate font-[family-name:var(--font-mono)] text-foreground-subtlest">
        {String(hint).slice(0, 48)}
      </span>
    ) : null;
  } catch {
    return null;
  }
}

function safePretty(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

/** 子代理委派类工具识别:返回展示名(agent/template),非委派类返回 null */
function parseSpawnTarget(tool: ToolInvocation): string | null {
  try {
    const o = JSON.parse(tool.argsText || "{}") as Record<string, unknown>;
    switch (tool.name) {
      case "spawn_agent":
      case "delegate":
        return String(o.task ?? o.prompt ?? "agent").slice(0, 40) || "agent";
      case "use_namedagent":
        return String(o.name ?? "named agent");
      case "teammate_spawn":
        return String(o.name ?? "teammate");
      default:
        return null;
    }
  } catch {
    return null;
  }
}

/** diff 结果逐行渲染(P4-3):识别 unified diff +/- 行上色;非 diff 文本原样 */
function DiffAwareResult({ isDiffTool, result, isError }: { isDiffTool: boolean; result: string; isError: boolean }) {
  const looksLikeDiff = isDiffTool && /^(diff --git|@@ -|[+-]{1}[^+-])/m.test(result);
  if (!looksLikeDiff) {
    return (
      <pre
        className="text-ui-xs max-h-72 overflow-auto p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap"
        style={{ color: isError ? "var(--color-destructive-surface-fg)" : "var(--color-foreground-subtle)" }}
      >
        {result}
      </pre>
    );
  }
  return (
    <pre className="text-ui-xs max-h-72 overflow-auto p-3 font-[family-name:var(--font-mono)] whitespace-pre-wrap">
      {result.split("\n").map((line, i) => {
        let bg = "transparent";
        let fg = "var(--color-foreground-subtle)";
        if (line.startsWith("+")) {
          bg = "var(--color-success-surface)";
          fg = "var(--color-success-surface-fg)";
        } else if (line.startsWith("-")) {
          bg = "var(--color-destructive-surface)";
          fg = "var(--color-destructive-surface-fg)";
        } else if (line.startsWith("@@")) {
          fg = "var(--color-info, #4c9bd8)";
        }
        return (
          <span key={i} style={{ background: bg, color: fg, display: "block" }}>
            {line || " "}
          </span>
        );
      })}
    </pre>
  );
}
