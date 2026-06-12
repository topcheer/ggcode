package im

import (
	"fmt"
	"strings"
)

type ExtendedIMSlashOptions struct {
	Manager        *Manager
	SelfAdapter    string
	Text           string
	HelpExtraLines []string
	OnRestart      func(debug bool) (string, error)
	OnProvider     func(vendor, endpoint string) (string, error)
	OnModel        func(model string) (string, error)
	OnConfig       func() (string, error)
	OnExtra        func(parts []string) (string, bool)
}

func ExecuteExtendedIMSlashCommand(opts ExtendedIMSlashOptions) CommonIMSlashResult {
	if result := ExecuteCommonIMSlashCommand(opts.Manager, opts.SelfAdapter, opts.Text, CommonIMSlashOptions{
		HelpExtraLines: opts.HelpExtraLines,
	}); result.Handled {
		return result
	}

	parts := strings.Fields(strings.TrimSpace(opts.Text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return CommonIMSlashResult{}
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/restart":
		if opts.OnRestart == nil {
			return CommonIMSlashResult{Handled: true, Response: "❌ Restart not available in this mode."}
		}
		debugMode := len(parts) > 1 && strings.ToLower(parts[1]) == "debug"
		resp, err := opts.OnRestart(debugMode)
		if err != nil {
			return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("❌ %v", err)}
		}
		return CommonIMSlashResult{Handled: true, Response: resp}

	case "/provider":
		if opts.OnProvider == nil {
			return CommonIMSlashResult{Handled: true, Response: "❌ Provider switching not available in this mode."}
		}
		vendor := ""
		endpoint := ""
		if len(parts) > 1 {
			vendor = parts[1]
		}
		if len(parts) > 2 {
			endpoint = parts[2]
		}
		resp, err := opts.OnProvider(vendor, endpoint)
		if err != nil {
			return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("❌ %v", err)}
		}
		return CommonIMSlashResult{Handled: true, Response: resp}

	case "/model":
		if opts.OnModel == nil {
			return CommonIMSlashResult{Handled: true, Response: "❌ Model switching not available in this mode."}
		}
		model := ""
		if len(parts) > 1 {
			model = parts[1]
		}
		resp, err := opts.OnModel(model)
		if err != nil {
			return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("❌ %v", err)}
		}
		return CommonIMSlashResult{Handled: true, Response: resp}

	case "/config":
		if opts.OnConfig == nil {
			return CommonIMSlashResult{Handled: true, Response: "❌ Config display not available in this mode."}
		}
		resp, err := opts.OnConfig()
		if err != nil {
			return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("❌ %v", err)}
		}
		return CommonIMSlashResult{Handled: true, Response: resp}
	}

	if opts.OnExtra != nil {
		if resp, handled := opts.OnExtra(parts); handled {
			return CommonIMSlashResult{Handled: true, Response: resp}
		}
	}

	return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("Unknown command: %s. Try /help", cmd)}
}
