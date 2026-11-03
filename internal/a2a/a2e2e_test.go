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
	"testing"
	"time"
)

type discoveryCluster struct {
	t         *testing.T
	regDir    string
	instances []*discoveryNode
}

type discoveryNode struct {
	name     string
	server   *Server
	registry *Registry
	remote   *RemoteTool
	client   *Client
}

func newDiscoveryCluster(t *testing.T) *discoveryCluster {
	t.Helper()

	regDir := filepath.Join(t.TempDir(), "a2a")
	os.MkdirAll(regDir, 0755)

	c := &discoveryCluster{t: t, regDir: regDir}

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

		reg := &Registry{}
		remote := NewRemoteTool(reg, "")
		client := NewClient(srv.Endpoint(), "")

		c.instances = append(c.instances, &discoveryNode{
			name: ws.name, server: srv,
			registry: reg, remote: remote, client: client,
		})
	}

	return c
}

func (c *discoveryCluster) registerAll() {
	c.t.Helper()
	for _, n := range c.instances {
		inst := InstanceInfo{
			ID:        n.name + "-id",
			PID:       os.Getpid(),
			Workspace: "/tmp/test-a2a/" + n.name,
			Endpoint:  n.server.Endpoint(),
			Status:    "ready",
		}
		data, _ := json.MarshalIndent(inst, "", "  ")
		os.WriteFile(filepath.Join(c.regDir, inst.ID+".json"), data, 0644)
	}
	c.refreshFromDisk()
}

func (c *discoveryCluster) refreshFromDisk() {
	c.t.Helper()
	entries, err := os.ReadDir(c.regDir)
	if err != nil {
		c.t.Fatalf("read regDir %s: %v", c.regDir, err)
	}
	var instances []InstanceInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.regDir, e.Name()))
		if err != nil {
			continue
		}
		var inst InstanceInfo
		if json.Unmarshal(data, &inst); err == nil && inst.ID != "" {
			instances = append(instances, inst)
		}
	}
	for _, n := range c.instances {
		n.registry.SetCachedInstances(instances)
	}
}

func (c *discoveryCluster) addInstance(inst InstanceInfo) {
	data, _ := json.MarshalIndent(inst, "", "  ")
	os.WriteFile(filepath.Join(c.regDir, inst.ID+".json"), data, 0644)
	c.refreshFromDisk()
}

func (c *discoveryCluster) removeInstance(id string) {
	os.Remove(filepath.Join(c.regDir, id+".json"))
	c.refreshFromDisk()
}

func (c *discoveryCluster) node(name string) *discoveryNode {
	for _, n := range c.instances {
		if n.name == name {
			return n
		}
	}
	c.t.Fatalf("node %s not found", name)
	return nil
}

func TestCluster_MutualDiscovery(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	for _, node := range c.instances {
		result, err := node.remote.Execute(context.Background(), json.RawMessage(
			`{"target":"list","skill":"full-task","message":"test"}`))
		if err != nil {
			t.Fatalf("[%s] list failed: %v", node.name, err)
		}

		for _, expected := range []string{"user-service", "order-service", "gateway"} {
			if !containsStr(result.Content, expected) {
				t.Errorf("[%s] expected to see %s in:\n%s", node.name, expected, result.Content)
			}
		}
		t.Logf("[%s] sees all 3 instances ✅", node.name)
	}
}

func TestCluster_CrossInstanceCall(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx := context.Background()

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

	result, err = c.node("order-service").remote.Execute(ctx, json.RawMessage(
		`{"target":"gateway","skill":"file-search","message":"show status"}`))
	if err != nil {
		t.Fatalf("B→C: %v", err)
	}
	if !containsStr(result.Content, "Task sent to gateway") {
		t.Errorf("B→C unexpected result: %s", result.Content)
	}
	t.Logf("B→C ✅")

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

func TestCluster_ChainedCall(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx := context.Background()

	result, err := c.node("user-service").remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"file-search","message":"list orders"}`))
	if err != nil {
		t.Fatalf("A→B: %v", err)
	}

	taskID := extractTaskID(result.Content)
	if taskID == "" {
		t.Fatalf("no task ID in A→B result: %s", result.Content)
	}
	t.Logf("A→B task ID: %s", taskID)

	bClient := c.node("order-service").client
	task, err := bClient.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("C→B get task: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("task ID mismatch: %s vs %s", task.ID, taskID)
	}
	t.Logf("C→B (get task %s: %s) ✅", taskID, task.Status.State)

	result, err = c.node("gateway").remote.Execute(ctx, json.RawMessage(
		`{"target":"user-service","skill":"full-task","message":"process result from order-service"}`))
	if err != nil {
		t.Fatalf("C→A: %v", err)
	}
	t.Logf("C→A (chained) ✅")
}

func TestCluster_NewInstanceDiscovered(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	result, _ := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "Found 3") {
		t.Errorf("expected 3 instances, got: %s", result.Content)
	}
	t.Logf("Before: A sees 3 instances ✅")

	handlerD := NewTaskHandler("/tmp/test-a2a/notification-service", nil, nil)
	srvD := NewServer(ServerConfig{Port: 0}, handlerD)
	if err := srvD.Start(); err != nil {
		t.Fatal(err)
	}
	defer srvD.Stop()

	newInst := InstanceInfo{
		ID:        "notification-service-id",
		PID:       os.Getpid(),
		Workspace: "/tmp/test-a2a/notification-service",
		Endpoint:  srvD.Endpoint(),
		Status:    "ready",
	}
	c.addInstance(newInst)

	nodeA.remote.RefreshCache()

	result, _ = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "notification-service") {
		t.Errorf("expected to see notification-service after refresh, got:\n%s", result.Content)
	}
	t.Logf("After refresh: A sees 4 instances (including notification-service) ✅")

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

func TestCluster_InstanceGone(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	result, _ := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"list","skill":"full-task","message":"test"}`))
	if !containsStr(result.Content, "order-service") {
		t.Fatal("should see order-service initially")
	}
	t.Logf("Before: A sees order-service ✅")

	c.node("order-service").server.Stop()
	c.removeInstance("order-service-id")

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

func TestCluster_ConcurrentCrossCalls(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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

func TestCluster_FuzzyMatching(t *testing.T) {
	c := newDiscoveryCluster(t)
	c.registerAll()

	ctx := context.Background()
	nodeA := c.node("user-service")

	result, err := nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"order-service","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("exact match failed: %v / %s", err, result.Content)
	}
	t.Logf("exact 'order-service' ✅")

	result, err = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"order","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("partial match failed: %v / %s", err, result.Content)
	}
	t.Logf("partial 'order' → order-service ✅")

	result, err = nodeA.remote.Execute(ctx, json.RawMessage(
		`{"target":"gate","skill":"file-search","message":"test"}`))
	if err != nil || result.IsError {
		t.Fatalf("partial 'gate' failed: %v / %s", err, result.Content)
	}
	t.Logf("partial 'gate' → gateway ✅")
}

func extractTaskID(content string) string {
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
