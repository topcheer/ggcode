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

// extendedSlashParts tokenizes raw slash text into whitespace-separated
// fields and the lowercased command token. ok is false when the text is
// empty after trimming or the first token does not start with "/"
// (i.e. not a slash command).
func extendedSlashParts(text string) (parts []string, cmd string, ok bool) {
	parts = strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return nil, "", false
	}
	return parts, strings.ToLower(parts[0]), true
}

// invokeExtendedSlashCallback wraps a handler callback outcome into the
// uniform handled response: "❌ %v" on error, otherwise the raw response.
func invokeExtendedSlashCallback(resp string, err error) CommonIMSlashResult {
	if err != nil {
		return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("❌ %v", err)}
	}
	return CommonIMSlashResult{Handled: true, Response: resp}
}

func ExecuteExtendedIMSlashCommand(opts ExtendedIMSlashOptions) CommonIMSlashResult {
	if result := ExecuteCommonIMSlashCommand(opts.Manager, opts.SelfAdapter, opts.Text, CommonIMSlashOptions{
		HelpExtraLines: opts.HelpExtraLines,
	}); result.Handled {
		return result
	}

	parts, cmd, ok := extendedSlashParts(opts.Text)
	if !ok {
		return CommonIMSlashResult{}
	}

	switch cmd {
	case "/restart":
		return handleRestartSlash(parts, opts)
	case "/provider":
		return handleProviderSlash(parts, opts)
	case "/model":
		return handleModelSlash(parts, opts)
	case "/config":
		return handleConfigSlash(opts)
	}

	if opts.OnExtra != nil {
		if resp, handled := opts.OnExtra(parts); handled {
			return CommonIMSlashResult{Handled: true, Response: resp}
		}
	}

	return CommonIMSlashResult{Handled: true, Response: fmt.Sprintf("Unknown command: %s. Try /help", cmd)}
}

// handleRestartSlash dispatches "/restart [debug]" to opts.OnRestart.
func handleRestartSlash(parts []string, opts ExtendedIMSlashOptions) CommonIMSlashResult {
	if opts.OnRestart == nil {
		return CommonIMSlashResult{Handled: true, Response: "❌ Restart not available in this mode."}
	}
	debugMode := len(parts) > 1 && strings.ToLower(parts[1]) == "debug"
	resp, err := opts.OnRestart(debugMode)
	return invokeExtendedSlashCallback(resp, err)
}

// handleProviderSlash dispatches "/provider [vendor] [endpoint]" to opts.OnProvider.
func handleProviderSlash(parts []string, opts ExtendedIMSlashOptions) CommonIMSlashResult {
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
	return invokeExtendedSlashCallback(resp, err)
}

// handleModelSlash dispatches "/model [model]" to opts.OnModel.
func handleModelSlash(parts []string, opts ExtendedIMSlashOptions) CommonIMSlashResult {
	if opts.OnModel == nil {
		return CommonIMSlashResult{Handled: true, Response: "❌ Model switching not available in this mode."}
	}
	model := ""
	if len(parts) > 1 {
		model = parts[1]
	}
	resp, err := opts.OnModel(model)
	return invokeExtendedSlashCallback(resp, err)
}

// handleConfigSlash dispatches "/config" to opts.OnConfig.
func handleConfigSlash(opts ExtendedIMSlashOptions) CommonIMSlashResult {
	if opts.OnConfig == nil {
		return CommonIMSlashResult{Handled: true, Response: "❌ Config display not available in this mode."}
	}
	resp, err := opts.OnConfig()
	return invokeExtendedSlashCallback(resp, err)
}
