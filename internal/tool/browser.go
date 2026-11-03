package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/topcheer/ggcode/internal/safego"
)

type browserProfile struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	tabs        map[string]*browserTab
	lastUsed    time.Time
}

type browserTab struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type Browser struct {
	profiles map[string]*browserProfile
	mu       sync.Mutex
	gcOnce   sync.Once
	gcStop   chan struct{}
	gcClose  sync.Once
	refs     *browserRefRegistry
}

func NewBrowser() *Browser {
	return &Browser{
		profiles: make(map[string]*browserProfile),
		gcStop:   make(chan struct{}),
		refs:     newBrowserRefRegistry(),
	}
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Close() error {
	b.mu.Lock()

	if b.gcStop != nil {
		b.gcClose.Do(func() { close(b.gcStop) })
	}
	defer b.mu.Unlock()

	for name, p := range b.profiles {

		for _, tab := range p.tabs {
			tab.cancel()
		}
		p.tabs = nil

		p.allocCancel()
		delete(b.profiles, name)
	}
	return nil
}

func (b *Browser) Description() string {
	return "Go-native browser automation via Chrome DevTools Protocol. Full SPA/JS support without Node.js or Playwright. Supports multiple Chrome profiles and tabs. Requires Chrome/Chromium. Chrome-for-Testing is not auto-downloaded - if you use CfT, expose its binary as your system Chrome (e.g. symlink) and the probe picks it up. For simple non-JS page fetching, use web_fetch."
}

func (b *Browser) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"description": "Browser action to perform.",
			"enum": ["navigate", "click", "type", "extract", "screenshot", "evaluate", "wait", "links", "scroll", "back", "content", "close", "status", "select", "hover", "press", "upload", "cookies", "resize", "wait_not", "drag", "snapshot", "open_tab"]
		},
		"url": {
			"type": "string",
			"description": "URL to navigate to (for 'navigate' action). Only http/https URLs are allowed; file:// and other schemes are rejected (use read_file for local files)."
		},
		"profile": {
			"type": "string",
			"description": "Chrome profile name. Each profile runs a separate Chrome instance with its own cookies, extensions, and settings. Use 'default' for a clean ephemeral profile, or specify a name like 'work', 'personal' to persist data between sessions. Defaults to 'default' (temporary, cleared on exit). 'system' is a persistent dedicated profile under ~/.ggcode/browser-profiles/system - it does NOT reuse your logged-in Chrome session (Chrome locks the default user data dir while running, and Chrome 136+ disables CDP debugging on it), so reuse of an already-open browser is not supported; log in once inside the 'system' profile and it persists across sessions."
		},
		"session": {
			"type": "string",
			"description": "Tab/session ID within a profile. Each session is a separate browser tab. Defaults to 'default'. Use different IDs for parallel browsing within the same profile."
		},
		"selector": {
			"type": "string",
			"description": "CSS selector for click, type, extract, wait, scroll actions, OR an @eN element ref from the 'snapshot' action (e.g. '@e3'). Refs stay valid until the tab navigates; on failure re-run 'snapshot'. Examples: 'button.submit', '#login-form', 'a[href*=\"github\"]', 'input[name=email]', '@e3'."
		},
		"output_path": {
			"type": "string",
			"description": "screenshot action only: also save the PNG to this local file path (returned as absolute) so downstream tools that need a real file (e.g. sending it via IM) can reference it"
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

type browserActionArgs struct {
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
	OutputPath  string `json:"output_path"`
}

var browserActions = map[string]func(*Browser, context.Context, *browserActionArgs) (Result, error){
	"navigate": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.URL == "" {
			return errResult("url is required for navigate action")
		}
		return b.doNavigate(ctx, a.Profile, a.Session, a.URL, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"click": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector is required for click action")
		}
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doClick(ctx, a.Profile, a.Session, sel, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"type": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector is required for type action")
		}
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doType(ctx, a.Profile, a.Session, sel, a.Text, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"extract": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doExtract(ctx, a.Profile, a.Session, sel, a.Headless)
	},
	"screenshot": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doScreenshot(ctx, a.Profile, a.Session, a.Selector, a.Headless, a.OutputPath)
	},
	"evaluate": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Expression == "" {
			return errResult("expression is required for evaluate action")
		}
		return b.doEvaluate(ctx, a.Profile, a.Session, a.Expression, a.Frame, a.Headless)
	},
	"wait": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.WaitFor == "" {
			return errResult("wait_for selector is required for wait action")
		}
		return b.doWait(ctx, a.Profile, a.Session, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"links": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doLinks(ctx, a.Profile, a.Session, a.Headless)
	},
	"select": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector is required for select action")
		}
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doSelect(ctx, a.Profile, a.Session, sel, a.Value, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"hover": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector is required for hover action")
		}
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doHover(ctx, a.Profile, a.Session, sel, a.Headless)
	},
	"press": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Key == "" {
			return errResult("key is required for press action")
		}
		return b.doPress(ctx, a.Profile, a.Session, a.Key, a.Headless)
	},
	"scroll": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doScroll(ctx, a.Profile, a.Session, sel, a.Headless)
	},
	"back": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doBack(ctx, a.Profile, a.Session, a.Headless)
	},
	"content": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doContent(ctx, a.Profile, a.Session, a.Headless)
	},
	"close": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doCloseSession(a.Profile, a.Session)
	},
	"status": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doStatus()
	},
	"upload": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector is required for upload action (the input[type=file] element)")
		}
		if a.Path == "" {
			return errResult("path is required for upload action")
		}

		sel, ok := b.resolveSelectorArg(a.Profile, a.Session, a.Selector)
		if !ok {
			return errResult(fmt.Sprintf("unresolved element ref %q: run the 'snapshot' action first (refs die on navigation)", a.Selector))
		}
		return b.doUpload(ctx, a.Profile, a.Session, sel, a.Path, a.Headless)
	},
	"cookies": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		return b.doCookies(ctx, a.Profile, a.Session, a.Value, a.Name, a.URL, a.Headless)
	},
	"resize": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Width == 0 || a.Height == 0 {
			return errResult("width and height are required for resize action")
		}
		return b.doResize(ctx, a.Profile, a.Session, a.Width, a.Height, a.Headless)
	},
	"wait_not": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.WaitFor == "" {
			return errResult("wait_for selector is required for wait_not action")
		}
		return b.doWaitNot(ctx, a.Profile, a.Session, a.WaitFor, a.WaitTimeout, a.Headless)
	},
	"drag": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.Selector == "" {
			return errResult("selector (source element) is required for drag action")
		}
		if a.Value == "" {
			return errResult("value (target CSS selector) is required for drag action")
		}
		return b.doDrag(ctx, a.Profile, a.Session, a.Selector, a.Value, a.Headless)
	},

	"snapshot": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		tab, err := b.getSession(a.Profile, a.Session, a.Headless)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
		defer cancel()
		entries, err := b.collectBrowserRefs(timeoutCtx, a.Profile, a.Session)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("snapshot failed: %v", err)}, nil
		}
		return Result{Content: renderRefTree(entries) + "\n\nUse a ref (e.g. '@e3') in the selector parameter of click/type/extract/select/hover/upload/scroll. Refs stay valid until this tab navigates."}, nil
	},

	"open_tab": func(b *Browser, ctx context.Context, a *browserActionArgs) (Result, error) {
		if a.URL == "" {
			return errResult("url is required for open_tab action")
		}
		return b.doNavigate(ctx, a.Profile, a.Session, a.URL, a.WaitFor, a.WaitTimeout, a.Headless)
	},
}

func errResult(msg string) (Result, error) {
	return Result{IsError: true, Content: msg}, nil
}

func (b *Browser) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args browserActionArgs
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

	handler, ok := browserActions[args.Action]
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s", args.Action)}, nil
	}
	return handler(b, ctx, &args)
}

const minChromeMajorVersion = 90

func chromeNotFoundHelp() string {
	switch runtime.GOOS {
	case "darwin":
		return "Chrome or Chromium not found. Install from https://www.google.com/chrome/ or run: brew install --cask google-chrome"
	case "windows":
		return "No Chrome, Chromium, or Edge found. Install Chrome (https://www.google.com/chrome/ or: winget install Google.Chrome) - or use the preinstalled Edge (winget install Microsoft.Edge to restore it). Edge works too: the tool falls back to it automatically."
	default:
		return "Chrome or Chromium not found. Install via your package manager, e.g.: sudo apt install google-chrome-stable OR sudo snap install chromium"
	}
}

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

			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			filepath.Join(os.Getenv("USERPROFILE"), `AppData\Local\Microsoft\Edge\Application\msedge.exe`),
		}
	default:

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

		if filepath.IsAbs(p) {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

func getChromeVersion(chromePath string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chromePath, "--version")

	cmd.Cancel = func() error {
		return cmd.Process.Kill()
	}
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

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

func (b *Browser) getProfile(name string, headless *bool) (*browserProfile, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if p, ok := b.profiles[name]; ok {
		p.touch()
		return p, nil
	}

	if name != "system" && name != "default" {
		if err := validateBrowserProfileName(name); err != nil {
			return nil, err
		}
	}

	b.startBrowserProfileGC()

	chromePath := findChromeExecutable()
	if chromePath == "" {
		return nil, fmt.Errorf("%s\n\nThe browser tool requires Chrome or Chromium to be installed on this system.", chromeNotFoundHelp())
	}

	chromeVersion := getChromeVersion(chromePath)
	if chromeVersion > 0 && chromeVersion < minChromeMajorVersion {

		fmt.Fprintf(os.Stderr, "[browser] WARNING: Chrome %d detected, recommended version is %d+\n", chromeVersion, minChromeMajorVersion)
	}

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

	if name != "default" {
		dataDir := filepath.Join(homeDir(), ".ggcode", "browser-profiles", name)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create profile directory: %w", err)
		}
		opts = append(opts, chromedp.UserDataDir(dataDir))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	p := &browserProfile{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		tabs:        make(map[string]*browserTab),
	}
	p.touch()
	b.profiles[name] = p

	b.evictLRUBrowserProfiles()
	return p, nil
}

func (b *Browser) getSession(profileName, sessionID string, headless *bool) (*browserTab, error) {
	p, err := b.getProfile(profileName, headless)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()

	if cur, ok := b.profiles[profileName]; !ok || cur != p {
		b.mu.Unlock()
		p, err = b.getProfile(profileName, headless)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		if cur2, ok2 := b.profiles[profileName]; !ok2 || cur2 != p {
			b.mu.Unlock()
			return nil, fmt.Errorf("browser profile %q was evicted while creating the session; retry the operation", profileName)
		}
	}
	defer b.mu.Unlock()

	if tab, ok := p.tabs[sessionID]; ok {
		if tab.ctx.Err() == nil {
			return tab, nil
		}
		delete(p.tabs, sessionID)
	}

	taskCtx, cancel := chromedp.NewContext(p.allocCtx)

	startDone := make(chan error, 1)
	safego.Go("browser.tabStart", func() { startDone <- chromedp.Do(taskCtx) })
	select {
	case runErr := <-startDone:
		if runErr != nil {
			cancel()
			return nil, fmt.Errorf("failed to create browser tab (startup handshake failed): %w", runErr)
		}
	case <-time.After(60 * time.Second):
		cancel()
		return nil, fmt.Errorf("failed to create browser tab: startup handshake timed out after 60s (Chrome/Edge boot is too slow or blocked by AV/EDR on this machine)")
	}

	safego.Go("browser.dialogHandler", func() {
		for _, evErr := range chromedp.Events(taskCtx, page.JavascriptDialogOpening) {
			if evErr != nil {
				return
			}
			_, _ = cdp.Call(taskCtx, chromedp.FromContext(taskCtx).Target, page.HandleJavaScriptDialog,
				page.HandleJavaScriptDialogParams{Accept: false})
		}
	})

	tab := &browserTab{ctx: taskCtx, cancel: cancel}
	p.tabs[sessionID] = tab
	return tab, nil
}

func isAllowedNavScheme(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	}
	return false
}

func (b *Browser) doNavigate(ctx context.Context, profile, session, rawURL, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	if !isAllowedNavScheme(rawURL) {
		return Result{IsError: true, Content: fmt.Sprintf("navigation refused: only http/https URLs are allowed (got %q). file:// and other schemes bypass the workspace sandbox (#741). Use the read_file tool for local file access, subject to permission checks.", rawURL)}, nil
	}
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	actions := []chromedp.Action[chromedp.Void]{chromedp.Navigate(rawURL)}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(chromedp.CSS(waitFor)))
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+15)*time.Second)
	defer cancel()

	safego.Go("browser.cancelPropagate", func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-timeoutCtx.Done():
		}
	})

	if err := chromedp.Do(timeoutCtx, actions...); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("navigation failed: %v", err)}, nil
	}

	title, _ := chromedp.Run(timeoutCtx, chromedp.Title())
	finalURL, _ := chromedp.Run(timeoutCtx, chromedp.Location())

	b.refs.invalidate(browserRefKey(profile, session))

	return Result{Content: fmt.Sprintf("Navigated to: %s\nTitle: %s\nURL: %s\nProfile: %s\nSession: %s\n\nUse action 'content' for page text, 'extract' with selector, or 'screenshot' for visual capture.", rawURL, title, finalURL, profile, session)}, nil
}

func (b *Browser) doClick(ctx context.Context, profile, session, selector, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	if res := b.runClickPrecheck(timeoutCtx, selector); res != nil && !res.OK {
		return Result{IsError: true, Content: formatClickBlocked(selector, res)}, nil
	}

	urlBefore, _ := chromedp.Run(timeoutCtx, chromedp.Location())
	stateBefore := b.evaluateClickState(timeoutCtx, selector)

	actions := []chromedp.Action[chromedp.Void]{
		chromedp.WaitVisible(chromedp.CSS(selector)),
		chromedp.Click(chromedp.CSS(selector)),
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(chromedp.CSS(waitFor)))
	}

	if err := chromedp.Do(timeoutCtx, actions...); err != nil {

		return b.browserActionFailure("click", tab.ctx, selector, err), nil
	}

	urlAfter, _ := chromedp.Run(timeoutCtx, chromedp.Location())
	stateAfter := b.evaluateClickState(timeoutCtx, selector)

	if urlAfter != urlBefore {
		b.refs.invalidate(browserRefKey(profile, session))
	}
	note := clickEffectNote(urlBefore, urlAfter, stateBefore, stateAfter)
	return Result{Content: fmt.Sprintf("Clicked: %s\nCurrent URL: %s%s", selector, urlAfter, note)}, nil
}

const stateProbeFailed = "\x00probe-failed"

func (b *Browser) evaluateClickState(timeoutCtx context.Context, selector string) string {

	expr := fmt.Sprintf(`(function(){var e=document.querySelector(%q);if(!e)return "";`+
		`return e.checked||e.selected||e.getAttribute("aria-expanded")||e.getAttribute("aria-checked")||"";})()`, selector)
	raw, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[any](expr))
	if err != nil {
		return stateProbeFailed
	}
	if v := stateFromRaw(raw); v != "" {
		return v
	}

	return ""
}

func stateFromRaw(raw any) string {
	switch v := raw.(type) {
	case bool:
		if v {
			return "true"
		}
		return ""
	case string:
		return v
	default:
		return ""
	}
}

func clickEffectNote(urlBefore, urlAfter, stateBefore, stateAfter string) string {

	if urlBefore != "" && urlAfter != urlBefore && urlAfter != "" {
		return fmt.Sprintf("\neffect: confirmed (navigation to %s)", urlAfter)
	}

	if stateAfter != "" && stateAfter != stateProbeFailed && stateBefore != stateProbeFailed && stateAfter != stateBefore {
		return fmt.Sprintf("\neffect: confirmed (state: %v)", stateAfter)
	}
	return "\n⚠ no observable effect: URL unchanged and selector state unchanged — " +
		"follow up with action 'extract'/'screenshot' to confirm the click landed before building on it"
}

func (b *Browser) doType(ctx context.Context, profile, session, selector, text, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	actions := []chromedp.Action[chromedp.Void]{
		chromedp.WaitVisible(chromedp.CSS(selector)),
		chromedp.Clear(chromedp.CSS(selector)),
	}
	if text != "" {
		actions = append(actions, chromedp.SendKeys(chromedp.CSS(selector), text))
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(chromedp.CSS(waitFor)))
	}

	if err := chromedp.Do(timeoutCtx, actions...); err != nil {
		return b.browserActionFailure("type", tab.ctx, selector, err), nil
	}

	return Result{Content: fmt.Sprintf("Typed into %s: %q", selector, text)}, nil
}

func (b *Browser) doExtract(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

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

		if err := chromedp.Do(timeoutCtx, chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("wait failed: %v", err)}, nil
		}
		var evalErr error
		results, evalErr = chromedp.Run(timeoutCtx, chromedp.Evaluate[[]map[string]string](js))
		if evalErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("extract failed: %v", evalErr)}, nil
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

	if err := chromedp.Do(timeoutCtx, chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("extract failed: %v", err)}, nil
	}
	pageText, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[string](`document.body.innerText.substring(0, 50000)`))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("extract failed: %v", err)}, nil
	}
	return Result{Content: pageText}, nil
}

func (b *Browser) doScreenshot(ctx context.Context, profile, session, selector string, headless *bool, outputPath string) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

	_, _ = chromedp.Run(timeoutCtx, chromedp.Evaluate[string](`(() => {
		const s = document.createElement('style');
		s.id = 'ggcode-hide-scrollbars';
		s.textContent = '::-webkit-scrollbar{display:none}';
		document.head.appendChild(s);
		return '1';
	})()`))
	defer func() {
		_, _ = chromedp.Run(timeoutCtx, chromedp.Evaluate[string](`(() => {
			const s = document.getElementById('ggcode-hide-scrollbars');
			if (s) s.remove();
			return '1';
		})()`))
	}()

	var buf []byte
	if selector != "" {
		if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(selector))); err != nil {
			return b.browserActionFailure("screenshot", tab.ctx, selector, err), nil
		}
		buf, err = chromedp.Run(timeoutCtx, chromedp.Screenshot(chromedp.CSS(selector)))
		if err != nil {
			return b.browserActionFailure("screenshot", tab.ctx, selector, err), nil
		}
	} else {
		buf, err = chromedp.Run(timeoutCtx, chromedp.FullScreenshot(90))
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v", err)}, nil
		}
	}

	content := fmt.Sprintf("Screenshot captured (%d bytes, PNG). The image is included as an image block for visual analysis.", len(buf))
	if outputPath != "" {

		abs, err := filepath.Abs(outputPath)
		if err != nil {
			abs = outputPath
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("screenshot save failed (mkdir): %v", err)}, nil
		}
		if err := os.WriteFile(abs, buf, 0o644); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("screenshot save failed: %v", err)}, nil
		}
		content += fmt.Sprintf(" Saved to: %s", abs)
	}
	return Result{
		Content: content,
		Images:  []ResultImage{{MIME: "image/png", Base64: base64.StdEncoding.EncodeToString(buf)}},
	}, nil
}

func (b *Browser) doEvaluate(ctx context.Context, profile, session, expression, frame string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

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

	result, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[any](actualExpr))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("JavaScript evaluation failed: %v", err)}, nil
	}

	resultStr := formatJSResult(result)
	if len([]rune(resultStr)) > 50000 {
		resultStr = string([]rune(resultStr)[:50000]) + "\n... [truncated]"
	}
	return Result{Content: fmt.Sprintf("Result:\n%s", resultStr)}, nil
}

func (b *Browser) doWait(ctx context.Context, profile, session, selector string, timeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(selector))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("wait timed out after %ds: %v", timeout, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Element appeared: %s", selector)}, nil
}

func (b *Browser) doLinks(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

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

	if err := chromedp.Do(timeoutCtx, chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("links extraction failed: %v", err)}, nil
	}
	var linksErr error
	links, linksErr = chromedp.Run(timeoutCtx, chromedp.Evaluate[[]linkData](js))
	if linksErr != nil {
		return Result{IsError: true, Content: fmt.Sprintf("links extraction failed: %v", linksErr)}, nil
	}

	var sb strings.Builder
	locCtx, locCancel := context.WithTimeout(tab.ctx, 5*time.Second)
	currentURL, _ := chromedp.Run(locCtx, chromedp.Location())
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

func (b *Browser) doScroll(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()

	if selector != "" {
		if err := chromedp.Do(timeoutCtx, chromedp.ScrollIntoView(chromedp.CSS(selector))); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("scroll failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Scrolled %s into view.", selector)}, nil
	}

	if _, err := chromedp.Run(timeoutCtx,
		chromedp.Evaluate[any](`window.scrollTo(0, document.body.scrollHeight)`),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("scroll failed: %v", err)}, nil
	}
	return Result{Content: "Scrolled to bottom of page."}, nil
}

func (b *Browser) doBack(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

	if err := chromedp.Do(timeoutCtx, chromedp.NavigateBack()); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("back navigation failed: %v", err)}, nil
	}

	urlAfter, _ := chromedp.Run(timeoutCtx, chromedp.Location())

	b.refs.invalidate(browserRefKey(profile, session))
	return Result{Content: fmt.Sprintf("Navigated back. Current URL: %s", urlAfter)}, nil
}

func (b *Browser) doContent(ctx context.Context, profile, session string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 30*time.Second)
	defer cancel()

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

	if err := chromedp.Do(timeoutCtx, chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("content extraction failed: %v", err)}, nil
	}
	var contentErr error
	content, contentErr = chromedp.Run(timeoutCtx, chromedp.Evaluate[pageContent](js))
	if contentErr != nil {
		return Result{IsError: true, Content: fmt.Sprintf("content extraction failed: %v", contentErr)}, nil
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
				url, _ = chromedp.Run(statusCtx, chromedp.Location())
				title, _ = chromedp.Run(statusCtx, chromedp.Title())
				statusCancel()
			}
			status := "active"
			if tab.ctx.Err() != nil {
				status = "closed"
			}
			if url != "" {
				sb.WriteString(fmt.Sprintf("    └─ Session: %s [%s] %s - %s\n", sessID, status, title, url))
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

func (b *Browser) doSelect(ctx context.Context, profile, session, selector, value, waitFor string, waitTimeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(waitTimeout+5)*time.Second)
	defer cancel()

	if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(selector))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("select failed: %v", err)}, nil
	}

	js := fmt.Sprintf(`(() => {
		const sel = document.querySelector(%q);
		if (!sel) return { ok: false, error: 'element not found' };
			if (!sel.options) return { ok: false, error: 'element is not a <select> - cannot set value' };
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

	selResult, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[map[string]interface{}](js))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("select failed: %v", err)}, nil
	}
	if waitFor != "" {
		if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(waitFor))); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("select failed: %v", err)}, nil
		}
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

func (b *Browser) doHover(ctx context.Context, profile, session, selector string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()

	js := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) return false;
		el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
		el.dispatchEvent(new MouseEvent('mouseenter', { bubbles: true }));
		el.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
		return true;
	})()`, selector)

	if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(selector))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("hover failed: %v", err)}, nil
	}
	if _, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[any](js)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("hover failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Hovered over: %s", selector)}, nil
}

func (b *Browser) doPress(ctx context.Context, profile, session, key string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	keyMap := map[string]string{
		"enter":     "\r",
		"tab":       "\t",
		"escape":    "\x1b",
		"backspace": "\x08",
		"space":     " ",
	}

	lowerKey := strings.ToLower(key)

	var cdpKeyName string
	if mapped, ok := keyMap[lowerKey]; ok {
		cdpKeyName = mapped
	} else if len(key) == 1 {
		cdpKeyName = key
	} else {
		switch lowerKey {
		case "arrowup":
			cdpKeyName = kb.ArrowUp
		case "arrowdown":
			cdpKeyName = kb.ArrowDown
		case "arrowleft":
			cdpKeyName = kb.ArrowLeft
		case "arrowright":
			cdpKeyName = kb.ArrowRight
		case "delete":
			cdpKeyName = kb.Delete
		case "home":
			cdpKeyName = kb.Home
		case "end":
			cdpKeyName = kb.End
		case "pageup":
			cdpKeyName = kb.PageUp
		case "pagedown":
			cdpKeyName = kb.PageDown
		default:
			return Result{IsError: true, Content: fmt.Sprintf("unsupported key: %s (supported: Enter, Tab, Escape, Backspace, Delete, Space, ArrowUp/Down/Left/Right, Home, End, PageUp, PageDown, or single characters)", key)}, nil
		}
	}

	if err := chromedp.Do(timeoutCtx, chromedp.KeyEvent(cdpKeyName)); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("key press failed: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Pressed key: %s", key)}, nil
}

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

	if err := chromedp.Do(timeoutCtx,

		chromedp.WaitReady(chromedp.CSS(selector)),
		chromedp.SetUploadFiles(chromedp.CSS(selector), []string{absPath}),
	); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("upload failed: %v", err)}, nil
	}

	countExpr := fmt.Sprintf(`(function(){var e=document.querySelector(%q);return e&&e.files?e.files.length:-1;})()`, selector)
	fileCount, countErr := chromedp.Run(timeoutCtx, chromedp.Evaluate[int](countExpr))
	if countErr == nil && fileCount == 0 {
		return Result{IsError: true, Content: fmt.Sprintf("upload had no effect: %s reports 0 selected files (controlled input cleared it?) — retry via a visible file chooser or verify the selector", selector)}, nil
	}
	countNote := ""
	if fileCount > 0 {
		countNote = fmt.Sprintf("\neffect: confirmed (%d file(s) selected)", fileCount)
	}
	return Result{Content: fmt.Sprintf("Uploaded file: %s into %s%s", absPath, selector, countNote)}, nil
}

func (b *Browser) doCookies(ctx context.Context, profile, session, value, name, rawURL string, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	if name != "" && value != "" && rawURL != "" {
		if err := chromedp.Do(timeoutCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, network.SetCookie, network.SetCookieParams{Name: name, Value: value, URL: rawURL})
			return err
		})); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("set cookie failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Set cookie '%s'='%s' for %s", name, value, rawURL)}, nil
	}

	if name != "" && value == "" && rawURL != "" {
		if err := chromedp.Do(timeoutCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, network.DeleteCookies, network.DeleteCookiesParams{Name: name, URL: rawURL})
			return err
		})); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("delete cookie failed: %v", err)}, nil
		}
		return Result{Content: fmt.Sprintf("Deleted cookie '%s' for %s", name, rawURL)}, nil
	}

	var cookies []*network.Cookie
	if err := chromedp.Do(timeoutCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		res, err := cdp.Call(ctx, t, network.GetCookies, network.GetCookiesParams{})
		cookies = res.Cookies
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

func (b *Browser) doResize(ctx context.Context, profile, session string, width, height int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()

	if err := chromedp.Do(timeoutCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width:             int64(width),
			Height:            int64(height),
			DeviceScaleFactor: 1.0,
			Mobile:            false,
		})
		return err
	})); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("resize failed: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Viewport resized to %dx%d", width, height)}, nil
}

func (b *Browser) doWaitNot(ctx context.Context, profile, session, selector string, timeout int, headless *bool) (Result, error) {
	tab, err := b.getSession(profile, session, headless)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(tab.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if err := chromedp.Do(timeoutCtx, chromedp.WaitNotPresent(chromedp.CSS(selector))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("element still present after %ds: %v", timeout, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Element disappeared: %s", selector)}, nil
}

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

	if err := chromedp.Do(timeoutCtx, chromedp.WaitVisible(chromedp.CSS(sourceSel))); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("drag failed: %v", err)}, nil
	}
	dragResult, err := chromedp.Run(timeoutCtx, chromedp.Evaluate[map[string]interface{}](js))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("drag failed: %v", err)}, nil
	}

	if ok, _ := dragResult["ok"].(bool); !ok {
		errMsg, _ := dragResult["error"].(string)
		return Result{IsError: true, Content: fmt.Sprintf("drag failed: %s", errMsg)}, nil
	}
	return Result{Content: fmt.Sprintf("Dragged %s to %s", sourceSel, targetSel)}, nil
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
