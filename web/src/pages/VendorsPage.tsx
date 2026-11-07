import { useCallback, useEffect, useState } from "react";
import { api } from "@/api/client";

/** VendorsPage(巡检轮3):字段级表单——vendor 列表→端点卡片编辑(base_url/protocol/api_key)
 * 契约(已核 server_handlers.go):
 * GET /api/vendors → [{id, display_name, has_api_key, endpoint_ids}]
 * GET /api/vendors/{v} → {display_name, has_api_key, endpoints:{name:{base_url,protocol,has_api_key,models?}}}
 * PUT /api/vendors/{v} {display_name, api_key}  (api_key="__unchanged__" 保留)
 * PUT /api/vendors/{v}/endpoints/{e} EndpointConfig 全量
 * PUT /api/vendors/{v}/endpoints/{e}/apikey {api_key}
 */

type VendorSummary = { id: string; display_name: string; has_api_key: boolean; endpoint_ids: string[] };
type EndpointInfo = { base_url?: string; protocol?: string; has_api_key?: boolean; models?: unknown };
type VendorDetail = { id: string; display_name: string; has_api_key: boolean; endpoints: Record<string, EndpointInfo> };

export default function VendorsPage() {
  const [vendors, setVendors] = useState<VendorSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<VendorDetail | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const loadList = useCallback(() => {
    api
      .get<VendorSummary[]>("/api/vendors")
      .then((d) => {
        setVendors(d);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  useEffect(loadList, [loadList]);

  const openVendor = (id: string) => {
    setSelected(id);
    setDetail(null);
    api
      .get<VendorDetail>(`/api/vendors/${id}`)
      .then((d) => setDetail(d))
      .catch((e: Error) => setErr(e.message));
  };

  const flash = (m: string) => {
    setNote(m);
    setTimeout(() => setNote(null), 1500);
  };

  if (!selected) {
    return (
      <div className="mx-auto max-w-[860px] p-8">
        <Header title="供应商" note="点击进入端点管理" />
        {err && <ErrorBar msg={err} />}
        <ul className="flex flex-col gap-2">
          {vendors.map((v) => (
            <li key={v.id}>
              <button
                className="flex w-full items-center gap-3 rounded-xl border border-border bg-card p-3 text-left transition-colors hover:bg-[var(--color-surface-hover)]"
                onClick={() => openVendor(v.id)}
              >
                <span className="text-ui-base font-medium">{v.display_name || v.id}</span>
                <span className="text-ui-xs font-[family-name:var(--font-mono)] text-foreground-subtlest">{v.id}</span>
                <span className="ml-auto flex items-center gap-2">
                  <KeyBadge has={v.has_api_key} />
                  <span className="text-ui-xs text-foreground-subtlest">{v.endpoint_ids.length} 端点</span>
                </span>
              </button>
            </li>
          ))}
          {vendors.length === 0 && !err && <EmptyNote text="(无供应商)" />}
        </ul>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[860px] p-8">
      <div className="mb-4 flex items-baseline gap-3">
        <button className="text-ui-sm text-foreground-subtle hover:text-foreground" onClick={() => setSelected(null)}>
          ← 供应商列表
        </button>
        <h2 className="text-ui-lg font-semibold">{detail?.display_name || selected}</h2>
        {detail && <KeyBadge has={detail.has_api_key} />}
        {note && <span className="text-ui-xs text-[var(--color-success)]">{note}</span>}
      </div>
      {err && <ErrorBar msg={err} />}
      {!detail ? (
        <EmptyNote text="加载中…" />
      ) : (
        <ul className="flex flex-col gap-3">
          {Object.entries(detail.endpoints).map(([name, ep]) => (
            <li key={name}>
              <EndpointCard
                vendor={selected}
                name={name}
                ep={ep}
                onSaved={(m) => {
                  flash(m);
                  openVendor(selected); // 重取以刷新 has_api_key
                }}
                onError={setErr}
              />
            </li>
          ))}
          {Object.keys(detail.endpoints).length === 0 && <EmptyNote text="(无端点)" />}
        </ul>
      )}
    </div>
  );
}

function EndpointCard({
  vendor,
  name,
  ep,
  onSaved,
  onError,
}: {
  vendor: string;
  name: string;
  ep: EndpointInfo;
  onSaved: (msg: string) => void;
  onError: (msg: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [baseUrl, setBaseUrl] = useState(ep.base_url ?? "");
  const [protocol, setProtocol] = useState(ep.protocol ?? "");
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);

  const saveEndpoint = () => {
    setSaving(true);
    api
      .put(`/api/vendors/${vendor}/endpoints/${name}`, {
        ...(ep as Record<string, unknown>),
        base_url: baseUrl,
        protocol: protocol || undefined,
      })
      .then(() => {
        setSaving(false);
        onSaved("端点已保存");
      })
      .catch((e: Error) => {
        setSaving(false);
        onError(e.message);
      });
  };

  const saveApiKey = () => {
    if (!apiKey.trim()) return;
    setSaving(true);
    api
      .put(`/api/vendors/${vendor}/endpoints/${name}/apikey`, { api_key: apiKey })
      .then(() => {
        setSaving(false);
        setApiKey("");
        onSaved("API Key 已保存");
      })
      .catch((e: Error) => {
        setSaving(false);
        onError(e.message);
      });
  };

  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      <button
        className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-[var(--color-hover)]"
        onClick={() => setOpen(!open)}
      >
        <span className="text-ui-sm font-medium">{name}</span>
        <span className="text-ui-xs font-[family-name:var(--font-mono)] text-foreground-subtlest">
          {ep.base_url || "(未配置)"}
        </span>
        <span className="ml-auto flex items-center gap-2">
          <KeyBadge has={ep.has_api_key ?? false} />
          <span className="text-ui-xs text-foreground-subtlest">{open ? "▾" : "▸"}</span>
        </span>
      </button>
      {open && (
        <div className="flex flex-col gap-3 border-t border-border p-4">
          <Field label="Base URL">
            <input
              className="text-ui-sm w-full rounded-lg border border-border bg-input px-3 py-2 font-[family-name:var(--font-mono)] text-foreground focus:outline-none"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://api.example.com/v1"
              spellCheck={false}
            />
          </Field>
          <Field label="协议">
            <input
              className="text-ui-sm w-48 rounded-lg border border-border bg-input px-3 py-2 font-[family-name:var(--font-mono)] text-foreground focus:outline-none"
              value={protocol}
              onChange={(e) => setProtocol(e.target.value)}
              placeholder="openai / anthropic / …"
              spellCheck={false}
            />
          </Field>
          <div className="flex items-center gap-2">
            <button
              className="text-ui-sm rounded-lg bg-brand px-4 py-1.5 font-medium text-white disabled:opacity-40"
              disabled={saving}
              onClick={saveEndpoint}
            >
              保存端点
            </button>
          </div>
          <div className="border-t border-border pt-3">
            <Field label={`API Key ${ep.has_api_key ? "(已设置,输入以更换)" : "(未设置)"}`}>
              <div className="flex gap-2">
                <input
                  className="text-ui-sm w-full rounded-lg border border-border bg-input px-3 py-2 font-[family-name:var(--font-mono)] text-foreground focus:outline-none"
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder="sk-…"
                  autoComplete="off"
                />
                <button
                  className="text-ui-sm rounded-lg border border-border px-4 py-1.5 text-foreground-subtle hover:bg-hover hover:text-foreground disabled:opacity-40"
                  disabled={saving || !apiKey.trim()}
                  onClick={saveApiKey}
                >
                  保存 Key
                </button>
              </div>
            </Field>
          </div>
        </div>
      )}
    </div>
  );
}

function Header({ title, note }: { title: string; note: string }) {
  return (
    <div className="mb-4">
      <h2 className="text-ui-lg font-semibold">{title}</h2>
      <p className="text-ui-sm text-foreground-subtle">{note}</p>
    </div>
  );
}

function ErrorBar({ msg }: { msg: string }) {
  return (
    <div className="text-ui-sm mb-3 rounded-lg bg-[var(--color-destructive-surface)] px-3 py-2 text-[var(--color-destructive-surface-fg)]">
      {msg}
    </div>
  );
}

function EmptyNote({ text }: { text: string }) {
  return <p className="text-ui-sm text-foreground-subtlest">{text}</p>;
}

function KeyBadge({ has }: { has: boolean }) {
  return (
    <span
      className="text-ui-xs rounded-full px-2 py-0.5"
      style={
        has
          ? { background: "var(--color-success-surface)", color: "var(--color-success-surface-fg)" }
          : { background: "var(--color-warning-surface)", color: "var(--color-warning-surface-fg)" }
      }
    >
      {has ? "已配置 Key" : "无 Key"}
    </span>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-ui-xs text-foreground-subtle">{label}</span>
      {children}
    </label>
  );
}
