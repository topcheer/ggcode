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
	ID          string
	DisplayName string
	Available   bool
	Binary      string
	InstallHint string
	Evidence    []string
}

type WorkspaceStatus struct {
	Workspace string
	Languages []LanguageStatus
}

type ResolvedServer struct {
	LanguageID  string
	DisplayName string
	Binary      string
	InstallHint string
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
		binary, available := firstAvailableBinary(spec.binaries)
		languages = append(languages, LanguageStatus{
			ID:          spec.id,
			DisplayName: spec.displayName,
			Available:   available,
			Binary:      binary,
			InstallHint: installHint(spec.id),
			Evidence:    evidence,
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
				binary, available := firstAvailableBinary(spec.binaries)
				return ResolvedServer{
					LanguageID:  languageIDForFile(spec.id, ext),
					DisplayName: spec.displayName,
					Binary:      binary,
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
			return ResolvedServer{
				LanguageID:  languageIDForFile(spec.id, firstLanguageExtension(spec)),
				DisplayName: spec.displayName,
				Binary:      lang.Binary,
				InstallHint: installHint(spec.id),
			}, true
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
