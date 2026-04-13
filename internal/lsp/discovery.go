package lsp

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type LanguageStatus struct {
	ID             string
	DisplayName    string
	Available      bool
	Binary         string
	InstallHint    string
	InstallOptions []InstallOption
	Evidence       []string
}

type WorkspaceStatus struct {
	Workspace string
	Languages []LanguageStatus
}

type ResolvedServer struct {
	LanguageID  string
	DisplayName string
	Binary      string
	Args        []string
	InstallHint string
}

type InstallOption struct {
	ID          string
	Label       string
	Binary      string
	Command     string
	Recommended bool
}

type serverSpec struct {
	id          string
	displayName string
	binaries    []string
	rootMarkers []string
	filenames   []string
	extensions  []string
}

const maxWorkspaceScanDepth = 3

var builtinServerSpecs = []serverSpec{
	{id: "go", displayName: "Go", binaries: []string{"gopls"}, rootMarkers: []string{"go.mod", "go.work"}, extensions: []string{".go"}},
	{id: "rust", displayName: "Rust", binaries: []string{"rust-analyzer"}, rootMarkers: []string{"Cargo.toml"}, extensions: []string{".rs"}},
	{id: "clang", displayName: "C / C++ / Objective-C", binaries: []string{"clangd"}, rootMarkers: []string{"compile_commands.json", "compile_flags.txt", "cmakelists.txt", "meson.build", "makefile"}, extensions: []string{".c", ".cc", ".cp", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm"}},
	{id: "lua", displayName: "Lua", binaries: []string{"lua-language-server"}, rootMarkers: []string{".luarc.json", ".luarc.jsonc"}, extensions: []string{".lua"}},
	{id: "swift", displayName: "Swift", binaries: []string{"sourcekit-lsp"}, rootMarkers: []string{"package.swift"}, extensions: []string{".swift"}},
	{id: "terraform", displayName: "Terraform / HCL", binaries: []string{"terraform-ls"}, rootMarkers: []string{"main.tf", "versions.tf", "terragrunt.hcl"}, extensions: []string{".tf", ".tfvars", ".hcl"}},
	{id: "yaml", displayName: "YAML", binaries: []string{"yaml-language-server"}, extensions: []string{".yaml", ".yml"}},
	{id: "json", displayName: "JSON", binaries: []string{"vscode-json-language-server"}, extensions: []string{".json", ".jsonc"}},
	{id: "dockerfile", displayName: "Dockerfile", binaries: []string{"docker-langserver"}, rootMarkers: []string{"dockerfile", "containerfile"}, filenames: []string{"Dockerfile", "Containerfile"}, extensions: []string{".dockerfile"}},
	{id: "shell", displayName: "Shell / Bash", binaries: []string{"bash-language-server"}, extensions: []string{".sh", ".bash", ".zsh", ".ksh"}},
	{id: "zig", displayName: "Zig", binaries: []string{"zls"}, rootMarkers: []string{"build.zig", "zls.json"}, extensions: []string{".zig"}},
	{id: "java", displayName: "Java", binaries: []string{"jdtls"}, rootMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, extensions: []string{".java"}},
	{id: "typescript", displayName: "TypeScript / JavaScript", binaries: []string{"typescript-language-server"}, rootMarkers: []string{"package.json", "tsconfig.json", "jsconfig.json"}, extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}},
	{id: "python", displayName: "Python", binaries: []string{"pyright-langserver", "pylsp"}, rootMarkers: []string{"pyproject.toml", "requirements.txt", "setup.py"}, extensions: []string{".py"}},
	{id: "csharp", displayName: "C#", binaries: []string{"csharp-ls"}, rootMarkers: []string{"Directory.Build.props", "global.json"}, extensions: []string{".cs", ".csproj", ".sln"}},
}

func DetectWorkspaceStatus(workspace string) WorkspaceStatus {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	status := WorkspaceStatus{Workspace: workspace}
	if workspace == "" {
		return status
	}

	rootEntries := readRootEntries(workspace)
	extensions := scanWorkspaceExtensions(workspace)
	languages := make([]LanguageStatus, 0, len(builtinServerSpecs))
	for _, spec := range builtinServerSpecs {
		evidence := detectLanguageEvidence(spec, rootEntries, extensions)
		if len(evidence) == 0 {
			continue
		}
		options := installOptions(spec, workspace)
		binary, available := detectAvailableBinary(spec, workspace)
		languages = append(languages, LanguageStatus{
			ID:             spec.id,
			DisplayName:    spec.displayName,
			Available:      available,
			Binary:         binary,
			InstallHint:    installHint(spec.id, workspace),
			InstallOptions: options,
			Evidence:       evidence,
		})
	}
	slices.SortFunc(languages, func(a, b LanguageStatus) int {
		if a.Available != b.Available {
			if a.Available {
				return -1
			}
			return 1
		}
		return strings.Compare(a.DisplayName, b.DisplayName)
	})
	status.Languages = languages
	return status
}

func ResolveServerForFile(workspace, path string) (ResolvedServer, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ResolvedServer{}, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	base := filepath.Base(path)
	for _, spec := range builtinServerSpecs {
		matched := false
		for _, candidateExt := range spec.extensions {
			if strings.EqualFold(candidateExt, ext) {
				matched = true
				break
			}
		}
		if !matched {
			for _, candidateName := range spec.filenames {
				if strings.EqualFold(candidateName, base) {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		binary, available := resolveServerBinary(spec, workspace)
		return ResolvedServer{
			LanguageID:  languageIDForFile(spec.id, path),
			DisplayName: spec.displayName,
			Binary:      binary,
			Args:        launchArgs(spec.id, binary, workspace),
			InstallHint: installHint(spec.id, workspace),
		}, available
	}
	return ResolvedServer{}, false
}

func ResolveServerForWorkspace(workspace string) (ResolvedServer, bool) {
	status := DetectWorkspaceStatus(workspace)
	for _, lang := range status.Languages {
		if !lang.Available {
			continue
		}
		for _, spec := range builtinServerSpecs {
			if spec.id != lang.ID {
				continue
			}
			binary, available := resolveServerBinary(spec, workspace)
			return ResolvedServer{
				LanguageID:  languageIDForFile(spec.id, firstLanguageExtension(spec)),
				DisplayName: spec.displayName,
				Binary:      binary,
				Args:        launchArgs(spec.id, binary, workspace),
				InstallHint: installHint(spec.id, workspace),
			}, available
		}
	}
	return ResolvedServer{}, false
}

func firstLanguageExtension(spec serverSpec) string {
	if len(spec.extensions) == 0 {
		return ""
	}
	return spec.extensions[0]
}

func readRootEntries(workspace string) map[string]struct{} {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		out[strings.ToLower(entry.Name())] = struct{}{}
	}
	return out
}

func scanWorkspaceExtensions(workspace string) map[string]struct{} {
	found := make(map[string]struct{})
	rootDepth := strings.Count(filepath.Clean(workspace), string(filepath.Separator))
	_ = filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxWorkspaceScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" {
			found[ext] = struct{}{}
		}
		return nil
	})
	return found
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".ggcode", "node_modules", "vendor", ".venv", "venv", ".idea", ".vscode", "dist", "build", "target", "coverage":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func detectLanguageEvidence(spec serverSpec, rootEntries, extensions map[string]struct{}) []string {
	evidence := make([]string, 0, 2)
	for _, marker := range spec.rootMarkers {
		if _, ok := rootEntries[strings.ToLower(marker)]; ok {
			evidence = append(evidence, marker)
		}
	}
	for _, name := range spec.filenames {
		if _, ok := rootEntries[strings.ToLower(name)]; ok {
			evidence = append(evidence, name)
		}
	}
	for _, ext := range spec.extensions {
		if _, ok := extensions[strings.ToLower(ext)]; ok {
			evidence = append(evidence, ext)
		}
	}
	return uniqueEvidence(evidence)
}

func uniqueEvidence(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstAvailableBinary(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, true
		}
	}
	if len(candidates) > 0 {
		return candidates[0], false
	}
	return "", false
}

func detectAvailableBinary(spec serverSpec, workspace string) (string, bool) {
	display, _, ok := resolveManagedBinary(spec, workspace)
	if ok {
		return display, true
	}
	if len(spec.binaries) > 0 {
		return spec.binaries[0], false
	}
	return "", false
}

func resolveServerBinary(spec serverSpec, workspace string) (string, bool) {
	_, command, ok := resolveManagedBinary(spec, workspace)
	if ok {
		return command, true
	}
	if len(spec.binaries) > 0 {
		return spec.binaries[0], false
	}
	return "", false
}

func resolveManagedBinary(spec serverSpec, workspace string) (display string, command string, ok bool) {
	if display, ok = firstAvailableBinary(spec.binaries); ok {
		return display, display, true
	}
	if display, command, ok = resolveWorkspaceToolFallback(spec.binaries, workspace); ok {
		return display, command, true
	}
	switch spec.id {
	case "rust":
		return resolveRustAnalyzerFallback()
	case "go":
		return resolveGoBinaryFallback("gopls")
	case "typescript", "yaml", "json", "dockerfile", "shell":
		return resolveNodeBinaryFallback(spec.binaries, workspace)
	case "python":
		return resolvePythonVenvFallback(spec.binaries, workspace)
	case "csharp":
		if display, command, ok := resolveCSharpWorkspaceToolFallback(workspace); ok {
			return display, command, true
		}
		return resolveDotnetToolFallback(spec.binaries)
	default:
		return "", "", false
	}
}

func resolveWorkspaceToolFallback(candidates []string, workspace string) (display string, command string, ok bool) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", "", false
	}
	for _, candidate := range candidates {
		for _, name := range executableNames(candidate) {
			path := filepath.Join(workspace, ".ggcode", "tools", name)
			if executableExists(path) {
				return candidate, path, true
			}
		}
	}
	return "", "", false
}

func resolveRustAnalyzerFallback() (display string, command string, ok bool) {
	if _, err := exec.LookPath("rustup"); err == nil {
		out, err := exec.Command("rustup", "which", "rust-analyzer").Output()
		if err == nil {
			path := strings.TrimSpace(string(out))
			if executableExists(path) {
				return "rust-analyzer", path, true
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".cargo", "bin", executableName("rust-analyzer"))
		if executableExists(path) {
			return "rust-analyzer", path, true
		}
	}
	return "", "", false
}

func resolveGoBinaryFallback(binary string) (display string, command string, ok bool) {
	candidates := make([]string, 0, 3)
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		candidates = append(candidates, filepath.Join(gobin, executableName(binary)))
	}
	if gopath := strings.TrimSpace(os.Getenv("GOPATH")); gopath != "" {
		first := strings.Split(gopath, string(os.PathListSeparator))[0]
		if strings.TrimSpace(first) != "" {
			candidates = append(candidates, filepath.Join(first, "bin", executableName(binary)))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "go", "bin", executableName(binary)))
	}
	for _, candidate := range candidates {
		if executableExists(candidate) {
			return binary, candidate, true
		}
	}
	return "", "", false
}

func resolvePythonVenvFallback(candidates []string, workspace string) (display string, command string, ok bool) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", "", false
	}
	for _, venvDir := range []string{".venv", "venv"} {
		for _, candidate := range candidates {
			for _, name := range executableNames(candidate) {
				path := filepath.Join(workspace, venvDir, venvBinDir(), name)
				if executableExists(path) {
					return candidate, path, true
				}
			}
		}
	}
	return "", "", false
}

func resolveNodeBinaryFallback(candidates []string, workspace string) (display string, command string, ok bool) {
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		for _, candidate := range candidates {
			for _, name := range executableNames(candidate) {
				path := filepath.Join(workspace, "node_modules", ".bin", name)
				if executableExists(path) {
					return candidate, path, true
				}
			}
		}
	}
	if _, err := exec.LookPath("npm"); err == nil {
		out, err := exec.Command("npm", "config", "get", "prefix").Output()
		if err == nil {
			prefix := strings.TrimSpace(string(out))
			if prefix != "" && prefix != "undefined" && prefix != "null" {
				globalBin := prefix
				if runtime.GOOS != "windows" {
					globalBin = filepath.Join(prefix, "bin")
				}
				for _, candidate := range candidates {
					for _, name := range executableNames(candidate) {
						path := filepath.Join(globalBin, name)
						if executableExists(path) {
							return candidate, path, true
						}
					}
				}
			}
		}
	}
	return "", "", false
}

func resolveDotnetToolFallback(candidates []string) (display string, command string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	for _, candidate := range candidates {
		for _, name := range executableNames(candidate) {
			path := filepath.Join(home, ".dotnet", "tools", name)
			if executableExists(path) {
				return candidate, path, true
			}
		}
	}
	return "", "", false
}

func resolveCSharpWorkspaceToolFallback(workspace string) (display string, command string, ok bool) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", "", false
	}
	for _, name := range executableNames("csharp-ls") {
		path := filepath.Join(workspace, ".ggcode", "tools", name)
		if executableExists(path) {
			return "csharp-ls", path, true
		}
	}
	return "", "", false
}

func executableExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}

func executableName(name string) string {
	names := executableNames(name)
	if len(names) == 0 {
		return name
	}
	return names[0]
}

func executableNames(name string) []string {
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".exe"), strings.HasSuffix(lower, ".cmd"), strings.HasSuffix(lower, ".bat"):
			return []string{name}
		default:
			return []string{name + ".exe", name + ".cmd", name + ".bat"}
		}
	}
	return []string{name}
}

func venvBinDir() string {
	if runtime.GOOS == "windows" {
		return "Scripts"
	}
	return "bin"
}

func installHint(languageID, workspace string) string {
	switch languageID {
	case "go":
		return commandWithPrereq("go", "go is required to install gopls. Install Go first.", "go install golang.org/x/tools/gopls@latest")
	case "rust":
		return commandWithPrereq("rustup", "rustup is required to install rust-analyzer. Install Rust first.", "rustup component add rust-analyzer")
	case "clang":
		switch runtime.GOOS {
		case "darwin":
			return workspaceLinkOrInstallCommand("clangd", "brew", "Homebrew is required to install clangd automatically on macOS.", "brew install llvm", "$(brew --prefix llvm)/bin/clangd", "clangd is unavailable. Install Xcode Command Line Tools or Homebrew llvm first.")
		case "windows":
			return commandWithPrereq("winget", "winget is required to install clangd on Windows.", "winget install --accept-package-agreements --accept-source-agreements LLVM.LLVM")
		default:
			return unsupportedInstallCommand("Automatic clangd installation is not configured for this OS yet. Install clangd manually and ensure it is on PATH.")
		}
	case "lua":
		switch runtime.GOOS {
		case "darwin":
			return commandWithPrereq("brew", "Homebrew is required to install lua-language-server on macOS.", "brew install lua-language-server")
		case "windows":
			return commandWithPrereq("winget", "winget is required to install lua-language-server on Windows.", "winget install --accept-package-agreements --accept-source-agreements LuaLS.lua-language-server")
		default:
			return unsupportedInstallCommand("Automatic lua-language-server installation is not configured for this OS yet. Install it manually from https://luals.github.io/.")
		}
	case "swift":
		switch runtime.GOOS {
		case "darwin":
			return workspaceLinkExistingCommand("sourcekit-lsp", "sourcekit-lsp is unavailable. Install Xcode Command Line Tools or a Swift toolchain first.")
		default:
			return unsupportedInstallCommand("Automatic sourcekit-lsp installation is not configured for this OS yet. Install a Swift toolchain that provides sourcekit-lsp and ensure it is on PATH.")
		}
	case "terraform":
		switch runtime.GOOS {
		case "darwin":
			return commandWithPrereq("brew", "Homebrew is required to install terraform-ls on macOS.", "brew install hashicorp/tap/terraform-ls")
		case "windows":
			return commandWithPrereq("winget", "winget is required to install terraform-ls on Windows.", "winget install --accept-package-agreements --accept-source-agreements HashiCorp.TerraformLS")
		default:
			return unsupportedInstallCommand("Automatic terraform-ls installation is not configured for this OS yet. Install it manually from HashiCorp releases.")
		}
	case "zig":
		switch runtime.GOOS {
		case "darwin":
			return commandWithPrereq("brew", "Homebrew is required to install zls on macOS.", "brew install zls")
		case "windows":
			return commandWithPrereq("winget", "winget is required to install zls on Windows.", "winget install --accept-package-agreements --accept-source-agreements zigtools.zls")
		default:
			return unsupportedInstallCommand("Automatic zls installation is not configured for this OS yet. Install it manually from https://github.com/zigtools/zls.")
		}
	case "java":
		switch runtime.GOOS {
		case "darwin":
			return commandWithPrereq("brew", "Homebrew is required to install jdtls on macOS.", "brew install jdtls")
		case "windows":
			return unsupportedInstallCommand("Automatic jdtls installation is not configured for Windows yet. Install a JDK and jdtls manually from https://github.com/eclipse-jdtls/eclipse.jdt.ls.")
		default:
			return unsupportedInstallCommand("Automatic jdtls installation is not configured for this OS yet. Install a JDK and jdtls manually from https://github.com/eclipse-jdtls/eclipse.jdt.ls.")
		}
	case "typescript":
		return commandWithPrereq("npm", "npm is required to install typescript-language-server. Install Node.js first.", "npm install -g typescript typescript-language-server")
	case "yaml":
		return nodeWorkspaceInstallCommand("yaml-language-server")
	case "json":
		return nodeWorkspaceInstallCommand("vscode-langservers-extracted")
	case "dockerfile":
		return nodeWorkspaceInstallCommand("dockerfile-language-server-nodejs")
	case "shell":
		return nodeWorkspaceInstallCommand("bash-language-server")
	case "python":
		return pythonVenvInstallCommand(workspace, "pyright")
	case "csharp":
		return csharpToolInstallCommand()
	default:
		return ""
	}
}

func installOptions(spec serverSpec, workspace string) []InstallOption {
	switch spec.id {
	case "python":
		return []InstallOption{
			{
				ID:          "pyright",
				Label:       "pyright-langserver",
				Binary:      "pyright-langserver",
				Command:     pythonVenvInstallCommand(workspace, "pyright"),
				Recommended: true,
			},
			{
				ID:      "pylsp",
				Label:   "pylsp",
				Binary:  "pylsp",
				Command: pythonVenvInstallCommand(workspace, "python-lsp-server"),
			},
		}
	case "typescript":
		return []InstallOption{{
			ID:          "typescript-language-server",
			Label:       "typescript-language-server",
			Binary:      "typescript-language-server",
			Command:     installHint(spec.id, workspace),
			Recommended: true,
		}}
	case "csharp":
		return []InstallOption{{
			ID:          "csharp-ls",
			Label:       "csharp-ls",
			Binary:      "csharp-ls",
			Command:     csharpToolInstallCommand(),
			Recommended: true,
		}}
	default:
		command := installHint(spec.id, workspace)
		binary := ""
		if len(spec.binaries) > 0 {
			binary = spec.binaries[0]
		}
		if strings.TrimSpace(command) == "" && strings.TrimSpace(binary) == "" {
			return nil
		}
		return []InstallOption{{
			ID:          spec.id,
			Label:       firstNonEmpty(binary, spec.displayName),
			Binary:      binary,
			Command:     command,
			Recommended: true,
		}}
	}
}

func csharpToolInstallCommand() string {
	if runtime.GOOS == "windows" {
		return strings.Join([]string{
			"if (-not (Get-Command dotnet -ErrorAction SilentlyContinue)) { Write-Error 'dotnet is required to install csharp-ls. Install the .NET SDK first.'; exit 1 }",
			"New-Item -ItemType Directory -Force -Path '.ggcode\\tools' | Out-Null",
			"New-Item -ItemType Directory -Force -Path '.ggcode\\dotnet-cli-home' | Out-Null",
			"$env:DOTNET_CLI_HOME = (Resolve-Path '.ggcode\\dotnet-cli-home').Path",
			"$toolPath = (Resolve-Path '.ggcode\\tools').Path",
			"if (Test-Path (Join-Path $toolPath 'csharp-ls.exe')) { dotnet tool update --tool-path $toolPath csharp-ls } else { dotnet tool install --tool-path $toolPath csharp-ls }",
		}, "; ")
	}
	return "if ! command -v dotnet >/dev/null 2>&1; then " +
		"echo 'dotnet is required to install csharp-ls. Install the .NET SDK first.' >&2; exit 1; fi " +
		"&& mkdir -p .ggcode/tools .ggcode/dotnet-cli-home " +
		"&& export DOTNET_CLI_HOME=\"$(pwd)/.ggcode/dotnet-cli-home\" " +
		"&& if [ -x .ggcode/tools/csharp-ls ]; then " +
		"dotnet tool update --tool-path \"$(pwd)/.ggcode/tools\" csharp-ls; " +
		"else dotnet tool install --tool-path \"$(pwd)/.ggcode/tools\" csharp-ls; fi"
}

func pythonVenvInstallCommand(workspace, packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return strings.Join([]string{
			"if (-not (Get-Command py -ErrorAction SilentlyContinue) -and -not (Get-Command python -ErrorAction SilentlyContinue)) { Write-Error 'Python is required to install Python LSP servers. Install Python first.'; exit 1 }",
			"if (Test-Path '.venv\\Scripts\\python.exe') { $venvPy = '.venv\\Scripts\\python.exe' }",
			"elseif (Test-Path 'venv\\Scripts\\python.exe') { $venvPy = 'venv\\Scripts\\python.exe' }",
			"elseif (Get-Command py -ErrorAction SilentlyContinue) { py -m venv .venv; $venvPy = '.venv\\Scripts\\python.exe' }",
			"else { python -m venv .venv; $venvPy = '.venv\\Scripts\\python.exe' }",
			"& $venvPy -m pip install " + packageName,
		}, "; ")
	}
	return "if [ -x .venv/bin/python ]; then VENV_PY=.venv/bin/python; " +
		"elif [ -x venv/bin/python ]; then VENV_PY=venv/bin/python; " +
		"elif command -v python3 >/dev/null 2>&1; then python3 -m venv .venv && VENV_PY=.venv/bin/python; " +
		"elif command -v python >/dev/null 2>&1; then python -m venv .venv && VENV_PY=.venv/bin/python; " +
		"else echo 'Python is required to install Python LSP servers. Install Python first.' >&2; exit 1; fi " +
		"&& \"$VENV_PY\" -m pip install " + packageName
}

func commandWithPrereq(command, missingMessage, installCommand string) string {
	command = strings.TrimSpace(command)
	missingMessage = strings.TrimSpace(missingMessage)
	installCommand = strings.TrimSpace(installCommand)
	if installCommand == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		if command == "" {
			return installCommand
		}
		return fmt.Sprintf("if (-not (Get-Command %s -ErrorAction SilentlyContinue)) { Write-Error '%s'; exit 1 }; %s", command, escapePowerShellSingleQuoted(missingMessage), installCommand)
	}
	if command == "" {
		return installCommand
	}
	return fmt.Sprintf("if ! command -v %s >/dev/null 2>&1; then echo '%s' >&2; exit 1; fi && %s", command, escapePOSIXSingleQuoted(missingMessage), installCommand)
}

func unsupportedInstallCommand(message string) string {
	message = strings.TrimSpace(message)
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Write-Error '%s'; exit 1", escapePowerShellSingleQuoted(message))
	}
	return fmt.Sprintf("echo '%s' >&2; exit 1", escapePOSIXSingleQuoted(message))
}

func nodeWorkspaceInstallCommand(packages ...string) string {
	joined := strings.Join(packages, " ")
	if runtime.GOOS == "windows" {
		return strings.Join([]string{
			"if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { Write-Error 'npm is required to install this LSP server. Install Node.js first.'; exit 1 }",
			"if (-not (Test-Path 'package.json')) { npm init -y | Out-Null }",
			"npm install --save-dev " + joined,
		}, "; ")
	}
	return "if ! command -v npm >/dev/null 2>&1; then " +
		"echo 'npm is required to install this LSP server. Install Node.js first.' >&2; exit 1; fi " +
		"&& if [ ! -f package.json ]; then npm init -y >/dev/null 2>&1; fi " +
		"&& npm install --save-dev " + joined
}

func workspaceLinkExistingCommand(binary, missingMessage string) string {
	if runtime.GOOS == "windows" {
		exe := executableName(binary)
		return strings.Join([]string{
			"if (-not (Get-Command " + binary + " -ErrorAction SilentlyContinue)) { Write-Error '" + escapePowerShellSingleQuoted(missingMessage) + "'; exit 1 }",
			"New-Item -ItemType Directory -Force -Path '.ggcode\\tools' | Out-Null",
			"$target = (Get-Command " + binary + ").Source",
			"$link = Join-Path (Resolve-Path '.ggcode\\tools').Path '" + exe + "'",
			"Copy-Item -Force $target $link",
		}, "; ")
	}
	return "if ! command -v " + binary + " >/dev/null 2>&1; then " +
		"echo '" + escapePOSIXSingleQuoted(missingMessage) + "' >&2; exit 1; fi " +
		"&& mkdir -p .ggcode/tools " +
		"&& ln -sf \"$(command -v " + binary + ")\" .ggcode/tools/" + executableName(binary)
}

func workspaceLinkOrInstallCommand(binary, installer, installerMissing, installCommand, linkedBinary, finalMissing string) string {
	if runtime.GOOS == "windows" {
		return commandWithPrereq(installer, installerMissing, installCommand)
	}
	return "mkdir -p .ggcode/tools " +
		"&& if command -v " + binary + " >/dev/null 2>&1; then " +
		"ln -sf \"$(command -v " + binary + ")\" .ggcode/tools/" + executableName(binary) + "; " +
		"elif command -v " + installer + " >/dev/null 2>&1; then " +
		installCommand + " && ln -sf \"" + linkedBinary + "\" .ggcode/tools/" + executableName(binary) + "; " +
		"else echo '" + escapePOSIXSingleQuoted(finalMissing) + "' >&2; exit 1; fi"
}

func escapePOSIXSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", `'\''`)
}

func escapePowerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func launchArgs(specID, binary, workspace string) []string {
	base := binaryBaseName(binary)
	switch {
	case base == "pyright-langserver":
		return []string{"--stdio"}
	case base == "typescript-language-server":
		return []string{"--stdio"}
	case specID == "yaml" && base == "yaml-language-server":
		return []string{"--stdio"}
	case specID == "json" && base == "vscode-json-language-server":
		return []string{"--stdio"}
	case specID == "dockerfile" && base == "docker-langserver":
		return []string{"--stdio"}
	case specID == "shell" && base == "bash-language-server":
		return []string{"start"}
	case specID == "csharp" && base == "csharp-ls":
		return csharpSolutionArgs(workspace)
	case specID == "terraform" && base == "terraform-ls":
		return []string{"serve"}
	default:
		return nil
	}
}

func csharpSolutionArgs(workspace string) []string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return nil
	}
	var slnSolutions []string
	var slnxSolutions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		switch strings.ToLower(filepath.Ext(name)) {
		case ".sln":
			slnSolutions = append(slnSolutions, filepath.Join(workspace, name))
		case ".slnx":
			slnxSolutions = append(slnxSolutions, filepath.Join(workspace, name))
		}
	}
	switch {
	case len(slnSolutions) == 1:
		return []string{"--solution", slnSolutions[0]}
	case len(slnSolutions) == 0 && len(slnxSolutions) == 1:
		if compat := ensureCSharpCompatSolution(workspace); compat != "" {
			return []string{"--solution", compat}
		}
		return []string{"--solution", slnxSolutions[0]}
	default:
		return nil
	}
}

func ensureCSharpCompatSolution(workspace string) string {
	if _, err := exec.LookPath("dotnet"); err != nil {
		return ""
	}
	projects := findCSharpProjects(workspace)
	if len(projects) == 0 {
		return ""
	}
	compatDir := filepath.Join(workspace, ".ggcode", "lsp")
	compatPath := filepath.Join(compatDir, "csharp-ls.sln")
	if compatSolutionUpToDate(compatPath, projects) {
		return compatPath
	}
	if err := os.MkdirAll(compatDir, 0o755); err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".ggcode", "dotnet-cli-home"), 0o755); err != nil {
		return ""
	}
	_ = os.Remove(compatPath)
	env := append([]string{}, os.Environ()...)
	env = append(env, "DOTNET_CLI_HOME="+filepath.Join(workspace, ".ggcode", "dotnet-cli-home"))
	newSln := exec.Command("dotnet", "new", "sln", "-f", "sln", "-n", "csharp-ls", "-o", compatDir, "--force")
	newSln.Dir = workspace
	newSln.Env = env
	if err := newSln.Run(); err != nil {
		return ""
	}
	args := append([]string{"sln", compatPath, "add"}, projects...)
	addProjects := exec.Command("dotnet", args...)
	addProjects.Dir = workspace
	addProjects.Env = env
	if err := addProjects.Run(); err != nil {
		return ""
	}
	return compatPath
}

func findCSharpProjects(workspace string) []string {
	var projects []string
	_ = filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := strings.TrimSpace(d.Name())
			if name == ".ggcode" || name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".csproj") {
			projects = append(projects, path)
		}
		return nil
	})
	slices.Sort(projects)
	return projects
}

func compatSolutionUpToDate(compatPath string, projects []string) bool {
	info, err := os.Stat(compatPath)
	if err != nil {
		return false
	}
	compatTime := info.ModTime()
	for _, project := range projects {
		projectInfo, err := os.Stat(project)
		if err != nil || projectInfo.ModTime().After(compatTime) {
			return false
		}
	}
	return true
}

func binaryBaseName(binary string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(binary)))
	switch {
	case strings.HasSuffix(base, ".exe"), strings.HasSuffix(base, ".cmd"), strings.HasSuffix(base, ".bat"):
		return strings.TrimSuffix(base, filepath.Ext(base))
	default:
		return base
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func languageIDForFile(specID, path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch specID {
	case "typescript":
		switch strings.ToLower(ext) {
		case ".js", ".jsx", ".mjs", ".cjs":
			return "javascript"
		default:
			return "typescript"
		}
	case "terraform":
		return "terraform"
	case "csharp":
		return "csharp"
	case "clang":
		switch ext {
		case ".c":
			return "c"
		case ".m":
			return "objective-c"
		case ".mm":
			return "objective-cpp"
		default:
			return "cpp"
		}
	case "swift":
		return "swift"
	case "yaml":
		return "yaml"
	case "json":
		return "json"
	case "dockerfile":
		return "dockerfile"
	case "shell":
		return "shellscript"
	default:
		return specID
	}
}
