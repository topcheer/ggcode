package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
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

// Close shuts down all browser profiles and their Chrome processes.
// Implements the Closer interface for graceful cleanup on agent exit.
func (b *Browser) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for name, p := range b.profiles {
		// Cancel all tab contexts within the profile
		for _, tab := range p.tabs {
			tab.cancel()
		}
		p.tabs = nil
		// Cancel the allocator (kills the Chrome process)
		p.allocCancel()
		delete(b.profiles, name)
	}
	return nil
}

func (b *Browser) Description() string {
	return "Go-native browser automation via Chrome DevTools Protocol. Full SPA/JS support without Node.js or Playwright. Supports multiple Chrome profiles and tabs. Requires Chrome/Chromium. For simple non-JS page fetching, use web_fetch."
}

func (b *Browser) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"description": "Browser action to perform.",
			"enum": ["navigate", "click", "type", "extract", "screenshot", "evaluate", "wait", "links", "scroll", "back", "content", "close", "status", "select", "hover", "press", "upload", "cookies", "resize", "wait_not", "drag"]
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
		"value": {
			"type": "string",
			"description": "Value to select in a dropdown (for 'select' action). Matches by option value or visible text."
		},
		"key": {
			"type": "string",
			"description": "Key to press (for 'press' action). Supports: Enter, Tab, Escape, Backspace, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, Space, or a single character."
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
		},
		"path": {
			"type": "string",
			"description": "File path on disk to upload (for 'upload' action)."
		},
		"name": {
			"type": "string",
			"description": "Cookie name (for 'cookies' action get/delete) or name to set."
		},
		"frame": {
			"type": "string",
			"description": "CSS selector of an iframe to operate within. When set, evaluate/extract/click/type actions will target elements inside the specified iframe (same-origin only). Example: 'iframe#content'."
		},
		"width": {
			"type": "integer",
			"description": "Viewport width in pixels (for 'resize' action)."
		},
		"height": {
			"type": "integer",
			"description": "Viewport height in pixels (for 'resize' action)."
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
		Value       string `json:"value"`
		Key         string `json:"key"`
		Name        string `json:"name"`
		Path        string `json:"path"`
		Frame       string `json:"frame"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
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
		return b.doEvaluate(ctx, args.Profile, args.Session, args.Expression, args.Frame, args.Headless)

	case "wait":
		if args.WaitFor == "" {
			return Result{IsError: true, Content: "wait_for selector is required for wait action"}, nil
		}
		return b.doWait(ctx, args.Profile, args.Session, args.WaitFor, args.WaitTimeout, args.Headless)

	case "links":
		return b.doLinks(ctx, args.Profile, args.Session, args.Headless)

	case "select":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector is required for select action"}, nil
		}
		return b.doSelect(ctx, args.Profile, args.Session, args.Selector, args.Value, args.WaitFor, args.WaitTimeout, args.Headless)

	case "hover":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector is required for hover action"}, nil
		}
		return b.doHover(ctx, args.Profile, args.Session, args.Selector, args.Headless)

	case "press":
		if args.Key == "" {
			return Result{IsError: true, Content: "key is required for press action"}, nil
		}
		return b.doPress(ctx, args.Profile, args.Session, args.Key, args.Headless)

	case "scroll":
		return b.doScroll(ctx, args.Profile, args.Session, args.Selector, args.Headless)

	case "back":
		return b.doBack(ctx, args.Profile, args.Session, args.Headless)

	case "content":
		return b.doContent(ctx, args.Profile, args.Session, args.Headless)

	case "close":
		return b.doCloseSession(args.Profile, args.Session)

	case "status":
		return b.doStatus()

	case "upload":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector is required for upload action (the input[type=file] element)"}, nil
		}
		if args.Path == "" {
			return Result{IsError: true, Content: "path is required for upload action"}, nil
		}
		return b.doUpload(ctx, args.Profile, args.Session, args.Selector, args.Path, args.Headless)

	case "cookies":
		return b.doCookies(ctx, args.Profile, args.Session, args.Value, args.Name, args.URL, args.Headless)

	case "resize":
		if args.Width == 0 || args.Height == 0 {
			return Result{IsError: true, Content: "width and height are required for resize action"}, nil
		}
		return b.doResize(ctx, args.Profile, args.Session, args.Width, args.Height, args.Headless)

	case "wait_not":
		if args.WaitFor == "" {
			return Result{IsError: true, Content: "wait_for selector is required for wait_not action"}, nil
		}
		return b.doWaitNot(ctx, args.Profile, args.Session, args.WaitFor, args.WaitTimeout, args.Headless)

	case "drag":
		if args.Selector == "" {
			return Result{IsError: true, Content: "selector (source element) is required for drag action"}, nil
		}
		if args.Value == "" {
			return Result{IsError: true, Content: "value (target CSS selector) is required for drag action"}, nil
		}
		return b.doDrag(ctx, args.Profile, args.Session, args.Selector, args.Value, args.Headless)

	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s", args.Action)}, nil
	}
}

// minChromeMajorVersion is the minimum Chrome version required for reliable
// CDP support. Chrome < 90 may have missing or incompatible CDP domains.
const minChromeMajorVersion = 90

// chromeNotFoundHelp returns a human-friendly error message when Chrome
// cannot be found, with platform-specific install instructions.
func chromeNotFoundHelp() string {
	switch runtime.GOOS {
	case "darwin":
		return "Chrome or Chromium not found. Install from https://www.google.com/chrome/ or run: brew install --cask google-chrome"
	case "windows":
		return "Chrome or Chromium not found. Install from https://www.google.com/chrome/ or: winget install Google.Chrome"
	default:
		return "Chrome or Chromium not found. Install via your package manager, e.g.: sudo apt install google-chrome-stable OR sudo snap install chromium"
	}
}

// findChromeExecutable locates a Chrome/Chromium binary across platforms.
// Returns the path if found, empty string otherwise.
func findChromeExecutable() string {
	var locations []string
	switch runtime.GOOS {
	case "darwin":
		locations = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			filepath.Join(homeDir(), "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"),
		}
	case "windows":
		locations = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			filepath.Join(os.Getenv("USERPROFILE"), `AppData\Local\Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("USERPROFILE"), `AppData\Local\Chromium\Application\chrome.exe`),
		}
	default:
		// Linux and other Unix-like systems
		locations = []string{
			"google-chrome-stable",
			"google-chrome",
			"chromium-browser",
			"chromium",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/snap/bin/chromium",
			"/opt/google/chrome/chrome",
		}
	}

	for _, p := range locations {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
		// For absolute paths, check directly
		if filepath.IsAbs(p) {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

// getChromeVersion returns the major version number of the installed Chrome.
// Returns 0 if the version cannot be determined (non-fatal — we warn but don't block).
func getChromeVersion(chromePath string) int {
	cmd := exec.Command(chromePath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	// Output format: "Google Chrome 149.0.7827.201" or "Chromium 132.0.6834.110"
	re := regexp.MustCompile(`(\d+)\.`)
	match := re.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return 0
	}
	ver, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return ver
}

// getProfile returns an existing Chrome profile or lazily creates one.
// Each profile has its own Chrome allocator (separate process, cookies, data dir).
func (b *Browser) getProfile(name string, headless *bool) (*browserProfile, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if p, ok := b.profiles[name]; ok {
		return p, nil
	}

	// Pre-flight check: verify Chrome/Chromium is installed
	chromePath := findChromeExecutable()
	if chromePath == "" {
		return nil, fmt.Errorf("%s\n\nThe browser tool requires Chrome or Chromium to be installed on this system.", chromeNotFoundHelp())
	}

	// Check version — warn (but don't block) if too old
	chromeVersion := getChromeVersion(chromePath)
	if chromeVersion > 0 && chromeVersion < minChromeMajorVersion {
		// Non-fatal: older Chrome may work for basic operations
		fmt.Fprintf(os.Stderr, "[browser] WARNING: Chrome %d detected, recommended version is %d+\n", chromeVersion, minChromeMajorVersion)
	}

	// Build allocator options
	headlessMode := true
	if headless != nil {
		headlessMode = *headless
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(chromePath),
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

	// Auto-dismiss JS dialogs (alert/confirm/prompt/beforeunload) to prevent
	// Chrome from hanging when a page shows a dialog. Dialogs are dismissed
	// (not accepted) so automation can continue.
	chromedp.ListenTarget(taskCtx, func(ev interface{}) {
		if _, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			go func() {
				_ = chromedp.Run(taskCtx, page.HandleJavaScriptDialog(false))
			}()
		}
	})

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
				if len([]rune(text)) > 200 {
					text = string([]rune(text)[:197]) + "..."
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
func (b *Browser) doEvaluate(ctx context.Context, profile, session, expression, frame string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	// When frame is set, wrap the expression to execute inside the iframe's
	// contentDocument (same-origin only). This allows evaluating JS and
	// extracting/clicking elements within iframes.
	actualExpr := expression
	if frame != "" {
		actualExpr = fmt.Sprintf(`(() => {
			const f = document.querySelector(%q);
			if (!f) throw new Error("iframe not found: %s");
			if (!f.contentDocument) throw new Error("cannot access iframe contentDocument (cross-origin?): %s");
			const fn = new Function(%q);
			return fn.call(f.contentDocument.defaultView, f.contentDocument);
		})()`, frame, frame, frame, expression)
	}

	var result interface{}
	if err := chromedp.Run(tab.ctx, chromedp.Evaluate(actualExpr, &result)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("JavaScript evaluation failed: %v", err)}, nil
	}

	resultStr := formatJSResult(result)
	if len([]rune(resultStr)) > 50000 {
		resultStr = string([]rune(resultStr)[:50000]) + "\n... [truncated]"
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
	locCtx, locCancel := context.WithTimeout(tab.ctx, 5*time.Second)
	_ = chromedp.Run(locCtx, chromedp.Location(&currentURL))
	locCancel()
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
		if len([]rune(text)) > 80 {
			text = string([]rune(text)[:77]) + "..."
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

// doStatus lists all active browser profiles and sessions.
func (b *Browser) doStatus() (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.profiles) == 0 {
		return Result{Content: "No active browser profiles. Use 'navigate' to start a browser session."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Browser Status (%d profile(s) active):\n\n", len(b.profiles)))

	for name, p := range b.profiles {
		sb.WriteString(fmt.Sprintf("  Profile: %s (%d tab(s))\n", name, len(p.tabs)))
		for sessID, tab := range p.tabs {
			var url, title string
			if tab.ctx.Err() == nil {
				statusCtx, statusCancel := context.WithTimeout(tab.ctx, 5*time.Second)
				_ = chromedp.Run(statusCtx,
					chromedp.Location(&url),
					chromedp.Title(&title),
				)
				statusCancel()
			}
			status := "active"
			if tab.ctx.Err() != nil {
				status = "closed"
			}
			if url != "" {
				sb.WriteString(fmt.Sprintf("    └─ Session: %s [%s] %s — %s\n", sessID, status, title, url))
			} else {
				sb.WriteString(fmt.Sprintf("    └─ Session: %s [%s]\n", sessID, status))
			}
		}
	}

	chromePath := findChromeExecutable()
	if chromePath != "" {
		sb.WriteString(fmt.Sprintf("\nChrome: %s", chromePath))
		ver := getChromeVersion(chromePath)
		if ver > 0 {
			sb.WriteString(fmt.Sprintf(" (v%d)", ver))
		}
		sb.WriteString("\n")
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

// doSelect selects an option in a <select> dropdown element.
func (b *Browser) doSelect(ctx context.Context, profile, session, selector, value, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	}

	// Try to set the value via JS — this handles both value and visible text matching
	js := fmt.Sprintf(`(() => {
		const sel = document.querySelector(%q);
		if (!sel) return { ok: false, error: 'element not found' };
			if (!sel.options) return { ok: false, error: 'element is not a <select> — cannot set value' };
		for (const opt of sel.options) {
			if (opt.value === %q || opt.text.trim() === %q) {
				sel.value = opt.value;
				opt.selected = true;
				sel.dispatchEvent(new Event('change', { bubbles: true }));
				return { ok: true, value: opt.value, text: opt.text.trim() };
			}
		}
		return { ok: false, error: 'option not found', available: Array.from(sel.options).map(o => o.value + ':' + o.text.trim()) };
	})()`, selector, value, value)

	var selResult map[string]interface{}
	actions = append(actions, chromedp.Evaluate(js, &selResult))
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("select failed: %v", err)}, nil
	}

	if ok, _ := selResult["ok"].(bool); !ok {
		errMsg, _ := selResult["error"].(string)
		if avail, ok := selResult["available"].([]interface{}); ok && len(avail) > 0 {
			var opts []string
			for _, a := range avail {
				opts = append(opts, fmt.Sprintf("  - %v", a))
			}
			return Result{IsError: true, Content: fmt.Sprintf("select failed: %s. Available options:\n%s", errMsg, strings.Join(opts, "\n"))}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("select failed: %s", errMsg)}, nil
	}

	selectedText, _ := selResult["text"].(string)
	return Result{Content: fmt.Sprintf("Selected '%s' (%s) in %s", value, selectedText, selector)}, nil
}

// doHover hovers over an element to trigger menus, tooltips, etc.
func (b *Browser) doHover(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()

	// Use chromedp.EmulateHover via JS dispatchEvent for reliability
	js := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) return false;
		el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
		el.dispatchEvent(new MouseEvent('mouseenter', { bubbles: true }));
		el.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
		return true;
	})()`, selector)

	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Evaluate(js, nil),
	}

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("hover failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Hovered over: %s", selector)}, nil
}

// doPress simulates a keyboard key press on the focused element or the page.
func (b *Browser) doPress(ctx context.Context, profile, session, key string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	// Map common key names to CDP key values
	keyMap := map[string]string{
		"enter":     "\r",
		"tab":       "\t",
		"escape":    "\x1b",
		"backspace": "\x08",
		"space":     " ",
	}

	lowerKey := strings.ToLower(key)

	// Map all supported keys to their CDP key names for KeyEvent dispatch.
	// KeyEvent sends directly to the Input domain — it targets the focused
	// element without needing a CSS selector.
	var cdpKeyName string
	if mapped, ok := keyMap[lowerKey]; ok {
		cdpKeyName = mapped
	} else if len(key) == 1 {
		cdpKeyName = key
	} else {
		switch lowerKey {
		case "arrowup":
			cdpKeyName = "ArrowUp"
		case "arrowdown":
			cdpKeyName = "ArrowDown"
		case "arrowleft":
			cdpKeyName = "ArrowLeft"
		case "arrowright":
			cdpKeyName = "ArrowRight"
		case "delete":
			cdpKeyName = "Delete"
		case "home":
			cdpKeyName = "Home"
		case "end":
			cdpKeyName = "End"
		case "pageup":
			cdpKeyName = "PageUp"
		case "pagedown":
			cdpKeyName = "PageDown"
		default:
			return Result{IsError: true, Content: fmt.Sprintf("unsupported key: %s (supported: Enter, Tab, Escape, Backspace, Delete, Space, ArrowUp/Down/Left/Right, Home, End, PageUp, PageDown, or single characters)", key)}, nil
		}
	}

	if err := chromedp.Run(timeoutCtx, chromedp.KeyEvent(cdpKeyName)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("key press failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Pressed key: %s", key)}, nil
}

// doUpload sets a file on an <input type="file"> element.
func (b *Browser) doUpload(ctx context.Context, profile, session, selector, filePath string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid file path: %v", err)}, nil
	}
	if _, err := os.Stat(absPath); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("file not found: %s", absPath)}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()

	if err := chromedp.Run(timeoutCtx,
		// WaitReady, NOT WaitVisible — many sites hide the real <input type="file">
		// with display:none and use a styled button. SetUploadFiles only needs the
		// DOM node to exist, not be visible.
		chromedp.WaitReady(selector, chromedp.ByQuery),
		chromedp.SetUploadFiles(selector, []string{absPath}, chromedp.ByQuery),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("upload failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Uploaded file: %s into %s", absPath, selector)}, nil
}

// doCookies manages browser cookies. With no args, gets all cookies.
// With name+value+url, sets a cookie. With name+url (no value), deletes.
func (b *Browser) doCookies(ctx context.Context, profile, session, value, name, rawURL string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	if name != "" && value != "" && rawURL != "" {
		if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookie(name, value).WithURL(rawURL).Do(ctx)
		})); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("set cookie failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Set cookie '%s'='%s' for %s", name, value, rawURL)}, nil
	}

	if name != "" && value == "" && rawURL != "" {
		if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return network.DeleteCookies(name).WithURL(rawURL).Do(ctx)
		})); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("delete cookie failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Deleted cookie '%s' for %s", name, rawURL)}, nil
	}

	var cookies []*network.Cookie
	if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().Do(ctx)
		return err
	})); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("get cookies failed: %v", err)}, nil
	}
	if len(cookies) == 0 {
		return Result{Content: "No cookies set."}, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Cookies (%d):\n", len(cookies)))
	for _, c := range cookies {
		sb.WriteString(fmt.Sprintf("  %s = %s (domain: %s, path: %s, secure: %v)\n", c.Name, c.Value, c.Domain, c.Path, c.Secure))
	}
	return Result{Content: sb.String()}, nil
}

// doResize changes the browser viewport size for responsive testing.
func (b *Browser) doResize(ctx context.Context, profile, session string, width, height int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1.0, false).Do(ctx)
	})); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("resize failed: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Viewport resized to %dx%d", width, height)}, nil
}

// doWaitNot waits for an element to disappear (not present in DOM).
func (b *Browser) doWaitNot(ctx context.Context, profile, session, selector string, timeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if err := chromedp.Run(timeoutCtx, chromedp.WaitNotPresent(selector, chromedp.ByQuery)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("element still present after %ds: %v", timeout, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Element disappeared: %s", selector)}, nil
}

// doDrag simulates drag-and-drop from source to target via HTML5 drag events.
func (b *Browser) doDrag(ctx context.Context, profile, session, sourceSel, targetSel string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()

	js := fmt.Sprintf(`(() => {
		const src = document.querySelector(%q);
		const tgt = document.querySelector(%q);
		if (!src) return { ok: false, error: 'source not found' };
		if (!tgt) return { ok: false, error: 'target not found' };
		const sr = src.getBoundingClientRect(), tr = tgt.getBoundingClientRect();
			const dt = new DataTransfer();
			const fire = (el, type, x, y) => {
				el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, dataTransfer: dt }));
			};
		fire(src, 'dragstart', sr.left+sr.width/2, sr.top+sr.height/2);
		fire(src, 'drag', sr.left+sr.width/2, sr.top+sr.height/2);
		fire(tgt, 'dragenter', tr.left+tr.width/2, tr.top+tr.height/2);
		fire(tgt, 'dragover', tr.left+tr.width/2, tr.top+tr.height/2);
		fire(tgt, 'drop', tr.left+tr.width/2, tr.top+tr.height/2);
		fire(src, 'dragend', tr.left+tr.width/2, tr.top+tr.height/2);
		return { ok: true };
	})()`, sourceSel, targetSel)

	var dragResult map[string]interface{}
	if err := chromedp.Run(timeoutCtx,
		chromedp.WaitVisible(sourceSel, chromedp.ByQuery),
		chromedp.Evaluate(js, &dragResult),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("drag failed: %v", err)}, nil
	}

	if ok, _ := dragResult["ok"].(bool); !ok {
		errMsg, _ := dragResult["error"].(string)
		return Result{IsError: true, Content: fmt.Sprintf("drag failed: %s", errMsg)}, nil
	}
	return Result{Content: fmt.Sprintf("Dragged %s to %s", sourceSel, targetSel)}, nil
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
