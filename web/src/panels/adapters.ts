/**
 * 面板数据形状归一层:把异形既有端点归一到组件期望形状。
 * 形状契约源(后端 handler 实测):
 * - /api/im/adapters/<name>  → 平面 config.IMAdapterConfig 对象(POST/PUT 可写,
 *   secret 字段值 "__unchanged__" 表示保持不变)
 * - /api/cron/jobs           → 顶层 CronJobView[] {id,cron_expr,prompt,recurring,paused}
 * - /api/knight/skills       → {active:[],staging:[]}
 * - /api/mcp                 → 顶层 entries[](name/runtime.status 嵌套)
 */
import type { FormData_, ListDetailData } from "./types";

type Field = NonNullable<FormData_["fields"]>[number];

const IM_FIELD_ORDER: { key: string; label: string; type?: string; help?: string }[] = [
  { key: "enabled", label: "启用", type: "checkbox" },
  { key: "platform", label: "平台", type: "text" },
  { key: "transport", label: "传输", type: "text" },
  { key: "command", label: "命令", type: "text" },
  { key: "output_mode", label: "输出模式", type: "text" },
  { key: "args", label: "参数(逗号分隔)", type: "text" },
  { key: "allow_from", label: "白名单(逗号分隔)", type: "text" },
];

/** endpoint adapter: GET+PUT /api/im/adapters/<name> — 平面对象 → 表单 fields 归一 */
export function adaptIMAdapter(raw: Record<string, unknown>, dataUrl: string): FormData_ {
  const fields: Field[] = [];
  for (const f of IM_FIELD_ORDER) {
    const v = raw[f.key];
    if (f.type === "checkbox") {
      fields.push({ name: f.key, label: f.label, type: f.type, help: f.help, value: v === true ? "true" : "false" });
    } else if (Array.isArray(v)) {
      fields.push({ name: f.key, label: f.label, type: f.type, help: f.help, value: v.map(String).join(",") });
    } else if (v !== undefined) {
      fields.push({ name: f.key, label: f.label, type: f.type, help: f.help, value: String(v ?? "") });
    }
  }
  // env / extra:map 对象 → K=V 行(只读呈现,值含 __unchanged__ 的 secret 保持)
  for (const bucket of ["env", "extra"] as const) {
    const m = raw[bucket];
    if (m && typeof m === "object" && !Array.isArray(m)) {
      const lines = Object.entries(m as Record<string, unknown>)
        .map(([k, v]) => `${k}=${v === "__unchanged__" ? "" : String(v ?? "")}`)
        .join("\n");
      fields.push({ name: bucket, label: bucket === "env" ? "环境变量" : "扩展配置", type: "textarea", value: lines, help: "K=V 每行;secret 留空=保持" });
    }
  }
  return { kind: "form", fields, submit: { method: "PUT", url: dataUrl } } as FormData_;
}

/** endpoint payload builder: 表单值 → IM 适配器 PUT body(checkbox/数组/map 行重组;secret 空值剔除) */
export function imFormToConfig(vals: Record<string, string>, fieldDefs: Field[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const defs = new Map(fieldDefs.map((f) => [f.name, f]));
  for (const [k, v] of Object.entries(vals)) {
    const def = defs.get(k);
    if (!def || v === "") continue;
    if (def.type === "checkbox") {
      out[k] = v === "true";
      continue;
    }
    if (k === "env" || k === "extra") {
      const m: Record<string, string> = {};
      for (const line of v.split("\n")) {
        const i = line.indexOf("=");
        if (i > 0) {
          const val = line.slice(i + 1).trim();
          if (val !== "") m[line.slice(0, i).trim()] = val;
        }
      }
      if (Object.keys(m).length > 0) out[k] = m;
      continue;
    }
    if (def.label.includes("逗号分隔")) {
      out[k] = v.split(",").map((s) => s.trim()).filter(Boolean);
      continue;
    }
    out[k] = v;
  }
  return out;
}

type RawItem = Record<string, unknown>;

/** endpoint adapter: cron/skills/mcp 三异形 endpoint → ListDetailData items 归一 */
export function adaptListDetail(raw: unknown, dataUrl: string): ListDetailData {
  const items: { id: string; title: string; subtitle?: string; badges?: string[] }[] = [];
  const arr = Array.isArray(raw) ? (raw as RawItem[]) : [];
  if (dataUrl.startsWith("/api/cron/jobs")) {
    for (const j of arr) {
      items.push({
        id: String(j.id ?? ""),
        title: String(j.prompt ?? "").slice(0, 60) || "(无提示词)",
        subtitle: `${String(j.cron_expr ?? "")}${j.paused ? " · 已暂停" : ""}${j.recurring ? "" : " · 一次性"}`,
        badges: j.paused ? ["paused"] : [],
      });
    }
  } else if (dataUrl.startsWith("/api/mcp")) {
    for (const e of arr) {
      const rt = (e.runtime ?? {}) as RawItem;
      items.push({
        id: String(e.name ?? rt.name ?? ""),
        title: String(e.name ?? rt.name ?? "?"),
        subtitle: String(rt.status ?? rt.state ?? ""),
        badges: String(rt.status ?? "") === "connected" ? ["connected"] : [],
      });
    }
  } else if (dataUrl.startsWith("/api/knight/skills")) {
    const o = (raw ?? {}) as { active?: RawItem[]; staging?: RawItem[] };
    for (const s of [...(o.active ?? []), ...(o.staging ?? [])]) {
      const staged = (o.staging ?? []).includes(s);
      items.push({
        id: String(s.name ?? s.id ?? ""),
        title: String(s.name ?? s.id ?? "?"),
        subtitle: String(s.description ?? "").slice(0, 80),
        badges: staged ? ["staging"] : ["active"],
      });
    }
  } else {
    // 通用兜底:顶层数组或 {items}/{sessions}
    const generic = Array.isArray(raw)
      ? raw
      : ((raw as { items?: RawItem[] })?.items ?? (raw as { sessions?: RawItem[] })?.sessions ?? []);
    for (const g of generic as RawItem[]) {
      items.push({ id: String(g.id ?? g.name ?? ""), title: String(g.title ?? g.name ?? g.id ?? "?"), subtitle: g.subtitle ? String(g.subtitle) : undefined });
    }
  }
  return { kind: "list-detail", items };
}
