package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Issue #5552 (follow-up to #5550, P0 #5549): the #5543 knife-5 split let
// buildRunTurnContextD2 hand back a never-assigned named return, so agent.go
// overwrote D1's full definition set with nil and the provider boundary saw
// zero tool definitions - every agent died at 19:18Z. These pins hold the
// guarantees the issue asks for:
//  1. toolDefs arrives non-empty at every provider ChatStream call;
//  2. no named return on the toolDef plumbing is ever handed back unassigned
//     (the exact #5549 shape: declared, never assigned, returned);
//  3. interleaved empty/tool turns do not accumulate the consecutive-empty
//     counter toward the abort threshold (scope-ext from the #5551 review).

// issue5552Provider records the tool definitions each ChatStream call
// receives and replays scripted responses, mirroring mockProvider's event
// conversion so the full RunStream path is exercised.
type issue5552Provider struct {
	mu        sync.Mutex
	responses []*provider.ChatResponse
	defs      [][]provider.ToolDefinition
}

func (p *issue5552Provider) Name() string      { return "issue5552-capture" }
func (p *issue5552Provider) ModelName() string { return "issue5552-model" }

func (p *issue5552Provider) Chat(context.Context, []provider.Message, []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p *issue5552Provider) CountTokens(context.Context, []provider.Message) (int, error) {
	return 0, nil
}

func (p *issue5552Provider) ChatStream(_ context.Context, _ []provider.Message, defs []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.defs = append(p.defs, append([]provider.ToolDefinition(nil), defs...))
	var resp *provider.ChatResponse
	if len(p.responses) > 0 {
		resp = p.responses[0]
		p.responses = p.responses[1:]
	}
	p.mu.Unlock()
	if resp == nil {
		resp = &provider.ChatResponse{Message: provider.Message{Role: "assistant"}}
	}
	events := streamEventsFromResponse(resp)
	ch := make(chan provider.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (p *issue5552Provider) captured() [][]provider.ToolDefinition {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]provider.ToolDefinition(nil), p.defs...)
}

// TestIssue5552ToolDefsNonEmptyAtProviderBoundary is the core regression pin:
// run a real agent to completion and assert every provider ChatStream call
// received the full registered definition set. Pre-fix #5549 (D2 returning
// its never-assigned named return) fails here with zero definitions.
func TestIssue5552ToolDefsNonEmptyAtProviderBoundary(t *testing.T) {
	p := &issue5552Provider{responses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("done")}}},
	}}
	registry := tool.NewRegistry()
	for _, name := range []string{"echo", "ping", "status"} {
		if err := registry.Register(countingTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	a := NewAgent(p, registry, "", 3)
	if err := a.RunStream(context.Background(), "do the work", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	captured := p.captured()
	if len(captured) == 0 {
		t.Fatal("provider boundary never reached: ChatStream was not called")
	}
	for i, defs := range captured {
		if len(defs) == 0 {
			t.Fatalf("ChatStream call %d received ZERO tool definitions - #5549 named-return regression shape (nil toolDefs overwrote the D1 set)", i+1)
		}
	}
	first := map[string]bool{}
	for _, d := range captured[0] {
		first[d.Name] = true
	}
	for _, want := range []string{"echo", "ping", "status"} {
		if !first[want] {
			t.Fatalf("tool %q missing from provider-boundary definitions: got %v", want, first)
		}
	}
	if len(first) != 3 {
		t.Fatalf("expected exactly the 3 registered tools at the boundary, got %d: %v", len(first), first)
	}
}

// TestIssue5552InterleavedEmptyToolTurnsDoNotAccumulate pins the #5551 fix
// face scoped into this issue: empty and productive tool turns alternate, so
// no three responses are ever consecutive-empty and the run must finish.
// Pre-fix #5551 (reset only inside handleNoToolResponse) the counter crossed
// the threshold on the third interleaved empty turn and aborted the run.
func TestIssue5552InterleavedEmptyToolTurnsDoNotAccumulate(t *testing.T) {
	empty := func() *provider.ChatResponse {
		return &provider.ChatResponse{Message: provider.Message{Role: "assistant"}}
	}
	echoTurn := func(id string) *provider.ChatResponse {
		return &provider.ChatResponse{Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.ToolUseBlock(id, "echo", []byte("{}"))},
		}}
	}
	done := &provider.ChatResponse{Message: provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{provider.TextBlock("all done")},
	}}
	p := &issue5552Provider{responses: []*provider.ChatResponse{
		empty(), echoTurn("c1"), empty(), echoTurn("c2"), empty(), done,
	}}
	registry := tool.NewRegistry()
	echoCount := 0
	if err := registry.Register(countingTool{name: "echo", executed: &echoCount}); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	a := NewAgent(p, registry, "", 12)
	if err := a.RunStream(context.Background(), "keep going", func(provider.StreamEvent) {}); err != nil {
		t.Fatalf("interleaved empty/tool run must not abort - st.consecutiveEmptyResponses must reset after each productive tool turn: %v", err)
	}
	if echoCount != 2 {
		t.Fatalf("expected 2 productive echo turns, got %d", echoCount)
	}
}

// TestIssue5552ThreeTrulyConsecutiveEmptyTurnsStillAbort is the negative
// control for the interleave pin: the abort itself must stay armed, so the
// interleave test above cannot pass vacuously.
func TestIssue5552ThreeTrulyConsecutiveEmptyTurnsStillAbort(t *testing.T) {
	p := &issue5552Provider{responses: []*provider.ChatResponse{
		{Message: provider.Message{Role: "assistant"}},
		{Message: provider.Message{Role: "assistant"}},
		{Message: provider.Message{Role: "assistant"}},
	}}
	registry := tool.NewRegistry()
	if err := registry.Register(countingTool{name: "echo"}); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	a := NewAgent(p, registry, "", 6)
	err := a.RunStream(context.Background(), "stay empty", func(provider.StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "aborted after 3 consecutive empty responses") {
		t.Fatalf("three genuinely consecutive empty responses must abort, got: %v", err)
	}
}

// issue5552PlumbingFiles are the #5543 knife-2..5 extraction targets plus the
// caller and the turn-error helper that now carries the toolDef parameter.
var issue5552PlumbingFiles = []string{
	"agent.go",
	"run_stream_setup.go",
	"conversation_loop.go",
	"chat_response_processing.go",
	"chat_tool_loop.go",
	"chat_turn_error.go",
}

func issue5552IsSliceOf(expr ast.Expr, pkg, sel string) bool {
	at, ok := expr.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return false
	}
	se, ok := at.Elt.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := se.X.(*ast.Ident)
	return ok && id.Name == pkg && se.Sel.Name == sel
}

func issue5552Analyze(body ast.Node, name string) (assigned, retRef, bare bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name && as.Tok != token.EQL {
					assigned = true
				}
			}
		}
		if is, ok := n.(*ast.IncDecStmt); ok {
			if id, ok := is.X.(*ast.Ident); ok && id.Name == name {
				assigned = true
			}
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // closure returns belong to the closure
		}
		if rs, ok := n.(*ast.ReturnStmt); ok {
			if len(rs.Results) == 0 {
				bare = true
			}
			for _, e := range rs.Results {
				if id, ok := e.(*ast.Ident); ok && id.Name == name {
					retRef = true
				}
			}
		}
		return true
	})
	return assigned, retRef, bare
}

// TestIssue5552NoUnassignedNamedReturnsOnToolDefPlumbing is the static guard
// for the "ideally" half of acceptance: no named return of []provider.ToolDefinition
// or []provider.Message type on the plumbing may be returned unassigned (Go
// hands back the nil zero value exactly like #5549). It failed against the
// pre-#5550 D2 and against D1's never-assigned msgs return on main.
func TestIssue5552NoUnassignedNamedReturnsOnToolDefPlumbing(t *testing.T) {
	guarded := 0
	for _, name := range issue5552PlumbingFiles {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Type.Results == nil || fd.Body == nil {
				continue
			}
			type watched struct {
				name string
				kind string
			}
			var ws []watched
			for _, r := range fd.Type.Results.List {
				for _, n := range r.Names {
					switch {
					case issue5552IsSliceOf(r.Type, "provider", "ToolDefinition"):
						ws = append(ws, watched{n.Name, "[]provider.ToolDefinition"})
					case issue5552IsSliceOf(r.Type, "provider", "Message"):
						ws = append(ws, watched{n.Name, "[]provider.Message"})
					}
				}
			}
			if len(ws) == 0 {
				continue
			}
			guarded += len(ws)
			for _, w := range ws {
				assigned, retRef, bare := issue5552Analyze(fd.Body, w.name)
				if assigned || (!retRef && !bare) {
					continue
				}
				how := "is returned"
				if retRef && bare {
					how = "is returned and a bare return exists"
				} else if bare {
					how = "flows out through a bare return"
				}
				t.Errorf("%s: %s named return %q of type %s is never assigned but %s - Go hands back the nil zero value (#5549 shape)", name, fd.Name.Name, w.name, w.kind, how)
			}
		}
	}
	if guarded < 2 {
		t.Fatalf("guard went vacuous: only %d named toolDef/message results found across the plumbing files; update the file list or type matcher", guarded)
	}
}
