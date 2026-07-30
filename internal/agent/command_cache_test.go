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
		// Build/test commands — cacheable
		{"make", true},
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

		// Non-cacheable: file-modifying commands
		{"rm -rf /tmp/old", false},
		{"mv file.go file2.go", false},
		{"cp src.go dst.go", false},
		{"touch newfile.go", false},
		{"mkdir newdir", false},

		// Non-cacheable: network commands
		{"curl https://example.com", false},
		{"wget https://example.com/file", false},

		// Non-cacheable: git state-modifying commands
		{"git add .", false},
		{"git commit -m test", false},
		{"git stash", false},

		// Non-cacheable: package management
		{"npm install", false},
		{"go get github.com/pkg/errors", false},
		{"pip install flask", false},

		// Non-cacheable: commands with pipes/redirects
		{"go test ./... | tee output.txt", false},
		{"echo hello > file.txt", false},
		{"go test >> output.txt", false},

		// Non-cacheable: go generate/mod
		{"go generate ./...", false},
		{"go mod tidy", false},

		// Non-cacheable: misc non-build commands
		{"echo hello", false},
		{"ls -la", false},
		{"cat file.go", false},
		{"grep pattern file.go", false},
		{"date", false},
		{"ps aux", false},
		{"", false},

		// Cacheable with cd prefix — the final segment is checked
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

	// Non-cacheable command should not be stored or retrieved.
	cc.put("echo hello", "", tool.Result{Content: "hello"})
	if _, hit := cc.get("echo hello", ""); hit {
		t.Fatal("non-cacheable command should not be cached")
	}

	// Cacheable command — first get is a miss.
	if _, hit := cc.get("make test", ""); hit {
		t.Fatal("expected cache miss on first get")
	}

	// Store and retrieve.
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

	// Cache hit before invalidation.
	if _, hit := cc.get("make build", ""); !hit {
		t.Fatal("expected cache hit before invalidation")
	}

	// Invalidate (simulates file edit).
	cc.invalidate()

	// Cache miss after invalidation.
	if _, hit := cc.get("make build", ""); hit {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestCommandCache_DifferentWorkDirs(t *testing.T) {
	cc := newCommandCache()
	result1 := tool.Result{Content: "from dir1"}
	cc.put("make test", "/dir1", result1)

	// Same command, different workDir — should be a miss.
	if _, hit := cc.get("make test", "/dir2"); hit {
		t.Fatal("expected miss for different workDir")
	}

	// Same command, same workDir — should be a hit.
	got, hit := cc.get("make test", "/dir1")
	if !hit {
		t.Fatal("expected hit for same workDir")
	}
	if got.Content != "from dir1" {
		t.Errorf("wrong content: %q", got.Content)
	}
}

func TestCommandCache_MaxAgeExpiry(t *testing.T) {
	// Temporarily reduce max age for testing.
	oldAge := maxCommandCacheAge
	t.Cleanup(func() { maxCommandCacheAge = oldAge })

	cc := newCommandCache()
	result := tool.Result{Content: "old result"}
	cc.put("make test", "", result)

	// Manually age the entry.
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

	// Fill beyond max entries.
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

	// Store a result.
	result := tool.Result{Content: "BUILD OK"}
	a.commandCache.put("make build", "", result)

	// Retrieve via checkCommandCache — should be annotated.
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

	// Store a non-cacheable command (put should be a no-op).
	a.commandCache.put("echo hello", "", tool.Result{Content: "hello"})

	args := []byte(`{"command":"echo hello"}`)
	if _, hit := a.checkCommandCache("run_command", args); hit {
		t.Fatal("non-cacheable command should not hit")
	}
}
