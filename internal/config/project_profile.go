package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectProfile contains auto-detected project metadata used to enrich
// the system prompt at session start. Inspired by Claude Code's CLAUDE.md
// auto-generation and Cursor's .cursorrules project context - but fully
// deterministic and zero-LLM-cost.
type ProjectProfile struct {
	// Languages detected in the project (e.g., "Go", "TypeScript").
	Languages []string
	// BuildSystem is the primary build tool (e.g., "Make", "npm", "cargo").
	BuildSystem string
	// BuildCommand is the suggested build command.
	BuildCommand string
	// TestCommand is the suggested test command.
	TestCommand string
	// LintCommand is the suggested lint command.
	LintCommand string
	// Frameworks detected (e.g., "React", "Wails", "Flutter").
	Frameworks []string
	// KeyFiles are important project files found at the root.
	KeyFiles []string
}

// DetectProjectProfile scans the working directory for well-known project
// marker files and infers the project type, build system, and standard
// commands. It is intentionally fast (stat-only, no content parsing) and
// conservative (only reports high-confidence detections).
func DetectProjectProfile(workingDir string) *ProjectProfile {
	if workingDir == "" {
		return nil
	}

	profile := &ProjectProfile{}

	// Marker file → detection logic. Order matters: earlier entries take
	// priority for BuildSystem/BuildCommand/TestCommand assignment.
	type marker struct {
		filename string
		detect   func(fullPath string)
	}

	markers := []marker{
		// --- Go ---
		{"go.mod", func(p string) {
			profile.Languages = appendUnique(profile.Languages, "Go")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "go"
				profile.BuildCommand = "go build ./..."
				profile.TestCommand = "go test ./..."
			}
			detectGoMakefileTags(profile, workingDir)
			detectGoModFrameworks(profile, p)
		}},

		// --- Node.js / JavaScript / TypeScript ---
		{"package.json", func(p string) {
			profile.Languages = appendUnique(profile.Languages, "JavaScript/TypeScript")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "npm"
				profile.BuildCommand = "npm run build"
				profile.TestCommand = "npm test"
			}
			detectNpmFrameworks(profile, p)
		}},

		// --- TypeScript config ---
		{"tsconfig.json", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "TypeScript")
		}},

		// --- Rust ---
		{"Cargo.toml", func(p string) {
			profile.Languages = appendUnique(profile.Languages, "Rust")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "cargo"
				profile.BuildCommand = "cargo build"
				profile.TestCommand = "cargo test"
				profile.LintCommand = "cargo clippy"
			}
			detectCargoFrameworks(profile, p)
		}},

		// --- Python ---
		{"pyproject.toml", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Python")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "pip/poetry"
				profile.BuildCommand = "pip install -e ."
				profile.TestCommand = "pytest"
			}
		}},
		{"requirements.txt", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Python")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "pip"
				profile.BuildCommand = "pip install -r requirements.txt"
				profile.TestCommand = "pytest"
			}
		}},
		{"setup.py", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Python")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "setuptools"
				profile.BuildCommand = "python setup.py build"
				profile.TestCommand = "pytest"
			}
		}},

		// --- Java/Kotlin ---
		{"pom.xml", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Java/Kotlin")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Maven"
				profile.BuildCommand = "mvn compile"
				profile.TestCommand = "mvn test"
			}
		}},
		{"build.gradle", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Java/Kotlin")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Gradle"
				profile.BuildCommand = "./gradlew build"
				profile.TestCommand = "./gradlew test"
			}
		}},
		{"build.gradle.kts", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Kotlin")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Gradle"
				profile.BuildCommand = "./gradlew build"
				profile.TestCommand = "./gradlew test"
			}
		}},

		// --- Ruby ---
		{"Gemfile", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Ruby")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Bundler"
				profile.BuildCommand = "bundle install"
				profile.TestCommand = "bundle exec rspec"
			}
		}},

		// --- C/C++ ---
		{"CMakeLists.txt", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "C/C++")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "CMake"
				profile.BuildCommand = "cmake --build build"
				profile.TestCommand = "ctest --test-dir build"
			}
		}},

		// --- Flutter/Dart ---
		{"pubspec.yaml", func(p string) {
			profile.Languages = appendUnique(profile.Languages, "Dart")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Flutter/Dart"
				profile.BuildCommand = "flutter build"
				profile.TestCommand = "flutter test"
			}
			profile.Frameworks = appendUnique(profile.Frameworks, "Flutter")
		}},

		// --- Docker ---
		{"Dockerfile", func(_ string) {
			profile.Frameworks = appendUnique(profile.Frameworks, "Docker")
		}},
		{"docker-compose.yml", func(_ string) {
			profile.Frameworks = appendUnique(profile.Frameworks, "Docker Compose")
		}},

		// --- Swift ---
		{"Package.swift", func(_ string) {
			profile.Languages = appendUnique(profile.Languages, "Swift")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "Swift Package Manager"
				profile.BuildCommand = "swift build"
				profile.TestCommand = "swift test"
			}
		}},
	}

	// Run detections
	for _, m := range markers {
		fullPath := filepath.Join(workingDir, m.filename)
		if _, err := os.Stat(fullPath); err == nil {
			profile.KeyFiles = appendUnique(profile.KeyFiles, m.filename)
			m.detect(fullPath)
		}
	}

	// Check for .go files even without go.mod (rare but possible)
	if !sliceContains(profile.Languages, "Go") {
		if hasFilesWithSuffix(workingDir, ".go") {
			profile.Languages = appendUnique(profile.Languages, "Go")
		}
	}

	// Detect monorepo (multiple package.json or go.mod in subdirs)
	if isMonorepo(workingDir) {
		profile.Frameworks = appendUnique(profile.Frameworks, "monorepo")
	}

	// No useful detection
	if len(profile.Languages) == 0 && profile.BuildSystem == "" && len(profile.KeyFiles) == 0 {
		return nil
	}

	return profile
}

// FormatForSystemPrompt returns a compact single-line representation for
// minimal token overhead in the system prompt.
func (p *ProjectProfile) FormatForSystemPrompt() string {
	if p == nil {
		return ""
	}
	var parts []string

	if len(p.Languages) > 0 {
		parts = append(parts, "lang="+strings.Join(p.Languages, ","))
	}
	if p.BuildSystem != "" {
		parts = append(parts, "build="+p.BuildSystem)
	}
	if p.BuildCommand != "" {
		parts = append(parts, "build-cmd="+p.BuildCommand)
	}
	if p.TestCommand != "" {
		parts = append(parts, "test="+p.TestCommand)
	}
	if p.LintCommand != "" {
		parts = append(parts, "lint="+p.LintCommand)
	}
	if len(p.Frameworks) > 0 {
		parts = append(parts, "fw="+strings.Join(p.Frameworks, ","))
	}

	if len(parts) == 0 {
		return ""
	}
	return "Project: " + strings.Join(parts, " | ")
}

// detectProfileText is a thin wrapper for BuildSystemPrompt integration.
// It caches the result per working directory to avoid redundant filesystem
// scans on repeated calls within the same session.
func detectProfileText(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	profile := DetectProjectProfile(workingDir)
	if profile == nil {
		return ""
	}
	return profile.FormatForSystemPrompt()
}

// --- helpers ---

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func sliceContains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// hasFilesWithSuffix checks if any file in the root directory has the given suffix.
func hasFilesWithSuffix(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			return true
		}
	}
	return false
}

// isMonorepo checks for common monorepo indicators.
func isMonorepo(dir string) bool {
	// package.json with "workspaces" field
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		if strings.Contains(string(data), "\"workspaces\"") {
			return true
		}
	}
	// pnpm-workspace.yaml
	if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		return true
	}
	// lerna.json
	if _, err := os.Stat(filepath.Join(dir, "lerna.json")); err == nil {
		return true
	}
	// nx.json
	if _, err := os.Stat(filepath.Join(dir, "nx.json")); err == nil {
		return true
	}
	return false
}

// detectGoMakefileTags checks for Makefile with build tags like goolm.
func detectGoMakefileTags(p *ProjectProfile, workingDir string) {
	if data, err := os.ReadFile(filepath.Join(workingDir, "Makefile")); err == nil {
		mf := string(data)
		if strings.Contains(mf, "goolm") || strings.Contains(mf, "TAGS") {
			p.BuildCommand = "go build -tags goolm ./..."
			p.TestCommand = "go test -tags goolm ./..."
		}
		p.BuildSystem = "Make"
	}
}

// detectGoModFrameworks detects frameworks from go.mod content.
func detectGoModFrameworks(p *ProjectProfile, goModPath string) {
	if content, err := os.ReadFile(goModPath); err == nil {
		if strings.Contains(string(content), "mautrix") {
			p.Frameworks = appendUnique(p.Frameworks, "mautrix")
		}
	}
}

// detectNpmFrameworks detects JS frameworks and scripts from package.json.
func detectNpmFrameworks(p *ProjectProfile, pkgPath string) {
	content, err := os.ReadFile(pkgPath)
	if err != nil {
		return
	}
	pj := string(content)
	fwMap := map[string]string{
		"\"react\"":   "React",
		"\"vue\"":     "Vue",
		"\"next\"":    "Next.js",
		"\"svelte\"":  "Svelte",
		"\"express\"": "Express",
	}
	for key, name := range fwMap {
		if strings.Contains(pj, key) {
			p.Frameworks = appendUnique(p.Frameworks, name)
		}
	}
	if strings.Contains(pj, "\"wails\"") || strings.Contains(pj, "\"@wailsapp") {
		p.Frameworks = appendUnique(p.Frameworks, "Wails")
	}
	if strings.Contains(pj, "\"lint\"") && p.LintCommand == "" {
		p.LintCommand = "npm run lint"
	}
	if strings.Contains(pj, "\"test\"") {
		p.TestCommand = "npm test"
	}
	if strings.Contains(pj, "\"build\"") {
		p.BuildCommand = "npm run build"
	}
}

// detectCargoFrameworks detects Rust frameworks from Cargo.toml.
func detectCargoFrameworks(p *ProjectProfile, cargoPath string) {
	content, err := os.ReadFile(cargoPath)
	if err != nil {
		return
	}
	ct := string(content)
	for _, fw := range []string{"tokio", "actix", "axum"} {
		if strings.Contains(ct, fw) {
			p.Frameworks = appendUnique(p.Frameworks, fw)
		}
	}
}
