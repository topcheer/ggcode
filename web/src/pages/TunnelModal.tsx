import { useEffect, useState } from "react";
import { fetchTunnelStatus, sendCommand, type EtStatus } from "@/api/tunnel";

/** et 隧道弹层:开启 /et→显示公网 URL 可直接点击(关闭弹层不关隧道;显式按钮停隧道) */
export default function TunnelModal({ onClose }: { onClose: () => void }) {
  const [st, setSt] = useState<EtStatus | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    let stop = false;
    // 打开即开启(幂等:已激活返回现 URL)
    sendCommand("/et on").catch(() => setErr("开启隧道失败"));
    const tick = async () => {
      try {
        const d = await fetchTunnelStatus();
        if (!stop) setSt(d.et);
      } catch {
        if (!stop) setErr("状态拉取失败");
      }
    };
    tick();
    const iv = setInterval(tick, 1000);
    return () => {
      stop = true;
      clearInterval(iv);
    };
  }, []);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="w-[92vw] max-w-[420px] rounded-2xl border border-border bg-popover p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-ui-base font-semibold">et 公网隧道</h3>
          <button className="text-ui-sm text-foreground-subtle hover:text-foreground" onClick={onClose}>
            ✕
          </button>
        </div>
        {err && <p className="text-ui-sm text-[var(--color-warning-surface-fg)]">{err}</p>}
        {st?.active && st.url ? (
          <div className="flex flex-col gap-3">
            <a
              href={st.url}
              target="_blank"
              rel="noreferrer"
              className="text-ui-sm break-all rounded-lg bg-hover px-3 py-2 text-[var(--color-accent,#4c8dff)] underline"
            >
              {st.url}
            </a>
            <p className="text-ui-xs text-foreground-subtlest">公网可直接点击访问;关闭本弹层不影响隧道。</p>
          </div>
        ) : (
          <p className="text-ui-sm text-foreground-subtlest">隧道开启中…(免费计划随机子域名,约数秒)</p>
        )}
        <div className="mt-4 flex justify-end">
          <button
            className="text-ui-sm rounded-lg border border-border px-3 py-1 text-foreground-subtle hover:text-foreground"
            onClick={() => {
              sendCommand("/et off");
              onClose();
            }}
          >
            停止隧道
          </button>
        </div>
      </div>
    </div>
  );
}
