import { useEffect, useRef, useState } from "react";
import { fetchTunnelStatus, sendCommand, type ShareStatus } from "@/api/tunnel";

/** Share 弹层:发起共享→显示连接二维码→mobile app 扫码接入后自动隐藏(轮询 connected) */
export default function ShareModal({ onClose }: { onClose: () => void }) {
  const [st, setSt] = useState<ShareStatus | null>(null);
  const [err, setErr] = useState("");
  const autoClosed = useRef(false);

  useEffect(() => {
    let stop = false;
    // 打开即发起共享(幂等:已激活经 TUI 全链复用现 URL)
    sendCommand("/share").catch(() => setErr("发起共享失败"));
    const tick = async () => {
      try {
        const d = await fetchTunnelStatus();
        if (stop) return;
        setSt(d.share);
        // 需求契约:扫码连接之后二维码自动隐藏
        if (d.share.connected && !autoClosed.current) {
          autoClosed.current = true;
          onClose();
          return;
        }
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
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="w-[92vw] max-w-[360px] rounded-2xl border border-border bg-popover p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-ui-base font-semibold">共享到 Mobile App</h3>
          <button className="text-ui-sm text-foreground-subtle hover:text-foreground" onClick={onClose}>
            ✕
          </button>
        </div>
        {err && <p className="text-ui-sm text-[var(--color-warning-surface-fg)]">{err}</p>}
        {!st && !err && <p className="text-ui-sm text-foreground-subtlest">正在发起共享…</p>}
        {st?.connected ? (
          <p className="text-ui-sm">✓ 已连接,二维码已自动隐藏</p>
        ) : st?.qrcode_png_b64 ? (
          <div className="flex flex-col items-center gap-3">
            <img
              src={`data:image/png;base64,${st.qrcode_png_b64}`}
              alt="连接二维码"
              className="w-52 h-52 rounded-xl bg-white p-2"
            />
            <p className="text-ui-xs text-foreground-subtlest">用 Mobile App 扫码接入(接入后本页自动关闭)</p>
            {st.connect_url && <p className="w-full truncate text-center text-ui-xs text-foreground-subtlest">{st.connect_url}</p>}
          </div>
        ) : (
          <p className="text-ui-sm text-foreground-subtlest">{st?.starting ? "隧道建立中…" : "等待二维码…"}</p>
        )}
        <div className="mt-4 flex justify-end">
          <button
            className="text-ui-sm rounded-lg border border-border px-3 py-1 text-foreground-subtle hover:text-foreground"
            onClick={() => {
              sendCommand("/share stop");
              onClose();
            }}
          >
            停止共享
          </button>
        </div>
      </div>
    </div>
  );
}
