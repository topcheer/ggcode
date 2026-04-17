package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
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

// loadIMConfig loads the config file, falling back to default resolution.
func loadIMConfig(cfgFilePath string) (*config.Config, error) {
	path := cfgFilePath
	if path == "" {
		path = config.ConfigPath()
	}
	return config.Load(path)
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
			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "IM Status")
			fmt.Fprintln(out, strings.Repeat("─", 40))

			// Workspace
			wd, _ := os.Getwd()
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
			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

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

			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

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

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an IM adapter configuration",
		Long: `Add an IM adapter to the ggcode configuration.

Examples:
  ggcode im config add my-qq --platform qq --extra app_id=cli_xxx --extra app_secret=sss --extra token=xxx
  ggcode im config add my-feishu --platform feishu --extra app_id=cli_xxx --extra app_secret=sss
  ggcode im config add my-tg --platform telegram --extra token=123456:ABC
  ggcode im config add my-ding --platform dingtalk --extra client_id=xxx --extra client_secret=sss
  ggcode im config add my-discord --platform discord --extra token=Bot xxx
  ggcode im config add my-slack --platform slack --extra bot_token=xoxb-xxx --extra app_token=xapp-xxx`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if platform == "" {
				return fmt.Errorf("--platform is required (qq, telegram, feishu, dingtalk, discord, slack, privateclaw)")
			}

			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
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

			fmt.Fprintf(cmd.OutOrStdout(), "Added IM adapter %q (platform: %s) to %s\n", name, platform, cfg.FilePath)
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "adapter platform (qq, telegram, feishu, dingtalk, discord, slack, privateclaw)")
	cmd.Flags().StringVar(&transport, "transport", "", "transport mode (default: auto)")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable the adapter")
	cmd.Flags().StringArrayVar(&extras, "extra", nil, "platform-specific key=value parameter (repeatable)")
	cmd.Flags().StringArrayVar(&allowFrom, "allow-from", nil, "allowed source IDs (repeatable)")
	_ = cmd.MarkFlagRequired("platform")
	return cmd
}

func newIMConfigRemoveCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an IM adapter configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if err := cfg.RemoveIMAdapter(name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed IM adapter %q from %s\n", name, cfg.FilePath)
			return nil
		},
	}
}

func newIMConfigShowCmd(cfgFile *string) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show adapter configuration details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
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

			cfg, err := loadIMConfig(*cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
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
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
			case "platform":
				adapter.Platform = value
				cfg.IM.Adapters[name] = adapter
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
			case "transport":
				adapter.Transport = value
				cfg.IM.Adapters[name] = adapter
				if err := cfg.Save(); err != nil {
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
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
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
	return s[:4] + strings.Repeat("*", len(s)-4)
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
