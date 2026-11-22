package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func jsonInput(t *testing.T, v map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return b
}

func newWiredPair(sessionID string) (*LeaderInboxStore, ReportToLeaderTool, LeaderInboxTool) {
	store := NewLeaderInboxStore()
	leader := LeaderInboxTool{Inbox: store, SessionID: func() string { return sessionID }, leader: true}
	// Worker instances are what clone-bearing worker toolsets hold
	// (Registry.Clone flips the role) - construct them directly here.
	worker := ReportToLeaderTool{Inbox: store, SessionID: func() string { return sessionID }, worker: true}
	return store, worker, leader
}

// TestLeaderInboxSessionIsolation is the core property: a worker of session
// A pushes; the leader of session B sees NOTHING; the leader of session A
// sees the message; the pull consumes (second drain is empty).
func TestLeaderInboxSessionIsolation(t *testing.T) {
	store, workerA, _ := newWiredPair("session-A")

	res, err := workerA.Execute(context.Background(), jsonInput(t, map[string]string{
		"message": "blocked on missing spec",
		"from":    "agent-1",
	}))
	if err != nil || res.IsError {
		t.Fatalf("worker A push failed: err=%v res=%+v", err, res)
	}

	// Cross-session read must see nothing.
	leaderB := LeaderInboxTool{Inbox: store, SessionID: func() string { return "session-B" }, leader: true}
	resB, err := leaderB.Execute(context.Background(), jsonInput(t, nil))
	if err != nil {
		t.Fatalf("leader B drain error: %v", err)
	}
	if resB.IsError {
		t.Fatalf("leader B drain must not error: %+v", resB)
	}
	if !strings.Contains(resB.Content, "No pending") {
		t.Fatalf("leader B must see zero messages, got: %s", resB.Content)
	}
	if got := store.Len("session-B"); got != 0 {
		t.Fatalf("session B queue must stay empty, got %d", got)
	}
	if got := store.Len("session-A"); got != 1 {
		t.Fatalf("session A queue must hold the push, got %d", got)
	}

	// Same-session leader sees exactly the pushed message.
	leaderA := LeaderInboxTool{Inbox: store, SessionID: func() string { return "session-A" }, leader: true}
	resA, err := leaderA.Execute(context.Background(), jsonInput(t, nil))
	if err != nil || resA.IsError {
		t.Fatalf("leader A drain failed: err=%v res=%+v", err, resA)
	}
	if !strings.Contains(resA.Content, "blocked on missing spec") || !strings.Contains(resA.Content, "agent-1") {
		t.Fatalf("leader A must see worker A's report, got: %s", resA.Content)
	}

	// Read-on-consume: second drain is empty and the queue is gone.
	resA2, _ := leaderA.Execute(context.Background(), jsonInput(t, nil))
	if !strings.Contains(resA2.Content, "No pending") {
		t.Fatalf("drain must consume: second drain got: %s", resA2.Content)
	}
	if got := store.Len("session-A"); got != 0 {
		t.Fatalf("session A queue must be empty after drain, got %d", got)
	}
}

// TestReportToLeaderSessionKeyNeverCallerSupplied pins that the session key
// comes from the wired getter, never from tool input: a caller cannot spoof
// another session by adding a session field to the arguments.
func TestReportToLeaderSessionKeyNeverCallerSupplied(t *testing.T) {
	store, workerA, _ := newWiredPair("session-A")
	// Attempt to smuggle a different session id through the input.
	res, err := workerA.Execute(context.Background(), json.RawMessage(`{"message":"x","from":"tm-9","session":"session-Z"}`))
	if err != nil || res.IsError {
		t.Fatalf("push failed: err=%v res=%+v", err, res)
	}
	if store.Len("session-Z") != 0 {
		t.Fatalf("caller-supplied session key must be ignored")
	}
	if got := store.Len("session-A"); got != 1 {
		t.Fatalf("report must land in the resolved session, got %d in session-A", got)
	}
}

// TestReportToLeaderRejectsLeaderInstance: the leader's own registered
// instance (not a clone) must error - the leader cannot report to itself.
func TestReportToLeaderRejectsLeaderInstance(t *testing.T) {
	store := NewLeaderInboxStore()
	// The instance registered on the leader's own registry: worker=false.
	leaderPush := ReportToLeaderTool{Inbox: store, SessionID: func() string { return "session-A" }}
	res, err := leaderPush.Execute(context.Background(), jsonInput(t, map[string]string{"message": "hi"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("leader instance must refuse to push, got: %+v", res)
	}
	if !strings.Contains(res.Content, "worker-only") {
		t.Fatalf("error must explain the role rule, got: %s", res.Content)
	}
}

// TestLeaderInboxRejectsWorkerClone: a worker clone must not be able to
// drain the leader's inbox.
func TestLeaderInboxRejectsWorkerClone(t *testing.T) {
	store, workerA, _ := newWiredPair("session-A")
	if _, err := workerA.Execute(context.Background(), jsonInput(t, map[string]string{"message": "m", "from": "tm-1"})); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	// The clone the swarm toolBuilder hands a teammate must refuse to drain.
	leader := LeaderInboxTool{Inbox: store, SessionID: func() string { return "session-A" }, leader: true}
	cloned := leader.Clone().(LeaderInboxTool)
	res, err := cloned.Execute(context.Background(), jsonInput(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("worker clone must refuse to drain, got: %+v", res)
	}
	if got := store.Len("session-A"); got != 1 {
		t.Fatalf("refused drain must not consume, queue len %d", got)
	}
}

// TestCloneFreezesSessionID: worker clones keep the session they were
// spawned in even after the leader's live getter rebinds to a new session.
func TestCloneFreezesSessionID(t *testing.T) {
	store := NewLeaderInboxStore()
	current := "session-A"
	leaderGetter := func() string { return current }
	push := ReportToLeaderTool{Inbox: store, SessionID: leaderGetter}
	cloned := push.Clone().(ReportToLeaderTool)

	// Leader rebinds (TUI session switch) AFTER the clone was handed out.
	current = "session-B"

	res, err := cloned.Execute(context.Background(), jsonInput(t, map[string]string{"message": "still session A", "from": "agent-7"}))
	if err != nil || res.IsError {
		t.Fatalf("clone push failed: err=%v res=%+v", err, res)
	}
	if got := store.Len("session-B"); got != 0 {
		t.Fatalf("clone must NOT report into the rebound session, got %d", got)
	}
	if got := store.Len("session-A"); got != 1 {
		t.Fatalf("clone must report into its frozen session, got %d", got)
	}
}

// TestLeaderInboxQueueCap: a session whose leader never drains gets a
// deterministic rejection instead of unbounded growth.
func TestLeaderInboxQueueCap(t *testing.T) {
	_, workerA, _ := newWiredPair("session-A")
	for i := 0; i < maxLeaderInboxPerSession; i++ {
		if _, err := workerA.Execute(context.Background(), jsonInput(t, map[string]string{"message": "m"})); err != nil {
			t.Fatalf("push %d failed: %v", i, err)
		}
	}
	res, err := workerA.Execute(context.Background(), jsonInput(t, map[string]string{"message": "overflow"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "full") {
		t.Fatalf("cap overflow must error clearly, got: %+v", res)
	}
}

// TestDropSessionRemovesQueue: session-switch cleanup discards stale
// queues so the next session's leader can never inherit them.
func TestDropSessionRemovesQueue(t *testing.T) {
	store, workerA, _ := newWiredPair("session-A")
	if _, err := workerA.Execute(context.Background(), jsonInput(t, map[string]string{"message": "m", "from": "tm-3"})); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	store.DropSession("session-A")
	if got := store.Len("session-A"); got != 0 {
		t.Fatalf("dropped session queue must be empty, got %d", got)
	}
	leaderA := LeaderInboxTool{Inbox: store, SessionID: func() string { return "session-A" }, leader: true}
	res, _ := leaderA.Execute(context.Background(), jsonInput(t, nil))
	if !strings.Contains(res.Content, "No pending") {
		t.Fatalf("dropped session must drain nothing, got: %s", res.Content)
	}
}

// TestPushRejectsUnscopedSession: without a resolvable session id the push
// is rejected - session scoping is mandatory, never defaulted.
func TestPushRejectsUnscopedSession(t *testing.T) {
	store := NewLeaderInboxStore()
	push := ReportToLeaderTool{Inbox: store, SessionID: func() string { return "" }}
	res, err := push.Execute(context.Background(), jsonInput(t, map[string]string{"message": "m"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "session") {
		t.Fatalf("unscoped push must be rejected, got: %+v", res)
	}
}

// TestLeaderInboxRegistryCloneWiring pins the integration property the
// runtime relies on: after Registry.Clone() (how sub-agent/teammate
// toolsets are built), report_to_leader becomes worker-enabled and
// leader_inbox becomes worker-disabled.
func TestLeaderInboxRegistryCloneWiring(t *testing.T) {
	store, workerA, leader := newWiredPair("session-A")
	_ = workerA
	reg := NewRegistry()
	if err := reg.Register(leader); err != nil {
		t.Fatalf("register leader_inbox: %v", err)
	}
	if err := reg.Register(ReportToLeaderTool{Inbox: store, SessionID: func() string { return "session-A" }}); err != nil {
		t.Fatalf("register report_to_leader: %v", err)
	}

	clonedReg := reg.Clone()

	clonedPush, ok := clonedReg.Get("report_to_leader")
	if !ok {
		t.Fatalf("worker toolset must keep report_to_leader")
	}
	res, err := clonedPush.Execute(context.Background(), jsonInput(t, map[string]string{"message": "worker via clone", "from": "agent-2"}))
	if err != nil || res.IsError {
		t.Fatalf("cloned report_to_leader must work for workers: err=%v res=%+v", err, res)
	}

	clonedPull, ok := clonedReg.Get("leader_inbox")
	if !ok {
		t.Fatalf("worker toolset still contains leader_inbox (blocked lists remove it upstream); clone must still refuse")
	}
	res, err = clonedPull.Execute(context.Background(), jsonInput(t, nil))
	if err != nil || !res.IsError {
		t.Fatalf("cloned leader_inbox must refuse workers: err=%v res=%+v", err, res)
	}
	if got := store.Len("session-A"); got != 1 {
		t.Fatalf("refused clone drain must not consume, queue len %d", got)
	}
}
