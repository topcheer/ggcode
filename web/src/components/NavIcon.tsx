/** 线性图标(24 viewBox,随文字取色,不依赖 emoji 字体):左栏导航 / 折叠开关 / 欢迎页入口 */
export type IconName = "chat" | "settings" | "share" | "tunnel" | "panelLeft" | "clock" | "check" | "history" | "reasoning";

export default function NavIcon({ name, className = "h-4 w-4 shrink-0" }: { name: IconName; className?: string }) {
  const common = { fill: "none", stroke: "currentColor", strokeWidth: 1.75, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  let body: React.ReactNode;
  switch (name) {
    case "chat":
      body = <path d="M4 6a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3v7a3 3 0 0 1-3 3h-6l-4.5 4v-4H7a3 3 0 0 1-3-3z" />;
      break;
    case "settings":
      body = (
        <>
          <circle cx="12" cy="12" r="3" />
          <circle cx="12" cy="12" r="6.5" />
          <path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M18.7 5.3l-2.1 2.1M7.4 16.6l-2.1 2.1" />
        </>
      );
      break;
    case "share":
      body = (
        <>
          <rect x="7" y="2.5" width="10" height="19" rx="2.5" />
          <path d="M11 18.5h2" />
        </>
      );
      break;
    case "tunnel":
      body = <path d="M4 8h13l-3-3M20 16H7l3 3" />;
      break;
    case "panelLeft":
      body = (
        <>
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M9.5 4v16" />
        </>
      );
      break;
    case "clock":
      body = (
        <>
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7v5l3 2" />
        </>
      );
      break;
    case "check":
      body = <path d="M5 12.5l4.5 4.5L19 7.5" />;
      break;
    case "reasoning":
      // 四角星 + 小星:与正文/工具卡区分,但保持线性细笔画的安静感
      body = (
        <>
          <path d="M10 4l1.6 4.4L16 10l-4.4 1.6L10 16l-1.6-4.4L4 10l4.4-1.6z" />
          <path d="M17.5 14.5l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8z" />
        </>
      );
      break;
    case "history":
      body = (
        <>
          <path d="M3.5 12a8.5 8.5 0 1 0 2.5-6L3.5 8.5" />
          <path d="M3.5 4v4.5H8" />
          <path d="M12 8v4.5l3 2" />
        </>
      );
      break;
  }
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden {...common}>
      {body}
    </svg>
  );
}
