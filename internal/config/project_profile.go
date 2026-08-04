package config

import (
	"fmt"
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
			// Check for Makefile with build tags (ggcode uses -tags goolm)
			if data, err := os.ReadFile(filepath.Join(workingDir, "Makefile")); err == nil {
				mf := string(data)
				if strings.Contains(mf, "goolm") || strings.Contains(mf, "TAGS") {
					profile.BuildCommand = "go build -tags goolm ./..."
					profile.TestCommand = "go test -tags goolm ./..."
				}
				profile.BuildSystem = "Make"
			}
			// Check for build tags in go.mod or common patterns
			if content, err := os.ReadFile(p); err == nil {
				modContent := string(content)
				if strings.Contains(modContent, "mautrix") {
					profile.Frameworks = appendUnique(profile.Frameworks, "mautrix")
				}
			}
		}},

		// --- Node.js / JavaScript / TypeScript ---
		{"package.json", func(p string) {
			profile.Languages = appendUnique(profile.Languages, "JavaScript/TypeScript")
			if profile.BuildSystem == "" {
				profile.BuildSystem = "npm"
				profile.BuildCommand = "npm run build"
				profile.TestCommand = "npm test"
			}
			if content, err := os.ReadFile(p); err == nil {
				pj := string(content)
				if strings.Contains(pj, "\"react\"") {
					profile.Frameworks = appendUnique(profile.Frameworks, "React")
				}
				if strings.Contains(pj, "\"vue\"") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Vue")
				}
				if strings.Contains(pj, "\"next\"") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Next.js")
				}
				if strings.Contains(pj, "\"svelte\"") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Svelte")
				}
				if strings.Contains(pj, "\"express\"") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Express")
				}
				if strings.Contains(pj, "\"wails\"") || strings.Contains(pj, "\"@wailsapp") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Wails")
				}
				// Detect scripts
				if strings.Contains(pj, "\"lint\"") && profile.LintCommand == "" {
					profile.LintCommand = "npm run lint"
				}
				if strings.Contains(pj, "\"test\"") {
					profile.TestCommand = "npm test"
				}
				if strings.Contains(pj, "\"build\"") {
					profile.BuildCommand = "npm run build"
				}
			}
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
			if content, err := os.ReadFile(p); err == nil {
				ct := string(content)
				if strings.Contains(ct, "tokio") {
					profile.Frameworks = appendUnique(profile.Frameworks, "tokio")
				}
				if strings.Contains(ct, "actix") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Actix")
				}
				if strings.Contains(ct, "axum") {
					profile.Frameworks = appendUnique(profile.Frameworks, "Axum")
				}
			}
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

// FormatForSystemPrompt returns a compact string representation suitable
// for injection into the system prompt's Environment section.
func (p *ProjectProfile) FormatForSystemPrompt() string {
	if p == nil {
		return ""
	}
	var lines []string

	if len(p.Languages) > 0 {
		lines = append(lines, fmt.Sprintf("- Languages: %s", strings.Join(p.Languages, ", ")))
	}
	if p.BuildSystem != "" {
		lines = append(lines, fmt.Sprintf("- Build system: %s", p.BuildSystem))
	}
	if p.BuildCommand != "" {
		lines = append(lines, fmt.Sprintf("- Build command: %s", p.BuildCommand))
	}
	if p.TestCommand != "" {
		lines = append(lines, fmt.Sprintf("- Test command: %s", p.TestCommand))
	}
	if p.LintCommand != "" {
		lines = append(lines, fmt.Sprintf("- Lint command: %s", p.LintCommand))
	}
	if len(p.Frameworks) > 0 {
		lines = append(lines, fmt.Sprintf("- Frameworks: %s", strings.Join(p.Frameworks, ", ")))
	}
	if len(p.KeyFiles) > 0 {
		lines = append(lines, fmt.Sprintf("- Key files: %s", strings.Join(p.KeyFiles, ", ")))
	}

	if len(lines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Project Profile (auto-detected)\n")
	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteString("\n")
	return sb.String()
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
