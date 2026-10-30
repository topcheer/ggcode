package main

// Linear frontend for dumb/non-interactive terminals (#4604 Track 1).
//
// The interactive frontend is a Bubble Tea program: alternate-screen raw
// mode with high-frequency frame refresh. On terminals that cannot carry
// those escape sequences (TERM=dumb, screen sessions, limited SSH) that is
// exactly the garbled-output / repeated-warning / junk-character failure the
// issue reports, and --plain alone only stripped COLOR while still running
// the full TUI.
//
// This frontend runs NO Bubble Tea at all: a plain line-buffered stdin loop
// (no raw mode - dumb terminals are line-mode by nature) plus linear
// streaming print. It deliberately rides the SAME interactive runtime core
// the TUI uses (same agent, registry, session, config hot-swap), so every
// capability except the visual panels keeps working.
//
// Interactive-shaped tools degrade explicitly:
//   - ask_user is unregistered: there is no questionnaire UI to host it;
//     the agent instead ends its turn and the user answers by typing the
//     next line (multi-turn conversation is the native pause point).
//   - approvals default to Allow (GGCODE_PLAIN_APPROVE=deny switches to
//     Deny for read-only-style runs); there is no approval dialog either.

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// isDumbTerminal reports whether the environment cannot host the full TUI:
// TERM=dumb (or unset TERM on a tty). The check is centralized here so the
// piecemeal probes scattered across the TUI package (e.g. model.go's
// terminal-title writer) can stay point fixes while entry-level routing
// lives at the frontend fork itself.
func isDumbTerminal() bool {
	term := strings.TrimSpace(os.Getenv("TERM"))
	return term == "" || strings.EqualFold(term, "dumb")
}

// plainApproval is the linear frontend's interactive-approval stand-in:
// default Accept, configurable to Deny via GGCODE_PLAIN_APPROVE=deny.
func plainApproval(_ context.Context, toolName, _ string) permission.Decision {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("GGCODE_PLAIN_APPROVE")), "deny") {
		debug.Log("plain", "approval denied (GGCODE_PLAIN_APPROVE=deny): tool=%s", toolName)
		return permission.Deny
	}
	return permission.Allow
}

// RunLinearREPL drives the interactive agent from a plain line-oriented
// loop. It returns a process exit code.
func RunLinearREPL(ag *agent.Agent, registry *tool.Registry, cfg *config.Config, cfgPath string, startupNotices []string) int {
	_ = cfgPath // cfg.Save() persists to the scope the config was loaded from

	// No questionnaire UI: the agent ends its turn and waits for the next
	// typed line instead of calling ask_user (#4604 design decision).
	if registry != nil && registry.Unregister("ask_user") {
		fmt.Println("[plain] ask_user disabled - the agent ends its turn and you answer by typing the next line")
	}
	if ag != nil {
		ag.SetApprovalHandler(plainApproval)
	}

	fmt.Println("ggcode linear frontend (no TUI) - /help for commands, Ctrl-D to exit")
	for _, n := range startupNotices {
		fmt.Println("[notice]", n)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	ctx := context.Background()
	for {
		fmt.Print("ggcode> ")
		if !sc.Scan() {
			fmt.Println()
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "/"):
			if exit := handlePlainCommand(line, ag, cfg); exit {
				return 0
			}
		default:
			runLinearTurn(ctx, ag, line)
		}
	}
}

// handlePlainCommand executes the linear command subset. It returns true
// when the REPL should exit.
func handlePlainCommand(line string, ag *agent.Agent, cfg *config.Config) bool {
	fields := strings.Fields(line)
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "/exit", "/quit", "/q":
		return true
	case "/help", "/?":
		fmt.Println("commands:")
		fmt.Println("  /status                       show active vendor/endpoint/model")
		fmt.Println("  /model <name>                 hot-switch model (persists)")
		fmt.Println("  /provider <vendor> [ep]       switch vendor (optionally endpoint)")
		fmt.Println("  /provider --list [--all]      list endpoints+models (default: keyed+modelled only)")
		fmt.Println("  /provider --add-endpoint ...  add endpoint to active vendor")
		fmt.Println("  /endpoint <id>                switch endpoint on current vendor")
		fmt.Println("  /endpoint --show-models       all models on the active endpoint")
		fmt.Println("  /endpoint --refresh-models    force model refetch, then show")
		fmt.Println("  /endpoint --add-model ...     add a model to the active endpoint")
		fmt.Println("  /endpoint --set-apikey ...    set the API key on the active endpoint")
		fmt.Println("  /exit                         leave the REPL (also Ctrl-D)")
		fmt.Println("anything else is sent to the agent; approvals default to Accept")
		fmt.Println("(GGCODE_PLAIN_APPROVE=deny rejects instead); ask_user is disabled.")
	case "/status":
		if cfg == nil {
			fmt.Println("[plain] config unavailable")
			return false
		}
		if resolved, _, err := agentruntime.ResolveCurrentSelection(cfg); err != nil {
			fmt.Println("[plain] resolve failed:", err)
		} else {
			fmt.Printf("vendor=%s endpoint=%s model=%s\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
	case "/model":
		if len(args) != 1 {
			fmt.Println("usage: /model <name>")
			return false
		}
		switchPlainSelection(ag, cfg, "", "", args[0])
	case "/provider":
		handlePlainProvider(args, ag, cfg)
	case "/endpoint":
		handlePlainEndpoint(args, ag, cfg)
	default:
		fmt.Println("[plain] unknown command:", cmd, "- /help for the list")
	}
	return false
}

// switchPlainSelection applies a vendor/endpoint/model switch through the
// same ActivateCurrentSelection + ApplyProviderToAgent funnel the TUI, IM,
// and daemon switchers use (empty fields inherit the current selection),
// then persists the config so the switch survives restarts.
func switchPlainSelection(ag *agent.Agent, cfg *config.Config, vendor, endpoint, model string) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, vendor, endpoint, model)
	if err != nil {
		fmt.Println("[plain] switch failed:", err)
		return
	}
	if ag != nil {
		agentruntime.ApplyProviderToAgent(ag, prov, resolved)
	}
	if err := cfg.Save(); err != nil {
		fmt.Println("[plain] switch applied but not persisted:", err)
		return
	}
	fmt.Printf("[plain] now on vendor=%s endpoint=%s model=%s\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
}

// plainListMaxModels caps the per-endpoint model listing in /provider --list.
const plainListMaxModels = 10

// newPlainFlagSet builds a silent FlagSet (usage/errors are handled by the
// caller, not dumped to stderr mid-REPL).
func newPlainFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// plainArgsAreFlags reports whether the arg list starts with a "--" option,
// i.e. selects the subcommand mode instead of the positional switch form.
func plainArgsAreFlags(args []string) bool {
	return len(args) > 0 && strings.HasPrefix(args[0], "-")
}

// handlePlainProvider implements /provider: positional switch form plus the
// --list / --add-endpoint subcommands.
func handlePlainProvider(args []string, ag *agent.Agent, cfg *config.Config) {
	if !plainArgsAreFlags(args) {
		if len(args) < 1 || len(args) > 2 {
			fmt.Println("usage: /provider <vendor> [endpoint] | --list [--all] | --add-endpoint ...")
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
			fmt.Println("[plain] bad --list flags:", err)
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
			fmt.Println("[plain] bad --add-endpoint flags:", err)
			return
		}
		plainAddEndpoint(cfg, *name, *proto, *url, *model, *ctxWin, *maxOut, *apikey)
	default:
		fmt.Println("[plain] unknown /provider option:", mode, "- /help for the list")
	}
}

// plainListEndpoints prints every configured endpoint grouped by vendor.
// Default view: endpoints with a usable API key AND a non-empty model list;
// --all includes the rest, annotated. Up to 10 models per endpoint.
func plainListEndpoints(cfg *config.Config, all bool) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
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
				fmt.Printf("vendor %s:\n", vid)
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
					fmt.Printf("    (+%d more - use /endpoint --show-models)\n", extra)
				}
			}
		}
		if shown == 0 && all {
			fmt.Printf("vendor %s: (no endpoints)\n", vid)
		}
	}
}

// plainAddEndpoint appends a new endpoint to the ACTIVE vendor's map and
// persists. Refuses to overwrite an existing endpoint id.
func plainAddEndpoint(cfg *config.Config, name, proto, baseURL, model string, ctxWin, maxOut int, apikey string) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	if name == "" || proto == "" || baseURL == "" {
		fmt.Println("[plain] --endpoint-name/--endpoint-type/--endpoint-url are required")
		return
	}
	switch proto {
	case "openai", "openai-responses", "anthropic", "gemini", "copilot":
	default:
		fmt.Printf("[plain] unsupported endpoint-type %q (want openai|openai-responses|anthropic|gemini|copilot)\n", proto)
		return
	}
	if cfg.Vendor == "" {
		fmt.Println("[plain] no active vendor to add the endpoint to")
		return
	}
	v := cfg.Vendors[cfg.Vendor]
	// Lazy-init the endpoints map for a vendor declared without any.
	if v.Endpoints == nil {
		v.Endpoints = make(map[string]config.EndpointConfig)
	}
	if _, exists := v.Endpoints[name]; exists {
		fmt.Printf("[plain] endpoint %q already exists on vendor %q\n", name, cfg.Vendor)
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
		fmt.Println("[plain] endpoint added but not persisted:", err)
		return
	}
	fmt.Printf("[plain] endpoint %q added to vendor %q%s\n", name, cfg.Vendor, switchHint(model))
}

func switchHint(model string) string {
	if model != "" {
		return " - switch with: /endpoint " + model
	}
	return ""
}

// handlePlainEndpoint implements /endpoint: positional switch form plus the
// --show-models / --refresh-models / --add-model subcommands.
func handlePlainEndpoint(args []string, ag *agent.Agent, cfg *config.Config) {
	if !plainArgsAreFlags(args) {
		if len(args) != 1 {
			fmt.Println("usage: /endpoint <id> | --show-models | --refresh-models | --add-model ...")
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
			fmt.Println("[plain] bad --add-model flags:", err)
			return
		}
		plainAddModel(cfg, *model, *apikey)
	case "--set-apikey":
		fs := newPlainFlagSet("endpoint-set-apikey")
		apikey := fs.String("apikey", "", "new API key for the active endpoint")
		if err := fs.Parse(rest); err != nil {
			fmt.Println("[plain] bad --set-apikey flags:", err)
			return
		}
		plainSetEndpointAPIKey(cfg, *apikey)
	default:
		fmt.Println("[plain] unknown /endpoint option:", mode, "- /help for the list")
	}
}

// plainShowModels lists every model on the ACTIVE endpoint: the configured
// static list merged with live discovery (cache-aware; force refetches).
func plainShowModels(cfg *config.Config, force bool) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	resolved, _, err := agentruntime.ResolveCurrentSelection(cfg)
	if err != nil {
		fmt.Println("[plain] resolve failed:", err)
		return
	}
	var discovered []string
	if force {
		discovered, err = provider.ForceDiscoverModels(context.Background(), resolved)
	} else {
		discovered, err = provider.DiscoverModels(context.Background(), resolved)
	}
	if err != nil {
		fmt.Println("[plain] model discovery unavailable (showing configured list):", err)
	}
	// Merge: static config first (user intent), then discovery-only entries.
	seen := make(map[string]bool)
	var epModels []string
	if v, ok := cfg.Vendors[resolved.VendorID]; ok {
		if ep, ok := v.Endpoints[resolved.EndpointID]; ok {
			epModels = ep.Models
		}
	}
	fmt.Printf("models on %s/%s:\n", resolved.VendorID, resolved.EndpointID)
	for _, m := range epModels {
		if !seen[m] {
			seen[m] = true
			fmt.Println("  -", m)
		}
	}
	for _, m := range discovered {
		if !seen[m] {
			seen[m] = true
			fmt.Println("  -", m, "(discovered)")
		}
	}
	if len(seen) == 0 {
		fmt.Println("  (none - use /endpoint --add-model --model <id>)")
	}
}

// plainSetEndpointAPIKey overwrites the ACTIVE endpoint's API key and
// persists. Vendor-level keys are left untouched (endpoint key wins during
// resolution when present).
func plainSetEndpointAPIKey(cfg *config.Config, apikey string) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	if apikey == "" {
		fmt.Println("[plain] --apikey is required")
		return
	}
	if cfg.Vendor == "" || cfg.Endpoint == "" {
		fmt.Println("[plain] no active endpoint to set the key on")
		return
	}
	v := cfg.Vendors[cfg.Vendor]
	ep, ok := v.Endpoints[cfg.Endpoint]
	if !ok {
		fmt.Printf("[plain] active endpoint %q not found on vendor %q\n", cfg.Endpoint, cfg.Vendor)
		return
	}
	ep.APIKey = apikey
	v.Endpoints[cfg.Endpoint] = ep
	cfg.Vendors[cfg.Vendor] = v
	if err := cfg.Save(); err != nil {
		fmt.Println("[plain] api key set but not persisted:", err)
		return
	}
	fmt.Printf("[plain] api key set on endpoint %q\n", cfg.Endpoint)
}

// plainAddModel appends a model to the ACTIVE endpoint's static list and
// persists (optionally updating the endpoint API key). Unlike
// /provider --add-endpoint this never creates an endpoint - the active one
// is the target by definition.
func plainAddModel(cfg *config.Config, model, apikey string) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	if model == "" {
		fmt.Println("[plain] --model is required")
		return
	}
	if cfg.Vendor == "" || cfg.Endpoint == "" {
		fmt.Println("[plain] no active endpoint to add the model to")
		return
	}
	v := cfg.Vendors[cfg.Vendor]
	ep, ok := v.Endpoints[cfg.Endpoint]
	if !ok {
		fmt.Printf("[plain] active endpoint %q not found on vendor %q\n", cfg.Endpoint, cfg.Vendor)
		return
	}
	for _, m := range ep.Models {
		if m == model {
			fmt.Printf("[plain] model %q already on endpoint %q\n", model, cfg.Endpoint)
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
		fmt.Println("[plain] model added but not persisted:", err)
		return
	}
	fmt.Printf("[plain] model %q added to endpoint %q - switch with: /model %s\n", model, cfg.Endpoint, model)
}

// runLinearTurn streams one agent turn as plain linear output: text deltas
// print as they arrive, tool boundaries get one bracketed line each, and
// errors surface as single lines - no refresh, no cursor movement, no
// ANSI beyond what NO_COLOR already disabled.
func runLinearTurn(ctx context.Context, ag *agent.Agent, line string) {
	if ag == nil {
		fmt.Println("[plain] agent unavailable")
		return
	}
	err := ag.RunStream(ctx, line, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			fmt.Print(event.Text)
		case provider.StreamEventToolCallDone:
			if event.Tool.Name != "" {
				fmt.Printf("\n[tool] %s\n", event.Tool.Name)
			}
		case provider.StreamEventError:
			fmt.Printf("\n[error] %v\n", event.Error)
		case provider.StreamEventDone:
			fmt.Println()
		}
	})
	if err != nil {
		fmt.Printf("\n[error] turn failed: %v\n", err)
	}
}
