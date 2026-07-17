package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func newIMCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "im",
		Short: "Manage IM adapters, bindings, and pairing",
		Long: `Manage IM adapters, channel bindings, and pairing for ggcode.

Subcommands:
  status           Show overview of adapters and bindings (default)
  list             List configured IM adapters
  bindings         List channel bindings
  bind             Bind a channel to an adapter (skip pairing code)
  unbind           Unbind a channel
  pair             Start interactive pairing with an adapter
  share            Generate a PrivateClaw share link
  config add       Add an IM adapter configuration
  config remove    Remove an IM adapter configuration
  config show      Show adapter configuration details
  config set       Modify a single adapter setting`,
	}

	cmd.AddCommand(newIMStatusCmd(cfgFile))
	cmd.AddCommand(newIMListCmd(cfgFile))
	cmd.AddCommand(newIMBindingsCmd(cfgFile))
	cmd.AddCommand(newIMBindCmd(cfgFile))
	cmd.AddCommand(newIMUnbindCmd(cfgFile))
	cmd.AddCommand(newIMPairCmd(cfgFile))
	cmd.AddCommand(newIMShareCmd(cfgFile))
	cmd.AddCommand(newIMConfigCmd(cfgFile))

	// Default run: "ggcode im" → status
	cmd.RunE = newIMStatusCmd(cfgFile).RunE

	return cmd
}

type imCommandContext struct {
	cfg         *config.Config
	globalCfg   *config.Config
	instanceCfg *config.Config
	workingDir  string
}

// loadIMConfig loads the active config using the same path resolution and
// instance overlay semantics as the TUI and daemon.
func loadIMConfig(cfgFilePath string) (*imCommandContext, error) {
	path := cfgFilePath
	if path == "" {
		resolved, err := resolveConfigFilePath()
		if err != nil {
			return nil, err
		}
		path = resolved
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving working directory: %w", err)
	}
	cfg, err := config.LoadWithInstance(path, workingDir)
	if err != nil {
		return nil, err
	}
	globalCfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &imCommandContext{
		cfg:         cfg,
		globalCfg:   globalCfg,
		instanceCfg: config.LoadInstanceConfig(workingDir),
		workingDir:  normalizeWorkspacePath(workingDir),
	}, nil
}

func (c *imCommandContext) adapterInGlobal(name string) bool {
	if c == nil || c.globalCfg == nil || c.globalCfg.IM.Adapters == nil {
		return false
	}
	_, ok := c.globalCfg.IM.Adapters[name]
	return ok
}

func (c *imCommandContext) adapterInInstance(name string) bool {
	if c == nil || c.instanceCfg == nil || c.instanceCfg.IM.Adapters == nil {
		return false
	}
	_, ok := c.instanceCfg.IM.Adapters[name]
	return ok
}

func (c *imCommandContext) configPathForScope(scope string) string {
	if strings.EqualFold(strings.TrimSpace(scope), "instance") {
		if path := config.InstanceConfigPath(c.workingDir); path != "" {
			return path
		}
	}
	return c.cfg.FilePath
}

func addIMConfigScopeFlag(cmd *cobra.Command, scope *string) {
	cmd.Flags().StringVar(scope, "scope", "auto", "config target: auto, global, or instance")
}

func resolveIMConfigScope(ctx *imCommandContext, requested, adapterName string, creating bool) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(requested))
	if scope == "" {
		scope = "auto"
	}
	switch scope {
	case "auto":
	case "global":
		return "global", nil
	case "instance":
		if err := ctx.cfg.SetSaveScope("instance"); err != nil {
			return "", err
		}
		return "instance", nil
	default:
		return "", fmt.Errorf("unknown scope %q (expected auto, global, or instance)", requested)
	}

	if adapterName != "" {
		inGlobal := ctx.adapterInGlobal(adapterName)
		inInstance := ctx.adapterInInstance(adapterName)
		switch {
		case inInstance && !inGlobal:
			return "instance", nil
		case inGlobal:
			return "global", nil
		}
	}
	if creating && ctx.cfg.HasInstanceConfigFile() {
		return "instance", nil
	}
	return "global", nil
}

func prepareIMConfigWrite(ctx *imCommandContext, requestedScope, adapterName string, creating bool) (string, error) {
	scope, err := resolveIMConfigScope(ctx, requestedScope, adapterName, creating)
	if err != nil {
		return "", err
	}
	if err := ctx.cfg.SetSaveScope(scope); err != nil {
		return "", err
	}
	return scope, nil
}

// resolveBindingsPath returns the default IM bindings file path.
// Overridden in tests.
var resolveBindingsPath = defaultBindingsPath

func defaultBindingsPath() (string, error) {
	return im.DefaultBindingsPath()
}

// ============================================================
// im status
// ============================================================

func newIMStatusCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show IM overview (adapters + bindings)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "IM Status")
			fmt.Fprintln(out, strings.Repeat("─", 40))

			// Workspace
			wd := ctx.workingDir
			fmt.Fprintf(out, "  Workspace: %s\n", wd)

			// Adapters
			adapters := sortedAdapters(cfg)
			if len(adapters) == 0 {
				fmt.Fprintln(out, "\n  No adapters configured.")
			} else {
				fmt.Fprintln(out, "\n  Adapters:")
				for _, entry := range adapters {
					enabled := "disabled"
					if entry.adapter.Enabled {
						enabled = "enabled"
					}
					fmt.Fprintf(out, "    %-20s %-12s %s\n", entry.name, entry.adapter.Platform, enabled)
				}
			}

			// Bindings
			fmt.Fprintln(out)
			if err := printBindings(out, wd, false); err != nil {
				fmt.Fprintf(out, "  Bindings: (none or unreadable: %v)\n", err)
			}

			return nil
		},
	}
}

// ============================================================
// im list
// ============================================================

func newIMListCmd(cfgFile *string) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List configured IM adapters",
		Aliases: []string{"adapters"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg

			adapters := sortedAdapters(cfg)
			if len(adapters) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No IM adapters configured.")
				return nil
			}

			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), adaptersToSlice(cfg))
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tPLATFORM\tENABLED\tTRANSPORT")
			for _, entry := range adapters {
				name, a := entry.name, entry.adapter
				enabled := "false"
				if a.Enabled {
					enabled = "true"
				}
				transport := a.Transport
				if transport == "" {
					transport = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, a.Platform, enabled, transport)
			}
			tw.Flush()
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

// ============================================================
// im bindings
// ============================================================

func newIMBindingsCmd(cfgFile *string) *cobra.Command {
	var jsonOutput bool
	var workspace string

	cmd := &cobra.Command{
		Use:   "bindings",
		Short: "List channel bindings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspace == "" {
				workspace, _ = os.Getwd()
			}
			return printBindings(cmd.OutOrStdout(), workspace, jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().StringVar(&workspace, "workspace", "", "filter by workspace path (default: current directory)")
	return cmd
}

// ============================================================
// im bind
// ============================================================

func newIMBindCmd(cfgFile *string) *cobra.Command {
	var channel, thread, target, workspace string

	cmd := &cobra.Command{
		Use:   "bind <adapter>",
		Short: "Bind a channel to an adapter (skip pairing code)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			adapterName := strings.TrimSpace(args[0])
			if channel == "" {
				return fmt.Errorf("--channel is required")
			}

			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg

			// Validate adapter exists
			if cfg.IM.Adapters == nil {
				return fmt.Errorf("no IM adapters configured")
			}
			adapterCfg, ok := cfg.IM.Adapters[adapterName]
			if !ok {
				return fmt.Errorf("adapter %q not found in config", adapterName)
			}

			if workspace == "" {
				workspace, _ = os.Getwd()
			}
			workspace = normalizeWorkspacePath(workspace)

			targetID := target
			if targetID == "" {
				targetID = channel
			}

			binding := im.ChannelBinding{
				Workspace: workspace,
				Platform:  im.Platform(strings.TrimSpace(adapterCfg.Platform)),
				Adapter:   adapterName,
				TargetID:  targetID,
				ChannelID: channel,
				ThreadID:  thread,
				BoundAt:   time.Now(),
			}

			// Write binding directly
			bindingsPath, err := resolveBindingsPath()
			if err != nil {
				return fmt.Errorf("resolve bindings path: %w", err)
			}
			store, err := im.NewJSONFileBindingStore(bindingsPath)
			if err != nil {
				return fmt.Errorf("open bindings store: %w", err)
			}
			if err := store.Save(binding); err != nil {
				return fmt.Errorf("save binding: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Bound adapter %q → channel %s (workspace: %s)\n", adapterName, channel, workspace)
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "channel ID (required)")
	cmd.Flags().StringVar(&thread, "thread", "", "thread/topic ID (optional)")
	cmd.Flags().StringVar(&target, "target", "", "target ID (default: same as channel)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace path (default: current directory)")
	_ = cmd.MarkFlagRequired("channel")
	return cmd
}

// ============================================================
// im unbind
// ============================================================

func newIMUnbindCmd(cfgFile *string) *cobra.Command {
	var workspace string
	var all bool

	cmd := &cobra.Command{
		Use:   "unbind [<adapter>]",
		Short: "Unbind a channel",
		RunE: func(cmd *cobra.Command, args []string) error {
			bindingsPath, err := resolveBindingsPath()
			if err != nil {
				return fmt.Errorf("resolve bindings path: %w", err)
			}
			store, err := im.NewJSONFileBindingStore(bindingsPath)
			if err != nil {
				return fmt.Errorf("open bindings store: %w", err)
			}

			if all {
				bindings, err := store.List()
				if err != nil {
					return fmt.Errorf("list bindings: %w", err)
				}
				for _, b := range bindings {
					if err := store.Delete(b.Workspace, b.Adapter); err != nil {
						fmt.Fprintf(cmd.OutOrStdout(), "Warning: failed to delete %s/%s: %v\n", b.Workspace, b.Adapter, err)
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Unbound all %d binding(s)\n", len(bindings))
				return nil
			}

			if workspace != "" {
				workspace = normalizeWorkspacePath(workspace)
				bindings, err := store.ListByWorkspace(workspace)
				if err != nil {
					return fmt.Errorf("list bindings: %w", err)
				}
				if len(bindings) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "No bindings for workspace %s\n", workspace)
					return nil
				}
				for _, b := range bindings {
					if err := store.Delete(b.Workspace, b.Adapter); err != nil {
						return fmt.Errorf("delete binding %s/%s: %w", b.Workspace, b.Adapter, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Unbound adapter %q from workspace %s\n", b.Adapter, b.Workspace)
				}
				return nil
			}

			// Unbind by adapter name
			if len(args) == 0 {
				return fmt.Errorf("specify an adapter name, or use --workspace or --all")
			}
			adapterName := strings.TrimSpace(args[0])
			bindings, err := store.ListByAdapter(adapterName)
			if err != nil {
				return fmt.Errorf("list bindings: %w", err)
			}
			if len(bindings) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No binding found for adapter %q\n", adapterName)
				return nil
			}
			for _, b := range bindings {
				if err := store.Delete(b.Workspace, b.Adapter); err != nil {
					return fmt.Errorf("delete binding: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Unbound adapter %q from workspace %s\n", b.Adapter, b.Workspace)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "unbind all bindings for a workspace")
	cmd.Flags().BoolVar(&all, "all", false, "unbind all bindings")
	return cmd
}

// ============================================================
// im pair
// ============================================================

func newIMPairCmd(cfgFile *string) *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "pair <adapter>",
		Short: "Start interactive pairing with an adapter",
		Long: `Start an interactive pairing session with the specified adapter.

This command starts the adapter, waits for an inbound message from the
IM channel, displays a 4-digit pairing code, and waits for the user
to enter it on the IM side to complete the binding.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// pair requires running the full adapter stack, which depends on
			// Manager + Bridge + Agent loop. In the CLI-only context this is a
			// complex operation that needs the daemon mode (planned).
			// For now, provide guidance.
			fmt.Fprintln(cmd.OutOrStdout(), "Interactive pairing requires the full TUI or daemon runtime.")
			fmt.Fprintln(cmd.OutOrStdout(), "Use the TUI: open ggcode, then type /qq, /feishu, /tg, etc.")
			fmt.Fprintln(cmd.OutOrStdout(), "Or use 'ggcode im bind' to bind directly without pairing code.")
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "pairing wait timeout")
	return cmd
}

// ============================================================
// im share
// ============================================================

func newIMShareCmd(cfgFile *string) *cobra.Command {
	var adapter string

	cmd := &cobra.Command{
		Use:   "share",
		Short: "Generate a PrivateClaw share link",
		RunE: func(cmd *cobra.Command, args []string) error {
			// share requires a running Manager with PC adapter connected.
			// Defer to daemon mode.
			fmt.Fprintln(cmd.OutOrStdout(), "Share link generation requires a running PrivateClaw adapter.")
			fmt.Fprintln(cmd.OutOrStdout(), "This will be available once the daemon mode is implemented.")
			return nil
		},
	}
	cmd.Flags().StringVar(&adapter, "adapter", "_pc_builtin", "adapter name")
	return cmd
}

// ============================================================
// im config
// ============================================================

func newIMConfigCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage IM adapter configuration",
	}

	cmd.AddCommand(newIMConfigAddCmd(cfgFile))
	cmd.AddCommand(newIMConfigRemoveCmd(cfgFile))
	cmd.AddCommand(newIMConfigShowCmd(cfgFile))
	cmd.AddCommand(newIMConfigSetCmd(cfgFile))

	return cmd
}

func newIMConfigAddCmd(cfgFile *string) *cobra.Command {
	var platform, transport string
	var enabled bool
	var extras []string
	var allowFrom []string
	var scope string

	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add an IM adapter configuration",
		Long: `Add an IM adapter to the ggcode configuration.

Run without arguments to enter the interactive setup wizard.

Examples:
  ggcode im config add my-qq --platform qq --extra app_id=cli_xxx --extra app_secret=sss --extra token=xxx
  ggcode im config add my-feishu --platform feishu --extra app_id=cli_xxx --extra app_secret=sss
  ggcode im config add my-tg --platform telegram --extra token=123456:ABC
  ggcode im config add my-ding --platform dingtalk --extra client_id=xxx --extra client_secret=sss
  ggcode im config add my-discord --platform discord --extra token=Bot xxx
  ggcode im config add my-slack --platform slack --extra bot_token=xoxb-xxx --extra app_token=xapp-xxx`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Wizard mode: no name and no platform → interactive
			if len(args) == 0 && platform == "" {
				wizName, wizPlatform, wizExtras, err := imConfigAddWizard(cmd.OutOrStdout())
				if err != nil {
					return err
				}
				args = []string{wizName}
				platform = wizPlatform
				extras = wizExtras
			}

			name := strings.TrimSpace(args[0])
			if platform == "" {
				return fmt.Errorf("--platform is required (qq, telegram, feishu, dingtalk, discord, slack, privateclaw)")
			}

			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg
			effectiveScope, err := prepareIMConfigWrite(ctx, scope, name, true)
			if err != nil {
				return err
			}

			extraMap := make(map[string]interface{})
			for _, kv := range extras {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid --extra format %q (expected key=value)", kv)
				}
				extraMap[strings.TrimSpace(parts[0])] = parts[1]
			}

			adapter := config.IMAdapterConfig{
				Enabled:   enabled,
				Platform:  platform,
				Transport: transport,
				Extra:     extraMap,
				AllowFrom: allowFrom,
			}

			if err := cfg.AddIMAdapter(name, adapter); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added IM adapter %q (platform: %s) to %s\n", name, platform, ctx.configPathForScope(effectiveScope))
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "adapter platform (qq, telegram, feishu, dingtalk, discord, slack, privateclaw)")
	cmd.Flags().StringVar(&transport, "transport", "", "transport mode (default: auto)")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable the adapter")
	cmd.Flags().StringArrayVar(&extras, "extra", nil, "platform-specific key=value parameter (repeatable)")
	cmd.Flags().StringArrayVar(&allowFrom, "allow-from", nil, "allowed source IDs (repeatable)")
	addIMConfigScopeFlag(cmd, &scope)
	// Note: platform is not marked required because the wizard mode
	// provides it interactively. The RunE handler validates it instead.
	return cmd
}

func newIMConfigRemoveCmd(cfgFile *string) *cobra.Command {
	var scope string

	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an IM adapter configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg
			effectiveScope, err := prepareIMConfigWrite(ctx, scope, name, false)
			if err != nil {
				return err
			}
			if err := cfg.RemoveIMAdapter(name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed IM adapter %q from %s\n", name, ctx.configPathForScope(effectiveScope))
			return nil
		},
	}
	addIMConfigScopeFlag(cmd, &scope)
	return cmd
}

func newIMConfigShowCmd(cfgFile *string) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show adapter configuration details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg
			if cfg.IM.Adapters == nil {
				return fmt.Errorf("adapter %q not found", name)
			}
			adapter, ok := cfg.IM.Adapters[name]
			if !ok {
				return fmt.Errorf("adapter %q not found", name)
			}

			if jsonOutput {
				return printJSON(cmd.OutOrStdout(), adapter)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Adapter: %s\n", name)
			fmt.Fprintf(out, "  Platform:  %s\n", adapter.Platform)
			fmt.Fprintf(out, "  Enabled:   %v\n", adapter.Enabled)
			fmt.Fprintf(out, "  Transport: %s\n", valueOr(emptyStr(adapter.Transport), "-"))
			if len(adapter.Extra) > 0 {
				fmt.Fprintln(out, "  Extra:")
				keys := sortedKeys(adapter.Extra)
				for _, k := range keys {
					v := adapter.Extra[k]
					// Mask common secret fields
					if isSecretKey(k) {
						fmt.Fprintf(out, "    %s: %s\n", k, maskSecret(fmt.Sprintf("%v", v)))
					} else {
						fmt.Fprintf(out, "    %s: %v\n", k, v)
					}
				}
			}
			if len(adapter.AllowFrom) > 0 {
				fmt.Fprintf(out, "  AllowFrom: %s\n", strings.Join(adapter.AllowFrom, ", "))
			}
			if len(adapter.Targets) > 0 {
				fmt.Fprintln(out, "  Targets:")
				for _, t := range adapter.Targets {
					fmt.Fprintf(out, "    - id=%s channel=%s", t.ID, t.Channel)
					if t.Thread != "" {
						fmt.Fprintf(out, " thread=%s", t.Thread)
					}
					if t.Label != "" {
						fmt.Fprintf(out, " label=%s", t.Label)
					}
					fmt.Fprintln(out)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func newIMConfigSetCmd(cfgFile *string) *cobra.Command {
	var scope string

	cmd := &cobra.Command{
		Use:   "set <name> <key> <value>",
		Short: "Modify a single adapter setting",
		Long: `Modify a single adapter setting.

Supported keys:
  enabled             true/false
  platform            qq, telegram, feishu, dingtalk, discord, slack, privateclaw
  transport           transport mode
  extra.<key>         any platform-specific parameter (e.g. extra.token, extra.app_id)

Examples:
  ggcode im config set my-qq enabled false
  ggcode im config set my-qq extra.token new_token_value`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			key := strings.TrimSpace(args[1])
			value := args[2]

			ctx, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			cfg := ctx.cfg
			effectiveScope, err := prepareIMConfigWrite(ctx, scope, name, false)
			if err != nil {
				return err
			}
			if cfg.IM.Adapters == nil {
				return fmt.Errorf("adapter %q not found", name)
			}
			adapter, ok := cfg.IM.Adapters[name]
			if !ok {
				return fmt.Errorf("adapter %q not found", name)
			}

			switch key {
			case "enabled":
				adapter.Enabled = (value == "true" || value == "1")
				cfg.IM.Adapters[name] = adapter
				if err := cfg.SaveScoped(effectiveScope); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
			case "platform":
				adapter.Platform = value
				cfg.IM.Adapters[name] = adapter
				if err := cfg.SaveScoped(effectiveScope); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
			case "transport":
				adapter.Transport = value
				cfg.IM.Adapters[name] = adapter
				if err := cfg.SaveScoped(effectiveScope); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
			default:
				// Check for extra.<key> prefix
				if strings.HasPrefix(key, "extra.") {
					extraKey := strings.TrimPrefix(key, "extra.")
					if err := cfg.SetIMAdapterExtra(name, extraKey, value); err != nil {
						return err
					}
				} else {
					return fmt.Errorf("unknown key %q (supported: enabled, platform, transport, extra.<key>)", key)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Set %s.%s = %s\n", name, key, value)
			return nil
		},
	}
	addIMConfigScopeFlag(cmd, &scope)
	return cmd
}

// ============================================================
// helpers
// ============================================================

type adapterEntry struct {
	name    string
	adapter config.IMAdapterConfig
}

func sortedAdapters(cfg *config.Config) []adapterEntry {
	if cfg.IM.Adapters == nil {
		return nil
	}
	entries := make([]adapterEntry, 0, len(cfg.IM.Adapters))
	for name, a := range cfg.IM.Adapters {
		entries = append(entries, adapterEntry{name: name, adapter: a})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries
}

func adaptersToSlice(cfg *config.Config) []map[string]interface{} {
	entries := sortedAdapters(cfg)
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		m := map[string]interface{}{
			"name":      e.name,
			"platform":  e.adapter.Platform,
			"enabled":   e.adapter.Enabled,
			"transport": e.adapter.Transport,
		}
		if len(e.adapter.Extra) > 0 {
			m["extra"] = e.adapter.Extra
		}
		if len(e.adapter.AllowFrom) > 0 {
			m["allow_from"] = e.adapter.AllowFrom
		}
		out = append(out, m)
	}
	return out
}

func printBindings(out io.Writer, workspace string, asJSON bool) error {
	bindingsPath, err := resolveBindingsPath()
	if err != nil {
		return err
	}
	store, err := im.NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return err
	}

	var bindings []im.ChannelBinding
	if workspace != "" {
		bindings, err = store.ListByWorkspace(normalizeWorkspacePath(workspace))
	} else {
		bindings, err = store.List()
	}
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		fmt.Fprintln(out, "  No bindings.")
		return nil
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(bindings)
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ADAPTER\tPLATFORM\tWORKSPACE\tCHANNEL_ID\tBOUND")
	for _, b := range bindings {
		ws := b.Workspace
		if home, err := os.UserHomeDir(); err == nil {
			if strings.HasPrefix(ws, home) {
				ws = "~" + ws[len(home):]
			}
		}
		bound := "-"
		if !b.BoundAt.IsZero() {
			bound = b.BoundAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.Adapter, b.Platform, ws, b.ChannelID, bound)
	}
	tw.Flush()
	return nil
}

func printJSON(out io.Writer, v interface{}) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func normalizeWorkspacePath(p string) string {
	return session.NormalizeWorkspacePath(p)
}

func valueOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func emptyStr(s string) string { return s }

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "key")
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return string([]rune(s)[:4]) + strings.Repeat("*", len([]rune(s))-4)
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// imConfigAddWizard provides an interactive prompt to add an IM adapter.
// Returns (name, platform, extras).
func imConfigAddWizard(out io.Writer) (string, string, []string, error) {
	reader := bufio.NewReader(os.Stdin)

	prompt := func(label, def string) string {
		suffix := ""
		if def != "" {
			suffix = fmt.Sprintf(" [%s]", def)
		}
		fmt.Fprintf(out, "%s%s: ", label, suffix)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	fmt.Fprint(out, "\n=== IM Adapter Setup Wizard ===\n\n")

	// Step 1: Name
	name := prompt("Adapter name (e.g. my-qq, my-bot)", "")
	if name == "" {
		return "", "", nil, fmt.Errorf("adapter name is required")
	}

	// Step 2: Platform
	fmt.Fprintln(out, "\nSupported platforms:")
	fmt.Fprintln(out, "  1) qq         — QQ (Tencent)")
	fmt.Fprintln(out, "  2) telegram   — Telegram Bot")
	fmt.Fprintln(out, "  3) feishu     — Feishu / Lark")
	fmt.Fprintln(out, "  4) dingtalk   — DingTalk")
	fmt.Fprintln(out, "  5) discord    — Discord")
	fmt.Fprintln(out, "  6) slack      — Slack")
	fmt.Fprintln(out, "  7) privateclaw — Private Claw")
	platformChoice := prompt("Choose platform [1]", "1")

	platformMap := map[string]string{
		"1": "qq", "2": "telegram", "3": "feishu",
		"4": "dingtalk", "5": "discord", "6": "slack", "7": "privateclaw",
	}
	plt := platformMap[platformChoice]
	if plt == "" {
		plt = platformChoice // allow raw input
	}

	// Step 3: Platform-specific extras
	fmt.Fprintf(out, "\nConfiguring %s adapter.\n", plt)

	var requiredExtras []string
	switch plt {
	case "qq":
		requiredExtras = []string{"app_id", "app_secret", "token"}
	case "telegram":
		requiredExtras = []string{"token"}
	case "feishu":
		requiredExtras = []string{"app_id", "app_secret"}
	case "dingtalk":
		requiredExtras = []string{"client_id", "client_secret"}
	case "discord":
		requiredExtras = []string{"token"}
	case "slack":
		requiredExtras = []string{"bot_token", "app_token"}
	default:
		requiredExtras = nil
	}

	var extras []string
	for _, key := range requiredExtras {
		val := prompt(fmt.Sprintf("  %s", key), "")
		if val == "" {
			return "", "", nil, fmt.Errorf("%s is required for %s", key, plt)
		}
		extras = append(extras, key+"="+val)
	}

	// Step 4: Optional extras
	fmt.Fprintln(out, "\nOptional extras (KEY=VALUE). Press Enter on empty line to skip.")
	for {
		extra := prompt(fmt.Sprintf("Extra #%d", len(extras)+1), "")
		if extra == "" {
			break
		}
		extras = append(extras, extra)
	}

	// Review
	fmt.Fprintf(out, "\nReview:\n  name: %s\n  platform: %s\n  extras: %d items\n", name, plt, len(extras))
	confirm := prompt("\nConfirm add? [Y/n]", "Y")
	if confirm == "n" || confirm == "N" {
		return "", "", nil, fmt.Errorf("cancelled")
	}

	return name, plt, extras, nil
}
