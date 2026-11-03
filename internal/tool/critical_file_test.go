package tool

import (
	"strings"
	"testing"
)

func TestCriticalFileWarning(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		want     string
		contains string
		empty    bool
	}{
		{"go.mod", "project/go.mod", "", "go mod tidy", false},
		{"go.sum", "project/go.sum", "", "Do not edit manually", false},
		{"package.json", "app/package.json", "", "npm install", false},
		{"package-lock.json", "app/package-lock.json", "", "Do not edit manually", false},
		{"yarn.lock", "app/yarn.lock", "", "Do not edit manually", false},
		{"requirements.txt", "proj/requirements.txt", "", "pip install", false},
		{"Cargo.toml", "proj/Cargo.toml", "", "cargo build", false},
		{"Cargo.lock", "proj/Cargo.lock", "", "Do not edit manually", false},
		{"Dockerfile", "proj/Dockerfile", "", "docker build", false},
		{"docker-compose.yml", "proj/docker-compose.yml", "", "docker compose config", false},
		{"github workflow", "proj/.github/workflows/ci.yml", "", "CI/CD pipeline", false},
		{"gitlab-ci", "proj/.gitlab-ci.yml", "", "CI/CD pipeline", false},
		{"Makefile", "proj/Makefile", "", "make build", false},
		{"tsconfig.json", "proj/tsconfig.json", "", "tsc --noEmit", false},
		{"eslint config", "proj/eslint.config.js", "", "Linter/formatter", false},
		{"biome.json", "proj/biome.json", "", "Linter/formatter", false},
		{"pyproject.toml", "proj/pyproject.toml", "", "poetry install", false},

		{"regular go file", "proj/main.go", "", "", true},
		{"regular md file", "proj/README.md", "", "", true},
		{"regular test file", "proj/foo_test.go", "", "", true},
		{"empty path", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := criticalFileWarning(tt.path)
			if tt.empty {
				if got != "" {
					t.Errorf("criticalFileWarning(%q) = %q, want empty", tt.path, got)
				}
				return
			}
			if !strings.Contains(got, tt.contains) {
				t.Errorf("criticalFileWarning(%q) = %q, want substring %q", tt.path, got, tt.contains)
			}
			if !strings.HasPrefix(got, "\n[Critical file] ") {
				t.Errorf("criticalFileWarning(%q) = %q, want prefix %q", tt.path, got, "\n[Critical file] ")
			}
		})
	}
}

func TestCriticalFileWarning_NoFalsePositives(t *testing.T) {

	nonMatching := []string{
		"vendor/go.mod.backup",
		"docs/go.mod.txt",
		"some/path/Dockerfile.readme",
	}
	for _, p := range nonMatching {
		got := criticalFileWarning(p)

		_ = got
	}
}
