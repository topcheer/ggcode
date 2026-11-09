package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// memoryCuratorSource is the provenance source stamped on curator writes so
// the curator's own loop can ignore its events (feedback-loop break).
const memoryCuratorSource = "memory-curator"

// Defaults for the resident memory curator.
const (
	memoryCuratorDebounce  = 45 * time.Second
	memoryCuratorTimeout   = 90 * time.Second
	memoryCuratorMaxAction = 12
	memoryCuratorSinkCap   = 64
)

// MemoryCuratorOptions configures a MemoryCurator. Zero values fall back to
// the defaults (45s debounce, 90s batch timeout, 12 actions per batch).
type MemoryCuratorOptions struct {
	// Debounce is the quiet window used to aggregate events into one batch.
	Debounce time.Duration
	// Timeout bounds a single curation batch (LLM call + action execution).
	Timeout time.Duration
	// MaxActions caps how many LLM-proposed actions are executed per batch.
	MaxActions int
}

// MemoryCurator is a resident background agent that watches memory
// save/delete events and, after a debounce window, runs a headless one-shot
// LLM pass to keep the store tidy: normalize titles, merge near-duplicates,
// and archive stale temporal facts. Actions are applied through the same
// AutoMemory APIs the save_memory/delete_memory tools use (never raw file
// writes). Any LLM or parse failure abandons the batch silently - the
// curator never blocks the caller and never retries on its own.
type MemoryCurator struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
	prov       provider.Provider
	debounce   time.Duration
	timeout    time.Duration
	maxActions int

	events chan memory.ChangeEvent
	stopCh chan struct{}

	mu      sync.Mutex
	started bool
}

// NewMemoryCurator builds a curator over the global and project memory
// stores using the given LLM provider (its current default model). Either
// store may be nil; that scope is then ignored.
func NewMemoryCurator(globalMem, projectMem *memory.AutoMemory, prov provider.Provider, opts MemoryCuratorOptions) *MemoryCurator {
	if opts.Debounce <= 0 {
		opts.Debounce = memoryCuratorDebounce
	}
	if opts.Timeout <= 0 {
		opts.Timeout = memoryCuratorTimeout
	}
	if opts.MaxActions <= 0 {
		opts.MaxActions = memoryCuratorMaxAction
	}
	return &MemoryCurator{
		globalMem:  globalMem,
		projectMem: projectMem,
		prov:       prov,
		debounce:   opts.Debounce,
		timeout:    opts.Timeout,
		maxActions: opts.MaxActions,
	}
}

// Start subscribes the curator to memory change events and launches its
// resident loop. The loop stops when ctx is cancelled or Stop is called.
// Start is idempotent.
func (c *MemoryCurator) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.prov == nil {
		return
	}
	c.started = true
	c.events = make(chan memory.ChangeEvent, memoryCuratorSinkCap)
	c.stopCh = make(chan struct{})
	if c.globalMem != nil {
		c.globalMem.SetEventSink("global", c.events)
	}
	if c.projectMem != nil {
		c.projectMem.SetEventSink("project", c.events)
	}
	stopCh := c.stopCh
	safego.Go("memorycurator.loop", func() { c.loop(ctx, stopCh) })
	debug.Log("memory", "curator started (debounce %s, timeout %s, max %d actions)", c.debounce, c.timeout, c.maxActions)
}

// Stop terminates the resident loop and unregisters the event sinks. Stop is
// idempotent and safe to call when never started.
func (c *MemoryCurator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.started = false
	close(c.stopCh)
	if c.globalMem != nil {
		c.globalMem.SetEventSink("", nil)
	}
	if c.projectMem != nil {
		c.projectMem.SetEventSink("", nil)
	}
	debug.Log("memory", "curator stopped")
}

// loop aggregates events into batches: every event (re)arms the debounce
// timer; when the window expires with no new events, the accumulated batch
// is curated in one headless pass. Events sourced from the curator itself
// are dropped to break the feedback loop.
func (c *MemoryCurator) loop(ctx context.Context, stopCh <-chan struct{}) {
	pending := make(map[string]memory.ChangeEvent)
	var timer *time.Timer
	var drain <-chan time.Time
	armTimer := func() {
		if timer == nil {
			timer = time.NewTimer(c.debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.debounce)
		}
		drain = timer.C
	}
	for {
		select {
		case <-stopCh:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case ev := <-c.events:
			if ev.Source == memoryCuratorSource {
				continue
			}
			pending[ev.Scope+"/"+ev.Key] = ev
			armTimer()
		case <-drain:
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			drain = nil
			if len(pending) == 0 {
				continue
			}
			batch := make([]memory.ChangeEvent, 0, len(pending))
			for _, ev := range pending {
				batch = append(batch, ev)
			}
			pending = make(map[string]memory.ChangeEvent)
			sort.Slice(batch, func(i, j int) bool {
				if batch[i].Scope != batch[j].Scope {
					return batch[i].Scope < batch[j].Scope
				}
				return batch[i].Key < batch[j].Key
			})
			c.curateBatch(ctx, batch)
		}
	}
}

// curatorAction is one LLM-proposed mutation in the curation action list.
type curatorAction struct {
	Op      string `json:"op"`
	Scope   string `json:"scope"`
	Key     string `json:"key"`
	Content string `json:"content,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// curatorVerdict is the strict JSON contract the headless pass must return.
type curatorVerdict struct {
	Actions []curatorAction `json:"actions"`
}

// curateBatch runs one headless curation pass for the batch: build the
// prompt (list view + changed entries), make a single LLM Chat call, parse
// the JSON action list, and execute at most maxActions of them through the
// store APIs. Any failure aborts the batch without retry.
func (c *MemoryCurator) curateBatch(ctx context.Context, batch []memory.ChangeEvent) {
	defer func() {
		if r := recover(); r != nil {
			debug.Log("memory", "curator batch panicked (abandoned): %v", r)
		}
	}()
	bctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	prompt := c.buildBatchPrompt(batch)
	resp, err := c.prov.Chat(bctx, []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{provider.TextBlock(prompt)},
	}}, nil)
	if err != nil {
		debug.Log("memory", "curator LLM call failed, batch abandoned (%d keys): %v", len(batch), err)
		return
	}
	verdict, err := parseCuratorVerdict(resp.Message.Content)
	if err != nil {
		debug.Log("memory", "curator verdict unparsable, batch abandoned (%d keys): %v", len(batch), err)
		return
	}
	applied, skipped := 0, 0
	for _, act := range verdict.Actions {
		if applied >= c.maxActions {
			skipped++
			continue
		}
		if c.applyAction(act) {
			applied++
		} else {
			skipped++
		}
	}
	debug.Log("memory", "curator batch done: %d key(s), %d action(s) applied, %d skipped", len(batch), applied, skipped)
}

// applyAction executes one action through the same store APIs the memory
// tools use and reports whether it succeeded.
func (c *MemoryCurator) applyAction(act curatorAction) bool {
	var target *memory.AutoMemory
	switch act.Scope {
	case "global":
		target = c.globalMem
	case "project":
		target = c.projectMem
	default:
		debug.Log("memory", "curator action skipped: invalid scope %q (key %q)", act.Scope, act.Key)
		return false
	}
	if target == nil {
		debug.Log("memory", "curator action skipped: %s memory unavailable (key %q)", act.Scope, act.Key)
		return false
	}
	if strings.TrimSpace(act.Key) == "" {
		debug.Log("memory", "curator action skipped: empty key")
		return false
	}
	switch act.Op {
	case "save":
		if strings.TrimSpace(act.Content) == "" {
			debug.Log("memory", "curator save skipped: empty content (key %q)", act.Key)
			return false
		}
		if err := target.SaveMemoryWithSource(act.Key, act.Content, memoryCuratorSource); err != nil {
			debug.Log("memory", "curator save failed for %s/%s: %v", act.Scope, act.Key, err)
			return false
		}
		debug.Log("memory", "curator saved %s/%s (%d chars)%s", act.Scope, act.Key, len(act.Content), actionReasonSuffix(act.Reason))
		return true
	case "delete":
		if err := target.DeleteMemory(act.Key); err != nil {
			debug.Log("memory", "curator delete failed for %s/%s: %v", act.Scope, act.Key, err)
			return false
		}
		debug.Log("memory", "curator archived %s/%s%s", act.Scope, act.Key, actionReasonSuffix(act.Reason))
		return true
	default:
		debug.Log("memory", "curator action skipped: invalid op %q (key %q)", act.Op, act.Key)
		return false
	}
}

func actionReasonSuffix(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return " - " + strings.TrimSpace(reason)
}

// buildBatchPrompt renders the headless curation prompt: the list_memory
// view for both scopes (context) plus the full content of the changed
// entries (the only keys the LLM may act on).
func (c *MemoryCurator) buildBatchPrompt(batch []memory.ChangeEvent) string {
	var b strings.Builder
	b.WriteString("You are the memory curator for an AI coding assistant. You maintain its persistent memory store (global + project scopes).\n\n")

	b.WriteString("## Memory index (list_memory view)\n")
	for _, s := range []struct {
		label string
		mem   *memory.AutoMemory
	}{{"global", c.globalMem}, {"project", c.projectMem}} {
		if s.mem == nil {
			continue
		}
		entries, err := s.mem.ListDetailed()
		if err != nil || len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s (%d entries)\n", s.label, len(entries))
		for i, e := range entries {
			if i >= 150 {
				fmt.Fprintf(&b, "- ... (%d more omitted)\n", len(entries)-150)
				break
			}
			fmt.Fprintf(&b, "- %s (uses %d, updated %s): %s\n", e.Key, e.Uses, e.UpdatedAt.Format(time.RFC3339), truncateRunes(e.Preview, 100, " ..."))
		}
	}

	b.WriteString("\n## Batch changed entries (the ONLY keys you may act on)\n")
	for _, ev := range batch {
		content := ""
		if mem := c.memForScope(ev.Scope); mem != nil {
			if loaded, err := mem.LoadKey(ev.Key); err == nil {
				content = loaded
			}
		}
		fmt.Fprintf(&b, "\n[%s] %s (%s):\n%s\n", ev.Scope, ev.Key, ev.Op, truncateRunes(content, 2000, " ..."))
	}

	b.WriteString(`
## Your tasks
1. Categorize: normalize keys/titles into consistent kebab-case prefixes (e.g. "build-", "release-", "api-") and prepend a short "Tags:" line to the content when missing. Save the improved entry under the SAME key.
2. Merge near-duplicates: multiple entries about the same topic -> keep the most recent one, merge valuable older details into it (save merged content under the kept key), and delete the redundant older entries.
3. Archive stale temporal facts: entries describing one-time completed events ("has completed", "now", "wait", "已交付" and similar) that are no longer durable knowledge -> delete them.

## Rules
- Only operate on keys from the changed-entries batch; the index above is context only.
- At most 12 actions total. A "save" must carry the FULL resulting content (do not truncate).
- Never invent new facts; preserve the wording of durable content.

## Output
Return ONLY a JSON object, no prose, no code fences:
{"actions":[{"op":"save","scope":"project","key":"...","content":"...","reason":"..."},{"op":"delete","scope":"global","key":"...","reason":"..."}]}
An empty action list is valid: {"actions":[]}
`)
	return b.String()
}

func (c *MemoryCurator) memForScope(scope string) *memory.AutoMemory {
	if scope == "global" {
		return c.globalMem
	}
	return c.projectMem
}

// parseCuratorVerdict extracts and parses the JSON verdict from the LLM
// response blocks, tolerating surrounding prose and markdown fences.
func parseCuratorVerdict(blocks []provider.ContentBlock) (*curatorVerdict, error) {
	var raw strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" || blk.Text != "" {
			raw.WriteString(blk.Text)
		}
	}
	s := strings.TrimSpace(raw.String())
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object found in response")
	}
	var verdict curatorVerdict
	if err := json.Unmarshal([]byte(s[start:end+1]), &verdict); err != nil {
		return nil, fmt.Errorf("invalid action JSON: %w", err)
	}
	return &verdict, nil
}
