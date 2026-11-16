import { useEffect } from "react";
import type { PanelDirective } from "./types";
import SelectPanel from "./SelectPanel";
import ListDetailPanel from "./ListDetailPanel";
import FormPanel from "./FormPanel";
import StatsPanel from "./StatsPanel";

/** PanelModal:统一弹层(遮罩+Esc+样式基调对齐 TunnelModal),按 kind 分发四原型 */
export default function PanelModal({ panel, onClose }: { panel: PanelDirective; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="flex max-h-[86vh] w-[92vw] max-w-[560px] flex-col rounded-2xl border border-border bg-popover p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-ui-base font-semibold">{panel.title}</h3>
          <button className="text-ui-sm text-foreground-subtle hover:text-foreground" onClick={onClose}>
            ✕
          </button>
        </div>
        {panel.kind === "select" && <SelectPanel panel={panel} onClose={onClose} />}
        {panel.kind === "list-detail" && <ListDetailPanel panel={panel} onClose={onClose} />}
        {panel.kind === "form" && <FormPanel panel={panel} onClose={onClose} />}
        {panel.kind === "stats" && <StatsPanel panel={panel} onClose={onClose} />}
      </div>
    </div>
  );
}
