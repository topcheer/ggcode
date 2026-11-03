package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIsCacheableCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{

		{"make", false},
		{"make verify-ci", true},
		{"make build", true},
		{"go build ./...", true},
		{"go test ./internal/agent/", true},
		{"go vet ./...", true},
		{"npm test", true},
		{"npm run build", true},
		{"cargo build", true},
		{"cargo test", true},
		{"pytest", true},
		{"python -m pytest tests/", true},
		{"./gradlew test", true},
		{"mvn test", true},

		{"rm -rf /tmp/old", false},
		{"mv file.go file2.go", false},
		{"cp src.go dst.go", false},
		{"touch newfile.go", false},
		{"mkdir newdir", false},

		{"curl https://example.com", false},
		{"wget https://example.com/file", false},

		{"git add .", false},
		{"git commit -m test", false},
		{"git stash", false},

		{"npm install", false},
		{"go get github.com/pkg/errors", false},
		{"pip install flask", false},

		{"go test ./... | tee output.txt", false},
		{"echo hello > file.txt", false},
		{"go test >> output.txt", false},

		{"go generate ./...", false},
		{"go mod tidy", false},

		{"echo hello", false},
		{"ls -la", false},
		{"cat file.go", false},
		{"grep pattern file.go", false},
		{"date", false},
		{"ps aux", false},
		{"", false},

		{"cd /tmp && make test", true},
		{"cd src && go build ./...", true},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := isCacheableCommand(tt.cmd); got != tt.want {
				t.Errorf("isCacheableCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestCommandCache_BasicGetPut(t *testing.T) {
	cc := newCommandCache()

	cc.put("echo hello", "", tool.Result{Content: "hello"})
	if _, hit := cc.get("echo hello", ""); hit {
		t.Fatal("non-cacheable command should not be cached")
	}

	if _, hit := cc.get("make test", ""); hit {
		t.Fatal("expected cache miss on first get")
	}

	result := tool.Result{Content: "BUILD OK\nall tests passed"}
	cc.put("make test", "", result)
	got, hit := cc.get("make test", "")
	if !hit {
		t.Fatal("expected cache hit after put")
	}
	if got.Content != result.Content {
		t.Errorf("content mismatch: got %q want %q", got.Content, result.Content)
	}
}

func TestCommandCache_InvalidateOnFileEdit(t *testing.T) {
	cc := newCommandCache()
	result := tool.Result{Content: "BUILD OK"}
	cc.put("make build", "", result)

	if _, hit := cc.get("make build", ""); !hit {
		t.Fatal("expected cache hit before invalidation")
	}

	cc.invalidate()

	if _, hit := cc.get("make build", ""); hit {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestCommandCache_DifferentWorkDirs(t *testing.T) {
	cc := newCommandCache()
	result1 := tool.Result{Content: "from dir1"}
	cc.put("make test", "/dir1", result1)

	if _, hit := cc.get("make test", "/dir2"); hit {
		t.Fatal("expected miss for different workDir")
	}

	got, hit := cc.get("make test", "/dir1")
	if !hit {
		t.Fatal("expected hit for same workDir")
	}
	if got.Content != "from dir1" {
		t.Errorf("wrong content: %q", got.Content)
	}
}

func TestCommandCache_MaxAgeExpiry(t *testing.T) {

	oldAge := maxCommandCacheAge
	t.Cleanup(func() { maxCommandCacheAge = oldAge })

	cc := newCommandCache()
	result := tool.Result{Content: "old result"}
	cc.put("make test", "", result)

	cc.mu.Lock()
	for _, e := range cc.entries {
		e.storedAt = time.Now().Add(-20 * time.Minute)
	}
	cc.mu.Unlock()

	if _, hit := cc.get("make test", ""); hit {
		t.Fatal("expected cache miss for expired entry")
	}
}

func TestCommandCache_MaxEntries(t *testing.T) {
	cc := newCommandCache()

	for i := 0; i < maxCommandCacheEntries+5; i++ {
		cc.put("make test", "/dir"+string(rune('A'+i)), tool.Result{Content: "ok"})
	}

	cc.mu.Lock()
	count := len(cc.entries)
	cc.mu.Unlock()

	if count > maxCommandCacheEntries {
		t.Errorf("cache has %d entries, max is %d", count, maxCommandCacheEntries)
	}
}

func TestCheckCommandCache_NonRunCommand(t *testing.T) {
	a := &Agent{commandCache: newCommandCache()}
	if _, hit := a.checkCommandCache("read_file", []byte(`{"path":"/tmp"}`)); hit {
		t.Fatal("read_file should never hit command cache")
	}
}

func TestCheckCommandCache_CacheHitAnnotation(t *testing.T) {
	a := &Agent{commandCache: newCommandCache()}

	result := tool.Result{Content: "BUILD OK"}
	a.commandCache.put("make build", "", result)

	args := []byte(`{"command":"make build"}`)
	got, hit := a.checkCommandCache("run_command", args)
	if !hit {
		t.Fatal("expected cache hit")
	}
	if got.Content == "BUILD OK" {
		t.Error("cached result should be annotated, not returned as-is")
	}
}

func TestCheckCommandCache_NotCacheableCommand(t *testing.T) {
	a := &Agent{commandCache: newCommandCache()}

	a.commandCache.put("echo hello", "", tool.Result{Content: "hello"})

	args := []byte(`{"command":"echo hello"}`)
	if _, hit := a.checkCommandCache("run_command", args); hit {
		t.Fatal("non-cacheable command should not hit")
	}
}

func TestCommandCacheCompoundMutatingSegmentNotCacheable(t *testing.T) {
	if isCacheableCommand("git checkout . && go test ./...") {
		t.Fatal("compound with mutating first segment cached - rollback would be skipped")
	}
	if isCacheableCommand("git stash pop && go test ./...") {
		t.Fatal("stash-pop compound cached")
	}

	if !isCacheableCommand("cd /repo && go test ./internal/agent/") {
		t.Fatal("cd-then-test compound wrongly excluded")
	}

	if isCacheableCommand("make deploy") {
		t.Fatal("make deploy cached - repeat deploys swallowed")
	}
	if isCacheableCommand("go run -tags goolm ./cmd/gen") {
		t.Fatal("go run (arbitrary program) cached")
	}
	if !isCacheableCommand("make test") {
		t.Fatal("make test should stay cacheable")
	}
}

func TestIsCacheableCommandShellModel(t *testing.T) {
	notCacheable := []string{
		"go test ./... ; git checkout .",
		"go test ./... || git checkout .",
		"# reset\n\ngit checkout . && make test",
		"make deploy && make test",
		"cd /repo; rm -rf tmp && make test",
		"make",
		"go run ./cmd/x && make test",
	}
	for _, cmd := range notCacheable {
		if isCacheableCommand(cmd) {
			t.Errorf("isCacheableCommand(%q) = true, want false", cmd)
		}
	}
	cacheable := []string{
		"# Build and test\nmake test",
		"# verify\ngo build ./... && go test ./...",
		"cd /repo && make test",
		"make test", "go test ./...", "cargo check",
	}
	for _, cmd := range cacheable {
		if !isCacheableCommand(cmd) {
			t.Errorf("isCacheableCommand(%q) = false, want true", cmd)
		}
	}
}

func TestCommandCache1717SubstitutionAndErrors(t *testing.T) {
	for _, cmd := range []string{
		"go test $(git rev-parse --show-toplevel)/pkg",
		"go build ./... `pwd`",
		"make test < flags.txt",
		"go build ./pkg &",
	} {
		if isCacheableCommand(cmd) {
			t.Fatalf("substitution/background/redirection command must not be cacheable: %s", cmd)
		}
	}
	if !isCacheableCommand("go build ./internal/agent/") {
		t.Fatal("plain deterministic command must remain cacheable")
	}
	if !isCacheableCommand("go test ./pkg/ > /dev/null 2>&1 || true") {
		_ = 0
	}

	a := &Agent{commandCache: newCommandCache()}
	a.storeCommandResult("run_command", []byte(`{"command":"go build ./pkg/"}`), tool.Result{Content: "signal: killed", IsError: true})
	if _, hit := a.checkCommandCache("run_command", []byte(`{"command":"go build ./pkg/"}`)); hit {
		t.Fatal("failed execution must not be cached")
	}
	a.storeCommandResult("run_command", []byte(`{"command":"go build ./pkg/"}`), tool.Result{Content: "ok"})
	if _, hit := a.checkCommandCache("run_command", []byte(`{"command":"go build ./pkg/"}`)); !hit {
		t.Fatal("successful execution must still cache")
	}
}
