package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
	"golang.org/x/term"
)

func isDumbTerminal() bool {
	return termLooksDumb(strings.TrimSpace(os.Getenv("TERM")), runtime.GOOS == "windows")
}

// termLooksDumb decides whether the TERM value signals a dumb terminal.
// Unix shells always set TERM, so an empty value there means a dumb or
// non-standard environment. Windows terminals (cmd, PowerShell, Windows
// Terminal) do not set TERM by default, so an empty TERM there is normal
// rather than a dumb-terminal signal — treating it as dumb forced every
// Windows user into the linear plain frontend (regression from #4964's
// detection). Only an explicit TERM=dumb opts Windows out of the TUI.
func termLooksDumb(term string, isWindows bool) bool {
	if strings.EqualFold(term, "dumb") {
		return true
	}
	return !isWindows && term == ""
}

func plainApproval(_ context.Context, toolName, _ string) permission.Decision {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("GGCODE_PLAIN_APPROVE")), "deny") {
		debug.Log("plain", "approval denied (GGCODE_PLAIN_APPROVE=deny): tool=%s", toolName)
		return permission.Deny
	}
	return permission.Allow
}

func RunLinearREPL(ag *agent.Agent, registry *tool.Registry, cfg *config.Config, cfgPath string, startupNotices []string) int {
	_ = cfgPath
	plainLang = plainLangFromConfig(cfgLang(cfg))

	if registry != nil && registry.Unregister("ask_user") {
		fmt.Println(plainTr(plainLang, "askuser.off"))
	}
	if ag != nil {
		ag.SetApprovalHandler(plainApproval)

		ag.SuppressAutopilotInjection()
	}

	if persistSessionFlag {
		wirePlainPersist(ag, cfg)
	}

	fmt.Println(plainTr(plainLang, "banner"))
	for _, n := range startupNotices {
		fmt.Println(plainTr(plainLang, "notice"), n)
	}

	interactiveTTY := term.IsTerminal(int(os.Stdin.Fd()))
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	ctx := context.Background()
	for {
		var line string
		if interactiveTTY {
			s, err := plainReadLine(os.Stdin, plainTr(plainLang, "prompt"))
			if err != nil {
				fmt.Println()
				return 0
			}
			line = s
		} else {
			fmt.Print(plainTr(plainLang, "prompt"))
			if !sc.Scan() {
				fmt.Println()
				return 0
			}
			line = sc.Text()
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "!") {
			if exit := handlePlainCommand(line, ag, cfg); exit {
				return 0
			}
			continue
		}
		runLinearTurn(ctx, ag, line)
	}
}

func runPlainBang(w io.Writer, cmdLine string) {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		fmt.Fprintln(w, plainTr(plainLang, "bang.empty"))
		return
	}
	c := bangShellCmd(cmdLine)
	c.Stdout = w
	c.Stderr = w
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintln(w, plainTr(plainLang, "bang.exit")+strconv.Itoa(ee.ExitCode()))
			return
		}
		fmt.Fprintln(w, plainTr(plainLang, "bang.failed"), err)
	}
}

func bangShellCmd(cmdLine string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		sh := os.Getenv("COMSPEC")
		if sh == "" {
			sh = "cmd.exe"
		}
		return exec.Command(sh, "/C", cmdLine)
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return exec.Command(sh, "-c", cmdLine)
}

func handlePlainCommand(line string, ag *agent.Agent, cfg *config.Config) bool {

	if strings.HasPrefix(line, "!") {
		runPlainBang(os.Stdout, strings.TrimPrefix(line, "!"))
		return false
	}
	fields := strings.Fields(line)
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "/exit", "/quit", "/q":
		return true
	case "/help", "/?":
		fmt.Println(plainTr(plainLang, "help.title"))
		for _, k := range []string{"help.status", "help.model", "help.provider", "help.plist", "help.padd", "help.endpoint", "help.emodels", "help.erefresh", "help.eadd", "help.ekey", "help.lang", "help.bang", "help.exit"} {
			fmt.Println(plainTr(plainLang, k))
		}
		fmt.Println(plainTr(plainLang, "help.tail"))
		fmt.Println(plainTr(plainLang, "help.tail2"))
	case "/status":
		if cfg == nil {
			fmt.Println(plainTr(plainLang, "cfg.unavailable"))
			return false
		}
		if resolved, _, err := agentruntime.ResolveCurrentSelection(cfg); err != nil {
			fmt.Println(plainTr(plainLang, "resolve.failed"), err)
		} else {
			fmt.Printf("vendor=%s endpoint=%s model=%s\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
	case "/model":
		if len(args) != 1 {
			fmt.Println(plainTr(plainLang, "usage.model"))
			return false
		}
		switchPlainSelection(ag, cfg, "", "", args[0])
	case "/provider":
		handlePlainProvider(args, ag, cfg)
	case "/endpoint":
		handlePlainEndpoint(args, ag, cfg)
	case "/lang":
		if len(args) != 1 {
			fmt.Println(plainTr(plainLang, "lang.usage"))
			return false
		}
		next := tui.NormalizeLanguage(args[0])
		if next != tui.LangEnglish && next != tui.LangZhCN {
			fmt.Println(plainTr(plainLang, "lang.unknown"), args[0])
			return false
		}
		plainLang = next
		if cfg != nil {
			cfg.Language = string(next)
			if err := cfg.Save(); err != nil {
				fmt.Println(plainTr(plainLang, "lang.nopersist"), err)
				return false
			}
		}
		fmt.Printf(plainTr(plainLang, "lang.switched")+"\n", next)
	default:
		fmt.Println(plainTr(plainLang, "cmd.unknown"), cmd)
	}
	return false
}

func switchPlainSelection(ag *agent.Agent, cfg *config.Config, vendor, endpoint, model string) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, vendor, endpoint, model)
	if err != nil {
		fmt.Println(plainTr(plainLang, "switch.failed"), err)
		return
	}
	if ag != nil {
		agentruntime.ApplyProviderToAgent(ag, prov, resolved)
	}
	if err := cfg.Save(); err != nil {
		fmt.Println(plainTr(plainLang, "switch.nopersist"), err)
		return
	}
	fmt.Printf(plainTr(plainLang, "switch.ok")+"\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
}

const plainListMaxModels = 10

func newPlainFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func plainArgsAreFlags(args []string) bool {
	return len(args) > 0 && strings.HasPrefix(args[0], "-")
}

func handlePlainProvider(args []string, ag *agent.Agent, cfg *config.Config) {
	if !plainArgsAreFlags(args) {
		if len(args) < 1 || len(args) > 2 {
			fmt.Println(plainTr(plainLang, "usage.provider"))
			return
		}
		ep := ""
		if len(args) == 2 {
			ep = args[1]
		}
		switchPlainSelection(ag, cfg, args[0], ep, "")
		return
	}
	mode := args[0]
	rest := args[1:]
	switch mode {
	case "--list":
		fs := newPlainFlagSet("provider-list")
		all := fs.Bool("all", false, "include endpoints without api key or models")
		if err := fs.Parse(rest); err != nil {
			fmt.Println(plainTr(plainLang, "flags.bad"), "--list:", err)
			return
		}
		plainListEndpoints(cfg, *all)
	case "--add-endpoint":
		fs := newPlainFlagSet("provider-add-endpoint")
		name := fs.String("endpoint-name", "", "endpoint id (unique within the vendor)")
		proto := fs.String("endpoint-type", "", "protocol: openai|openai-responses|anthropic|gemini|copilot")
		url := fs.String("endpoint-url", "", "base URL")
		model := fs.String("endpoint-model", "", "initial/default model")
		ctxWin := fs.Int("context-window", 0, "context window in tokens")
		maxOut := fs.Int("max-output", 0, "max output tokens")
		apikey := fs.String("endpoint-apikey", "", "API key for the endpoint")
		if err := fs.Parse(rest); err != nil {
			fmt.Println(plainTr(plainLang, "flags.bad"), "--add-endpoint:", err)
			return
		}
		plainAddEndpoint(cfg, *name, *proto, *url, *model, *ctxWin, *maxOut, *apikey)
	default:
		fmt.Println(plainTr(plainLang, "opt.unknown"), mode)
	}
}

func plainListEndpoints(cfg *config.Config, all bool) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	vendors := make([]string, 0, len(cfg.Vendors))
	for id := range cfg.Vendors {
		vendors = append(vendors, id)
	}
	sort.Strings(vendors)
	for _, vid := range vendors {
		v := cfg.Vendors[vid]
		eps := make([]string, 0, len(v.Endpoints))
		for id := range v.Endpoints {
			eps = append(eps, id)
		}
		sort.Strings(eps)
		shown := 0
		for _, eid := range eps {
			ep := v.Endpoints[eid]
			keyed := config.ExpandEnv(v.APIKey) != "" || config.ExpandEnv(ep.APIKey) != ""
			modelled := len(ep.Models) > 0
			if !all && !(keyed && modelled) {
				continue
			}
			if shown == 0 {
				fmt.Printf(plainTr(plainLang, "list.vendor")+"\n", vid)
			}
			shown++
			mark := "  "
			if vid == cfg.Vendor && eid == cfg.Endpoint {
				mark = "* "
			}
			note := ""
			if !keyed {
				note = "  (no api key)"
			} else if !modelled {
				note = "  (no models configured)"
			}
			fmt.Printf("%s%s%s\n", mark, eid, note)
			if modelled {
				shownModels := ep.Models
				extra := 0
				if len(shownModels) > plainListMaxModels {
					extra = len(shownModels) - plainListMaxModels
					shownModels = shownModels[:plainListMaxModels]
				}
				for i, m := range shownModels {
					prefix := "    - "
					if m == ep.SelectedModel || (ep.SelectedModel == "" && m == ep.DefaultModel) {
						prefix = "    * "
					}
					_ = i
					fmt.Printf("%s%s\n", prefix, m)
				}
				if extra > 0 {
					fmt.Printf(plainTr(plainLang, "list.more")+"\n", extra)
				}
			}
		}
		if shown == 0 && all {
			fmt.Printf(plainTr(plainLang, "list.noendpoints")+"\n", vid)
		}
	}
}

func plainAddEndpoint(cfg *config.Config, name, proto, baseURL, model string, ctxWin, maxOut int, apikey string) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	if name == "" || proto == "" || baseURL == "" {
		fmt.Println(plainTr(plainLang, "add.req"))
		return
	}
	switch proto {
	case "openai", "openai-responses", "anthropic", "gemini", "copilot":
	default:
		fmt.Printf(plainTr(plainLang, "add.proto")+"\n", proto)
		return
	}
	if cfg.Vendor == "" {
		fmt.Println(plainTr(plainLang, "add.novendor"))
		return
	}
	v := cfg.Vendors[cfg.Vendor]

	if v.Endpoints == nil {
		v.Endpoints = make(map[string]config.EndpointConfig)
	}
	if _, exists := v.Endpoints[name]; exists {
		fmt.Printf(plainTr(plainLang, "add.duplicate")+"\n", name, cfg.Vendor)
		return
	}
	ep := config.EndpointConfig{
		Protocol:      proto,
		BaseURL:       baseURL,
		APIKey:        apikey,
		ContextWindow: ctxWin,
		MaxTokens:     maxOut,
		DefaultModel:  model,
	}
	if model != "" {
		ep.Models = []string{model}
		ep.SelectedModel = model
	}
	v.Endpoints[name] = ep
	cfg.Vendors[cfg.Vendor] = v
	if err := cfg.Save(); err != nil {
		fmt.Println(plainTr(plainLang, "add.savefail"), err)
		return
	}
	fmt.Printf(plainTr(plainLang, "add.ok")+"\n", name, cfg.Vendor, switchHint(name))
}

func switchHint(name string) string {
	if name != "" {
		return plainTr(plainLang, "add.switchhint") + name
	}
	return ""
}

func handlePlainEndpoint(args []string, ag *agent.Agent, cfg *config.Config) {
	if !plainArgsAreFlags(args) {
		if len(args) != 1 {
			fmt.Println(plainTr(plainLang, "usage.endpoint"))
			return
		}
		switchPlainSelection(ag, cfg, "", args[0], "")
		return
	}
	mode := args[0]
	rest := args[1:]
	switch mode {
	case "--show-models":
		plainShowModels(cfg, false)
	case "--refresh-models":
		plainShowModels(cfg, true)
	case "--add-model":
		fs := newPlainFlagSet("endpoint-add-model")
		model := fs.String("model", "", "model id to add")
		apikey := fs.String("apikey", "", "optional: update the endpoint API key too")
		if err := fs.Parse(rest); err != nil {
			fmt.Println(plainTr(plainLang, "flags.bad"), "--add-model:", err)
			return
		}
		plainAddModel(cfg, *model, *apikey)
	case "--set-apikey":
		fs := newPlainFlagSet("endpoint-set-apikey")
		apikey := fs.String("apikey", "", "new API key for the active endpoint")
		if err := fs.Parse(rest); err != nil {
			fmt.Println(plainTr(plainLang, "flags.bad"), "--set-apikey:", err)
			return
		}
		plainSetEndpointAPIKey(cfg, *apikey)
	default:
		fmt.Println(plainTr(plainLang, "opt.unknown.ep"), mode)
	}
}

func plainShowModels(cfg *config.Config, force bool) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	resolved, _, err := agentruntime.ResolveCurrentSelection(cfg)
	if err != nil {
		fmt.Println(plainTr(plainLang, "resolve.failed"), err)
		return
	}
	var discovered []string
	if force {
		discovered, err = provider.ForceDiscoverModels(context.Background(), resolved)
	} else {
		discovered, err = provider.DiscoverModels(context.Background(), resolved)
	}
	if err != nil {
		fmt.Println(plainTr(plainLang, "models.nodiscovery"), err)
	}

	seen := make(map[string]bool)
	var epModels []string
	if v, ok := cfg.Vendors[resolved.VendorID]; ok {
		if ep, ok := v.Endpoints[resolved.EndpointID]; ok {
			epModels = ep.Models
		}
	}
	fmt.Printf(plainTr(plainLang, "models.title")+"\n", resolved.VendorID, resolved.EndpointID)
	for _, m := range epModels {
		if !seen[m] {
			seen[m] = true
			fmt.Println("  -", m)
		}
	}
	for _, m := range discovered {
		if !seen[m] {
			seen[m] = true
			fmt.Println("  -", m, plainTr(plainLang, "models.discovered"))
		}
	}
	if len(seen) == 0 {
		fmt.Println(plainTr(plainLang, "models.none"))
	}
}

func plainSetEndpointAPIKey(cfg *config.Config, apikey string) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	if apikey == "" {
		fmt.Println(plainTr(plainLang, "setkey.req"))
		return
	}
	if cfg.Vendor == "" || cfg.Endpoint == "" {
		fmt.Println(plainTr(plainLang, "setkey.noactive"))
		return
	}
	v := cfg.Vendors[cfg.Vendor]
	ep, ok := v.Endpoints[cfg.Endpoint]
	if !ok {
		fmt.Printf(plainTr(plainLang, "setkey.nofound")+"\n", cfg.Endpoint, cfg.Vendor)
		return
	}
	ep.APIKey = apikey
	v.Endpoints[cfg.Endpoint] = ep
	cfg.Vendors[cfg.Vendor] = v
	if err := cfg.Save(); err != nil {
		fmt.Println(plainTr(plainLang, "setkey.savefail"), err)
		return
	}
	fmt.Printf(plainTr(plainLang, "setkey.ok")+"\n", cfg.Endpoint)
}

func plainAddModel(cfg *config.Config, model, apikey string) {
	if cfg == nil {
		fmt.Println(plainTr(plainLang, "cfg.unavailable"))
		return
	}
	if model == "" {
		fmt.Println(plainTr(plainLang, "addmodel.req"))
		return
	}
	if cfg.Vendor == "" || cfg.Endpoint == "" {
		fmt.Println(plainTr(plainLang, "addmodel.noactive"))
		return
	}
	v := cfg.Vendors[cfg.Vendor]
	ep, ok := v.Endpoints[cfg.Endpoint]
	if !ok {
		fmt.Printf(plainTr(plainLang, "addmodel.nofound")+"\n", cfg.Endpoint, cfg.Vendor)
		return
	}
	for _, m := range ep.Models {
		if m == model {
			fmt.Printf(plainTr(plainLang, "addmodel.duplicate")+"\n", model, cfg.Endpoint)
			return
		}
	}
	ep.Models = append(ep.Models, model)
	if ep.DefaultModel == "" {
		ep.DefaultModel = model
	}
	if apikey != "" {
		ep.APIKey = apikey
	}
	v.Endpoints[cfg.Endpoint] = ep
	cfg.Vendors[cfg.Vendor] = v
	if err := cfg.Save(); err != nil {
		fmt.Println(plainTr(plainLang, "addmodel.savefail"), err)
		return
	}
	fmt.Printf(plainTr(plainLang, "addmodel.ok")+"\n", model, cfg.Endpoint, model)
}

func wirePlainPersist(ag *agent.Agent, cfg *config.Config) (*session.JSONLStore, *session.Session) {
	if ag == nil {
		return nil, nil
	}
	dir, err := session.DefaultDir()
	if err != nil {
		fmt.Printf("[plain] persist unavailable: %v\n", err)
		return nil, nil
	}
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		fmt.Printf("[plain] persist unavailable: %v\n", err)
		return nil, nil
	}
	v, ep, m := "unknown", "unknown", "unknown"
	if resolved, _, rerr := agentruntime.ResolveCurrentSelection(cfg); rerr == nil {
		v, ep, m = resolved.VendorID, resolved.EndpointID, resolved.Model
	}
	ses := session.NewSession(v, ep, m)
	ses.Title = "plain session"
	ag.SetPersistHandler(func(msg provider.Message) {
		if err := store.AppendMessageToDisk(ses, msg); err != nil {
			debug.Log("plain", "persist append failed: %v", err)
		}
	})
	fmt.Printf("[plain] persisting session %s\n", ses.ID)
	return store, ses
}

func runLinearTurn(ctx context.Context, ag *agent.Agent, line string) {
	if ag == nil {
		fmt.Println(plainTr(plainLang, "agent.unavailable"))
		return
	}

	pendingToolCalls := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pendingToolCalls)
	err := ag.RunStream(ctx, line, printer, "human")
	closeTurn()
	if err != nil {
		fmt.Printf("\n[error] turn failed: %v\n", err)
	}
}

func plainStreamPrinter(pendingToolCalls map[string]bool) (func(provider.StreamEvent), func()) {

	thinkOpen := false
	closeThink := func() {
		if thinkOpen {
			fmt.Println()
			thinkOpen = false
		}
	}
	return func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				closeThink()
				fmt.Print(event.Text)
			case provider.StreamEventReasoning:

				if event.Text == "" || event.Text == "__redacted_thinking__" {
					return
				}
				if !thinkOpen {
					fmt.Print("\n[think] ")
					thinkOpen = true
				}
				fmt.Print(event.Text)
			case provider.StreamEventToolCallDone:
				if event.Tool.Name != "" {
					closeThink()

					fmt.Printf("\n[tool] %s", event.Tool.Name)
					pendingToolCalls[event.Tool.ID] = true
				}
			case provider.StreamEventToolResult:
				if !pendingToolCalls[event.Tool.ID] {
					return
				}
				if event.IsError {
					fmt.Printf(" (error)\n")
				} else {
					fmt.Printf(" (ok)\n")
				}
				delete(pendingToolCalls, event.Tool.ID)
			case provider.StreamEventError:
				closeThink()
				fmt.Printf("\n[error] %v\n", event.Error)
			}
		}, func() {

			closeThink()
			if len(pendingToolCalls) > 0 {
				fmt.Println()
			}
			clear(pendingToolCalls)
			fmt.Println()
		}
}
