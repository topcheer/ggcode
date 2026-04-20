//go:build !integration

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Multi-instance mutual discovery & cross-instance communication tests
// ---------------------------------------------------------------------------

// testCluster sets up 3 A2A instances sharing one registry file.
// It simulates the real scenario where 3 ggcode processes discover each other.
type testCluster struct {
	t         *testing.T
	regDir    string
	instances []*testNode
}

type testNode struct {
	name     string
	server   *Server
	registry *Registry
	remote   *RemoteTool
	client   *Client
}

func newTestCluster(t *testing.T) *testCluster {
	t.Helper()

	// Isolated registry dir.
	regDir := filepath.Join(t.TempDir(), "a2a")
	os.MkdirAll(regDir, 0755)

	c := &testCluster{t: t, regDir: regDir}

	workspaces := []struct {
		name string
		dir  string
	}{
		{"user-service", "/tmp/test-a2a/user-service"},
		{"order-service", "/tmp/test-a2a/order-service"},
		{"gateway", "/tmp/test-a2a/gateway"},
	}

	for _, ws := range workspaces {
		handler := NewTaskHandler(ws.dir, nil, nil)
		srv := NewServer(ServerConfig{Port: 0}, handler)
		if err := srv.Start(); err != nil {
			t.Fatalf("start %s: %v", ws.name, err)
		}
		t.Cleanup(srv.Stop)

		reg := &Registry{dir: regDir}
		remote := NewRemoteTool(reg, "")
		client := NewClient(srv.Endpoint(), "")

		c.instances = append(c.instances, &testNode{
			name: ws.name, server: srv,
			registry: reg, remote: remote, client: client,
		})
	}

	return c
}

// registerAll writes all instances into the shared registry file,
// simulating each process having registered on startup.
func (c *testCluster) registerAll() {
	c.t.Helper()
	var instances []InstanceInfo
	for _, n := range c.instances {
		instances = append(instances, InstanceInfo{
			ID:        n.name + "-id",
			PID:       os.Getpid(),
			Workspace: "/tmp/test-a2a/" + n.name,
			Endpoint:  n.server.Endpoint(),
			Status:    "ready",
		})
	}
	data, _ := json.MarshalIndent(instances, "", "  ")
	os.WriteFile(filepath.Join(c.regDir, "instances.json"), data, 0644)
}

func (c *testCluster) node(name string) *testNode {
	for _, n := range c.instances {
		if n.name == name {
			return n
		}
	}
	c.t.Fatalf("node %s not found", name)
	return nil
}

// ---------------------------------------------------------------------------

// TestCluster_MutualDiscovery tests that all 3 instances see each other.
func TestCluster_MutualDiscovery(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	for _, node := range c.instances {
		result, err := node.remote.Execute(context.Background(), json.RawMessage(
			`{"target":"list","skill":"full-task","message":"test"}`))
		if err != nil {
			t.Fatalf("[%s] list failed: %v", node.name, err)
		}

		// Each node should see 3 instances total.
		// (They don't exclude themselves since selfID is not set in test registries.)
		for _, expected := range []string{"user-service", "order-service", "gateway"} {
			if !containsStr(result.Content, expected) {
				t.Errorf("[%s] expected to see %s in:\n%s", node.name, expected, result.Content)
			}
		}
		t.Logf("[%s] sees all 3 instances ✅", node.name)
	}
}

// TestCluster_CrossInstanceCall tests A→B, B→C, C→A calls.
func TestCluster_CrossInstanceCall(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx := context.Background()

	// A calls B
	result, err := c.node("user-service").remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"file-search","message":"find orders"}`))
	if err != nil {
		t.Fatalf("A→B: %v", err)
	}
	if result.IsError {
		t.Errorf("A→B error: %s", result.Content)
	}
	if !containsStr(result.Content, "Task sent to order-service") {
		t.Errorf("A→B unexpected result: %s", result.Content)
	}
	t.Logf("A→B ✅")

	// B calls C
	result, err = c.node("order-service").remote.Execute(ctx, json.RawMessage(
		`{"target":"gateway","skill":"file-search","message":"show status"}`))
	if err != nil {
		t.Fatalf("B→C: %v", err)
	}
	// The task may succeed or fail (workspace may not have content), but the
	// important thing is the call went through — "Task sent" proves cross-instance routing.
	if !containsStr(result.Content, "Task sent to gateway") {
		t.Errorf("B→C unexpected result: %s", result.Content)
	}
	t.Logf("B→C ✅")

	// C calls A
	result, err = c.node("gateway").remote.Execute(ctx, json.RawMessage(
		`{"target":"user-service","skill":"command-exec","message":"ls files"}`))
	if err != nil {
		t.Fatalf("C→A: %v", err)
	}
	if !containsStr(result.Content, "Task sent to user-service") {
		t.Errorf("C→A unexpected result: %s", result.Content)
	}
	t.Logf("C→A ✅")
}

// TestCluster_ChainedCall tests A calls B, then uses B's result to call C.
func TestCluster_ChainedCall(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx := context.Background()

	// A sends task to B
	result, err := c.node("user-service").remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"file-search","message":"list orders"}`))
	if err != nil {
		t.Fatalf("A→B: %v", err)
	}

	// Extract task ID from result (contains "Task ID: xxx").
	taskID := extractTaskID(result.Content)
	if taskID == "" {
		t.Fatalf("no task ID in A→B result: %s", result.Content)
	}
	t.Logf("A→B task ID: %s", taskID)

	// Now C checks status of B's task (simulating a chained workflow).
	bClient := c.node("order-service").client
	task, err := bClient.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("C→B get task: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("task ID mismatch: %s vs %s", task.ID, taskID)
	}
	t.Logf("C→B (get task %s: %s) ✅", taskID, task.Status.State)

	// Then C sends its own task to A based on that info.
	result, err = c.node("gateway").remote.Execute(ctx, json.RawMessage(
		`{"target":"user-service","skill":"full-task","message":"process result from order-service"}`))
	if err != nil {
		t.Fatalf("C→A: %v", err)
	}
	t.Logf("C→A (chained) ✅")
}

// TestCluster_NewInstanceDiscovered tests that a new instance D is visible
// after refreshing the cache.
func TestCluster_NewInstanceDiscovered(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	// Initially A sees 3 instances.
	result, _ := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "Found 3") {
		t.Errorf("expected 3 instances, got: %s", result.Content)
	}
	t.Logf("Before: A sees 3 instances ✅")

	// Instance D starts up.
	handlerD := NewTaskHandler("/tmp/test-a2a/notification-service", nil, nil)
	srvD := NewServer(ServerConfig{Port: 0}, handlerD)
	if err := srvD.Start(); err != nil {
		t.Fatal(err)
	}
	defer srvD.Stop()

	// D registers itself.
	regPath := filepath.Join(c.regDir, "instances.json")
	data, _ := os.ReadFile(regPath)
	var instances []InstanceInfo
	json.Unmarshal(data, &instances)
	instances = append(instances, InstanceInfo{
		ID:        "notification-service-id",
		PID:       os.Getpid(),
		Workspace: "/tmp/test-a2a/notification-service",
		Endpoint:  srvD.Endpoint(),
		Status:    "ready",
	})
	newData, _ := json.MarshalIndent(instances, "", "  ")
	os.WriteFile(regPath, newData, 0644)

	// A's cache is stale (10s TTL). Force refresh.
	nodeA.remote.RefreshCache()

	// Now A should see 4 instances including notification-service.
	result, _ = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "notification-service") {
		t.Errorf("expected to see notification-service after refresh, got:\n%s", result.Content)
	}
	t.Logf("After refresh: A sees 4 instances (including notification-service) ✅")

	// A can now call D directly.
	result, err := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"notification","skill":"file-search","message":"find handlers"}`))
	if err != nil {
		t.Fatalf("A→D: %v", err)
	}
	if result.IsError {
		t.Errorf("A→D error: %s", result.Content)
	}
	t.Logf("A→D (notification-service) ✅")
}

// TestCluster_InstanceGone tests that a departed instance is cleaned up.
func TestCluster_InstanceGone(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	// A sees all 3.
	result, _ := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "order-service") {
		t.Fatal("should see order-service initially")
	}
	t.Logf("Before: A sees order-service ✅")

	// Stop order-service's server and remove it from registry.
	c.node("order-service").server.Stop()
	regPath := filepath.Join(c.regDir, "instances.json")
	data, _ := os.ReadFile(regPath)
	var instances []InstanceInfo
	json.Unmarshal(data, &instances)
	var filtered []InstanceInfo
	for _, inst := range instances {
		if inst.ID != "order-service-id" {
			filtered = append(filtered, inst)
		}
	}
	newData, _ := json.MarshalIndent(filtered, "", "  ")
	os.WriteFile(regPath, newData, 0644)

	// Refresh and verify.
	nodeA.remote.RefreshCache()

	result, _ = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"full-task","message":"test"}`))
	if !result.IsError {
		t.Error("expected error for departed instance")
	}
	if !containsStr(result.Content, "no instance matching") {
		t.Errorf("unexpected error: %s", result.Content)
	}
	t.Logf("After removal: A correctly can't find order-service ✅")
}

// TestCluster_ConcurrentCrossCalls tests all 3 instances calling each other
// simultaneously.
func TestCluster_ConcurrentCrossCalls(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 6 call pairs: A→B, A→C, B→A, B→C, C→A, C→B
	pairs := [][2]string{
		{"user-service", "order-service"},
		{"user-service", "gateway"},
		{"order-service", "user-service"},
		{"order-service", "gateway"},
		{"gateway", "user-service"},
		{"gateway", "order-service"},
	}

	var wg sync.WaitGroup
	results := make(chan string, len(pairs))

	for _, pair := range pairs {
		wg.Add(1)
		go func(from, to string) {
			defer wg.Done()
			node := c.node(from)
			result, err := node.remote.Execute(ctx, json.RawMessage(
				`{"target":"`+to+`","skill":"file-search","message":"test from `+from+`"}`))
			if err != nil {
				results <- fmt.Sprintf("%s→%s ERROR: %v", from, to, err)
				return
			}
			if result.IsError {
				results <- fmt.Sprintf("%s→%s FAIL: %s", from, to, result.Content)
			} else {
				results <- fmt.Sprintf("%s→%s OK", from, to)
			}
		}(pair[0], pair[1])
	}
	wg.Wait()
	close(results)

	ok, fail := 0, 0
	for r := range results {
		if containsStr(r, "OK") {
			ok++
		} else {
			fail++
			t.Logf("  ❌ %s", r)
		}
	}
	t.Logf("Concurrent cross-calls: %d OK, %d FAIL", ok, fail)
	if fail > 0 {
		t.Errorf("%d cross-calls failed", fail)
	}
	if ok != len(pairs) {
		t.Errorf("expected %d OK, got %d", len(pairs), ok)
	}
}

// TestCluster_FuzzyMatching tests partial name matching.
func TestCluster_FuzzyMatching(t *testing.T) {
	c := newTestCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	// Exact match.
	result, err := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("exact match failed: %v / %s", err, result.Content)
	}
	t.Logf("exact 'order-service' ✅")

	// Partial match — "order" should match "order-service".
	result, err = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"order","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("partial match failed: %v / %s", err, result.Content)
	}
	t.Logf("partial 'order' → order-service ✅")

	// Partial match — "gate" should match "gateway".
	result, err = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"gate","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("partial 'gate' failed: %v / %s", err, result.Content)
	}
	t.Logf("partial 'gate' → gateway ✅")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractTaskID(content string) string {
	// Find "Task ID: xxx" in content.
	prefix := "Task ID: "
	idx := containsStrAt(content, prefix)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(prefix):]
	end := len(rest)
	for i, ch := range rest {
		if ch == '\n' || ch == '\r' || ch == ' ' {
			end = i
			break
		}
	}
	return rest[:end]
}

func containsStrAt(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
