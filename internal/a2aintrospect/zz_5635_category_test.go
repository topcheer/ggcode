package a2aintrospect

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/a2a"
)

// #5635 take-ownership: category pull on the Sources pipeline - the
// formalized mounts/state/content layering the user ruled on. Category
// expands to its faces subset; an explicit faces list intersects; an
// unknown category is an error, not a silent empty snapshot.
func TestIssue5635_CategoryPull(t *testing.T) {
	src := &Sources{
		Status: func() *a2a.RuntimeStatus { return &a2a.RuntimeStatus{Model: "glm-5.3"} },
		Env:    func() *a2a.RuntimeEnv { return &a2a.RuntimeEnv{Workspace: "/w", Provider: "test"} },
		Plan:   func() *a2a.RuntimePlan { return &a2a.RuntimePlan{} },
		Now:    func() time.Time { return time.Unix(0, 0).UTC() },
	}

	// mounts = env only
	snap, err := src.RuntimeSnapshotV3(a2a.SnapshotRequest{Category: "mounts"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Env == nil {
		t.Fatal("mounts category must keep the env face")
	}
	if snap.Status != nil || snap.Plan != nil {
		t.Fatalf("mounts category must drop non-env faces: status=%v plan=%v", snap.Status, snap.Plan)
	}

	// state = status+inflight+children+schedule+plan
	snap, err = src.RuntimeSnapshotV3(a2a.SnapshotRequest{Category: "state"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status == nil || snap.Plan == nil {
		t.Fatal("state category must keep status and plan")
	}
	if snap.Env != nil {
		t.Fatal("state category must drop the env face")
	}

	// faces intersects category
	snap, err = src.RuntimeSnapshotV3(a2a.SnapshotRequest{Category: "state", Faces: []string{"status"}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status == nil {
		t.Fatal("faces filter within state must keep status")
	}
	if snap.Plan != nil {
		t.Fatal("faces filter within state must drop plan")
	}

	// unknown category errors instead of an empty snapshot
	if _, err := src.RuntimeSnapshotV3(a2a.SnapshotRequest{Category: "bogus"}); err == nil {
		t.Fatal("unknown category must error")
	}
}

// env face carries the memory key index (#5657 followup plan): keys only,
// no bodies - cross-seat knowledge visibility without content exposure.
func TestEnvFaceMemoryKeysSerialized(t *testing.T) {
	src := &Sources{
		Env: func() *a2a.RuntimeEnv {
			return &a2a.RuntimeEnv{Workspace: "/w", MemoryKeys: []string{"build-process", "api-gotcha"}}
		},
		Now: func() time.Time { return time.Unix(0, 0).UTC() },
	}
	snap, err := src.RuntimeSnapshotV3(a2a.SnapshotRequest{Depth: a2a.DepthBrief})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Env == nil || len(snap.Env.MemoryKeys) != 2 || snap.Env.MemoryKeys[0] != "build-process" {
		t.Fatalf("env memory keys not carried: %+v", snap.Env)
	}
}
