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

var (
	maxCommandCacheAge     = 10 * time.Minute
	maxCommandCacheEntries = 32
)

type commandCache struct {
	mu      sync.Mutex
	entries map[string]*commandCacheEntry
	editGen int64
}

type commandCacheEntry struct {
	result   tool.Result
	editGen  int64
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

func isCacheableCommand(command string) bool {

	cmd := strings.TrimSpace(stripLeadingShellComment(command))
	if cmd == "" {
		return false
	}

	segments := splitShellSegments(cmd)
	if len(segments) == 0 {
		return false
	}

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
	for _, seg := range segments {

		if strings.ContainsAny(seg, "`<>&|") || strings.Contains(seg, "$(") {
			return false
		}
		for _, p := range excludePrefixes {
			if strings.HasPrefix(seg, p) {
				return false
			}
		}
	}

	cacheablePrefixes := []string{
		"make build", "make test", "make lint", "make check", "make verify", "make ci",
		"make\tbuild", "make\ttest", "make\tlint", "make\tcheck", "make\tverify", "make\tci",

		"go build", "go test", "go vet", "go check", "go lint",
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
	for _, seg := range segments {
		if !isCacheableSegment(seg, cacheablePrefixes) {
			return false
		}
	}
	return true
}

func splitShellSegments(cmd string) []string {
	raw := strings.FieldsFunc(cmd, func(r rune) bool { return r == '\n' || r == ';' })
	var out []string
	for _, part := range raw {
		for _, seg := range splitAndOr(part) {
			seg = strings.TrimSpace(seg)
			for strings.HasPrefix(seg, "cd ") {
				rest := seg[3:]
				if i := strings.IndexAny(rest, " \t"); i >= 0 {
					seg = strings.TrimSpace(rest[i+1:])
				} else {
					seg = ""
				}
			}
			if seg != "" {
				out = append(out, seg)
			}
		}
	}
	return out
}

func splitAndOr(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "&&")
		j := strings.Index(s, "||")
		if i < 0 && j < 0 {
			out = append(out, s)
			return out
		}
		k := i
		if k < 0 || (j >= 0 && j < k) {
			k = j
		}
		out = append(out, s[:k])
		s = s[k+2:]
	}
}

func isCacheableSegment(seg string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(seg, p) {
			return true
		}
	}
	return false
}

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

	if entry.editGen != gen {
		return tool.Result{}, false
	}

	if time.Since(entry.storedAt) > maxCommandCacheAge {
		return tool.Result{}, false
	}
	return entry.result, true
}

func (cc *commandCache) put(command, workDir string, result tool.Result) {
	if !isCacheableCommand(command) {
		return
	}
	key := cmdCacheKey(command, workDir)
	cc.mu.Lock()
	defer cc.mu.Unlock()

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

func (a *Agent) storeCommandResult(name string, args []byte, result tool.Result) {
	if name != "run_command" {
		return
	}

	if result.IsError {
		return
	}
	command, workDir := parseRunCommandArgs(args)
	if command == "" {
		return
	}
	a.commandCache.put(command, workDir, result)
}
