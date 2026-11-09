import { useEffect, useState } from "react";

/** ⌘K 命令面板(§4.5 键盘全流程):切视图/主题/新会话 */
export type PaletteAction = { id: string; label: string; hint: string; run: () => void };

export default function CommandPalette({
  onClose,
  actions,
}: {
  onClose: () => void;
  actions: PaletteAction[];
}) {
  const [query, setQuery] = useState("");
  const filtered = actions.filter((a) => a.label.toLowerCase().includes(query.toLowerCase()));

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center bg-black/40 pt-[15vh]"
      onClick={onClose}
    >
      <div
        className="w-[92vw] max-w-[520px] rounded-2xl border border-border bg-popover p-2 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          autoFocus
          className="text-ui-base mb-1 w-full rounded-xl bg-input px-4 py-3 text-foreground placeholder:text-foreground-subtlest focus:outline-none"
          placeholder="输入命令…(Esc 关闭)"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              const first = filtered[0];
              if (first) {
                first.run();
                onClose();
              }
            }
          }}
        />
        <ul className="flex flex-col gap-0.5">
          {filtered.map((a) => (
            <li key={a.id}>
              <button
                className="text-ui-sm flex w-full items-center justify-between rounded-lg px-3 py-2 text-left hover:bg-hover"
                onClick={() => {
                  a.run();
                  onClose();
                }}
              >
                <span>{a.label}</span>
                <span className="text-ui-xs text-foreground-subtlest">{a.hint}</span>
              </button>
            </li>
          ))}
          {filtered.length === 0 && (
            <li className="text-ui-sm px-3 py-2 text-foreground-subtlest">(无匹配命令)</li>
          )}
        </ul>
      </div>
    </div>
  );
}
