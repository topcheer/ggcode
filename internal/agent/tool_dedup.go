package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	toolDedupTTL        = 60 * time.Second
	toolDedupMaxEntries = 128
)

var mutatingToolNames = map[string]bool{
	"run_command":      true,
	"start_command":    true,
	"write_file":       true,
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"batch_replace":    true,
	"lsp_rename":       true,
	"file_ops":         true,
	"git_add":          true,
	"git_commit":       true,
	"git_revert":       true,
	"git_tag":          true,
	"git_stash":        true,
	"git_reset":        true,
	"git_checkout":     true,
	"git_remote":       true,
	"cron_create":      true,
	"cron_delete":      true,
	"cron_update":      true,
	"cron_pause":       true,
	"cron_resume":      true,
	"im":               true,
	"delegate":         true,
	"task_create":      true,
	"task_update":      true,
	"task_stop":        true,
	"browser":          true,
	"open":             true,
}

var fileMutatingTools = map[string]bool{
	"write_file":       true,
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"file_ops":         true,
	"batch_replace":    true,
	"lsp_rename":       true,

	"git_checkout": true,
	"git_stash":    true,
	"git_reset":    true,
	"git_revert":   true,
}

type toolDedupEntry struct {
	fingerprint string
	at          time.Time
	result      tool.Result
}

type toolDedupLedger struct {
	mu         sync.Mutex
	ttl        time.Duration
	epoch      uint64
	count      int
	table      map[string]toolDedupEntry
	crashTable map[string]time.Time
}

func newToolDedupLedger() *toolDedupLedger {
	return &toolDedupLedger{
		ttl:        toolDedupTTL,
		table:      make(map[string]toolDedupEntry, 8),
		crashTable: make(map[string]time.Time),
	}
}

func (a *Agent) dedupLedger() *toolDedupLedger {
	a.toolDedupOnce.Do(func() {
		if os.Getenv("GGCODE_TOOL_DEDUP") == "0" {
			a.toolDedup = nil
		}
	})
	return a.toolDedup
}

func isMutatingTool(name string) bool {
	if mutatingToolNames[name] {
		return true
	}
	return strings.HasPrefix(name, "mcp__")
}

func (l *toolDedupLedger) suppressDuplicate(name, args string) *tool.Result {
	if l == nil || !isMutatingTool(name) {
		return nil
	}
	fp := l.fingerprint(name, args)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(time.Now())
	e, ok := l.table[fp]
	if !ok {

		cfp := crashFingerprint(name, args)
		if at, cok := l.crashTable[cfp]; cok {
			if time.Since(at) > crashDedupTTL {
				delete(l.crashTable, cfp)
				return nil
			}
			debug.Log("agent", "tool-dedup: suppressed crash-window replay of %s (%.0fmin old)", name, time.Since(at).Minutes())
			return &tool.Result{Content: fmt.Sprintf(
				"[crash-window dedup] An identical mutating call %q succeeded %.0f minutes ago, before the previous run crashed; it was NOT re-executed to avoid duplicating the side effect. The pre-crash output was not persisted - verify the actual state (e.g. git log / message history) before deciding whether a genuine re-run is needed. Vary the arguments if you intentionally want to execute it again.\n\n",
				name, time.Since(at).Minutes()), IsError: false}
		}
		return nil
	}
	notice := fmt.Sprintf(
		"[suppressed-as-duplicate] Identical mutating tool call %q already executed %ds ago and returned success; its output is replayed below instead of re-executing the side effect. If repeating is intentional, change the arguments or wait before retrying.\n\n",
		name, int(time.Since(e.at).Seconds()))
	replayed := notice + e.result.Content
	debug.Log("agent", "tool-dedup: suppressed duplicate mutating call %s (%ds old)", name, int(time.Since(e.at).Seconds()))
	return &tool.Result{Content: replayed, IsError: false}
}

func (l *toolDedupLedger) record(name, args string, res tool.Result) {
	if l == nil || res.IsError {
		return
	}

	if name == "undo_edit" {
		l.mu.Lock()
		l.epoch++
		l.mu.Unlock()
		return
	}
	if !isMutatingTool(name) {
		return
	}
	now := time.Now()
	fp := l.fingerprint(name, args)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if l.count >= toolDedupMaxEntries {
		return
	}
	l.table[fp] = toolDedupEntry{fingerprint: fp, at: now, result: res}
	l.count++

	if fileMutatingTools[name] || commandMayRewriteWorkspace(name, args) ||
		(strings.HasPrefix(name, "mcp__") && isMutatingTool(name)) {
		l.epoch++
	}
}

func commandMayRewriteWorkspace(name, args string) bool {
	if name != "run_command" && name != "start_command" {
		return false
	}
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil || payload.Command == "" {
		return false
	}
	cmd := payload.Command

	cmd = strings.ReplaceAll(cmd, "|", " | ")
	cmd = strings.ReplaceAll(cmd, ";", " ; ")

	for _, sink := range []string{"2>&1", "1>&2", "2>>/dev/null", "2>/dev/null", ">>/dev/null", ">/dev/null"} {
		cmd = strings.ReplaceAll(cmd, sink, " ")
	}
	if strings.Contains(cmd, ">") {
		return true
	}
	lower := strings.ToLower(cmd)
	for _, frag := range []string{
		"sed -i", "sed --in-place", "awk -i", "awk --in-place",
		" tee ", "git checkout", "git switch", "git restore",
		"git stash pop", "git stash apply", "git stash drop", "git stash push",
		"git reset", "git clean", "git apply", "git rebase", "git merge",
		"git cherry-pick", "git revert", "git rm", "git mv",
		"patch ", "truncate ", "install -m", "go fmt", "gofmt -w",
		"go generate", "go mod tidy", "go mod edit", "go get ", "pip install",
		"npm install", "yarn add", "pnpm add", "cargo fix",
	} {
		if strings.Contains(lower, frag) {
			return true
		}
	}

	for _, w := range []string{"cp", "mv", "rm", "dd", "rsync", "ln"} {
		if strings.Contains(" "+lower+" ", " "+w+" ") {
			return true
		}
	}
	return false
}

func (l *toolDedupLedger) fingerprint(name, args string) string {
	h := sha256.Sum256([]byte(name + "\x00" + args + "\x00" + fmt.Sprint(l.epoch)))
	return hex.EncodeToString(h[:])
}

func (l *toolDedupLedger) pruneLocked(now time.Time) {
	for fp, e := range l.table {
		if now.Sub(e.at) > l.ttl {
			delete(l.table, fp)
			l.count--
		}
	}
}
