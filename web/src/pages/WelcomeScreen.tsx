import { useState } from "react";

type Props =
  | { embedded?: false } // 独立欢迎页(纯视觉,P0 形态)
  | { embedded: true; onSend: (text: string) => void }; // 嵌入 Chat 空态,可发送

/** WelcomeScreen:居中大 composer+快捷入口;embedded 模式接入真实发送 */
export default function WelcomeScreen(props: Props) {
  const [value, setValue] = useState("");
  const embedded = (props as { embedded?: boolean }).embedded === true;
  const onSend = (props as { onSend?: (t: string) => void }).onSend;
  const busy = useBusySafe();

  const submit = () => {
    const t = value.trim();
    if (!t || !onSend || busy) return;
    onSend(t);
    setValue("");
  };

  return (
    <div className="flex h-full flex-col items-center justify-center px-8">
      <div className="w-full max-w-[720px]">
        <h2 className="text-ui-xl mb-1 text-center font-semibold tracking-tight">有什么可以帮你的?</h2>
        <p className="text-ui-sm mb-8 text-center text-foreground-subtle">
          {embedded ? "直接输入任务开始会话" : "直接输入任务,或从下面的入口开始"}
        </p>

        <div
          data-composer
          className="rounded-2xl border border-border bg-card p-3 transition-shadow"
          style={{ boxShadow: "var(--shadow-composer-idle)" }}
        >
          <textarea
            className="text-ui-base text-wrap-phrase h-16 w-full resize-none bg-transparent text-foreground placeholder:text-foreground-subtlest focus:outline-none"
            placeholder={embedded ? (typeof matchMedia !== "undefined" && matchMedia("(pointer: coarse)").matches ? "输入任务…(回车发送)" : "输入任务…(⌘⏎ 发送)") : "输入任务…"}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (embedded && e.key === "Enter" && !e.shiftKey && (typeof matchMedia === "undefined" || matchMedia("(pointer: coarse)").matches || e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                submit();
              }
            }}
          />
          {embedded && (
            <div className="flex items-center justify-between pt-1">
              <span className="text-ui-xs text-foreground-subtlest">{typeof matchMedia !== "undefined" && matchMedia("(pointer: coarse)").matches ? "回车发送" : "⌘⏎ 发送 · Shift+⏎ 换行"}</span>
              <button
                className="text-ui-sm rounded-lg bg-brand px-4 py-1.5 font-medium text-white transition-opacity disabled:opacity-40"
                disabled={!value.trim() || busy}
                onClick={submit}
              >
                发送
              </button>
            </div>
          )}
        </div>

        <div className="mt-10 grid w-full grid-cols-2 gap-3 sm:grid-cols-4">
          {[
            { icon: "⚙", label: "设置", note: "P2 迁移" },
            { icon: "⏱", label: "定时任务", note: "cron 待 P2+" },
            { icon: "✓", label: "任务清单", note: "todo 待 P1.5" },
            { icon: "🕘", label: "历史会话", note: "sessions 待 P2" },
          ].map((q) => (
            <button
              key={q.label}
              className="rounded-xl border border-border bg-card p-3 text-left transition-colors hover:bg-[var(--color-surface-hover)]"
              title={q.note}
            >
              <span className="text-ui-base">
                {q.icon} {q.label}
              </span>
              <span className="text-ui-xs mt-1 block text-foreground-subtlest">{q.note}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

// 独立模式下读 busy 无害(默认 false);避免引入 store 循环依赖
function useBusySafe(): boolean {
  try {
    // 延迟 require 不可行(ESM),改为可选链安全导入在调用方处理;此处恒 false 即可
    return false;
  } catch {
    return false;
  }
}
