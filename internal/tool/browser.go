package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// browserProfile holds a Chrome instance with its own allocator.
// Each profile = one Chrome process with its own user-data-dir.
type browserProfile struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	tabs        map[string]*browserTab
}

// browserTab holds state for a single browser tab within a profile.
type browserTab struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// Browser provides Go-native browser automation via Chrome DevTools Protocol (CDP).
//
// This replaces the Playwright MCP server dependency, eliminating Node.js overhead
// (~100-200MB RAM) while keeping full SPA/JavaScript support. Uses chromedp to
// control Chrome/Chromium directly from Go.
//
// Features:
//   - Full JavaScript execution (SPA support: React, Vue, Angular, etc.)
//   - Cookie/session persistence across actions within a profile
//   - Multiple Chrome profiles (each with its own cookies, extensions, settings)
//   - Multiple parallel sessions (tabs) within each profile
//   - Navigation, clicking, typing, screenshots, JS evaluation
//
// Requires: Chrome or Chromium installed on the system.
// For lightweight non-JS scraping, use web_fetch instead.
type Browser struct {
	profiles map[string]*browserProfile
	mu       sync.Mutex
}

// NewBrowser creates a new CDP-based browser tool.
func NewBrowser() *Browser {
	return &Browser{
		profiles: make(map[string]*browserProfile),
	}
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Description() string {
	return "Go-native browser automation via Chrome DevTools Protocol. Full SPA/JavaScript support without Node.js or Playwright. " +
		"Actions: navigate, click, type, extract, screenshot, evaluate (run JS), wait, links, scroll, back, content, close. " +
		"Supports multiple Chrome profiles (each with separate cookies/sessions) and multiple tabs per profile. " +
		"Requires Chrome/Chromium installed. For simple non-JS page fetching, use web_fetch instead."
}

func (b *Browser) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"description": "Browser action to perform.",
			"enum": ["navigate", "click", "type", "extract", "screenshot", "evaluate", "wait", "links", "scroll", "back", "content", "close"]
		},
		"url": {
			"type": "string",
			"description": "URL to navigate to (for 'navigate' action)."
		},
		"profile": {
			"type": "string",
			"description": "Chrome profile name. Each profile runs a separate Chrome instance with its own cookies, extensions, and settings. Use 'default' for a clean ephemeral profile, or specify a name like 'work', 'personal' to persist data between sessions. Defaults to 'default' (temporary, cleared on exit). To use your real Chrome profile, set profile to 'system'."
		},
		"session": {
			"type": "string",
			"description": "Tab/session ID within a profile. Each session is a separate browser tab. Defaults to 'default'. Use different IDs for parallel browsing within the same profile."
		},
		"selector": {
			"type": "string",
			"description": "CSS selector for click, type, extract, wait, scroll actions. Examples: 'button.submit', '#login-form', 'a[href*=\"github\"]', 'input[name=email]'."
		},
		"text": {
			"type": "string",
			"description": "Text to type into the selected element (for 'type' action). For form fields, clears existing content first."
		},
		"expression": {
			"type": "string",
			"description": "JavaScript expression to evaluate (for 'evaluate' action). Runs in page context with full DOM access. Example: 'document.querySelectorAll(\"article\").length'."
		},
		"wait_for": {
			"type": "string",
			"description": "CSS selector to wait for before continuing (for 'navigate' and 'wait' actions). Ensures SPA content has rendered."
		},
		"wait_timeout": {
			"type": "integer",
			"description": "Timeout in seconds for 'wait' and 'wait_for' (default 10)."
		},
		"headless": {
			"type": "boolean",
			"description": "Run browser in headless mode (default true). Set to false to show browser window for debugging."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI."
		}
	},
	"required": ["action", "description"]
}`)
}

func (b *Browser) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action      string `json:"action"`
		URL         string `json:"url"`
		Profile     string `json:"profile"`
		Session     string `json:"session"`
		Selector    string `json:"selector"`
		Text        string `json:"text"`
		Expression  string `json:"expression"`
		WaitFor     string `json:"wait_for"`
		WaitTimeout int    `json:"wait_timeout"`
		Headless    *bool  `json:"headless"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Profile == "" {
		args.Profile = "default"
	}
	if args.Session == "" {
		args.Session = "default"
	}
	if args.WaitTimeout == 0 {
		args.WaitTimeout = 10
	}

	switch args.Action {
	case "navigate":
		if args.URL == "" {
			return Result{IsError: true, Content: "url is required for navigate action"}, nil
		}
		return b.doNavigate(ctx, args.Profile, args.Session, args.URL, args.WaitFor, args.WaitTimeout, args.Headless)

	case "click":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector is required for click action"}, nil
		}
		return b.doClick(ctx, args.Profile, args.Session, args.Selector, args.WaitFor, args.WaitTimeout, args.Headless)

	case "type":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector is required for type action"}, nil
		}
		return b.doType(ctx, args.Profile, args.Session, args.Selector, args.Text, args.WaitFor, args.WaitTimeout, args.Headless)

	case "extract":
		return b.doExtract(ctx, args.Profile, args.Session, args.Selector, args.Headless)

	case "screenshot":
		return b.doScreenshot(ctx, args.Profile, args.Session, args.Selector, args.Headless)

	case "evaluate":
		if args.Expression == "" {
			return Result{IsError: true, Content: "expression is required for evaluate action"}, nil
		}
		return b.doEvaluate(ctx, args.Profile, args.Session, args.Expression, args.Headless)

	case "wait":
		if args.WaitFor == "" {
			return Result{IsError: true, Content: "wait_for selector is required for wait action"}, nil
		}
		return b.doWait(ctx, args.Profile, args.Session, args.WaitFor, args.WaitTimeout, args.Headless)

	case "links":
		return b.doLinks(ctx, args.Profile, args.Session, args.Headless)

	case "scroll":
		return b.doScroll(ctx, args.Profile, args.Session, args.Selector, args.Headless)

	case "back":
		return b.doBack(ctx, args.Profile, args.Session, args.Headless)

	case "content":
		return b.doContent(ctx, args.Profile, args.Session, args.Headless)

	case "close":
		return b.doCloseSession(args.Profile, args.Session)

	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s", args.Action)}, nil
	}
}

// getProfile returns an existing Chrome profile or lazily creates one.
// Each profile has its own Chrome allocator (separate process, cookies, data dir).
func (b *Browser) getProfile(name string, headless *bool) (*browserProfile, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if p, ok := b.profiles[name]; ok {
		return p, nil
	}

	// Build allocator options
	headlessMode := true
	if headless != nil {
		headlessMode = *headless
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1280, 800),
	}

	if headlessMode {
		opts = append(opts, chromedp.Headless)
	}

	// Profile-specific data directory
	if name == "system" {
		// Use the real Chrome profile directory — inherits existing cookies/login state
		userDataDir := findChromeUserDataDir()
		if userDataDir != "" {
			opts = append(opts, chromedp.UserDataDir(userDataDir))
		}
	} else if name != "default" {
		// Named profile: persist data in ~/.ggcode/browser-profiles/<name>
		dataDir := filepath.Join(homeDir(), ".ggcode", "browser-profiles", name)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create profile directory: %w", err)
		}
		opts = append(opts, chromedp.UserDataDir(dataDir))
	}
	// "default" profile uses chromedp's temp dir (ephemeral, cleaned on exit)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	p := &browserProfile{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		tabs:        make(map[string]*browserTab),
	}
	b.profiles[name] = p
	return p, nil
}

// getSession returns an existing tab or creates a new one within the profile.
func (b *Browser) getSession(profileName, sessionID string, headless *bool) (*browserTab, error) {
	p, err := b.getProfile(profileName, headless)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if tab, ok := p.tabs[sessionID]; ok {
		if tab.ctx.Err() == nil {
			return tab, nil
		}
		delete(p.tabs, sessionID)
	}

	taskCtx, cancel := chromedp.NewContext(p.allocCtx)
	if err := chromedp.Run(taskCtx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create browser tab: %w", err)
	}

	tab := &browserTab{ctx: taskCtx, cancel: cancel}
	p.tabs[sessionID] = tab
	return tab, nil
}

// doNavigate opens a URL and optionally waits for an element.
func (b *Browser) doNavigate(ctx context.Context, profile, session, rawURL, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	actions := []chromedp.Action{chromedp.Navigate(rawURL)}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+15)*time.Second)
	defer cancel()

	// Propagate caller cancellation
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-timeoutCtx.Done():
		}
	}()

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("navigation failed: %v", err)}, nil
	}

	var title, finalURL string
	_ = chromedp.Run(timeoutCtx,
		chromedp.Title(&title),
		chromedp.Location(&finalURL),
	)

	return Result{Content: fmt.Sprintf("Navigated to: %s\nTitle: %s\nURL: %s\nProfile: %s\nSession: %s\n\nUse action 'content' for page text, 'extract' with selector, or 'screenshot' for visual capture.", rawURL, title, finalURL, profile, session)}, nil
}

// doClick clicks an element.
func (b *Browser) doClick(ctx context.Context, profile, session, selector, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("click failed: %v", err)}, nil
	}

	var urlAfter string
	_ = chromedp.Run(timeoutCtx, chromedp.Location(&urlAfter))
	return Result{Content: fmt.Sprintf("Clicked: %s\nCurrent URL: %s", selector, urlAfter)}, nil
}

// doType clears an input field and types text into it.
func (b *Browser) doType(ctx context.Context, profile, session, selector, text, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Clear(selector, chromedp.ByQuery),
	}
	if text != "" {
		actions = append(actions, chromedp.SendKeys(selector, text, chromedp.ByQuery))
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("type failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Typed into %s: %q", selector, text)}, nil
}

// doExtract gets text content from the page or specific elements.
func (b *Browser) doExtract(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	if selector != "" {
		var results []map[string]string
		js := fmt.Sprintf(`(() => {
				const els = document.querySelectorAll(%q);
				return Array.from(els).map(el => ({
					tag: el.tagName.toLowerCase(),
					text: el.innerText.trim().substring(0, 5000),
					href: el.href || '',
					value: el.value || '',
					id: el.id || '',
					className: el.className || '',
				}));
			})()`, selector)

		if err := chromedp.Run(tab.ctx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("wait failed: %v", err)}, nil
		}
		if err := chromedp.Run(tab.ctx, chromedp.Evaluate(js, &results)); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("extract failed: %v", err)}, nil
		}
		if len(results) == 0 {
			return Result{Content: fmt.Sprintf("No elements matched: %s", selector)}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d elements matching '%s':\n\n", len(results), selector))
		for i, r := range results {
			if i >= 100 {
				sb.WriteString(fmt.Sprintf("\n... and %d more\n", len(results)-100))
				break
			}
			sb.WriteString(fmt.Sprintf("[%s", r["tag"]))
			if r["id"] != "" {
				sb.WriteString(fmt.Sprintf(" #%s", r["id"]))
			}
			if r["className"] != "" {
				parts := strings.Fields(r["className"])
				if len(parts) > 0 {
					sb.WriteString(fmt.Sprintf(" .%s", parts[0]))
				}
			}
			sb.WriteString("]")
			if r["text"] != "" {
				text := r["text"]
				if len(text) > 200 {
					text = text[:197] + "..."
				}
				sb.WriteString(" " + text)
			}
			if r["href"] != "" {
				sb.WriteString("\n  href: " + r["href"])
			}
			sb.WriteString("\n")
		}
		return Result{Content: sb.String()}, nil
	}

	// Full page text
	var pageText string
	if err := chromedp.Run(tab.ctx,
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`document.body.innerText.substring(0, 50000)`, &pageText),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("extract failed: %v", err)}, nil
	}
	return Result{Content: pageText}, nil
}

// doScreenshot captures a PNG screenshot.
func (b *Browser) doScreenshot(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	var buf []byte
	if selector != "" {
		if err := chromedp.Run(tab.ctx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
			chromedp.Screenshot(selector, &buf, chromedp.ByQuery),
		); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v", err)}, nil
		}
	} else {
		if err := chromedp.Run(tab.ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v", err)}, nil
		}
	}

	return Result{
		Content: fmt.Sprintf("Screenshot captured (%d bytes, PNG). The image is included as an image block for visual analysis.", len(buf)),
		Images:  []ResultImage{{MIME: "image/png", Base64: base64.StdEncoding.EncodeToString(buf)}},
	}, nil
}

// doEvaluate runs arbitrary JavaScript in the page context.
func (b *Browser) doEvaluate(ctx context.Context, profile, session, expression string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	var result interface{}
	// chromedp.Evaluate already wraps in an async function and awaits.
	// Do NOT add our own async wrapper — it causes results to come back
	// as empty objects instead of the actual values.
	if err := chromedp.Run(tab.ctx, chromedp.Evaluate(expression, &result)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("JavaScript evaluation failed: %v", err)}, nil
	}

	resultStr := formatJSResult(result)
	if len(resultStr) > 50000 {
		resultStr = resultStr[:50000] + "\n... [truncated]"
	}
	return Result{Content: fmt.Sprintf("Result:\n%s", resultStr)}, nil
}

// doWait waits for an element to appear.
func (b *Browser) doWait(ctx context.Context, profile, session, selector string, timeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if err := chromedp.Run(timeoutCtx, chromedp.WaitVisible(selector, chromedp.ByQuery)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("wait timed out after %ds: %v", timeout, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Element appeared: %s", selector)}, nil
}

// doLinks extracts all links from the page.
func (b *Browser) doLinks(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	type linkData struct {
		Text string `json:"text"`
		Href string `json:"href"`
	}
	var links []linkData

	js := `(() => {
		return Array.from(document.querySelectorAll("a[href]")).map(a => ({
			text: (a.innerText || a.textContent || "").trim().substring(0, 100),
			href: a.href,
		}));
	})()`

	if err := chromedp.Run(tab.ctx,
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(js, &links),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("links extraction failed: %v", err)}, nil
	}

	var sb strings.Builder
	var currentURL string
	_ = chromedp.Run(tab.ctx, chromedp.Location(&currentURL))
	sb.WriteString(fmt.Sprintf("Links on %s (%d total):\n\n", currentURL, len(links)))
	for i, l := range links {
		if i >= 200 {
			sb.WriteString(fmt.Sprintf("\n... and %d more\n", len(links)-200))
			break
		}
		text := l.Text
		if text == "" {
			text = "(no text)"
		}
		if len(text) > 80 {
			text = text[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %d. %s\n     → %s\n", i+1, text, l.Href))
	}
	return Result{Content: sb.String()}, nil
}

// doScroll scrolls the page or scrolls an element into view.
func (b *Browser) doScroll(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	if selector != "" {
		if err := chromedp.Run(tab.ctx, chromedp.ScrollIntoView(selector, chromedp.ByQuery)); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("scroll failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Scrolled %s into view.", selector)}, nil
	}

	if err := chromedp.Run(tab.ctx,
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("scroll failed: %v", err)}, nil
	}
	return Result{Content: "Scrolled to bottom of page."}, nil
}

// doBack navigates back in history.
func (b *Browser) doBack(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	if err := chromedp.Run(tab.ctx, chromedp.NavigateBack()); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("back navigation failed: %v", err)}, nil
	}

	var urlAfter string
	_ = chromedp.Run(tab.ctx, chromedp.Location(&urlAfter))
	return Result{Content: fmt.Sprintf("Navigated back. Current URL: %s", urlAfter)}, nil
}

// doContent gets structured page content.
func (b *Browser) doContent(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	type pageContent struct {
		Title    string `json:"title"`
		URL      string `json:"url"`
		Text     string `json:"text"`
		Headings string `json:"headings"`
	}

	var content pageContent
	js := `(() => {
		const headings = Array.from(document.querySelectorAll("h1, h2, h3, h4"))
			.map(h => "#".repeat(parseInt(h.tagName[1])) + " " + h.innerText.trim())
			.join("\n");
		return {
			title: document.title,
			url: window.location.href,
			text: document.body.innerText.substring(0, 40000),
			headings: headings,
		};
	})()`

	if err := chromedp.Run(tab.ctx,
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(js, &content),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("content extraction failed: %v", err)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("URL: %s\nTitle: %s\n\n", content.URL, content.Title))
	if content.Headings != "" {
		sb.WriteString("Page Structure:\n" + content.Headings + "\n\n")
	}
	sb.WriteString("Content:\n" + content.Text)
	if len(content.Text) >= 40000 {
		sb.WriteString("\n... [truncated]")
	}
	return Result{Content: sb.String()}, nil
}

// doCloseSession closes a browser tab and removes the session.
func (b *Browser) doCloseSession(profile, session string) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	p, ok := b.profiles[profile]
	if !ok {
		return Result{Content: fmt.Sprintf("Profile '%s' does not exist.", profile)}, nil
	}

	tab, ok := p.tabs[session]
	if !ok {
		return Result{Content: fmt.Sprintf("Session '%s' does not exist.", session)}, nil
	}
	tab.cancel()
	delete(p.tabs, session)
	return Result{Content: fmt.Sprintf("Closed session '%s' in profile '%s'.", session, profile)}, nil
}

// formatJSResult converts a JavaScript evaluation result to a readable string.
func formatJSResult(result interface{}) string {
	if result == nil {
		return "undefined"
	}
	switch v := result.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case []interface{}:
		var parts []string
		for _, item := range v {
			parts = append(parts, formatJSResult(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		data, _ := json.MarshalIndent(v, "", "  ")
		return string(data)
	default:
		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data)
	}
}

// findChromeUserDataDir locates the system Chrome user data directory.
func findChromeUserDataDir() string {
	home := homeDir()
	if home == "" {
		return ""
	}

	// Platform-specific Chrome user data directories
	candidates := []string{
		// macOS
		filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
		// Linux
		filepath.Join(home, ".config", "google-chrome"),
		filepath.Join(home, ".config", "chromium"),
		// Windows (Git Bash style paths)
		filepath.Join(home, "AppData", "Local", "Google", "Chrome", "User Data"),
	}

	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	return ""
}

// homeDir returns the user's home directory.
func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
