import { useEffect, useState } from "react";
import { api } from "@/api/client";

type KnightSkill = {
  name: string;
  description: string;
  scope: string;
  usage_count: number;
  frozen: boolean;
};

type KnightCandidate = {
  name: string;
  description: string;
  category: string;
  score: number;
  evidence_count: number;
};

type KnightStatus = {
  enabled: boolean;
  running: boolean;
  status: string;
  budget: { used: number; remaining: number; limit: number };
  active: KnightSkill[];
  staging: KnightSkill[];
  queue: KnightCandidate[];
};

/** KnightPage(P2 细化):状态徽章+预算条+active/staging 技能卡+候选队列 */
export default function KnightPage() {
  const [st, setSt] = useState<KnightStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<KnightStatus>("/api/knight")
      .then((d) => {
        setSt(d);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  if (err) {
    return (
      <div className="mx-auto max-w-[960px] p-8">
        <div className="text-ui-sm rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">{err}</div>
      </div>
    );
  }
  if (!st) return <div className="p-8 text-ui-sm text-foreground-subtlest">加载中…</div>;

  const budgetPct = st.budget.limit > 0 ? Math.round((st.budget.used / st.budget.limit) * 100) : 0;

  const SkillCard = ({ s, tone }: { s: KnightSkill; tone: "active" | "staging" }) => (
    <div className="rounded-xl border border-border bg-card px-3 py-2">
      <div className="flex items-center gap-2">
        <span className="text-ui-sm font-medium text-foreground">{s.name}</span>
        <span className="text-ui-xs rounded-md bg-[var(--color-surface)] px-2 py-0.5 text-foreground-subtlest">{s.scope}</span>
        {tone === "staging" && (
          <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-warning-surface)", color: "var(--color-warning-surface-fg)" }}>
            staging
          </span>
        )}
        {s.frozen && (
          <span className="text-ui-xs rounded-full px-2 py-0.5" style={{ background: "var(--color-destructive-surface)", color: "var(--color-destructive-surface-fg)" }}>
            frozen
          </span>
        )}
        <span className="text-ui-xs ml-auto text-foreground-subtlest">{s.usage_count} 次</span>
      </div>
      <p className="text-ui-xs mt-1 text-wrap-phrase text-foreground-subtle">{s.description}</p>
    </div>
  );

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <div className="mb-4 flex items-center gap-2">
        <h2 className="text-ui-lg font-semibold">Knight</h2>
        <span
          className="text-ui-xs rounded-full px-2 py-0.5"
          style={{
            background: st.enabled ? "var(--color-success-surface)" : "var(--color-surface)",
            color: st.enabled ? "var(--color-success-surface-fg)" : "var(--color-foreground-subtlest)",
          }}
        >
          {st.status}
        </span>
      </div>

      {st.enabled && (
        <div className="mb-5 rounded-xl border border-border bg-card p-4">
          <div className="text-ui-xs mb-1.5 flex justify-between text-foreground-subtlest">
            <span>本轮预算</span>
            <span>
              {st.budget.used} / {st.budget.limit}（余 {st.budget.remaining}）
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-surface)]">
            <div
              className="h-full rounded-full transition-[width]"
              style={{ width: `${Math.min(budgetPct, 100)}%`, background: budgetPct >= 90 ? "var(--color-destructive)" : "var(--color-brand)" }}
            />
          </div>
        </div>
      )}

      {st.active.length > 0 && (
        <>
          <h3 className="text-ui-sm mb-2 text-foreground-subtle">已启用技能（{st.active.length}）</h3>
          <div className="mb-5 flex flex-col gap-2">
            {st.active.map((s) => (
              <SkillCard key={s.name} s={s} tone="active" />
            ))}
          </div>
        </>
      )}

      {st.staging.length > 0 && (
        <>
          <h3 className="text-ui-sm mb-2 text-foreground-subtle">暂存（{st.staging.length}）</h3>
          <div className="mb-5 flex flex-col gap-2">
            {st.staging.map((s) => (
              <SkillCard key={s.name} s={s} tone="staging" />
            ))}
          </div>
        </>
      )}

      {st.queue.length > 0 && (
        <>
          <h3 className="text-ui-sm mb-2 text-foreground-subtle">候选队列（{st.queue.length}）</h3>
          <div className="flex flex-col gap-2">
            {st.queue.map((c) => (
              <div key={c.name} className="rounded-xl border border-border bg-card px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="text-ui-sm font-medium text-foreground">{c.name}</span>
                  <span className="text-ui-xs rounded-md bg-[var(--color-surface)] px-2 py-0.5 text-foreground-subtlest">{c.category}</span>
                  <span className="text-ui-xs ml-auto text-foreground-subtlest">
                    score {c.score.toFixed(2)} · {c.evidence_count} 证据
                  </span>
                </div>
                <p className="text-ui-xs mt-1 text-wrap-phrase text-foreground-subtle">{c.description}</p>
              </div>
            ))}
          </div>
        </>
      )}

      {!st.enabled && st.active.length === 0 && st.queue.length === 0 && (
        <p className="text-ui-sm text-foreground-subtlest">(Knight 未启用或无技能)</p>
      )}
    </div>
  );
}
