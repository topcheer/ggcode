package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/sessionidx"
)

type ToolProgressKey struct{}

type ToolProgressFunc func(toolID, toolName, output string)

type Result struct {
	Content             string             `json:"content"`
	IsError             bool               `json:"is_error"`
	Images              []ResultImage      `json:"images,omitempty"`
	SuggestedWorkingDir string             `json:"suggested_working_dir,omitempty"`
	FollowUpMessages    []provider.Message `json:"follow_up_messages,omitempty"`
	// HaltAgent (#5520): a successful result carrying this flag terminates
	// the main agent loop immediately after the result is recorded - no
	// further tool calls in the batch, no further turns. Reserved for tools
	// that hand the session over out-of-band (restart: the durable resume
	// nudge is already armed, so any further turn is wasted work). Ignored
	// on IsError results.
	HaltAgent bool `json:"halt_agent,omitempty"`
}

type ResultImage struct {
	MIME       string `json:"mime"`
	Base64     string `json:"base64"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SourcePath string `json:"source_path"`
}

type Tool interface {
	Name() string

	Description() string

	Parameters() json.RawMessage

	Execute(ctx context.Context, input json.RawMessage) (Result, error)
}

type Closer interface {
	Close() error
}

type ToolMeta struct {
	CostEstimate float64 `json:"cost_estimate,omitempty"`

	RateLimitRPS float64 `json:"rate_limit_rps,omitempty"`

	RetryPolicy string `json:"retry_policy,omitempty"`

	MaxRetries int `json:"max_retries,omitempty"`
}

type MetaProvider interface {
	ToolMeta() ToolMeta
}

type SandboxSafe interface {
	SandboxSafe() bool
}

type Registry struct {
	tools        map[string]Tool
	codeIndex    *CodeIndexManager
	jobManager   *CommandJobManager
	descAugment  func(toolName string) string
	sessionIndex *sessionidx.Manager
	checkpoints  *checkpoint.Manager
	mu           sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) CodeIndex() *CodeIndexManager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.codeIndex
}

func (r *Registry) JobManager() *CommandJobManager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.jobManager
}

// SessionIndex returns the session-recall index manager (nil until set).
func (r *Registry) SessionIndex() *sessionidx.Manager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionIndex
}

// SetSessionIndex attaches the session-recall index manager.
func (r *Registry) SetSessionIndex(m *sessionidx.Manager) {
	r.mu.Lock()
	r.sessionIndex = m
	r.mu.Unlock()
}

// Checkpoints returns the checkpoint provenance manager (nil until set).
func (r *Registry) Checkpoints() *checkpoint.Manager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.checkpoints
}

// SetCheckpoints attaches the checkpoint provenance manager so provenance
// tools (why) can resolve edit intents lazily at call time.
func (r *Registry) SetCheckpoints(m *checkpoint.Manager) {
	r.mu.Lock()
	r.checkpoints = m
	r.mu.Unlock()
}

func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func (r *Registry) CloseAll() []error {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	r.mu.RUnlock()

	var errs []error
	for _, t := range tools {
		if c, ok := t.(Closer); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close %s: %w", t.Name(), err))
			}
		}
	}
	return errs
}

func (r *Registry) SetDescriptionAugmenter(fn func(toolName string) string) {
	r.mu.Lock()
	r.descAugment = fn
	r.mu.Unlock()
}

func (r *Registry) ToDefinitions() []provider.ToolDefinition {
	r.mu.RLock()
	augment := r.descAugment
	r.mu.RUnlock()
	tools := r.List()
	defs := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if ac, ok := t.(AvailabilityChecker); ok && !ac.Available() {
			continue
		}
		desc := t.Description()
		if augment != nil {
			if extra := augment(t.Name()); extra != "" {
				desc = desc + extra
			}
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        t.Name(),
			Description: desc,

			Parameters: injectArgRationaleSchema(t.Name(), t.Parameters()),
		})
	}
	return defs
}

type AvailabilityChecker interface {
	Available() bool
}

func (r *Registry) ToolNames() []string {
	tools := r.List()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

func (r *Registry) GetMeta(name string) (ToolMeta, bool) {
	if t, ok := r.Get(name); ok {
		if mp, ok := t.(MetaProvider); ok {
			return mp.ToolMeta(), true
		}
	}
	return ToolMeta{}, false
}

type Cloner interface {
	Clone() Tool
}

func (r *Registry) Clone() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	newReg := &Registry{tools: make(map[string]Tool, len(r.tools)), codeIndex: r.codeIndex, descAugment: r.descAugment}
	for name, t := range r.tools {
		if c, ok := t.(Cloner); ok {
			newReg.tools[name] = c.Clone()
		} else {

			newReg.tools[name] = t
		}
	}
	return newReg
}

func CheckRequired(fields ...string) string {
	var missing []string
	for i := 0; i+1 < len(fields); i += 2 {
		name := fields[i]
		value := fields[i+1]
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	if len(missing) == 1 {
		return fmt.Sprintf("missing required parameter: %s", missing[0])
	}
	return fmt.Sprintf("missing required parameters: %s", strings.Join(missing, ", "))
}
