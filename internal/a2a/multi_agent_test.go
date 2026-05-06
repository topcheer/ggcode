//go:build integration

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---------------------------------------------------------------------------
// Multi-Agent Collaboration Test
//
// Design principle: give PM agent a HIGH-LEVEL requirement.
// PM's LLM must AUTONOMOUSLY decide:
//   - What to delegate to which worker
//   - In what order (respecting dependencies)
//   - How to handle failures
//
// We do NOT specify a2a_remote parameters in the prompt.
// The LLM must figure out the tool calls itself.
// ---------------------------------------------------------------------------

const mAPIKey = "ggcode-a2a-test-key-2025"

type agentNode struct {
	name   string
	dir    string
	server *Server
	client *Client
}

type testCluster struct {
	t      *testing.T
	nodes  []*agentNode
	regDir string
	cfg    *config.Config
}

func newCluster(t *testing.T, specs []struct{ Name, Role string }) *testCluster {
	t.Helper()

	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	regDir := filepath.Join(t.TempDir(), "a2a-reg")
	os.MkdirAll(regDir, 0755)

	c := &testCluster{t: t, regDir: regDir, cfg: cfg}

	for _, spec := range specs {
		name := spec.Name
		role := spec.Role
		dir := filepath.Join(t.TempDir(), name)
		os.MkdirAll(dir, 0755)

		resolved, err := cfg.ResolveActiveEndpoint()
		if err != nil {
			t.Fatalf("[%s] resolve: %v", name, err)
		}
		prov, err := provider.NewProvider(resolved)
		if err != nil {
			t.Fatalf("[%s] provider: %v", name, err)
		}

		policy := permission.NewConfigPolicyWithMode(nil, []string{dir}, permission.BypassMode)
		registry := tool.NewRegistry()
		if err := tool.RegisterBuiltinTools(registry, policy, dir); err != nil {
			t.Fatalf("[%s] tools: %v", name, err)
		}

		sysPrompt := fmt.Sprintf(
			"You are '%s', a %s in a multi-agent team.\n"+
				"Your workspace: %s\n"+
				"You have an a2a_remote tool. Use target='list' to see all available agents.\n"+
				"When you receive a task, execute it using your code tools.\n"+
				"When you need help from another agent, use a2a_remote to delegate.\n"+
				"Write real, working Go code. Be concise.",
			name, role, dir,
		)

		ag := agent.NewAgent(prov, registry, sysPrompt, 0)

		reg := &Registry{dir: regDir}
		remoteTool := NewRemoteTool(reg, mAPIKey)
		registry.Register(remoteTool)

		// First agent (coordinator) gets longer timeout since it waits for all workers.
		taskTimeout := 5 * time.Minute
		if len(c.nodes) == 0 { // first node = coordinator
			taskTimeout = 15 * time.Minute
		}

		handler := NewTaskHandler(dir, ag, registry,
			WithMaxTasks(10),
			WithTimeout(taskTimeout),
		)
		srv := NewServer(ServerConfig{Host: "127.0.0.1", Port: 0, APIKey: mAPIKey}, handler)
		if err := srv.Start(); err != nil {
			t.Fatalf("[%s] start: %v", name, err)
		}
		t.Cleanup(srv.Stop)

		t.Logf("[%s] (%s) → %s", name, role, srv.Endpoint())
		c.nodes = append(c.nodes, &agentNode{name: name, dir: dir, server: srv, client: NewClient(srv.Endpoint(), mAPIKey)})
	}

	// Register all instances via per-ID files.
	for _, n := range c.nodes {
		inst := InstanceInfo{
			ID: n.name + "-id", PID: os.Getpid(),
			Workspace: n.dir, Endpoint: n.server.Endpoint(), Status: "ready",
		}
		data, _ := json.MarshalIndent(inst, "", "  ")
		os.WriteFile(filepath.Join(c.regDir, inst.ID+".json"), data, 0644)
	}

	return c
}

func (c *testCluster) node(name string) *agentNode {
	for _, n := range c.nodes {
		if n.name == name {
			return n
		}
	}
	c.t.Fatalf("node %q not found", name)
	return nil
}

func (c *testCluster) dispatchAndWait(ctx context.Context, target, skill, message string) *Task {
	c.t.Helper()
	n := c.node(target)
	task, err := n.client.SendMessage(ctx, skill, message)
	if err != nil {
		c.t.Fatalf("[%s] send: %v", target, err)
	}
	// Use ctx deadline for polling.
	deadline, ok := ctx.Deadline()
	timeout := 10 * time.Minute
	if ok {
		timeout = time.Until(deadline) - 10*time.Second
		if timeout < 30*time.Second {
			timeout = 30 * time.Second
		}
	}
	return c.pollUntil(ctx, target, task.ID, timeout)
}

func (c *testCluster) pollUntil(ctx context.Context, target, taskID string, timeout time.Duration) *Task {
	c.t.Helper()
	n := c.node(target)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got, err := n.client.GetTask(ctx, taskID)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		s := got.Status.State
		if s == TaskStateCompleted || s == TaskStateFailed || s == TaskStateRejected {
			return got
		}
		time.Sleep(3 * time.Second)
	}
	c.t.Fatalf("[%s] timeout for task %s", target, taskID)
	return nil
}

func (c *testCluster) logTask(target string, task *Task) {
	c.t.Helper()
	c.t.Logf("[%s] Task %s → %s", target, task.ID, task.Status.State)
	for _, m := range task.History {
		for _, p := range m.Parts {
			if len(p.Text) > 0 {
				c.t.Logf("  [%s] %s", m.Role, truncStr(p.Text, 400))
			}
		}
	}
	for i, a := range task.Artifacts {
		for _, p := range a.Parts {
			if len(p.Text) > 0 {
				c.t.Logf("  [result-%d] %s", i, truncStr(p.Text, 600))
			}
		}
	}
}

// countGoFiles counts .go files with real content (>20 bytes) in a worker's dir.
func (c *testCluster) countGoFiles(target string) int {
	c.t.Helper()
	n := c.node(target)
	entries, _ := os.ReadDir(n.dir)
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			data, err := os.ReadFile(filepath.Join(n.dir, e.Name()))
			if err == nil && len(data) > 20 {
				count++
			}
		}
	}
	return count
}

// scanWorkerOutput lists all files created by a worker.
func (c *testCluster) scanWorkerOutput(target string) []string {
	c.t.Helper()
	n := c.node(target)
	entries, _ := os.ReadDir(n.dir)
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			data, err := os.ReadFile(filepath.Join(n.dir, e.Name()))
			if err == nil && len(data) > 10 {
				files = append(files, fmt.Sprintf("%s (%d bytes)", e.Name(), len(data)))
			}
		}
	}
	return files
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ===========================================================================
// Test Scenarios — HIGH-LEVEL requirements, LLM decides the rest
// ===========================================================================

// Scenario 1: 2 agents — PM + engineer
// PM receives a vague requirement, must figure out what to tell the engineer.
func TestMultiAgent_2_ArchitectAndEngineer(t *testing.T) {
	c := newCluster(t, []struct{ Name, Role string }{
		{"architect", "software architect who designs systems and delegates implementation to engineers"},
		{"engineer", "Go backend engineer who implements services based on specifications"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	t.Log("=== Scenario: Architect receives vague requirement, must design + delegate ===")

	// Give architect a REAL-WORLD requirement — no tool call instructions.
	task := c.dispatchAndWait(ctx, "architect", "full-task",
		"I need a URL shortener service. It should accept a long URL and return a short code, "+
			"and redirect short codes back to the original URL. "+
			"Design the API and have the engineer implement it.",
	)

	c.logTask("architect", task)

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("architect failed: %s", task.Status.State)
	}

	// Verify: engineer's workspace should have at least 1 .go file.
	goFiles := c.countGoFiles("engineer")
	if goFiles == 0 {
		t.Error("engineer produced no .go files — architect may not have delegated properly")
	} else {
		t.Logf("✅ Engineer created %d Go file(s): %v", goFiles, c.scanWorkerOutput("engineer"))
	}
}

// Scenario 2: 3 agents — PM + backend + frontend
func TestMultiAgent_3_FullStack(t *testing.T) {
	c := newCluster(t, []struct{ Name, Role string }{
		{"pm", "product manager who coordinates backend and frontend teams"},
		{"backend", "Go backend engineer who builds REST APIs"},
		{"frontend", "frontend engineer who builds HTML/JS pages that call APIs"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	t.Log("=== Scenario: PM coordinates full-stack app with dependency ===")

	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"We need a real-time chat app. The backend should provide a simple message API "+
			"(send message, list messages). The frontend should display messages and have an input to send new ones. "+
			"Coordinate both backend and frontend teams. "+
			"Backend first, then frontend (frontend needs the API endpoints).",
	)

	c.logTask("pm", task)

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("PM failed: %s", task.Status.State)
	}

	// Both workers should have files.
	backendFiles := c.countGoFiles("backend")
	frontendFiles := c.scanWorkerOutput("frontend")

	t.Logf("Backend files: %d Go files", backendFiles)
	t.Logf("Frontend files: %v", frontendFiles)

	if backendFiles == 0 {
		t.Error("backend produced nothing — delegation may have failed")
	}
	if len(frontendFiles) == 0 {
		t.Error("frontend produced nothing — dependency coordination may have failed")
	}

	if backendFiles > 0 && len(frontendFiles) > 0 {
		t.Log("✅ Full-stack app built with both backend and frontend")
	}
}

// Scenario 3: 5 agents — microservice system
// PM must figure out the architecture and delegate to the right agents.
func TestMultiAgent_5_Microservices(t *testing.T) {
	c := newCluster(t, []struct{ Name, Role string }{
		{"tech-lead", "tech lead who designs microservice architecture and delegates to service teams"},
		{"user-team", "team responsible for user management service"},
		{"order-team", "team responsible for order management service"},
		{"payment-team", "team responsible for payment processing service"},
		{"infra-team", "team responsible for API gateway and infrastructure"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	t.Log("=== Scenario: Tech lead designs and delegates microservice architecture ===")

	task := c.dispatchAndWait(ctx, "tech-lead", "full-task",
		"Build an e-commerce backend with these services:\n"+
			"- User service: register/login users\n"+
			"- Order service: create/list orders\n"+
			"- Payment service: process payments for orders\n"+
			"- API gateway: route to all services, health check\n\n"+
			"Each service should be its own Go HTTP server with in-memory storage.\n"+
			"Design the architecture, decide ports, then delegate to the right teams.\n"+
			"Finally, have the infra team build a gateway that routes to all services.",
	)

	c.logTask("tech-lead", task)

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("tech-lead failed: %s", task.Status.State)
	}

	// Verify: at least 3 out of 4 teams should have produced files.
	var teamsWithCode int
	for _, name := range []string{"user-team", "order-team", "payment-team", "infra-team"} {
		count := c.countGoFiles(name)
		files := c.scanWorkerOutput(name)
		if count > 0 {
			teamsWithCode++
			t.Logf("  [%s] ✅ %v", name, files)
		} else {
			t.Logf("  [%s] ❌ no .go files", name)
		}
	}

	if teamsWithCode < 3 {
		t.Errorf("only %d/4 teams produced code — coordination unreliable", teamsWithCode)
	} else {
		t.Logf("✅ %d/4 teams produced code", teamsWithCode)
	}
}

// Scenario 4: Cross-review — worker A writes code, worker B reviews and fixes it.
func TestMultiAgent_CrossReview(t *testing.T) {
	c := newCluster(t, []struct{ Name, Role string }{
		{"lead", "team lead who assigns tasks and coordinates reviews"},
		{"junior", "junior developer who writes initial code (may have bugs)"},
		{"reviewer", "senior developer who reviews code and fixes issues"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	t.Log("=== Scenario: Junior writes code → Reviewer reviews and fixes ===")

	task := c.dispatchAndWait(ctx, "lead", "full-task",
		"We need a string utility library with functions: Reverse, ToUpper, ToLower, CountWords.\n\n"+
			"Step 1: Have the junior developer write the initial implementation.\n"+
			"Step 2: Have the senior reviewer check the code and fix any bugs or missing functions.\n\n"+
			"Coordinate both steps. The final code should be in the reviewer's workspace as utils.go.",
	)

	c.logTask("lead", task)

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("lead task failed: %s", task.Status.State)
	}

	// Verify: junior should have written something.
	juniorFiles := c.scanWorkerOutput("junior")
	t.Logf("Junior created: %v", juniorFiles)

	// Verify: reviewer should have created a reviewed/fixed version.
	reviewerFiles := c.scanWorkerOutput("reviewer")
	t.Logf("Reviewer created: %v", reviewerFiles)

	if len(juniorFiles) == 0 {
		t.Log("⚠️ Junior didn't produce code (lead may not have delegated)")
	}
	if len(reviewerFiles) == 0 {
		t.Log("⚠️ Reviewer didn't produce code (review step may have been skipped)")
	}

	if len(juniorFiles) > 0 || len(reviewerFiles) > 0 {
		t.Log("✅ Cross-review workflow executed")
	}
}
