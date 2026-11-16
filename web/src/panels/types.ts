/** 面板指令:POST /api/chat/commands 对无参面板命令的响应 */
export type PanelDirective = {
  status: "panel";
  command: string;
  kind: "select" | "list-detail" | "form" | "stats";
  title: string;
  dataUrl: string;
  cmdPrefix?: string;
};

export type SubmitResult =
  | { status: "submitted"; text: string }
  | PanelDirective
  | { status: string; [k: string]: unknown };

/** 提交 slash 命令,返回面板指令或提交态 */
export async function submitCommand(text: string): Promise<SubmitResult> {
  const r = await fetch("/api/chat/commands", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  });
  if (!r.ok) throw new Error((await r.text()) || `command rejected (${r.status})`);
  return r.json();
}

export type SelectOption = { id: string; label: string; badge?: string; description?: string; current?: boolean };
export type SelectData = {
  kind: "select";
  grouped?: boolean;
  options?: SelectOption[];
  groups?: { label: string; options: SelectOption[] }[];
  current?: string;
  /** TUI 选型面上下文(/model 面板显示 active vendor/endpoint/protocol) */
  context?: { vendor?: string; endpoint?: string; protocol?: string };
  /** 后端取数失败显式空态(/model 无 active 端点) */
  error?: string;
  /** 提交双模式:http(方法+URL 回写,如 impersonate PUT)优先;command(拼参回填命令) */
  submit?: {
    command?: string;
    argFrom?: "id" | "label";
    method?: string;
    url?: string;
    /** http 模式:回写 body 的字段名(值=argFrom 选中的 id/label) */
    bodyField?: string;
  };
};

export type ListDetailData = {
  kind: "list-detail";
  items?: { id: string; title: string; subtitle?: string; badges?: string[] }[];
  submit?: { command: string; argFrom?: string };
  [k: string]: unknown;
};

export type FormData_ = {
  kind: "form";
  fields?: { name: string; label: string; type?: string; placeholder?: string; secret?: boolean; help?: string; value?: string }[];
  /** 提交双模式:http(方法+URL 回写,如 IM 适配器 PUT)优先;command(拼参回填命令) */
  submit?: { method?: string; url?: string; command?: string };
  [k: string]: unknown;
};

export type StatsData = {
  kind: "stats";
  sections?: { title: string; metrics: { label: string; value: string; trend?: string }[] }[];
  emptyReason?: string;
  [k: string]: unknown;
};
