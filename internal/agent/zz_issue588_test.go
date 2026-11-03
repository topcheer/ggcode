package agent

import (
	"testing"
)

func TestIssue588_Bug2_MakefileTampering(t *testing.T) {
	tests := []struct {
		name     string
		makefile string
		want     bool
	}{
		{
			name: "no tampering - normal makefile",
			makefile: `
test:
	go test ./...
build:
	go build ./...
`,
			want: false,
		},
		{
			name: "tampering - test target replaced with echo",
			makefile: `
test:
	@echo "all tests passed"
build:
	go build ./...
`,
			want: true,
		},
		{
			name: "tampering - test target replaced with true",
			makefile: `
test:
	@true
build:
	go build ./...
`,
			want: true,
		},
		{
			name: "tampering - test target replaced with exit 0",
			makefile: `
test:
	@exit 0
build:
	go build ./...
`,
			want: true,
		},
		{
			name: "tampering - test target replaced with pass",
			makefile: `
test:
	@pass
build:
	go build ./...
`,
			want: true,
		},
		{
			name: "tampering - build target replaced with echo",
			makefile: `
test:
	go test ./...
build:
	@echo "build successful"
`,
			want: true,
		},
		{
			name: "tampering - both targets replaced",
			makefile: `
test:
	@true
build:
	@exit 0
`,
			want: true,
		},
		{
			name: "legitimate - test target disabled (commented, not tampered)",
			makefile: `
# test:
# 	go test ./...
build:
	go build ./...
`,
			want: false,
		},
		{
			name: "legitimate - target with echo before real command",
			makefile: `
test:
	@echo "Running tests..."
	go test ./...
`,
			want: false,
		},
		{

			name: "tampering - test target deleted entirely",
			makefile: `
.PHONY: build test
build:
	go build ./...
`,
			want: true,
		},
		{

			name: "legitimate - pure-build Makefile (no test target ever)",
			makefile: `
build:
	go build ./...
`,
			want: false,
		},
		{
			name: "legitimate - only helper targets edited",
			makefile: `
clean:
	rm -rf bin
install:
	go install ./...
test:
	go test ./...
`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got := hasMakefileTamperingContent(tt.makefile)
			if got != tt.want {
				t.Errorf("hasMakefileTamperingContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssue588_Bug1_SkipMarkerRemovalExemptions(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantFlag bool
	}{
		{
			name:     "grep investigation - exempt",
			command:  "grep -rn 't.Skip(' .",
			wantFlag: false,
		},
		{
			name:     "sed removing skip marker - exempt",
			command:  "sed -i 's/t.Skip(\"bug #123\")/t.Log(\"test disabled temporarily\")/' foo_test.go",
			wantFlag: false,
		},
		{
			name:     "sed removing t.Skip without quotes - exempt",
			command:  `sed -i 's/t.Skip(/t.Log(/' foo_test.go`,
			wantFlag: false,
		},
		{
			name:     "sed removing @pytest.mark.skip - exempt",
			command:  `sed -i 's/@pytest.mark.skip//' test_foo.py`,
			wantFlag: false,
		},
		{
			name:     "git log -S investigation - exempt",
			command:  "git log -S't.Skip(' --oneline",
			wantFlag: false,
		},
		{
			name:     "git log -G investigation - exempt",
			command:  "git log -G'@skip' --oneline",
			wantFlag: false,
		},
		{
			name:     "awk removing skip marker - exempt",
			command:  `awk '{gsub(/t.Skip\(/, "t.Log("); print}' foo_test.go > tmp && mv tmp foo_test.go`,
			wantFlag: false,
		},
		{
			name:     "awk removing escaped skip - exempt",
			command:  `awk '{gsub(/t\.Skip\(/, "t.Log("); print}' foo_test.go > tmp && mv tmp foo_test.go`,
			wantFlag: false,
		},
		{
			name:     "sed adding skip marker - NOT exempt (gaming)",
			command:  `sed -i 's/func TestX()/@skip\nfunc TestX()/' test.go`,
			wantFlag: true,
		},
		{
			name:     "echo skip marker - NOT exempt (gaming)",
			command:  `echo "@skip" >> test.py`,
			wantFlag: true,
		},
		{
			name:     "git grep investigation - exempt",
			command:  "git grep '@skip' -- '*.py'",
			wantFlag: false,
		},
		{
			name:     "sed removing xtest - exempt",
			command:  `sed -i 's/xtest(/test(/' foo_test.go`,
			wantFlag: false,
		},
		{
			name:     "sed removing xdescribe - exempt",
			command:  `sed -i 's/xdescribe(/describe(/' test.js`,
			wantFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSkipMarkersInCommands([]string{tt.command})
			if got != tt.wantFlag {
				t.Errorf("hasSkipMarkersInCommands(%q) = %v, want %v", tt.command, got, tt.wantFlag)
			}
		})
	}
}

func TestIssue588_Bug3_TestWritingTaskVocabulary(t *testing.T) {
	tests := []struct {
		name       string
		userPrompt string
		want       bool
	}{
		{
			name:       "existing: test keyword",
			userPrompt: "Write a test for the auth function",
			want:       true,
		},
		{
			name:       "existing: 测试 keyword",
			userPrompt: "为认证函数写测试",
			want:       true,
		},
		{
			name:       "new: spec keyword",
			userPrompt: "Add specs for the user API",
			want:       true,
		},
		{
			name:       "new: specs keyword",
			userPrompt: "Update specs for payment module",
			want:       true,
		},
		{
			name:       "new: specification keyword",
			userPrompt: "Write specification tests for order flow",
			want:       true,
		},
		{
			name:       "new: coverage keyword",
			userPrompt: "Add coverage for the missing paths",
			want:       true,
		},
		{
			name:       "new: test coverage keyword",
			userPrompt: "Improve test coverage to 80%",
			want:       true,
		},
		{
			name:       "new: 单测 keyword",
			userPrompt: "补齐单测",
			want:       true,
		},
		{
			name:       "new: 单元测试 keyword",
			userPrompt: "补充单元测试",
			want:       true,
		},
		{
			name:       "non-test: feature request",
			userPrompt: "Add user authentication feature",
			want:       false,
		},
		{
			name:       "non-test: bug fix",
			userPrompt: "Fix the null pointer exception in login",
			want:       false,
		},
		{
			name:       "edge: contains 'spec' but not spec-related",
			userPrompt: "Inspect the specs of the server (hardware specs)",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTestWritingTask(tt.userPrompt)
			if got != tt.want {
				t.Errorf("isTestWritingTask(%q) = %v, want %v", tt.userPrompt, got, tt.want)
			}
		})
	}
}

func TestIssue588_Bug4_StripTestSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"foo/bar_test.go", "foo/bar.go"},
		{"utils.test.ts", "utils.ts"},
		{"component.spec.tsx", "component.tsx"},
		{"foo_test.py", "foo.py"},
		{"Test.java", "Test.java"},
		{"module_test.rs", "module.rs"},
		{"lib_test.c", "lib.c"},
		{"app_test.cpp", "app.cpp"},
		{"file.test.kt", "file.kt"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripTestSuffix(tt.input)
			if got != tt.want {
				t.Errorf("stripTestSuffix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIssue588_Bug5_ConfigSuffixes(t *testing.T) {
	tests := []struct {
		path         string
		wantIsConfig bool
	}{
		{"README.md", false},
		{"LICENSE.txt", false},
		{"Dockerfile", true},
		{"dockerfile", true},
		{"Dockerfile.prod", false},
		{"mydockerfile", false},

		{"Makefile", false},
		{"Cargo.toml", true},
		{"config.yaml", true},
		{"go.sum", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isConfigOrLockFile(tt.path)
			if got != tt.wantIsConfig {
				t.Errorf("isConfigOrLockFile(%q) = %v, want %v", tt.path, got, tt.wantIsConfig)
			}
		})
	}
}

func TestIssue588_Bug5_DockerfileCaseInsensitive(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"exact match", "Dockerfile", true},
		{"lowercase", "dockerfile", true},
		{"mixed case", "DockerFile", true},
		{"all caps", "DOCKERFILE", true},
		{"with path", "./Dockerfile", true},
		{"with path lowercase", "./dockerfile", true},
		{"suffix variant - NOT config", "Dockerfile.prod", false},
		{"prefix variant - NOT config", "myDockerfile", false},
		{"in dir - NOT config", "mydockerfile", false},
		{"random case prefix - NOT config", "prodDockerfile", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConfigOrLockFile(tt.path)
			if got != tt.want {
				t.Errorf("isConfigOrLockFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIssue588_Bug2_MakefileCIConfigPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"Makefile", false},
		{"makefile", false},
		{"./Makefile", false},
		{".github/workflows/test.yml", true},
		{"pytest.ini", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isCIConfigPath(tt.path)
			if got != tt.want {
				t.Errorf("isCIConfigPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
