package wailskit

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/lsp"
)

// LSPInstallOption is a single installable language server variant.
type LSPInstallOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Binary      string `json:"binary"`
	Recommended bool   `json:"recommended"`
	Scope       string `json:"scope"` // "user", "global", "project"
}

// LSPServerStatus is the JSON-serializable status of a single language server.
type LSPServerStatus struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Available   bool   `json:"available"`
	Binary      string `json:"binary"`
	InstallHint string `json:"install_hint"`
	// Override indicates whether the user configured a custom binary path.
	Override       bool               `json:"override"`
	CanInstall     bool               `json:"can_install"`
	InstallOptions []LSPInstallOption `json:"install_options"`
}

// LSPStatusResponse is the full LSP status payload for the frontend.
type LSPStatusResponse struct {
	Workspace string            `json:"workspace"`
	Languages []LSPServerStatus `json:"languages"`
}

// LSPInstallResult holds the outcome of an install attempt.
type LSPInstallResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
}

// GetLSPStatus returns the detected language server status for the current workspace.
// The frontend calls this to populate the Settings > Language Servers panel.
func (b *ChatBridge) GetLSPStatus() LSPStatusResponse {
	b.mu.Lock()
	wd := b.workingDir
	b.mu.Unlock()

	status := lsp.DetectWorkspaceStatus(wd)
	overrides := lsp.ServerOverrides()

	result := LSPStatusResponse{
		Workspace: status.Workspace,
	}
	for _, lang := range status.Languages {
		opts := lsp.GetInstallOptions(lang.ID, wd)
		var installOpts []LSPInstallOption
		canInstall := false
		for _, opt := range opts {
			cmd := strings.TrimSpace(opt.Command)
			// Filter out pure error messages (unsupported platforms).
			realCommand := cmd != "" &&
				!strings.HasPrefix(cmd, "echo ") &&
				!strings.HasPrefix(cmd, "Write-Error ")
			if realCommand {
				canInstall = true
			}
			installOpts = append(installOpts, LSPInstallOption{
				ID:          opt.ID,
				Label:       opt.Label,
				Binary:      opt.Binary,
				Recommended: opt.Recommended,
				Scope:       string(opt.Scope),
			})
		}
		_, hasOverride := overrides[lang.ID]
		result.Languages = append(result.Languages, LSPServerStatus{
			ID:             lang.ID,
			DisplayName:    lang.DisplayName,
			Available:      lang.Available,
			Binary:         lang.Binary,
			InstallHint:    lang.InstallHint,
			Override:       hasOverride,
			CanInstall:     canInstall,
			InstallOptions: installOpts,
		})
	}
	return result
}

// InstallLSPServer runs the install command for the given language server.
// If optionID is empty, the recommended option is used.
// The command runs in the workspace directory.
func (b *ChatBridge) InstallLSPServer(languageID, optionID string) LSPInstallResult {
	b.mu.Lock()
	wd := b.workingDir
	b.mu.Unlock()

	opts := lsp.GetInstallOptions(languageID, wd)
	if len(opts) == 0 {
		return LSPInstallResult{
			Success: false,
			Output:  fmt.Sprintf("No install options available for language: %s", languageID),
		}
	}

	// Find the requested option (or recommended default).
	var selected *lsp.InstallOption
	for i := range opts {
		if optionID != "" && opts[i].ID == optionID {
			selected = &opts[i]
			break
		}
		if optionID == "" && opts[i].Recommended {
			selected = &opts[i]
			break
		}
	}
	if selected == nil {
		// Fall back to first option.
		selected = &opts[0]
	}

	cmd := strings.TrimSpace(selected.Command)
	if cmd == "" {
		return LSPInstallResult{
			Success: false,
			Output:  "Install command is empty.",
		}
	}
	if strings.HasPrefix(cmd, "echo ") || strings.HasPrefix(cmd, "Write-Error ") {
		return LSPInstallResult{
			Success: false,
			Output:  "This language server cannot be installed automatically on this platform.",
		}
	}

	debug.Log("lsp", "installing %s (%s): %s", languageID, selected.ID, cmd)

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("powershell", "-NoProfile", "-Command", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Dir = wd
	output, err := c.CombinedOutput()

	result := LSPInstallResult{
		Output: string(output),
	}
	if err != nil {
		result.Success = false
		if result.Output == "" {
			result.Output = err.Error()
		} else {
			result.Output += "\n" + err.Error()
		}
		debug.Log("lsp", "install %s failed: %v", languageID, err)
	} else {
		result.Success = true
		debug.Log("lsp", "install %s succeeded", languageID)
	}
	return result
}
