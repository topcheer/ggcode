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
  submit?: { command: string; argFrom: "id" | "label" };
};

export type ListDetailData = {
  kind: "list-detail";
  items?: { id: string; title: string; subtitle?: string; badges?: string[] }[];
  submit?: { command: string; argFrom?: string };
  [k: string]: unknown;
};

export type FormData_ = {
  kind: "form";
  fields?: { name: string; label: string; type?: string; placeholder?: string; secret?: boolean; help?: string }[];
  submit?: { command: string };
  [k: string]: unknown;
};

export type StatsData = {
  kind: "stats";
  sections?: { title: string; metrics: { label: string; value: string; trend?: string }[] }[];
  emptyReason?: string;
  [k: string]: unknown;
};
