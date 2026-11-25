// 工具展示的分类学:webui 侧的"工具是什么、长什么样、结果怎么读"。
// 展示名/目标由服务端给出(tool.DescribeTool,与 TUI/desktop/tunnel 同源);
// 这里只负责:类别 → 图标/色调、参数 → 结构化字段、结果 → 可读形态。

import type { ToolInvocation } from "@/store/chat";

export type ToolKind =
  | "read" // 读文件/目录
  | "edit" // 改文件(diff)
  | "write" // 写文件
  | "search" // grep/glob/code_search
  | "run" // shell 命令
  | "web" // web_fetch/web_search
  | "delegate" // spawn/子代理/teammate
  | "task" // task_*/cron_*/swarm_task_*
  | "todo"
  | "ask"
  | "skill"
  | "mcp"
  | "other";

const KIND_BY_NAME: Record<string, ToolKind> = {
  read_file: "read", multi_file_read: "read", list_directory: "read", notebook_read: "read",
  edit_file: "edit", multi_edit: "edit", multi_edit_file: "edit", multi_file_edit: "edit", apply_patch: "edit", notebook_edit: "edit",
  write_file: "write", multi_file_write: "write",
  grep: "search", search_files: "search", code_search: "search", glob: "search", lsp_workspace_symbols: "search", lsp_references: "search",
  run_command: "run", start_command: "run", bash: "run", powershell: "run",
  write_command_input: "run", read_command_output: "run", wait_command: "run", stop_command: "run", list_commands: "run",
  web_fetch: "web", web_search: "web",
  spawn_agent: "delegate", delegate: "delegate", use_namedagent: "delegate", teammate_spawn: "delegate", list_agents: "delegate", list_namedagent: "delegate", wait_agent: "delegate",
  todo_write: "todo",
  ask_user: "ask",
  skill: "skill",
};

export function toolKind(name: string): ToolKind {
  if (KIND_BY_NAME[name]) return KIND_BY_NAME[name];
  if (name.startsWith("mcp__")) return "mcp";
  if (name.startsWith("task_") || name.startsWith("cron_") || name.startsWith("swarm_task") || name === "lanchat") return "task";
  if (name.startsWith("lsp_")) return "search";
  return "other";
}

/** 类别图标(BMP 符号,无 emoji 变体选择器)与色调(CSS 变量) */
export function kindVisual(kind: ToolKind): { icon: string; color: string } {
  switch (kind) {
    case "read":
      return { icon: "≡", color: "var(--color-brand)" };
    case "edit":
      return { icon: "✎", color: "var(--color-warning)" };
    case "write":
      return { icon: "✚", color: "var(--color-warning)" };
    case "search":
      return { icon: "⌕", color: "var(--color-brand)" };
    case "run":
      return { icon: "❯", color: "var(--color-success)" };
    case "web":
      return { icon: "↗", color: "var(--color-brand)" };
    case "delegate":
      return { icon: "⑂", color: "var(--color-agent-node)" };
    case "task":
      return { icon: "☐", color: "var(--color-brand)" };
    case "todo":
      return { icon: "☰", color: "var(--color-brand)" };
    case "ask":
      return { icon: "?", color: "var(--color-warning)" };
    case "skill":
      return { icon: "✦", color: "var(--color-skill-node)" };
    case "mcp":
      return { icon: "◆", color: "var(--color-brand)" };
    default:
      return { icon: "•", color: "var(--color-foreground-subtlest)" };
  }
}

/** 与 Go 侧 prettifyToolName 同算法:下划线分段首字母大写 */
export function prettifyToolName(name: string): string {
  const parts = name.split("_").filter(Boolean);
  return parts.map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join(" ") || "Tool";
}

/** MCP 工具名 mcp__server__tool → "Server › Tool" */
export function mcpTitle(name: string): string | null {
  if (!name.startsWith("mcp__")) return null;
  const rest = name.slice(5);
  const i = rest.indexOf("__");
  if (i < 0) return prettifyToolName(rest);
  const server = rest.slice(0, i);
  const tool = rest.slice(i + 2);
  return `${server.charAt(0).toUpperCase() + server.slice(1)} › ${prettifyToolName(tool)}`;
}

/** 卡片标题:服务端展示名 > MCP 友好名 > prettify */
export function toolTitle(tool: ToolInvocation): string {
  const kind = toolKind(tool.name);
  if (tool.displayName) {
    // 委派类展示名可能就是任务文本,过长时截断
    return tool.displayName;
  }
  if (kind === "mcp") return mcpTitle(tool.name) ?? prettifyToolName(tool.name);
  return prettifyToolName(tool.name);
}

/** 标题旁的紧凑目标(等宽、截断):服务端 detail > 前端推导 */
export function toolTarget(tool: ToolInvocation): string {
  if (tool.detail) return tool.detail;
  const a = parseArgs(tool.argsText);
  return str(a.file_path) || str(a.path) || str(a.pattern) || str(a.query) || str(a.url) || str(a.command) || str(a.cmd) || "";
}

export function parseArgs(argsText: string): Record<string, unknown> {
  if (!argsText) return {};
  try {
    const o = JSON.parse(argsText);
    return o && typeof o === "object" && !Array.isArray(o) ? (o as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

export function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

/** 相对化常见前缀,保留足够定位信息 */
export function shortPath(p: string): string {
  return p.replace(/^file:\/\//, "").replace(/^\.\//, "");
}

// ---------- 结果推导(服务端未给 summary 时) ----------

export function countLines(s: string): number {
  if (!s) return 0;
  return s.split("\n").length;
}

/** 折叠态徽章:优先服务端摘要;否则按类别推导一行 */
export function resultBadge(tool: ToolInvocation): string | null {
  if (!tool.done) return null;
  const r = tool.result ?? "";
  if (tool.isError) {
    // 命令失败:徽章给失败原因(Command failed: …),而不是 stdout 首行
    if (toolKind(tool.name) === "run") {
      const m = r.match(/Command failed:\s*(.+)/);
      if (m) return `失败 · ${truncate(m[1].trim(), 48)}`;
    }
    const line = firstLine(r).replace(/^Error:\s*/, "");
    return line ? `失败 · ${truncate(line, 48)}` : "失败";
  }
  if (tool.resultSummary) return truncate(tool.resultSummary, 60);
  const kind = toolKind(tool.name);
  switch (kind) {
    case "run": {
      if (/^(\s|\S)*Command completed with no output\./.test(r)) return "无输出";
      return `${countLines(r)} 行`;
    }
    case "read":
      return r ? `${countLines(r)} 行` : null;
    case "search": {
      const m = parseSearchResult(r);
      return m ? `${m.matches} 处匹配` : r ? `${countLines(r)} 行` : null;
    }
    case "web":
      return r ? `${r.length} 字符` : null;
    case "todo":
      return "已更新";
    default: {
      // JSON 结果:数组给条数,对象给"JSON",不把原文塞进徽章
      const t = r.trim();
      if (t.startsWith("[")) {
        try {
          const arr = JSON.parse(t);
          if (Array.isArray(arr)) return `${arr.length} 项`;
        } catch { /* fallthrough */ }
      } else if (t.startsWith("{")) return "JSON";
      return r ? truncate(firstLine(r), 60) : null;
    }
  }
}

export function firstLine(s: string): string {
  const i = s.indexOf("\n");
  return (i >= 0 ? s.slice(0, i) : s).trim();
}

export function truncate(s: string, n: number): string {
  const chars = [...s];
  return chars.length > n ? chars.slice(0, n - 1).join("") + "…" : s;
}

// ---------- 结果解析 ----------

export type SearchMatch = { path: string; line: number; text: string };
export type SearchResult = { matches: number; files: number; byFile: { path: string; rows: { line: number; text: string }[] }[] };

/** grep/search_files 输出 `path:line: text`(含 `--` 分隔与 "Found N" 头) */
export function parseSearchResult(r: string): SearchResult | null {
  const byFileMap = new Map<string, { line: number; text: string }[]>();
  let total = 0;
  for (const raw of r.split("\n")) {
    const line = raw.replace(/\r$/, "");
    if (!line || line === "--" || /^(Found|Showing|Truncated|\[truncated)/i.test(line)) continue;
    const m = line.match(/^(.+?):(\d+):(?:\d+:)?\s?(.*)$/);
    if (!m) continue;
    const path = m[1];
    if (!byFileMap.has(path)) byFileMap.set(path, []);
    byFileMap.get(path)!.push({ line: Number(m[2]), text: m[3] });
    total++;
  }
  if (total === 0) return null;
  return { matches: total, files: byFileMap.size, byFile: [...byFileMap.entries()].map(([path, rows]) => ({ path, rows })) };
}

/** read_file 的 `cat -n` 行号格式 → {number, text}[];非该格式返回 null */
export type NumberedLine = { no: number; text: string };
export function parseNumberedLines(r: string): NumberedLine[] | null {
  const lines = r.split("\n");
  const out: NumberedLine[] = [];
  let numbered = 0;
  for (const l of lines) {
    const m = l.match(/^\s*(\d+)\t(.*)$/);
    if (m) {
      numbered++;
      out.push({ no: Number(m[1]), text: m[2] });
    } else {
      out.push({ no: NaN, text: l });
    }
  }
  return numbered >= Math.min(lines.length, 2) && numbered > 0 ? out : null;
}

/** run_command 结果分段:stdout / stderr / 失败原因(失败行在末尾,stderr 在中段) */
export type RunResult = { stdout: string; stderr: string; failed: string; noOutput: boolean };
export function parseRunResult(r: string): RunResult {
  const noOutput = r.trim() === "Command completed with no output.";
  let text = r;
  let stderr = "";
  let failed = "";
  const fi = text.lastIndexOf("\nCommand failed:");
  if (fi >= 0) {
    failed = text.slice(fi + 1);
    text = text.slice(0, fi);
  }
  const si = text.indexOf("STDERR:\n");
  if (si >= 0) {
    stderr = text.slice(si + 8);
    text = text.slice(0, si);
  }
  text = text.replace(/^Command completed with no output\.\n?/, "");
  return { stdout: text.replace(/\n+$/, ""), stderr: stderr.replace(/\n+$/, ""), failed, noOutput };
}

/** 默认展开规则:错误 / 编辑(diff 即结论)/ todo 清单 / 小结果直接展开;大输出折叠,由徽章给结论 */
export function defaultExpanded(tool: ToolInvocation): boolean {
  if (toolKind(tool.name) === "delegate") return false;
  if (tool.isError) return true;
  const kind = toolKind(tool.name);
  if (kind === "edit" || kind === "todo") return true;
  if (!tool.done) return false;
  const r = tool.result ?? "";
  return r.length <= 800 && countLines(r) <= 12;
}

/** 文本里是否含 unified diff 行(@@ / +/- 前缀),用于 diff 着色 */
export function looksLikeDiff(s: string): boolean {
  return /^@@ -\d+/m.test(s) || (/^[+-][^+-]/m.test(s) && /^(-|\+)/m.test(s));
}

/** 把 old/new 文本拼成可着色的 diff 行(编辑类工具的参数视图) */
export type DiffLine = { kind: "del" | "add" | "ctx" | "hunk"; text: string };
export function buildDiff(oldText: string, newText: string, context = 0): DiffLine[] {
  const out: DiffLine[] = [];
  const oldLines = oldText.split("\n");
  const newLines = newText.split("\n");
  // 简单 LCS 对齐(小片段足够,长度上限保护)
  if (oldLines.length * newLines.length > 40000) {
    oldLines.forEach((l) => out.push({ kind: "del", text: l }));
    newLines.forEach((l) => out.push({ kind: "add", text: l }));
    return out;
  }
  const n = oldLines.length, m = newLines.length;
  const dp: number[] = new Array((n + 1) * (m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i * (m + 1) + j] = oldLines[i] === newLines[j] ? dp[(i + 1) * (m + 1) + j + 1] + 1 : Math.max(dp[(i + 1) * (m + 1) + j], dp[i * (m + 1) + j + 1]);
  let i = 0, j = 0;
  const raw: DiffLine[] = [];
  while (i < n && j < m) {
    if (oldLines[i] === newLines[j]) {
      raw.push({ kind: "ctx", text: oldLines[i] });
      i++; j++;
    } else if (dp[(i + 1) * (m + 1) + j] >= dp[i * (m + 1) + j + 1]) {
      raw.push({ kind: "del", text: oldLines[i] });
      i++;
    } else {
      raw.push({ kind: "add", text: newLines[j] });
      j++;
    }
  }
  while (i < n) raw.push({ kind: "del", text: oldLines[i++] });
  while (j < m) raw.push({ kind: "add", text: newLines[j++] });
  // 折叠未变化的上下文:只保留变更行附近 context 行,中间用 hunk 分隔
  const keep = new Array(raw.length).fill(false);
  raw.forEach((l, k) => {
    if (l.kind === "ctx") return;
    for (let d = -context; d <= context; d++) if (raw[k + d]) keep[k + d] = true;
  });
  let lastKept = -2;
  raw.forEach((l, k) => {
    if (!keep[k]) return;
    if (k !== lastKept + 1 && lastKept >= 0) out.push({ kind: "hunk", text: "···" });
    out.push(l);
    lastKept = k;
  });
  return out;
}

/** 统一 diff 文本 → DiffLine(用于 edit_file 结果里 compactDiff 等输出) */
export function parseUnifiedDiff(s: string): DiffLine[] | null {
  if (!looksLikeDiff(s)) return null;
  const out: DiffLine[] = [];
  for (const line of s.split("\n")) {
    if (line.startsWith("@@")) out.push({ kind: "hunk", text: line });
    else if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff --git") || line.startsWith("index ")) out.push({ kind: "ctx", text: line });
    else if (line.startsWith("+")) out.push({ kind: "add", text: line.slice(1) });
    else if (line.startsWith("-")) out.push({ kind: "del", text: line.slice(1) });
    else out.push({ kind: "ctx", text: line.replace(/^ /, "") });
  }
  return out;
}

/** 按扩展名给代码块打语言标签 */
export function langOf(path: string): string | null {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = { go: "Go", ts: "TS", tsx: "TS", js: "JS", jsx: "JS", py: "Python", rs: "Rust", java: "Java", c: "C", cpp: "C++", h: "C", rb: "Ruby", sh: "sh", zsh: "sh", bash: "sh", md: "Markdown", json: "JSON", yaml: "YAML", yml: "YAML", toml: "TOML", html: "HTML", css: "CSS", scss: "SCSS", sql: "SQL", dart: "Dart", swift: "Swift", kt: "Kotlin" };
  return map[ext] ?? null;
}
