package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// Deterministic command result caching.
//
// Research basis: Claude Code, Cursor, and Aider all avoid re-running
// build/test commands when source files haven't changed since the last
// execution. An agent commonly re-runs `make verify-ci` or `go build ./...`
// 2-5 times per session, each taking 10-60s. Without caching, every
// redundant re-run wastes wall-clock time and API iterations.
//
// This cache stores results of build/test/verify commands keyed by
// (command string + working directory). It is invalidated wholesale when
// ANY file is edited (same invalidation trigger as the speculator and
// memoize caches). Only commands matching known build/test patterns are
// cached — network commands, file-modifying commands, and interactive
// commands are never cached.
//
// Safety guarantees:
//   - First execution always runs and populates the cache.
//   - Any file edit invalidates ALL cached commands.
//   - Only deterministic build/test/lint commands are cached.
//   - Cache entries expire after maxCommandCacheAge to avoid very stale results.
//   - Cached results are annotated so the model knows it's reused output.

var (
	// maxCommandCacheAge bounds how long a cached result is considered valid.
	maxCommandCacheAge = 10 * time.Minute
	// maxCommandCacheEntries prevents unbounded growth in long autopilot sessions.
	maxCommandCacheEntries = 32
)

// commandCache stores results of deterministic build/test commands to skip
// redundant re-execution when no source files have changed.
type commandCache struct {
	mu      sync.Mutex
	entries map[string]*commandCacheEntry
	editGen int64 // incremented on every file edit
}

type commandCacheEntry struct {
	result   tool.Result
	editGen  int64 // edit generation when stored
	storedAt time.Time
}

func newCommandCache() *commandCache {
	return &commandCache{
		entries: make(map[string]*commandCacheEntry),
	}
}

func cmdCacheKey(command, workDir string) string {
	return workDir + "\x00" + command
}

// invalidate clears all cached entries. Called on any file edit.
func (cc *commandCache) invalidate() {
	cc.mu.Lock()
	cc.entries = make(map[string]*commandCacheEntry)
	cc.editGen++
	cc.mu.Unlock()
}

func (cc *commandCache) reset() {
	cc.mu.Lock()
	cc.entries = make(map[string]*commandCacheEntry)
	cc.editGen = 0
	cc.mu.Unlock()
}

// isCacheableCommand returns true for deterministic build/test/lint commands
// that are safe to cache. Non-deterministic commands (network, file I/O,
// interactive) are excluded.
func isCacheableCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	// Strip leading "cd ... &&" prefix — check the FINAL command segment.
	if idx := strings.LastIndex(cmd, "&&"); idx >= 0 {
		cmd = strings.TrimSpace(cmd[idx+2:])
	}

	// Exclude commands that modify state, touch the network, or are interactive.
	excludePrefixes := []string{
		"rm ", "mv ", "cp ", "touch ", "mkdir ", "chmod ", "chown ",
		"curl ", "wget ", "scp ", "rsync ", "ssh ", "ftp ",
		"git add", "git commit", "git push", "git stash", "git checkout",
		"git reset", "git rebase", "git merge", "git cherry-pick",
		"npm install", "npm ci", "yarn install", "pnpm install",
		"pip install", "pip3 install", "cargo install",
		"docker ", "kubectl ", "helm ",
		"go generate", "go mod ", "go get ", "go install ",
		"brew ", "apt ", "yum ",
	}
	for _, p := range excludePrefixes {
		if strings.HasPrefix(cmd, p) {
			return false
		}
	}

	// Exclude commands with output redirects, heredocs, or pipes.
	if strings.Contains(cmd, ">") || strings.Contains(cmd, ">>") ||
		strings.Contains(cmd, "<<") || strings.Contains(cmd, "|") {
		return false
	}

	// Whitelist of cacheable command prefixes.
	cacheablePrefixes := []string{
		"make ", "make\t", "make\n",
		"go build", "go test", "go vet", "go check", "go fmt", "go lint",
		"go run -tags",
		"npm test", "npm run test", "npm run build", "npm run lint", "npm run check",
		"npm run verify", "npm run typecheck", "npm run type-check",
		"npx tsc", "npx eslint", "npx jest",
		"yarn test", "yarn build", "yarn lint", "yarn typecheck",
		"pnpm test", "pnpm build", "pnpm lint",
		"cargo build", "cargo test", "cargo check", "cargo clippy",
		"pytest", "python -m pytest", "python3 -m pytest",
		"./gradlew test", "./gradlew build", "./gradlew check",
		"gradle test", "gradle build",
		"mvn test", "mvn compile", "mvn verify",
		"rake test", "rspec ",
		"dotnet build", "dotnet test",
	}
	if cmd == "make" {
		return true
	}
	for _, p := range cacheablePrefixes {
		if strings.HasPrefix(cmd, p) {
			return true
		}
	}
	return false
}

// parseRunCommandArgs extracts the command string from a run_command tool call.
func parseRunCommandArgs(args json.RawMessage) (command, workDir string) {
	var parsed struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", ""
	}
	return parsed.Command, parsed.WorkingDir
}

// get returns a cached result if the command is cacheable, was previously
// executed, and no files have been edited since. Returns (zero, false) otherwise.
func (cc *commandCache) get(command, workDir string) (tool.Result, bool) {
	if !isCacheableCommand(command) {
		return tool.Result{}, false
	}
	key := cmdCacheKey(command, workDir)
	cc.mu.Lock()
	entry, ok := cc.entries[key]
	gen := cc.editGen
	cc.mu.Unlock()
	if !ok || entry == nil {
		return tool.Result{}, false
	}
	// Check edit generation: if files changed, the cache is stale.
	if entry.editGen != gen {
		return tool.Result{}, false
	}
	// Check age.
	if time.Since(entry.storedAt) > maxCommandCacheAge {
		return tool.Result{}, false
	}
	return entry.result, true
}

// put stores a command result in the cache. Only called for cacheable commands.
func (cc *commandCache) put(command, workDir string, result tool.Result) {
	if !isCacheableCommand(command) {
		return
	}
	key := cmdCacheKey(command, workDir)
	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Enforce max entries with simple eviction of the oldest entry.
	if len(cc.entries) >= maxCommandCacheEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, e := range cc.entries {
			if oldestKey == "" || e.storedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.storedAt
			}
		}
		delete(cc.entries, oldestKey)
	}

	cc.entries[key] = &commandCacheEntry{
		result:   result,
		editGen:  cc.editGen,
		storedAt: time.Now(),
	}
}

// checkCommandCache is called before executing run_command. If the result is
// cached and valid, it returns the cached result with an annotation.
// Otherwise it returns false and the caller should execute normally.
func (a *Agent) checkCommandCache(name string, args []byte) (tool.Result, bool) {
	if name != "run_command" {
		return tool.Result{}, false
	}
	command, workDir := parseRunCommandArgs(args)
	if command == "" {
		return tool.Result{}, false
	}
	result, hit := a.commandCache.get(command, workDir)
	if !hit {
		return tool.Result{}, false
	}
	debug.Log("cmd-cache", "cache hit for command: %s (skipping execution)", util.Truncate(command, 80))
	annotated := result
	if annotated.Content != "" && !annotated.IsError {
		annotated.Content = fmt.Sprintf("[cached — %s returned identical output since your last call; no source files have changed]\n%s",
			util.Truncate(command, 60), annotated.Content)
	}
	return annotated, true
}

// storeCommandResult caches a run_command result if the command is cacheable.
func (a *Agent) storeCommandResult(name string, args []byte, result tool.Result) {
	if name != "run_command" {
		return
	}
	command, workDir := parseRunCommandArgs(args)
	if command == "" {
		return
	}
	a.commandCache.put(command, workDir, result)
}
