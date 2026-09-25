package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
)

// ggcode doctor runs read-only health checks over the local installation and
// reports problems the agent cannot see by itself - most importantly config
// keys that are silently ignored because they do not match the schema
// (ggcode loads YAML non-strict, so `modle:` instead of `model:` would
// otherwise vanish without a trace).

type doctorCheck struct {
	Name   string   `json:"name"`
	Status string   `json:"status"` // "ok", "warn", "fail"
	Detail string   `json:"detail,omitempty"`
	Items  []string `json:"items,omitempty"`
}

func (c doctorCheck) failed() bool { return c.Status == "fail" }
func (c doctorCheck) warned() bool { return c.Status == "warn" }

// appendUnknownItem adds a human-readable line for one unknown-key finding.
func (c *doctorCheck) appendUnknownItem(f config.UnknownKeyFinding) {
	item := fmt.Sprintf("unknown key %q (line %d)", f.Path, f.Line)
	if f.Hint != "" {
		item += fmt.Sprintf(" - did you mean %q?", f.Hint)
	}
	c.Items = append(c.Items, item)
}

const (
	doctorStatusOK   = "ok"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
)

func newDoctorCmd(cfgFile *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run read-only health checks on config and environment",
		Long: `Run read-only health checks and print a report.

Checks: config file (including unknown keys that are silently ignored),
vendor/endpoint resolution, API key presence, model selection, MCP server
entries, and git repository state. Exits 1 if any check fails.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks, fails := runDoctorChecks(*cfgFile)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(checks); err != nil {
					return fmt.Errorf("encode doctor report: %w", err)
				}
			} else {
				printDoctorReport(checks)
			}
			if fails > 0 {
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true
				return fmt.Errorf("%d check(s) failed", fails)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output the report as JSON")
	return cmd
}

// runDoctorChecks executes all checks. It never panics on partial state: a
// failure to load the config short-circuits the config-dependent checks and
// is itself reported.
func runDoctorChecks(cfgFile string) ([]doctorCheck, int) {
	var checks []doctorCheck

	cfg, cfgCheck := doctorCheckConfig(cfgFile)
	checks = append(checks, cfgCheck)
	if cfg == nil {
		// Config-dependent checks cannot run; report them as warnings so the
		// report shape stays stable without inventing a config.
		reason := "config unavailable"
		if cfgCheck.Detail != "" {
			reason = "config unavailable: " + cfgCheck.Detail
		}
		for _, name := range []string{"vendor/endpoint", "api key", "model", "mcp servers"} {
			checks = append(checks, doctorCheck{Name: name, Status: doctorStatusWarn, Detail: reason})
		}
		checks = append(checks, doctorCheckGit())
		return checks, countDoctorFails(checks)
	}

	resolved, epCheck := doctorCheckEndpoint(cfg)
	checks = append(checks, epCheck)
	if resolved != nil {
		checks = append(checks, doctorCheckAPIKey(resolved), doctorCheckModel(resolved))
	}

	checks = append(checks, doctorCheckMCP(cfg), doctorCheckGit())
	return checks, countDoctorFails(checks)
}

// doctorCheckConfig loads the config and reports unknown keys as warnings.
func doctorCheckConfig(cfgFile string) (*config.Config, doctorCheck) {
	check := doctorCheck{Name: "config"}
	if err := config.LoadKeysEnv(); err != nil {
		check.Status = doctorStatusWarn
		check.Detail = fmt.Sprintf("loading keys.env: %v", err)
	}
	path := cfgFile
	if path == "" {
		path = config.ConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			check.Status = doctorStatusOK
			check.Detail = "no config file found (defaults in effect)"
			return nil, check
		}
		check.Status = doctorStatusFail
		check.Detail = fmt.Sprintf("reading %s: %v", path, err)
		return nil, check
	}

	findings := config.FindUnknownKeys(data)
	cfg, err := config.LoadWithInstance(cfgFile, "")
	if err != nil {
		check.Status = doctorStatusFail
		check.Detail = fmt.Sprintf("loading config: %v", err)
		// Still surface unknown keys: a load failure does not make a typo
		// elsewhere irrelevant.
		for _, f := range findings {
			check.appendUnknownItem(f)
		}
		return nil, check
	}
	check.Status = doctorStatusOK
	check.Detail = path
	for _, f := range findings {
		if check.Status != doctorStatusFail {
			check.Status = doctorStatusWarn
		}
		check.appendUnknownItem(f)
	}
	return cfg, check
}

// doctorCheckEndpoint verifies the active vendor/endpoint resolves.
func doctorCheckEndpoint(cfg *config.Config) (*config.ResolvedEndpoint, doctorCheck) {
	check := doctorCheck{Name: "vendor/endpoint"}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		check.Status = doctorStatusFail
		check.Detail = err.Error()
		return nil, check
	}
	check.Status = doctorStatusOK
	check.Detail = fmt.Sprintf("%s / %s (%s)", resolved.VendorID, resolved.EndpointID, resolved.Protocol)
	return resolved, check
}

func doctorCheckAPIKey(resolved *config.ResolvedEndpoint) doctorCheck {
	check := doctorCheck{Name: "api key"}
	if resolved.APIKey != "" {
		check.Status = doctorStatusOK
		check.Detail = "resolved for active endpoint"
		return check
	}
	check.Status = doctorStatusWarn
	check.Detail = fmt.Sprintf("no API key resolved for %s/%s - set api_key or the endpoint env var if it requires auth", resolved.VendorID, resolved.EndpointID)
	return check
}

func doctorCheckModel(resolved *config.ResolvedEndpoint) doctorCheck {
	check := doctorCheck{Name: "model"}
	if resolved.Model == "" {
		check.Status = doctorStatusFail
		check.Detail = "no model selected - set model: in the config or run /model"
		return check
	}
	check.Status = doctorStatusOK
	check.Detail = resolved.Model
	return check
}

func doctorCheckMCP(cfg *config.Config) doctorCheck {
	check := doctorCheck{Name: "mcp servers"}
	servers := cfg.MCPServers
	if len(servers) == 0 {
		check.Status = doctorStatusOK
		check.Detail = "none configured"
		return check
	}
	seen := map[string]bool{}
	for _, s := range servers {
		switch {
		case s.Name == "":
			check.Items = append(check.Items, "an MCP server entry has an empty name")
		case seen[s.Name]:
			check.Items = append(check.Items, fmt.Sprintf("duplicate MCP server name %q", s.Name))
		}
		seen[s.Name] = true
		if s.Command == "" && s.URL == "" {
			check.Items = append(check.Items, fmt.Sprintf("MCP server %q has neither command nor url", s.Name))
		}
	}
	check.Status = doctorStatusOK
	if len(check.Items) > 0 {
		check.Status = doctorStatusWarn
	}
	check.Detail = fmt.Sprintf("%d configured", len(servers))
	return check
}

func doctorCheckGit() doctorCheck {
	check := doctorCheck{Name: "git"}
	out, err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Output()
	if err != nil {
		if _, lookErr := exec.LookPath("git"); lookErr != nil {
			check.Status = doctorStatusWarn
			check.Detail = "git not found in PATH"
			return check
		}
		check.Status = doctorStatusWarn
		check.Detail = "not inside a git repository (worktree/commit features unavailable)"
		return check
	}
	if string(out) != "true\n" {
		check.Status = doctorStatusWarn
		check.Detail = "not inside a git work tree"
		return check
	}
	check.Status = doctorStatusOK
	check.Detail = "inside a git work tree"
	return check
}

func countDoctorFails(checks []doctorCheck) int {
	n := 0
	for _, c := range checks {
		if c.failed() {
			n++
		}
	}
	return n
}

func printDoctorReport(checks []doctorCheck) {
	fmt.Println("ggcode doctor")
	mark := map[string]string{
		doctorStatusOK:   "✓",
		doctorStatusWarn: "⚠",
		doctorStatusFail: "✗",
	}
	warns, fails := 0, 0
	for _, c := range checks {
		line := fmt.Sprintf("  %s %-16s %s", mark[c.Status], c.Name, c.Detail)
		fmt.Println(line)
		for _, item := range c.Items {
			fmt.Printf("      - %s\n", item)
		}
		switch {
		case c.failed():
			fails++
		case c.warned():
			warns++
		}
	}
	fmt.Printf("\n%d check(s): %d failed, %d warning(s)\n", len(checks), fails, warns)
}
