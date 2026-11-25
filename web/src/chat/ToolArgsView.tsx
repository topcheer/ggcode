import { useState } from "react";
import type { ToolInvocation } from "@/store/chat";
import DiffView from "./DiffView";
import {
  buildDiff, countLines, langOf, parseArgs, shortPath, str, toolKind, num,
  type DiffLine,
} from "./toolPresent";

/** 参数(输入)视图:按工具类别给出符合直觉的呈现,而非 JSON 原文 */
export default function ToolArgsView({ tool }: { tool: ToolInvocation }) {
  const a = parseArgs(tool.argsText);
  const kind = toolKind(tool.name);
  const path = shortPath(str(a.file_path) || str(a.path) || str(a.notebook_path));
  switch (kind) {
    case "run":
      return <RunArgs tool={tool} a={a} />;
    case "read":
      // 路径/语言已由头部与结果区给出,这里只补范围信息
      return (
        <div className="flex flex-wrap items-center gap-1.5 px-3 py-2 text-ui-xs text-foreground-subtle">
          {num(a.offset) !== undefined && <Chip>起始行 {num(a.offset)}</Chip>}
          {num(a.limit) !== undefined && <Chip>读 {num(a.limit)} 行</Chip>}
          {str(a.pattern) && <Chip mono>/{str(a.pattern)}/</Chip>}
          {!path && Object.keys(a).length === 0 && <RawArgs tool={tool} />}
        </div>
      );
    case "edit":
      return <EditArgs a={a} />;
    case "write":
      return <WriteArgs tool={tool} a={a} />;
    case "search":
      return (
        <div className="flex flex-wrap items-center gap-1.5 px-3 py-2 text-ui-xs text-foreground-subtle">
          {(str(a.pattern) || str(a.query)) && <Chip mono>/{str(a.pattern) || str(a.query)}/</Chip>}
          {(str(a.path) || str(a.directory)) && <Chip mono>{shortPath(str(a.path) || str(a.directory))}</Chip>}
          {str(a.glob) && <Chip mono>{str(a.glob)}</Chip>}
          {str(a.type) && <Chip>{str(a.type)}</Chip>}
          {a.case_insensitive === true && <Chip>忽略大小写</Chip>}
          {a["-i"] === true && <Chip>忽略大小写</Chip>}
          {num(a.head_limit) !== undefined && <Chip>最多 {num(a.head_limit)}</Chip>}
          {str(a.query) && str(a.prompt) && <div className="w-full px-0 py-1 text-foreground-subtle whitespace-pre-wrap">{str(a.prompt)}</div>}
        </div>
      );
    case "web":
      return (
        <div className="flex flex-col gap-1 px-3 py-2 text-ui-xs">
          {str(a.url) && (
            <a href={str(a.url)} target="_blank" rel="noreferrer" className="truncate text-brand underline decoration-dashed underline-offset-2">
              {str(a.url)}
            </a>
          )}
          {str(a.query) && <div className="text-foreground-subtle">搜索:{str(a.query)}</div>}
          {str(a.prompt) && <div className="whitespace-pre-wrap text-foreground-subtle">{str(a.prompt)}</div>}
        </div>
      );
    case "todo":
      return <TodoArgs a={a} />;
    case "ask":
      return <AskArgs a={a} />;
    case "delegate":
      return (
        <div className="px-3 py-2 text-ui-xs">
          {str(a.task) || str(a.prompt) ? (
            <div className="whitespace-pre-wrap text-foreground-subtle">{str(a.task) || str(a.prompt)}</div>
          ) : (
            <KeyValueArgs a={a} />
          )}
        </div>
      );
    case "task":
      return <KeyValueArgs a={a} />;
    default:
      return <KeyValueArgs a={a} fallback={<RawArgs tool={tool} />} />;
  }
}

function Chip({ children, mono }: { children: React.ReactNode; mono?: boolean }) {
  return (
    <span
      className={
        "rounded-md px-1.5 py-0.5 " +
        (mono ? "font-[family-name:var(--font-mono)] " : "") +
        "bg-[var(--color-tag)] text-[var(--color-tag-fg)]"
      }
      style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
    >
      {children}
    </span>
  );
}

/** 小节标题(命令块/内容块的头部) */
function SectionHead({ left, right }: { left: React.ReactNode; right?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 border-b border-border bg-[var(--color-surface)] px-3 py-1 text-ui-xs text-foreground-subtlest">
      <span className="truncate">{left}</span>
      {right && <span className="ml-auto flex shrink-0 items-center gap-2">{right}</span>}
    </div>
  );
}

function RunArgs({ tool, a }: { tool: ToolInvocation; a: Record<string, unknown> }) {
  const cmd = str(a.command) || str(a.cmd);
  const desc = str(a.description);
  if (!cmd) return <RawArgs tool={tool} />;
  const lines = countLines(cmd);
  return (
    <div>
      <SectionHead
        left={<span className="font-[family-name:var(--font-mono)]">shell</span>}
        right={<>
          {str(a.timeout) && <span>timeout {str(a.timeout)}s</span>}
          {a.background === true && <span>后台</span>}
          <span>{lines} 行</span>
        </>}
      />
      <pre className="overflow-auto px-3 py-2 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap text-foreground">{cmd}</pre>
      {desc && desc !== cmd && <div className="px-3 pb-2 text-ui-xs text-foreground-subtlest">{desc}</div>}
    </div>
  );
}

function EditArgs({ a }: { a: Record<string, unknown> }) {
  const path = shortPath(str(a.file_path) || str(a.path));
  const oldText = str(a.old_text) || str(a.old_string) || str(a.oldText);
  const newText = str(a.new_text) || str(a.new_string) || str(a.newText);
  const edits = Array.isArray(a.edits) ? (a.edits as Record<string, unknown>[]) : null;
  const files = Array.isArray(a.files) ? (a.files as Record<string, unknown>[]) : null;
  if (edits || files) {
    // multi_edit / multi_file_edit:逐条列出
    const items = (edits ?? files ?? []).map((e, i) => {
      const p = shortPath(str(e.file_path) || str(e.path) || path);
      const o = str(e.old_text) || str(e.old_string) || str(e.oldString);
      const n = str(e.new_text) || str(e.new_string) || str(e.newString);
      return { key: i, p, diff: buildDiff(o, n, 1) };
    });
    return (
      <div className="flex flex-col">
        {items.map((it) => (
          <div key={it.key}>
            <SectionHead left={<span className="font-[family-name:var(--font-mono)]">{it.p || path}</span>} />
            <DiffView lines={it.diff} maxHeight={200} />
          </div>
        ))}
      </div>
    );
  }
  const diff = buildDiff(oldText, newText, 0);
  return (
    <div>
      <SectionHead
        left={<span className="font-[family-name:var(--font-mono)]">{path}</span>}
        right={<>
          {a.replace_all === true && <span>全部替换</span>}
          <span className="font-[family-name:var(--font-mono)]">
            <span className="text-[var(--color-destructive-surface-fg)]">−{diff.filter((d) => d.kind === "del").length}</span>{" "}
            <span className="text-[var(--color-success-surface-fg)]">+{diff.filter((d) => d.kind === "add").length}</span>
          </span>
        </>}
      />
      <DiffView lines={diff} />
    </div>
  );
}

function WriteArgs({ tool, a }: { tool: ToolInvocation; a: Record<string, unknown> }) {
  const path = shortPath(str(a.file_path) || str(a.path));
  const content = str(a.content);
  if (!content) return <RawArgs tool={tool} />;
  return <ContentPreview path={tool.detail || path} content={content} />;
}

const PREVIEW_LINES = 40;
function ContentPreview({ path, content, lang }: { path?: string; content: string; lang?: string | null }) {
  const [all, setAll] = useState(false);
  const lines = content.split("\n");
  const shown = all ? lines : lines.slice(0, PREVIEW_LINES);
  return (
    <div>
      <SectionHead
        left={<span className="font-[family-name:var(--font-mono)]">{path}</span>}
        right={<>
          {(lang ?? (path ? langOf(path) : null)) && <span>{lang ?? langOf(path ?? "")}</span>}
          <span>{lines.length} 行</span>
        </>}
      />
      <pre className="overflow-auto px-3 py-2 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap text-foreground" style={{ maxHeight: 360 }}>
        {shown.join("\n")}
      </pre>
      {lines.length > PREVIEW_LINES && (
        <button className="w-full border-t border-border px-3 py-1 text-left text-ui-xs text-brand hover:bg-[var(--color-hover)]" onClick={() => setAll(!all)}>
          {all ? "收起" : `显示全部 ${lines.length} 行`}
        </button>
      )}
    </div>
  );
}

function TodoArgs({ a }: { a: Record<string, unknown> }) {
  const todos = Array.isArray(a.todos) ? (a.todos as { id?: string; content?: string; status?: string }[]) : null;
  if (!todos) return <KeyValueArgs a={a} />;
  return (
    <ul className="flex flex-col gap-1 px-3 py-2 text-ui-xs">
      {todos.map((t, i) => (
        <li key={t.id ?? i} className="flex items-start gap-2">
          <span className="w-3 shrink-0 text-center" style={{ color: t.status === "done" ? "var(--color-success)" : t.status === "in_progress" ? "var(--color-warning)" : "var(--color-foreground-subtlest)" }}>
            {t.status === "done" ? "✓" : t.status === "in_progress" ? "▶" : "○"}
          </span>
          <span className={t.status === "done" ? "text-foreground-subtlest line-through" : "text-foreground"}>{t.content}</span>
        </li>
      ))}
    </ul>
  );
}

function AskArgs({ a }: { a: Record<string, unknown> }) {
  const qs = Array.isArray(a.questions) ? (a.questions as { id: string; title: string; kind?: string; choices?: { id: string; label: string }[] }[]) : null;
  return (
    <div className="flex flex-col gap-1.5 px-3 py-2 text-ui-xs">
      {str(a.title) && <div className="font-medium text-foreground">{str(a.title)}</div>}
      {qs?.map((q) => (
        <div key={q.id} className="text-foreground-subtle">
          <div>{q.title}</div>
          {q.choices?.length ? <div className="text-foreground-subtlest">{q.choices.map((c) => c.label).join(" / ")}</div> : <div className="text-foreground-subtlest">{q.kind === "text" ? "自由输入" : q.kind}</div>}
        </div>
      ))}
    </div>
  );
}

/** 键值表:扁平字段一行一个;嵌套/数组值用紧凑 JSON;全部不可读时回退 */
function KeyValueArgs({ a, fallback }: { a: Record<string, unknown>; fallback?: React.ReactNode }) {
  const entries = Object.entries(a);
  if (entries.length === 0) return <>{fallback ?? null}</>;
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 px-3 py-2 text-ui-xs">
      {entries.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-foreground-subtlest">{k}</dt>
          <dd className="min-w-0">
            {typeof v === "string" ? (
              <span className="whitespace-pre-wrap break-words text-foreground">{v.length > 400 ? v.slice(0, 400) + " …" : v}</span>
            ) : (
              <span className="break-all font-[family-name:var(--font-mono)] text-foreground-subtle">{compactJSON(v)}</span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function compactJSON(v: unknown): string {
  try {
    const s = JSON.stringify(v);
    return s.length > 300 ? s.slice(0, 300) + " …" : s;
  } catch {
    return String(v);
  }
}

export function RawArgs({ tool }: { tool: ToolInvocation }) {
  return (
    <pre className="px-3 py-2 font-[family-name:var(--font-mono)] text-ui-xs whitespace-pre-wrap break-all text-foreground-subtle">{pretty(tool.argsText)}</pre>
  );
}

function pretty(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

export { Chip, SectionHead, ContentPreview };
export type { DiffLine };
