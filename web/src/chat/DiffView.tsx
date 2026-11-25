import type { CSSProperties } from "react";
import type { DiffLine } from "./toolPresent";

/** 统一 diff 行渲染:删除红底 / 新增绿底 / hunk 信息色;行号由调用方决定是否提供 */
export default function DiffView({ lines, maxHeight = 320 }: { lines: DiffLine[]; maxHeight?: number }) {
  return (
    <pre className="overflow-auto whitespace-pre font-[family-name:var(--font-mono)] text-ui-xs leading-5" style={{ maxHeight }}>
      {lines.map((l, i) => {
        const style: CSSProperties = { display: "block", padding: "0 10px" };
        let prefix = " ";
        if (l.kind === "add") {
          style.background = "var(--color-success-surface)";
          style.color = "var(--color-success-surface-fg)";
          prefix = "+";
        } else if (l.kind === "del") {
          style.background = "var(--color-destructive-surface)";
          style.color = "var(--color-destructive-surface-fg)";
          prefix = "-";
        } else if (l.kind === "hunk") {
          style.color = "var(--color-warning)";
          prefix = " ";
        } else {
          style.color = "var(--color-foreground-subtle)";
        }
        return (
          <span key={i} style={style}>
            <span className="select-none opacity-60">{prefix} </span>
            {l.text || " "}
          </span>
        );
      })}
    </pre>
  );
}
