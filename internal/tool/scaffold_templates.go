package tool

import (
	"fmt"
	"strings"
)

// goTemplate generates a Go project skeleton.
func goTemplate(name, rootDir, modulePath string, ci, docker bool) []scaffoldFile {
	if modulePath == "" {
		modulePath = "github.com/example/" + sanitizeModuleName(name)
	}
	var files []scaffoldFile

	// Determine the subdirectory for Go source files.
	// Convention: cmd/<name>/main.go
	cmdDir := "cmd/" + sanitizeModuleName(name)

	files = append(files, scaffoldFile{
		Path: "go.mod",
		Content: fmt.Sprintf(`module %s

go 1.23
`, modulePath),
	})

	files = append(files, scaffoldFile{
		Path: cmdDir + "/main.go",
		Content: fmt.Sprintf(`package main

import "fmt"

func main() {
	fmt.Println("Hello from %s!")
}
`, name),
	})

	files = append(files, scaffoldFile{
		Path:    cmdDir + "/main_test.go",
		Content: testFileGo(name),
	})

	files = append(files, scaffoldFile{
		Path: "Makefile",
		Content: fmt.Sprintf(`.PHONY: build test lint run

build:
	go build -o bin/%s ./%s

test:
	go test ./...

lint:
	go vet ./...

run:
	go run ./%s
`, name, cmdDir, cmdDir),
	})

	files = append(files, scaffoldFile{
		Path:    ".gitignore",
		Content: goGitignore(),
	})

	files = append(files, scaffoldFile{
		Path:    "README.md",
		Content: readmeContent(name, "Go"),
	})

	if ci {
		files = append(files, scaffoldFile{
			Path:    ".github/workflows/ci.yml",
			Content: goCI(),
		})
	}

	if docker {
		files = append(files, scaffoldFile{
			Path: "Dockerfile",
			Content: fmt.Sprintf(`FROM golang:1.23-alpine AS builder
WORKDIR /app
# #865: fresh scaffold has no dependencies, so no go.sum exists — COPY go.mod only.
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /%s ./%s

FROM alpine:3.19
COPY --from=builder /%s /%s
ENTRYPOINT ["/%s"]
`, name, cmdDir, name, name, name),
		})
	}

	return files
}

// tsTemplate generates a TypeScript/Node.js project skeleton.
func tsTemplate(name, rootDir string, ci, docker bool) []scaffoldFile {
	var files []scaffoldFile

	files = append(files, scaffoldFile{
		Path: "package.json",
		Content: fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "description": "%s",
  "main": "dist/index.js",
  "scripts": {
    "build": "tsc",
    "dev": "ts-node src/index.ts",
    "test": "jest",
    "lint": "eslint src/"
  },
  "license": "MIT"
}
`, name, name),
	})

	files = append(files, scaffoldFile{
		Path: "tsconfig.json",
		Content: `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "commonjs",
    "lib": ["ES2022"],
    "outDir": "./dist",
    "rootDir": "./src",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true
  },
  "include": ["src/**/*"],
  "exclude": ["node_modules", "dist"]
}
`,
	})

	files = append(files, scaffoldFile{
		Path: "src/index.ts",
		Content: fmt.Sprintf(`console.log("Hello from %s!");
`, name),
	})

	files = append(files, scaffoldFile{
		Path:    "src/index.test.ts",
		Content: testFileTS(name),
	})

	files = append(files, scaffoldFile{
		Path:    ".gitignore",
		Content: tsGitignore(),
	})

	files = append(files, scaffoldFile{
		Path:    "README.md",
		Content: readmeContent(name, "TypeScript"),
	})

	if ci {
		files = append(files, scaffoldFile{
			Path:    ".github/workflows/ci.yml",
			Content: tsCI(),
		})
	}

	if docker {
		files = append(files, scaffoldFile{
			Path: "Dockerfile",
			Content: fmt.Sprintf(`FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
# #865: no package-lock.json is generated, so npm ci fails on a fresh scaffold.
RUN npm install
COPY . .
RUN npm run build
CMD ["node", "dist/index.js"]
`),
		})
	}

	return files
}

// pythonTemplate generates a Python project skeleton.
func pythonTemplate(name, rootDir string, ci, docker bool) []scaffoldFile {
	pkgName := sanitizePyModuleName(name)
	var files []scaffoldFile

	files = append(files, scaffoldFile{
		Path: "pyproject.toml",
		Content: fmt.Sprintf(`[build-system]
requires = ["setuptools>=64"]
build-backend = "setuptools.backends._legacy:_Backend"

[project]
name = "%s"
version = "0.1.0"
description = "%s"
requires-python = ">=3.10"

[tool.pytest.ini_options]
testpaths = ["tests"]
`, name, name),
	})

	files = append(files, scaffoldFile{
		Path:    pkgName + "/__init__.py",
		Content: fmt.Sprintf(`"""%s package."""\n`, name),
	})

	files = append(files, scaffoldFile{
		Path: pkgName + "/main.py",
		Content: fmt.Sprintf(`def main() -> None:
    print("Hello from %s!")


if __name__ == "__main__":
    main()
`, name),
	})

	files = append(files, scaffoldFile{
		Path:    "tests/test_main.py",
		Content: testFilePy(name),
	})

	files = append(files, scaffoldFile{
		Path:    ".gitignore",
		Content: pythonGitignore(),
	})

	files = append(files, scaffoldFile{
		Path:    "README.md",
		Content: readmeContent(name, "Python"),
	})

	if ci {
		files = append(files, scaffoldFile{
			Path:    ".github/workflows/ci.yml",
			Content: pythonCI(),
		})
	}

	if docker {
		files = append(files, scaffoldFile{
			Path: "Dockerfile",
			Content: fmt.Sprintf(`FROM python:3.12-slim
WORKDIR /app
COPY pyproject.toml .
RUN pip install -e .
COPY . .
CMD ["python", "-m", "%s.main"]
`, pkgName),
		})
	}

	return files
}

// rustTemplate generates a Rust project skeleton.
func rustTemplate(name, rootDir string, ci, docker bool) []scaffoldFile {
	var files []scaffoldFile

	files = append(files, scaffoldFile{
		Path: "Cargo.toml",
		Content: fmt.Sprintf(`[package]
name = "%s"
version = "0.1.0"
edition = "2021"

[dependencies]
`, sanitizeRustName(name)),
	})

	files = append(files, scaffoldFile{
		Path: "src/main.rs",
		Content: fmt.Sprintf(`fn main() {
    println!("Hello from %s!");
}
`, name),
	})

	files = append(files, scaffoldFile{
		Path: "src/lib.rs",
		Content: fmt.Sprintf(`//! %s library module.
`, name),
	})

	files = append(files, scaffoldFile{
		Path:    "tests/integration_test.rs",
		Content: testFileRust(name),
	})

	files = append(files, scaffoldFile{
		Path:    ".gitignore",
		Content: rustGitignore(),
	})

	files = append(files, scaffoldFile{
		Path:    "README.md",
		Content: readmeContent(name, "Rust"),
	})

	if ci {
		files = append(files, scaffoldFile{
			Path:    ".github/workflows/ci.yml",
			Content: rustCI(),
		})
	}

	if docker {
		files = append(files, scaffoldFile{
			Path: "Dockerfile",
			Content: fmt.Sprintf(`FROM rust:1.78-slim
WORKDIR /app
COPY . .
RUN cargo build --release
# #865: interpolate the crate name (binary is sanitizeRustName(name)); the
# raw placeholder made docker run fail with executable not found.
CMD ["./target/release/%s"]
`, sanitizeRustName(name)),
		})
	}

	return files
}

// --- Helpers ---

func sanitizeModuleName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

func sanitizePyModuleName(name string) string {
	return strings.ReplaceAll(sanitizeModuleName(name), "-", "_")
}

func sanitizeRustName(name string) string {
	return sanitizePyModuleName(name)
}

func readmeContent(name, lang string) string {
	return fmt.Sprintf(`# %s

A %s project.

## Getting Started

### Prerequisites

- %s toolchain

### Build

See the Makefile / package.json / pyproject.toml for available commands.

### Test

Run the test suite for this project.

## License

MIT
`, name, lang, lang)
}

// --- Test files ---

func testFileGo(name string) string {
	return fmt.Sprintf(`package main

import "testing"

func TestMainPlaceholder(t *testing.T) {
	// Verify the project name is non-empty.
	if "%s" == "" {
		t.Error("project name should not be empty")
	}
}
`, name)
}

func testFileTS(name string) string {
	return `import { describe, test, expect } from '@jest/globals';

describe('placeholder', () => {
  test('should pass', () => {
    expect(true).toBe(true);
  });
});
`
}

func testFilePy(name string) string {
	return `def test_placeholder():
    """Verify the test suite runs."""
    assert True
`
}

func testFileRust(name string) string {
	return `#[test]
fn test_placeholder() {
    assert!(true);
}
`
}

// --- .gitignore files ---

func goGitignore() string {
	return `# Binaries
bin/
*.exe

# Go workspace
go.work
go.work.sum

# IDE
.idea/
.vscode/
*.swp
`
}

func tsGitignore() string {
	return `# Dependencies
node_modules/

# Build output
dist/
build/

# Environment
.env
.env.local

# IDE
.idea/
.vscode/
*.swp
`
}

func pythonGitignore() string {
	return `# Python
__pycache__/
*.py[cod]
*.egg-info/
dist/
build/
.eggs/

# Virtual environments
venv/
.venv/
env/

# IDE
.idea/
.vscode/
*.swp
`
}

func rustGitignore() string {
	return `# Rust build artifacts
target/

# IDE
.idea/
.vscode/
*.swp
`
}

// --- CI files ---

func goCI() string {
	return `name: CI

on:
  push:
    branches: [main, master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go vet ./...
      - run: go test ./...
`
}

func tsCI() string {
	return `name: CI

on:
  push:
    branches: [main, master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm ci
      - run: npm run lint
      - run: npm test
`
}

func pythonCI() string {
	return `name: CI

on:
  push:
    branches: [main, master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        python-version: ['3.10', '3.11', '3.12']
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: ${{ matrix.python-version }}
      - run: pip install -e ".[dev]" || pip install -e .
      - run: pytest
`
}

func rustCI() string {
	return `name: CI

on:
  push:
    branches: [main, master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@stable
      - run: cargo fmt --all -- --check
      - run: cargo clippy -- -D warnings
      - run: cargo test
`
}
