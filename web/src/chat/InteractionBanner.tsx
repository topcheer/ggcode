import { useEffect, useState } from "react";
import { useChat } from "@/store/chat";
import type { ChatWS, InteractionAnswer } from "@/ws/client";

/** 待应答的 ask-type gate 横幅 (#5865):工具审批 或 ask_user 问卷。
 *  任一前端(TUI 键盘/IM/移动端/本页)应答后,服务端 interaction_resolved 清除。 */
export default function InteractionBanner({ ws }: { ws: ChatWS | null }) {
  const interactions = useChat((s) => s.interactions);
  const first = interactions[0];
  if (!first) return null;
  return (
    <div className="border-b border-[var(--color-warning-surface)] bg-[var(--color-warning-surface)] px-4 py-2 text-ui-sm text-[var(--color-warning-surface-fg)]">
      {interactions.length > 1 && (
        <div className="text-ui-xs text-foreground-subtle">还有 {interactions.length - 1} 个待处理</div>
      )}
      {first.kind === "approval" ? (
        <ApprovalCard ws={ws} id={first.id} tool={first.tool_name ?? "tool"} input={first.input ?? ""} />
      ) : (
        <AskUserCard ws={ws} id={first.id} request={first.request ?? { questions: [] }} />
      )}
    </div>
  );
}

function pretty(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}

function ApprovalCard({ ws, id, tool, input }: { ws: ChatWS | null; id: string; tool: string; input: string }) {
  const [open, setOpen] = useState(false);
  const respond = (decision: "allow" | "always_allow" | "deny") => ws?.sendInteractionResponse(id, decision);
  const body = pretty(input);
  return (
    <div className="flex flex-col gap-1.5">
      <div className="font-medium">需要审批:{tool}</div>
      <pre className="max-h-24 overflow-auto whitespace-pre-wrap text-ui-xs opacity-80">{open ? body : body.slice(0, 200)}</pre>
      {body.length > 200 && (
        <button className="self-start text-ui-xs underline" onClick={() => setOpen((v) => !v)}>
          {open ? "收起" : "展开全部"}
        </button>
      )}
      <div className="flex gap-2">
        <button className="rounded bg-[var(--color-success-surface)] px-3 py-1 text-ui-sm text-[var(--color-success-surface-fg)]" onClick={() => respond("allow")}>
          允许
        </button>
        <button className="rounded border border-border px-3 py-1 text-ui-sm" onClick={() => respond("always_allow")}>
          总是允许
        </button>
        <button className="rounded border border-[var(--color-destructive-surface)] px-3 py-1 text-ui-sm text-[var(--color-destructive-surface-fg)]" onClick={() => respond("deny")}>
          拒绝
        </button>
      </div>
    </div>
  );
}

type Draft = Record<string, { choices: string[]; text: string }>;

function AskUserCard({ ws, id, request }: { ws: ChatWS | null; id: string; request: { title?: string; questions: import("@/ws/client").AskUserQuestion[] } }) {
  const [draft, setDraft] = useState<Draft>({});
  useEffect(() => setDraft({}), [id]);
  const toggle = (qid: string, cid: string, multi: boolean) =>
    setDraft((d) => {
      const cur = d[qid] ?? { choices: [], text: "" };
      const has = cur.choices.includes(cid);
      const choices = multi ? (has ? cur.choices.filter((c) => c !== cid) : [...cur.choices, cid]) : has ? [] : [cid];
      return { ...d, [qid]: { ...cur, choices } };
    });
  const submit = () => {
    const answers: InteractionAnswer[] = (request.questions ?? []).map((q) => ({
      question_id: q.id,
      choice_ids: draft[q.id]?.choices ?? [],
      freeform_text: draft[q.id]?.text ?? "",
    }));
    ws?.sendInteractionResponse(id, "submitted", answers);
  };
  return (
    <div className="flex flex-col gap-2">
      <div className="font-medium">{request.title?.trim() ? request.title : "需要你的回答"}</div>
      {(request.questions ?? []).map((q) => (
        <div key={q.id} className="flex flex-col gap-1">
          <div className="text-ui-xs">{q.prompt?.trim() ? q.prompt : q.title}</div>
          {q.choices?.map((c) => (
            <label key={c.id} className="flex items-center gap-2 text-ui-sm">
              <input
                type={q.kind === "multi" ? "checkbox" : "radio"}
                name={`${id}-${q.id}`}
                checked={(draft[q.id]?.choices ?? []).includes(c.id)}
                onChange={() => toggle(q.id, c.id, q.kind === "multi")}
              />
              {c.label}
            </label>
          ))}
          {(q.kind === "text" || q.allow_freeform) && (
            <textarea
              className="rounded border border-border bg-background px-2 py-1 text-ui-sm text-foreground"
              rows={2}
              placeholder={q.placeholder ?? "自由输入…"}
              value={draft[q.id]?.text ?? ""}
              onChange={(e) => setDraft((d) => ({ ...d, [q.id]: { choices: d[q.id]?.choices ?? [], text: e.target.value } }))}
            />
          )}
        </div>
      ))}
      <div className="flex gap-2">
        <button className="rounded bg-[var(--color-success-surface)] px-3 py-1 text-ui-sm text-[var(--color-success-surface-fg)]" onClick={submit}>
          提交
        </button>
        <button className="rounded border border-border px-3 py-1 text-ui-sm" onClick={() => ws?.sendInteractionResponse(id, "cancelled")}>
          取消
        </button>
      </div>
    </div>
  );
}
