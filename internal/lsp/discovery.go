package lsp

import (
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
	extensions  []string
}

const maxWorkspaceScanDepth = 3

var builtinServerSpecs = []serverSpec{
	{id: "go", displayName: "Go", binaries: []string{"gopls"}, rootMarkers: []string{"go.mod", "go.work"}, extensions: []string{".go"}},
	{id: "rust", displayName: "Rust", binaries: []string{"rust-analyzer"}, rootMarkers: []string{"Cargo.toml"}, extensions: []string{".rs"}},
	{id: "lua", displayName: "Lua", binaries: []string{"lua-language-server"}, rootMarkers: []string{".luarc.json", ".luarc.jsonc"}, extensions: []string{".lua"}},
	{id: "terraform", displayName: "Terraform / HCL", binaries: []string{"terraform-ls"}, rootMarkers: []string{"main.tf", "versions.tf", "terragrunt.hcl"}, extensions: []string{".tf", ".tfvars", ".hcl"}},
	{id: "zig", displayName: "Zig", binaries: []string{"zls"}, rootMarkers: []string{"build.zig", "zls.json"}, extensions: []string{".zig"}},
	{id: "java", displayName: "Java", binaries: []string{"jdtls"}, rootMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, extensions: []string{".java"}},
	{id: "typescript", displayName: "TypeScript / JavaScript", binaries: []string{"typescript-language-server"}, rootMarkers: []string{"package.json", "tsconfig.json", "jsconfig.json"}, extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}},
	{id: "python", displayName: "Python", binaries: []string{"pyright-langserver", "pylsp"}, rootMarkers: []string{"pyproject.toml", "requirements.txt", "setup.py"}, extensions: []string{".py"}},
	{id: "csharp", displayName: "C#", binaries: []string{"csharp-ls", "OmniSharp"}, rootMarkers: []string{"Directory.Build.props", "global.json"}, extensions: []string{".cs", ".csproj", ".sln"}},
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
			InstallHint:    installHint(spec.id),
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
	for _, spec := range builtinServerSpecs {
		for _, candidateExt := range spec.extensions {
			if strings.EqualFold(candidateExt, ext) {
				binary, available := resolveServerBinary(spec, workspace)
				return ResolvedServer{
					LanguageID:  languageIDForFile(spec.id, ext),
					DisplayName: spec.displayName,
					Binary:      binary,
					Args:        launchArgs(spec.id, binary),
					InstallHint: installHint(spec.id),
				}, available
			}
		}
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
				Args:        launchArgs(spec.id, lang.Binary),
				InstallHint: installHint(spec.id),
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
	switch spec.id {
	case "rust":
		return resolveRustAnalyzerFallback()
	case "go":
		return resolveGoBinaryFallback("gopls")
	case "python":
		return resolvePythonVenvFallback(spec.binaries, workspace)
	default:
		return "", "", false
	}
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
			path := filepath.Join(workspace, venvDir, venvBinDir(), executableName(candidate))
			if executableExists(path) {
				return candidate, path, true
			}
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
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".exe"), strings.HasSuffix(lower, ".cmd"), strings.HasSuffix(lower, ".bat"):
			return name
		default:
			return name + ".exe"
		}
	}
	return name
}

func venvBinDir() string {
	if runtime.GOOS == "windows" {
		return "Scripts"
	}
	return "bin"
}

func installHint(languageID string) string {
	switch languageID {
	case "go":
		return "go install golang.org/x/tools/gopls@latest"
	case "rust":
		if runtime.GOOS == "windows" {
			return "rustup component add rust-analyzer"
		}
		return "rustup component add rust-analyzer"
	case "lua":
		switch runtime.GOOS {
		case "darwin":
			return "brew install lua-language-server"
		case "windows":
			return "winget install LuaLS.lua-language-server"
		default:
			return "brew install lua-language-server or install from https://luals.github.io/"
		}
	case "terraform":
		switch runtime.GOOS {
		case "darwin":
			return "brew install hashicorp/tap/terraform-ls"
		case "windows":
			return "winget install HashiCorp.TerraformLS"
		default:
			return "brew install hashicorp/tap/terraform-ls or download from HashiCorp releases"
		}
	case "zig":
		switch runtime.GOOS {
		case "darwin":
			return "brew install zls"
		case "windows":
			return "winget install zigtools.zls"
		default:
			return "brew install zls or build from https://github.com/zigtools/zls"
		}
	case "java":
		switch runtime.GOOS {
		case "darwin":
			return "brew install jdtls"
		case "windows":
			return "winget install EclipseAdoptium.Temurin.21.JDK && install jdtls"
		default:
			return "install a JDK and jdtls from https://github.com/eclipse-jdtls/eclipse.jdt.ls"
		}
	case "typescript":
		return "npm install -g typescript typescript-language-server"
	case "python":
		return "pip install pyright or pip install python-lsp-server"
	case "csharp":
		switch runtime.GOOS {
		case "darwin":
			return "brew install csharp-ls"
		case "windows":
			return "dotnet tool install --global csharp-ls"
		default:
			return "dotnet tool install --global csharp-ls"
		}
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
	default:
		command := installHint(spec.id)
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

func pythonVenvInstallCommand(workspace, packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return strings.Join([]string{
			"if (Test-Path '.venv\\Scripts\\python.exe') { $venvPy = '.venv\\Scripts\\python.exe' }",
			"elseif (Test-Path 'venv\\Scripts\\python.exe') { $venvPy = 'venv\\Scripts\\python.exe' }",
			"else { py -m venv .venv; $venvPy = '.venv\\Scripts\\python.exe' }",
			"& $venvPy -m pip install " + packageName,
		}, "; ")
	}
	return "if [ -x .venv/bin/python ]; then VENV_PY=.venv/bin/python; " +
		"elif [ -x venv/bin/python ]; then VENV_PY=venv/bin/python; " +
		"else python3 -m venv .venv && VENV_PY=.venv/bin/python; fi " +
		"&& \"$VENV_PY\" -m pip install " + packageName
}

func launchArgs(specID, binary string) []string {
	base := binaryBaseName(binary)
	switch {
	case base == "pyright-langserver":
		return []string{"--stdio"}
	case base == "typescript-language-server":
		return []string{"--stdio"}
	case specID == "terraform" && base == "terraform-ls":
		return []string{"serve"}
	default:
		return nil
	}
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

func languageIDForFile(specID, ext string) string {
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
	default:
		return specID
	}
}
