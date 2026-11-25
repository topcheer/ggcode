// Render smoke test for the webui tool cards: no browser needed (the
// headless harness cannot open websockets), so the components are rendered
// with react-dom/server against representative store shapes and the output
// HTML is asserted. Run: npm run test:render
import { renderToStaticMarkup } from "react-dom/server";
import type { ToolInvocation } from "../src/store/chat";
import ToolCallCard from "../src/chat/ToolCallCard";
import { useChat } from "../src/store/chat";
import { buildDiff, parseNumberedLines, parseRunResult, parseSearchResult, parseUnifiedDiff } from "../src/chat/toolPresent";

let failures = 0;
function check(name: string, cond: boolean, detail?: string) {
  if (cond) console.log(`ok   ${name}`);
  else {
    failures++;
    console.log(`FAIL ${name}${detail ? "\n     " + detail.slice(0, 400) : ""}`);
  }
}
function must(name: string, html: string, ...needles: string[]) {
  const missing = needles.filter((n) => !html.includes(n));
  check(name, missing.length === 0, `missing ${JSON.stringify(missing)} in:\n${html}`);
}
function mustNot(name: string, html: string, ...banned: string[]) {
  const present = banned.filter((n) => html.includes(n));
  check(name, present.length === 0, `unexpected ${JSON.stringify(present)}`);
}
/** 只看头部(正文之前的折叠行):原始工具名只允许出现在正文 footer */
function header(html: string): string {
  const i = html.indexOf("border-t");
  return i >= 0 ? html.slice(0, i) : html;
}

const base = (over: Partial<ToolInvocation>): ToolInvocation => ({
  id: "t1", name: "read_file", argsText: "{}", done: true, startedAt: 0, endedAt: 10, ...over,
});

// 1. 标题是展示名,不是裸工具名;目标是文件;折叠态有结果徽章
{
  const html = renderToStaticMarkup(
    <ToolCallCard tool={base({ name: "read_file", displayName: "Read", detail: "internal/webui/server.go", result: "1\tpackage main\n2\tfunc x(){}" })} />,
  );
  must("read: display name + target + line badge", html, "Read", "internal/webui/server.go", "2 行");
  mustNot("read: no raw tool name in header", header(html), ">read_file<");
}

// 2. 没有服务端展示名(旧事件)时 prettify 兜底,而不是裸名
{
  const html = renderToStaticMarkup(<ToolCallCard tool={base({ name: "list_directory", argsText: `{"path":"src"}` })} />);
  must("fallback: prettified name", html, "List Directory");
  mustNot("fallback: no raw snake_case in header", header(html), ">list_directory<");
}

// 3. MCP 工具:server › tool 标题
{
  const html = renderToStaticMarkup(<ToolCallCard tool={base({ name: "mcp__gitea__list_repos", argsText: `{"limit":5}` })} />);
  must("mcp: friendly title", html, "Gitea › List Repos");
}

// 4. 命令:标题为 description/命令,副标为工具名;展开后是 shell 块,不是 JSON
{
  const html = renderToStaticMarkup(
    <ToolCallCard tool={base({ name: "run_command", displayName: "Running tests", detail: "go test ./...", argsText: JSON.stringify({ command: "go test ./...", description: "Running tests" }), result: "ok\ngithub.com/x 0.1s" })} />,
  );
  must("run: title + sub + badge", html, "Running tests", "Run Command", "2 行");
}

// 5. 错误默认展开且红显
{
  const html = renderToStaticMarkup(
    <ToolCallCard tool={base({ name: "run_command", displayName: "go test", detail: "go test", argsText: `{"command":"go test"}`, result: "FAIL\ngo build failed\n\nCommand failed: exit status 1\nerror: undefined: x", isError: true })} />,
  );
  must("error: expanded with failure reason", html, "Command failed: exit status 1", "失败 · ", "undefined: x");
}

// 6. edit:展开后参数视图是 old→new diff(红/绿行),不是 JSON
{
  const html = renderToStaticMarkup(
    <ToolCallCard tool={base({ name: "edit_file", displayName: "Edit", detail: "a.go", argsText: JSON.stringify({ file_path: "/w/a.go", old_text: "foo()\nbar()", new_text: "foo()\nbaz()" }), result: "Replaced 1 occurrence in a.go: 2 lines -> 2 lines" })} />,
  );
  check("edit: args rendered as diff (-bar +baz)", html.includes("bar()") && html.includes("baz()") && html.includes("+") && html.includes("-"));
  must("edit: result badge from first line", html, "Replaced 1 occurrence in a.go");
  mustNot("edit: no raw old_text json", html, "&quot;old_text&quot;");
}

// 7. grep:结果按文件分组,行号 + 文本
{
  const html = renderToStaticMarkup(
    <ToolCallCard tool={base({ name: "grep", displayName: "Search", detail: "handleChatWS", argsText: `{"pattern":"handleChatWS"}`, result: "internal/webui/server.go:101:func (s *Server) handleChatWS(\ninternal/webui/server.go:140:x := handleChatWS\n--\nFound 2 matches" })} />,
  );
  must("grep: grouped + counted", html, "2 处匹配", "1 个文件", "server.go", ">101<", ">140<", "func (s *Server) handleChatWS(");
}

// 8. 原始模式入口存在
{
  const html = renderToStaticMarkup(<ToolCallCard tool={base({ name: "todo_write", argsText: `{"todos":[{"id":"a","content":"ship","status":"pending"}]}`, result: "ok" })} />);
  must("footer: raw toggle present", html, "原始 JSON", "todo_write");
  must("todo: checklist", html, "ship", "○");
}

// 9. 纯函数
{
  const d = buildDiff("a\nb\nc", "a\nB\nc", 0);
  check("buildDiff: -b +B", d.some((l) => l.kind === "del" && l.text === "b") && d.some((l) => l.kind === "add" && l.text === "B"));
  const r = parseRunResult("out\nSTDERR:\nerr1\nerr2\n\nCommand failed: exit status 2\nhint: fix it");
  check("parseRunResult: stdout/stderr/failed", r.stdout === "out" && r.stderr === "err1\nerr2" && r.failed.startsWith("Command failed: exit status 2") && r.failed.includes("hint: fix it"), JSON.stringify(r));
  check("parseRunResult: no output", parseRunResult("Command completed with no output.").noOutput);
  const s = parseSearchResult("a/b.go:12:foo\na/b.go:30:bar\nc.go:3:baz");
  check("parseSearchResult: 3 matches / 2 files", !!s && s.matches === 3 && s.files === 2 && s.byFile[0].rows.length === 2);
  const n = parseNumberedLines("1\tpackage main\n2\tfunc x(){}");
  check("parseNumberedLines", !!n && n[1].no === 2 && n[1].text === "func x(){}");
  const u = parseUnifiedDiff("@@ -1,2 +1,2 @@\n-old\n+new\n ctx");
  check("parseUnifiedDiff", !!u && u[1].kind === "del" && u[2].kind === "add" && u[3].kind === "ctx");
}

// 10. 队列镜像:事件携带全量快照时以快照为准(带图片占位前缀的文本不再依赖匹配),旧 seq 丢弃
{
  const st = useChat.getState();
  st.syncQueue([]);
  st.applyMsg({ type: "queue", event: "append", text: "[image: a.png] hi", items: ["hi"], count: 1, seq: 10 });
  const afterAppend = useChat.getState().queueItems;
  st.applyMsg({ type: "queue", event: "dequeue", text: "hi", items: [], count: 0, seq: 11 });
  const afterDequeue = useChat.getState().queueItems;
  st.applyMsg({ type: "queue", event: "append", text: "stale", items: ["stale"], count: 1, seq: 9 });
  const afterStale = useChat.getState().queueItems;
  // 旧服务端(无 items/seq)退回增量语义
  st.applyMsg({ type: "queue", event: "append", text: "legacy" });
  st.applyMsg({ type: "queue", event: "dequeue", text: "legacy" });
  const afterLegacy = useChat.getState().queueItems;
  check("queue: snapshot wins over text matching", JSON.stringify(afterAppend) === '["hi"]' && afterDequeue.length === 0, JSON.stringify({ afterAppend, afterDequeue }));
  check("queue: stale seq ignored", afterStale.length === 0, JSON.stringify(afterStale));
  check("queue: legacy incremental still works", afterLegacy.length === 0, JSON.stringify(afterLegacy));
}

if (failures) {
  console.log(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("\nall render checks passed");
