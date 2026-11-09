/** 双隧道 API(/api/tunnel/status 只读;启停经 /api/chat/commands slash 全链) */
export type ShareStatus = { active: boolean; starting?: boolean; connected?: boolean; connect_url?: string; qrcode_png_b64?: string };
export type EtStatus = { active: boolean; url?: string };
export type TunnelStatus = { share: ShareStatus; et: EtStatus };

export async function fetchTunnelStatus(): Promise<TunnelStatus> {
  const r = await fetch("/api/tunnel/status");
  const d = await r.json();
  return {
    share: d.share ?? { active: false, starting: false, connected: false },
    et: d.et ?? { active: false, url: "" },
  };
}

export function sendCommand(text: string): Promise<void> {
  return fetch("/api/chat/commands", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  }).then(() => undefined);
}
