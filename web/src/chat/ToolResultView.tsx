import { useState } from "react";
import type { ToolInvocation } from "@/store/chat";
import DiffView from "./DiffView";
import { MarkdownLite } from "./MessageBubble";
import { Chip, SectionHead } from "./ToolArgsView";
import {
  countLines, langOf, parseNumberedLines, parseRunResult, parseSearchResult, parseUnifiedDiff,
  shortPath, parseArgs, str, toolKind, num, type DiffLine,
} from "./toolPresent";

const MAX_LINES = 200;
const MAX_CHARS = 20000;

/** 结果视图:按工具类别给出可读输出;始终保留"原始"入口 */
export default function ToolResultView({ tool }: { tool: ToolInvocation }) {
  const raw = tool.result ?? "";
  const [all, setAll] = useState(false);
  const payload = tool.resultPayload || raw;
  const kind = toolKind(tool.name);

  if (!raw && !payload) {
    return <div className="px-3 py-2 text-ui-xs text-foreground-subtlest">(无输出)</div>;
  }

  // 错误:红块,保留失败原因分段
  if (tool.isError) {
    const failed = kind === "run" ? parseRunResult(raw).failed : "";
    return (
      <div className="px-3 py-2 text-ui-xs">
        {failed && <div className="mb-1 whitespace-pre-wrap font-medium text-[var(--color-destructive-surface-fg)]">{failed}</div>}
        <pre className="whitespace-pre-wrap break-words font-[family-name:var(--font-mono)] text-[var(--color-destructive-surface-fg)]" style={{ maxHeight: 320, overflow: "auto" }}>
          {kind === "run" ? parseRunResult(raw).stdout || parseRunResult(raw).stderr : raw}
        </pre>
      </div>
    );
  }

  switch (kind) {
    case "run":
      return <RunResult raw={raw} />;
    case "read":
      if (tool.name === "list_directory") {
        const names = raw.split("\n").map((l) => l.trim()).filter(Boolean);
        return (
          <div className="flex flex-wrap gap-1.5 px-3 py-2 text-ui-xs">
            {names.map((n, i) => <Chip key={i} mono>{n}</Chip>)}
          </div>
        );
      }
      return <ReadResult tool={tool} raw={raw} all={all} setAll={setAll} />;
    case "search":
      return <SearchResultView raw={raw} />;
    case "edit":
    case "write": {
      const diff = parseUnifiedDiff(raw);
      if (diff) {
        return (
          <div>
            <SectionHead left={<span>{firstLine(raw)}</span>} />
            <DiffView lines={diff} />
          </div>
        );
      }
      return <TextResult text={raw} />;
    }
    case "web":
      return <WebResult text={payload} raw={raw} />;
    case "todo":
      return <div className="px-3 py-2 text-ui-xs text-foreground-subtle">{raw.trim() ? raw.trim() : "已更新"}</div>;
    case "task":
    case "delegate":
    case "mcp":
    case "ask": {
      // 服务端给了结构化载荷(task_list / cron_list / json / text)
      if (tool.payloadMode === "json" || looksLikeJSON(payload)) return <TextResult text={prettyJSON(payload)} />;
      return <TextResult text={payload} summary={tool.resultSummary} />;
    }
    default:
      return <TextResult text={raw} />;
  }
}

function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return (i >= 0 ? s.slice(0, i) : s).trim();
}

function looksLikeJSON(s: string): boolean {
  const t = s.trim();
  return (t.startsWith("{") && t.endsWith("}")) || (t.startsWith("[") && t.endsWith("]"));
}

function prettyJSON(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

/** 通用文本块:截断 + 展开全部 + 行数 */
function TextResult({ text, summary }: { text: string; summary?: string }) {
  const [all, setAll] = useState(false);
  const lines = text.split("\n");
  const truncated = !all && (lines.length > MAX_LINES || text.length > MAX_CHARS);
  const shown = truncated ? lines.slice(0, MAX_LINES).join("\n").slice(0, MAX_CHARS) : text;
  return (
    <div>
      {summary && summary !== firstLine(text) && !firstLine(text).startsWith(summary) && <div className="px-3 pt-2 text-ui-xs text-foreground">{summary}</div>}
      <pre className="max-h-80 overflow-auto px-3 py-2 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap break-words text-foreground-subtle">{shown}</pre>
      {truncated && (
        <button className="w-full border-t border-border px-3 py-1 text-left text-ui-xs text-brand hover:bg-[var(--color-hover)]" onClick={() => setAll(true)}>
          显示全部 {lines.length} 行 / {text.length} 字符
        </button>
      )}
    </div>
  );
}

function RunResult({ raw }: { raw: string }) {
  const r = parseRunResult(raw);
  if (r.noOutput) return <div className="px-3 py-2 text-ui-xs text-foreground-subtlest">命令执行完成,无输出</div>;
  return (
    <div>
      {r.stdout && <TextResult text={r.stdout} />}
      {r.stderr && (
        <div>
          <SectionHead left={<span className="text-[var(--color-destructive-surface-fg)]">stderr</span>} right={<span>{countLines(r.stderr)} 行</span>} />
          <pre className="max-h-60 overflow-auto px-3 py-2 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap break-words text-[var(--color-destructive-surface-fg)]">{r.stderr}</pre>
        </div>
      )}
      {r.failed && <div className="border-t border-border px-3 py-2 text-ui-xs whitespace-pre-wrap text-[var(--color-destructive-surface-fg)]">{r.failed}</div>}
      {!r.stdout && !r.stderr && !r.failed && <TextResult text={raw} />}
    </div>
  );
}

/** `cat -n` 行号代码视图;否则普通文本 */
function ReadResult({ tool, raw, all, setAll }: { tool: ToolInvocation; raw: string; all: boolean; setAll: (v: boolean) => void }) {
  const a = parseArgs(tool.argsText);
  // 头部优先用服务端相对化后的 detail,避免整段绝对路径
  const path = tool.detail || shortPath(str(a.file_path) || str(a.path));
  const offset = num(a.offset) ?? 1;
  const numbered = parseNumberedLines(raw);
  const lines = numbered ?? raw.split("\n").map((t) => ({ no: NaN, text: t }));
  const limit = num(a.limit);
  const shown = all || lines.length <= MAX_LINES ? lines : lines.slice(0, MAX_LINES);
  const startNo = Number.isNaN(shown[0]?.no) ? offset : shown[0].no;
  return (
    <div>
      <SectionHead
        left={<span className="font-[family-name:var(--font-mono)]">{path}</span>}
        right={<>
          {path && langOf(path) && <span>{langOf(path)}</span>}
          <span>{lines.length} 行{limit ? ` · 请求 ${limit} 行` : ""}</span>
        </>}
      />
      <div className="max-h-96 overflow-auto py-1 font-[family-name:var(--font-mono)] text-ui-xs leading-5">
        {shown.map((l, i) => (
          <div key={i} className="flex">
            <span className="w-12 shrink-0 select-none pr-3 text-right text-foreground-subtlest">{Number.isNaN(l.no) ? startNo + i : l.no}</span>
            <span className="min-w-0 flex-1 whitespace-pre text-foreground">{l.text || " "}</span>
          </div>
        ))}
      </div>
      {lines.length > MAX_LINES && (
        <button className="w-full border-t border-border px-3 py-1 text-left text-ui-xs text-brand hover:bg-[var(--color-hover)]" onClick={() => setAll(!all)}>
          {all ? "只显示前 200 行" : `显示全部 ${lines.length} 行`}
        </button>
      )}
    </div>
  );
}

/** grep 输出按文件分组 */
function SearchResultView({ raw }: { raw: string }) {
  const parsed = parseSearchResult(raw);
  if (!parsed) return <TextResult text={raw} />;
  return (
    <div className="max-h-96 overflow-auto text-ui-xs">
      <div className="px-3 py-1.5 text-foreground-subtle">{parsed.matches} 处匹配 · {parsed.files} 个文件</div>
      {parsed.byFile.map((f) => (
        <div key={f.path}>
          <div className="sticky top-0 flex items-center gap-2 border-b border-border bg-[var(--color-surface)] px-3 py-1">
            <span className="truncate font-[family-name:var(--font-mono)] text-foreground">{shortPath(f.path)}</span>
            <span className="ml-auto shrink-0 text-foreground-subtlest">{f.rows.length}</span>
          </div>
          {f.rows.map((r, i) => (
            <div key={i} className="flex px-3 leading-5 hover:bg-[var(--color-hover)]">
              <span className="w-10 shrink-0 select-none pr-3 text-right text-foreground-subtlest">{r.line}</span>
              <span className="min-w-0 flex-1 truncate whitespace-pre font-[family-name:var(--font-mono)] text-foreground-subtle">{r.text}</span>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

/** 网页/搜索结果:markdown 渲染,超长折叠 */
function WebResult({ text, raw }: { text: string; raw: string }) {
  const [all, setAll] = useState(false);
  const body = text || raw;
  const truncated = !all && body.length > MAX_CHARS;
  return (
    <div className="px-3 py-2">
      <div className="max-h-96 overflow-auto text-ui-sm">
        <MarkdownLite text={truncated ? body.slice(0, MAX_CHARS) : body} />
      </div>
      {truncated && (
        <button className="mt-1 text-ui-xs text-brand hover:underline" onClick={() => setAll(true)}>
          显示完整内容({body.length} 字符)
        </button>
      )}
    </div>
  );
}

export { Chip, SectionHead, DiffLine };
