//go:build integration

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
// CRITICAL: The PM agent's LLM must REALISTICALLY decide to call a2a_remote.
// We do NOT call A2A Client directly. The full chain is:
//
//   PM LLM → "I should delegate" → calls a2a_remote(target, skill, msg)
//     → A2A protocol → Worker Server → Worker LLM → worker tools → files created
//     → result back to PM LLM → PM LLM produces summary
//
// This tests: LLM tool selection → A2A protocol → cross-instance LLM execution
// ---------------------------------------------------------------------------

const mAPIKey = "ggcode-a2a-test-key-2025"

type agentNode struct {
	name   string
	dir    string
	server *Server
	client *Client
}

type testCluster struct {
	t       *testing.T
	nodes   []*agentNode
	regDir  string
	regFile string
	cfg     *config.Config
}

func newCluster(t *testing.T, names []string) *testCluster {
	t.Helper()

	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	regDir := filepath.Join(t.TempDir(), "a2a-reg")
	os.MkdirAll(regDir, 0755)

	c := &testCluster{t: t, regDir: regDir, regFile: filepath.Join(regDir, "instances.json"), cfg: cfg}

	for _, name := range names {
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

		// System prompt is critical — tells the LLM what role it plays.
		sysPrompt := fmt.Sprintf(
			"You are agent '%s' in a multi-agent team. Your workspace: %s\n"+
				"You have an a2a_remote tool to call other agents. Use target='list' to see available agents.\n"+
				"When asked to delegate, ALWAYS use the a2a_remote tool — do NOT do the work yourself.\n"+
				"Be concise. Write real working code.",
			name, dir,
		)

		ag := agent.NewAgent(prov, registry, sysPrompt, 0)

		// Register a2a_remote so this agent can call others.
		reg := &Registry{dir: regDir}
		remoteTool := NewRemoteTool(reg, mAPIKey)
		registry.Register(remoteTool)

		handler := NewTaskHandler(dir, ag, registry,
			WithMaxTasks(5),
			WithTimeout(5*time.Minute),
		)
		srv := NewServer(ServerConfig{Host: "127.0.0.1", Port: 0, APIKey: mAPIKey}, handler)
		if err := srv.Start(); err != nil {
			t.Fatalf("[%s] start: %v", name, err)
		}
		t.Cleanup(srv.Stop)

		t.Logf("[%s] → %s", name, srv.Endpoint())
		c.nodes = append(c.nodes, &agentNode{name: name, dir: dir, server: srv, client: NewClient(srv.Endpoint(), mAPIKey)})
	}

	// Write all instances to registry.
	var instances []InstanceInfo
	for _, n := range c.nodes {
		instances = append(instances, InstanceInfo{
			ID: n.name + "-id", PID: os.Getpid(),
			Workspace: n.dir, Endpoint: n.server.Endpoint(), Status: "ready",
		})
	}
	data, _ := json.MarshalIndent(instances, "", "  ")
	os.WriteFile(c.regFile, data, 0644)

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

// dispatchAndWait sends a task to a node and polls until terminal state.
// Returns the final task.
func (c *testCluster) dispatchAndWait(ctx context.Context, target, skill, message string) *Task {
	c.t.Helper()
	n := c.node(target)
	task, err := n.client.SendMessage(ctx, skill, message)
	if err != nil {
		c.t.Fatalf("[%s] send: %v", target, err)
	}
	return c.pollUntil(ctx, target, task.ID, 3*time.Minute)
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

func (c *testCluster) verifyFile(target, filename string, mustContain ...string) {
	c.t.Helper()
	n := c.node(target)
	data, err := os.ReadFile(filepath.Join(n.dir, filename))
	if err != nil {
		c.t.Errorf("[%s] file %s not found: %v", target, filename, err)
		return
	}
	content := string(data)
	for _, sub := range mustContain {
		if !strings.Contains(content, sub) {
			c.t.Errorf("[%s] %s missing %q", target, filename, sub)
		}
	}
	c.t.Logf("[%s] ✅ %s verified (%d bytes)", target, filename, len(data))
}

// logTaskDetails prints task history for debugging.
func (c *testCluster) logTaskDetails(target string, task *Task) {
	c.t.Helper()
	c.t.Logf("[%s] Task %s → %s", target, task.ID, task.Status.State)
	for _, m := range task.History {
		for _, p := range m.Parts {
			c.t.Logf("  [%s] %s", m.Role, truncStr(p.Text, 300))
		}
	}
	for i, a := range task.Artifacts {
		for _, p := range a.Parts {
			c.t.Logf("  [artifact-%d] %s", i, truncStr(p.Text, 500))
		}
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ===========================================================================
// Tests
// ===========================================================================

// TestMultiAgent_2Nodes_PM delegates to 1 worker via LLM→a2a_remote.
func TestMultiAgent_2Nodes_PM(t *testing.T) {
	c := newCluster(t, []string{"pm", "backend"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("=== PM tells backend to create TODO API (via LLM → a2a_remote) ===")

	// PM receives a natural language request.
	// PM's LLM must: decide to call a2a_remote(target="backend", skill="code-edit", ...)
	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"Delegate this task to the backend agent: Create main.go implementing a TODO REST API. "+
			"Endpoints: GET /todos, POST /todos, DELETE /todos/{id}. In-memory storage. Port 8080. "+
			"Use the a2a_remote tool with target='backend'.")

	if task.Status.State != TaskStateCompleted {
		c.logTaskDetails("pm", task)
		t.Fatalf("PM task failed: %s", task.Status.State)
	}

	c.logTaskDetails("pm", task)

	// The key verification: did backend's workspace get a file?
	// This proves the chain: PM LLM → a2a_remote → A2A → backend server → backend LLM → write_file
	c.verifyFile("backend", "main.go", "func main()", "/todos")

	t.Log("✅ 2-agent LLM→a2a_remote→LLM chain verified")
}

// TestMultiAgent_3Nodes_PMParallel tests PM delegating to 2 workers in parallel.
func TestMultiAgent_3Nodes_PMParallel(t *testing.T) {
	c := newCluster(t, []string{"pm", "backend", "frontend"})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	t.Log("=== PM delegates to backend AND frontend in one task ===")

	// PM must call a2a_remote twice in a single session.
	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"You need to coordinate two agents to build a counter app:\n"+
			"1. Call a2a_remote(target='backend', skill='code-edit', message='Create server.go: Go HTTP server with GET /count returning {\"count\":N} and POST /increment that increments N. Port 8081.')\n"+
			"2. Call a2a_remote(target='frontend', skill='code-edit', message='Create index.html: HTML page with a counter display and Increment button. JS calls POST http://localhost:8081/increment then GET http://localhost:8081/count to update display.')\n"+
			"Call both agents. Report results.")

	if task.Status.State != TaskStateCompleted {
		c.logTaskDetails("pm", task)
		t.Fatalf("PM task failed: %s", task.Status.State)
	}

	c.logTaskDetails("pm", task)

	// Both workspaces should have files.
	c.verifyFile("backend", "server.go", "/count", "/increment")
	c.verifyFile("frontend", "index.html", "increment", "count")

	t.Log("✅ 3-agent parallel delegation verified")
}

// TestMultiAgent_5Nodes_Pipeline tests sequential pipeline across 5 agents.
func TestMultiAgent_5Nodes_Pipeline(t *testing.T) {
	c := newCluster(t, []string{"pm", "shared-lib", "user-svc", "order-svc", "gateway"})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	t.Log("=== 5-Agent Pipeline: shared-lib → user-svc + order-svc → gateway ===")

	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"Build a microservice system by delegating to these agents in order:\n"+
			"Step 1: a2a_remote(target='shared-lib', skill='code-edit', message='Create models.go with: package main; type User struct{ID,Name,Email string}; type Order struct{ID,UserID,Product string; Amount float64}')\n"+
			"Step 2: a2a_remote(target='user-svc', skill='code-edit', message='Create main.go: User service with POST /users and GET /users. In-memory map. Port 8091.')\n"+
			"Step 3: a2a_remote(target='order-svc', skill='code-edit', message='Create main.go: Order service with POST /orders and GET /orders. In-memory map. Port 8092.')\n"+
			"Step 4: a2a_remote(target='gateway', skill='code-edit', message='Create main.go: Gateway proxy /api/users→localhost:8091, /api/orders→localhost:8092. Port 8090.')\n"+
			"Execute steps 1-4 in order. Report each result.",
	)

	if task.Status.State != TaskStateCompleted {
		c.logTaskDetails("pm", task)
		t.Fatalf("PM task failed: %s", task.Status.State)
	}

	c.logTaskDetails("pm", task)

	// Verify all 4 workers created files.
	c.verifyFile("shared-lib", "models.go", "type User struct")
	c.verifyFile("user-svc", "main.go", "/users", "8091")
	c.verifyFile("order-svc", "main.go", "/orders", "8092")
	c.verifyFile("gateway", "main.go", "8090")

	t.Log("✅ 5-agent pipeline verified")
}

// TestMultiAgent_10Nodes_Stress tests PM coordinating 9 workers concurrently.
func TestMultiAgent_10Nodes_Stress(t *testing.T) {
	workers := []string{
		"svc-auth", "svc-user", "svc-order", "svc-payment",
		"svc-notification", "svc-search", "svc-analytics",
		"svc-config", "svc-logging",
	}
	all := append([]string{"pm"}, workers...)
	c := newCluster(t, all)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	t.Logf("=== 10-Agent Stress: PM coordinates 9 workers ===")

	// Send one big task to PM. PM must call a2a_remote 9 times.
	// This tests: concurrent A2A calls, task routing, result aggregation.
	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"Delegate to ALL 9 worker agents. Each must create a Go file. Call them ALL:\n"+
			"1. a2a_remote('svc-auth', 'code-edit', 'Create auth.go: package main; func Authenticate(token string) bool { return token != \"\" }')\n"+
			"2. a2a_remote('svc-user', 'code-edit', 'Create user.go: package main; type User struct{ID,Name string}; var Users = map[string]User{}')\n"+
			"3. a2a_remote('svc-order', 'code-edit', 'Create order.go: package main; type Order struct{ID,Product string}; var Orders = map[string]Order{}')\n"+
			"4. a2a_remote('svc-payment', 'code-edit', 'Create payment.go: package main; func ProcessPayment(id string, amt float64) string { return \"paid\" }')\n"+
			"5. a2a_remote('svc-notification', 'code-edit', 'Create notify.go: package main; func Send(to, msg string) { /* stub */ }')\n"+
			"6. a2a_remote('svc-search', 'code-edit', 'Create search.go: package main; func Search(q string) []string { return nil }')\n"+
			"7. a2a_remote('svc-analytics', 'code-edit', 'Create analytics.go: package main; func Track(name string) {}')\n"+
			"8. a2a_remote('svc-config', 'code-edit', 'Create config.go: package main; const Port = 8080')\n"+
			"9. a2a_remote('svc-logging', 'code-edit', 'Create logger.go: package main; func Info(m string) {}')\n"+
			"Execute all 9 calls. Report which succeeded and which failed.",
	)

	c.logTaskDetails("pm", task)

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("PM task: %s", task.Status.State)
	}

	// Count how many workers actually got files created.
	var created atomic.Int32
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			// Check if any .go file exists in the worker's dir.
			entries, err := os.ReadDir(c.node(name).dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".go") {
					data, err := os.ReadFile(filepath.Join(c.node(name).dir, e.Name()))
					if err == nil && len(data) > 10 {
						created.Add(1)
						t.Logf("[%s] ✅ %s (%d bytes)", name, e.Name(), len(data))
						return
					}
				}
			}
			t.Logf("[%s] ❌ no .go file found", name)
		}(w)
	}
	wg.Wait()

	count := created.Load()
	t.Logf("\n========== 10-Agent Results ==========")
	t.Logf("Workers with files: %d/9", count)

	if count < 6 {
		t.Errorf("only %d/9 workers produced files — unreliable", count)
	}

	t.Logf("✅ 10-agent stress test: %d/9 workers verified", count)
}

// TestMultiAgent_CrossVerification tests PM asks worker-A to write code,
// then asks worker-B to review worker-A's code.
func TestMultiAgent_CrossVerification(t *testing.T) {
	c := newCluster(t, []string{"pm", "coder", "reviewer"})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	t.Log("=== Cross-Verification: coder writes, reviewer reviews ===")

	// Step 1: PM tells coder to write code.
	task := c.dispatchAndWait(ctx, "pm", "full-task",
		"Step 1: Use a2a_remote(target='coder', skill='code-edit', message='Create calc.go: package main; "+
			"func Add(a,b int) int { return a+b }; func Subtract(a,b int) int { return a-b }; "+
			"func Multiply(a,b int) int { return a*b }')\n"+
			"Wait for the result, then...\n"+
			"Step 2: Use a2a_remote(target='reviewer', skill='code-review', message='Review the code that coder just created. "+
			"The coder was asked to create calc.go with Add, Subtract, Multiply functions. "+
			"Check if all 3 functions exist and are correct.')\n"+
			"Report both results.",
	)

	if task.Status.State != TaskStateCompleted {
		c.logTaskDetails("pm", task)
		t.Fatalf("PM task failed: %s", task.Status.State)
	}

	c.logTaskDetails("pm", task)
	c.verifyFile("coder", "calc.go", "func Add", "func Subtract", "func Multiply")

	t.Log("✅ Cross-verification (coder writes, reviewer reviews) verified")
}
