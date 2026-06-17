/* ============================================
   ggcode — Landing Page Logic
   ============================================ */

(function () {
  "use strict";

  /* ---- Language ---- */

  var LANG = localStorage.getItem("ggcode-lang") || "en";

  var STRINGS = {
    en: {
      "nav.features": "Features",
      "nav.install": "Install",
      "hero.title": "",
      "hero.sub":
        "Read code, edit files, run commands, wire in MCP, manage sessions, ship releases. Not a chat wrapper — a full engineering workflow.",
      "hero.cta": "Get Started →",
      "terminal.demo": "Explain the auth flow and refactor for readability",
      "features.title":
        "Everything you need.\n<span class='muted'>Nothing you don't.</span>",
      "features.terminal.title": "Full toolkit, zero context switching",
      "features.terminal.text":
        "File editing, shell commands, Git, LSP, search, code review — all in one session, no window juggling.",
      "features.mcp.title": "MCP integration",
      "features.mcp.text":
        "Connect Cloudflare, Playwright, web readers, databases, and custom tools via Model Context Protocol.",
      "features.agent.title": "Multi-agent",
      "features.agent.text":
        "Spawn parallel workers, delegate to teammates, collaborate across instances with A2A protocol.",
      "features.im.title": "IM bridging",
      "features.im.text":
        "Control from QQ, Telegram, Discord, Slack, Feishu, DingTalk. Your agent is always reachable.",
      "features.modes.title": "Permission modes",
      "features.modes.text":
        "Five trust levels from fully supervised to autopilot. Checkpoints and /undo keep changes reversible.",
      "features.harness.title": "Harness workflow",
      "features.harness.text":
        "Isolated task execution in git worktrees with automated checks, code review, and promotion.",
      "features.mobile.title": "Desktop + Mobile",
      "features.mobile.text":
        "Native apps for macOS, Windows, Linux, iOS, Android. Pair mobile via encrypted relay.",
      "features.sessions.title": "Resumable sessions",
      "features.sessions.text":
        "Pause any conversation, resume it later. Switch between projects without losing context.",
      "install.title":
        "Pick your path.\n<span class='muted'>Get running in seconds.</span>",
      "install.script.text": "No sudo required. Installs to ~/.local/bin",
      "install.win.text": "No admin required. Uses winget or PowerShell",
      "install.brew.text": "macOS and Linux via Homebrew tap",
      "install.npm.text": "Node ecosystem, installs release binary",
      "install.pip.text": "Python environments or team distribution",
      "install.native.title": "Native packages",
      "install.native.text": ".pkg · .msi · .deb · .rpm · .apk · AppImage",
      "install.native.cta": "Releases →",
      "cta.title":
        "<span class='gradient-text'>Ship faster</span><br />from your terminal.",
      "cta.text":
        "If you live in the terminal, care about workflow integrity, and still want delivery speed — ggcode belongs in your shell.",
      "cta.cta": "Install now →",
      "cta.github": "Star on GitHub",
    },
    zh: {
      "nav.features": "功能",
      "nav.install": "安装",
      "hero.title": "",
      "hero.sub":
        "读代码、编辑文件、执行命令、接入 MCP、管理会话、发布版本。不是聊天包装器 — 是完整的工程工作流。",
      "hero.cta": "开始使用 →",
      "terminal.demo": "解释认证流程并重构以提升可读性",
      "features.title":
        "你需要的一切。\n<span class='muted'>没有多余的。</span>",
      "features.terminal.title": "完整工具链，零上下文切换",
      "features.terminal.text":
        "文件编辑、Shell 命令、Git、LSP、搜索、代码审查 — 全在一个会话里，不用切窗口。",
      "features.mcp.title": "MCP 集成",
      "features.mcp.text":
        "连接 Cloudflare、Playwright、网页读取、数据库和自定义工具，通过 Model Context Protocol。",
      "features.agent.title": "多 Agent",
      "features.agent.text":
        "生成并行工作者、委托给队友、通过 A2A 协议跨实例协作。",
      "features.im.title": "IM 桥接",
      "features.im.text":
        "从 QQ、Telegram、Discord、Slack、飞书、钉钉控制。你的 Agent 触手可及。",
      "features.modes.title": "权限模式",
      "features.modes.text":
        "五种信任级别，从完全监督到自动驾驶。检查点和 /undo 让变更可逆。",
      "features.harness.title": "Harness 工作流",
      "features.harness.text":
        "在 git worktree 中隔离执行任务，自动检查、代码审查、合并推广。",
      "features.mobile.title": "桌面 + 移动端",
      "features.mobile.text":
        "macOS、Windows、Linux、iOS、Android 原生应用。通过加密 relay 配对移动端。",
      "features.sessions.title": "可恢复会话",
      "features.sessions.text":
        "随时暂停对话，稍后恢复。在项目间切换不丢失上下文。",
      "install.title":
        "选择你的方式。\n<span class='muted'>几秒钟就能跑起来。</span>",
      "install.script.text": "不需要 sudo。安装到 ~/.local/bin",
      "install.win.text": "不需要管理员。用 winget 或 PowerShell",
      "install.brew.text": "通过 Homebrew tap 安装，支持 macOS 和 Linux",
      "install.npm.text": "Node 生态，安装 release 二进制",
      "install.pip.text": "Python 环境或团队分发",
      "install.native.title": "原生安装包",
      "install.native.text": ".pkg · .msi · .deb · .rpm · .apk · AppImage",
      "install.native.cta": "下载 →",
      "cta.title":
        "<span class='gradient-text'>更快交付</span><br />从你的终端开始。",
      "cta.text":
        "如果你生活在终端里、关心工作流完整性、还想保持交付速度 — ggcode 就属于你的 shell。",
      "cta.cta": "立即安装 →",
      "cta.github": "GitHub 加星",
    },
  };

  function applyLang() {
    var dict = STRINGS[LANG] || STRINGS.en;
    document.querySelectorAll("[data-i18n]").forEach(function (el) {
      var key = el.getAttribute("data-i18n");
      if (dict[key] !== undefined && dict[key] !== "") {
        var val = dict[key];
        if (val.indexOf("<") !== -1) {
          el.innerHTML = val.replace(/\n/g, "<br />");
        } else {
          el.textContent = val;
        }
      }
    });
    document.documentElement.lang = LANG === "zh" ? "zh-CN" : "en";
  }

  var langToggle = document.getElementById("langToggle");
  if (langToggle) {
    langToggle.addEventListener("click", function () {
      LANG = LANG === "en" ? "zh" : "en";
      localStorage.setItem("ggcode-lang", LANG);
      applyLang();
    });
  }

  applyLang();

  /* ---- Install tabs ---- */

  var cmds = {
    macos: "curl -fsSL https://ggcode.dev/install.sh | bash",
    windows: "irm https://ggcode.dev/install.ps1 | iex",
    brew: "brew install topcheer/ggcode/ggcode",
    npm: "npm install -g @ggcode-cli/ggcode",
  };

  var tabs = document.querySelectorAll(".tab");
  var cmdEl = document.getElementById("installCmd");

  tabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      tabs.forEach(function (t) { t.classList.remove("active"); });
      tab.classList.add("active");
      var key = tab.getAttribute("data-tab");
      if (cmds[key] && cmdEl) {
        cmdEl.textContent = cmds[key];
      }
    });
  });

  /* ---- Copy buttons ---- */

  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function (e) {
      e.preventDefault();
      e.stopPropagation();
      var text = btn.getAttribute("data-copy");
      if (!text && cmdEl) text = cmdEl.textContent;

      function fallback() {
        var ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); } catch (_) {}
        document.body.removeChild(ta);
      }

      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).catch(fallback);
      } else {
        fallback();
      }

      btn.classList.add("copied");
      setTimeout(function () { btn.classList.remove("copied"); }, 1500);
    });
  });

  /* ---- Scroll reveal ---- */

  if ("IntersectionObserver" in window) {
    var io = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) {
            entry.target.classList.add("revealed");
            io.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.08, rootMargin: "0px 0px -40px 0px" }
    );

    document.querySelectorAll("[data-reveal]").forEach(function (el) {
      io.observe(el);
    });
  } else {
    document.querySelectorAll("[data-reveal]").forEach(function (el) {
      el.classList.add("revealed");
    });
  }

  /* ---- Terminal typing effect ---- */

  var typedLines = document.querySelectorAll(".terminal-content .terminal-line");
  var typedIndex = 0;

  function typeNext() {
    if (typedIndex >= typedLines.length) return;
    var line = typedLines[typedIndex];
    line.style.opacity = "0";
    line.style.transition = "opacity 0.3s";

    setTimeout(function () {
      line.style.opacity = "1";
      typedIndex++;
      if (typedIndex < typedLines.length) {
        setTimeout(typeNext, 200 + Math.random() * 250);
      }
    }, 100);
  }

  // Start typing effect after a short delay
  setTimeout(function () {
    // Hide all lines first
    typedLines.forEach(function (l) { l.style.opacity = "0"; });
    typeNext();
  }, 600);
})();
