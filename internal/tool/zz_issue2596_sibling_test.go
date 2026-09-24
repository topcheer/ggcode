package tool

import (
	"reflect"
	"testing"
)

// TestIssue2596SiblingClonesAreSettable pins the #2596 sibling fix:
// ReviewChanges/DepGraphTool/DesktopControlTool/OpenEditorTool are registered
// by VALUE in builtin.go. Before this fix they had no Cloner implementation,
// so Registry.Clone() shared one instance across every agent and
// syncToolWorkingDir's reflection could not retarget them (a value stored in
// the registry map is not addressable, so f.CanSet() was false and the Set was
// silently skipped). Every cloned teammate/sub-agent kept the original
// registry's WorkingDir forever: dep_graph and review_changes resolve their
// target via resolveDir(args.Path, t.WorkingDir), so a worktree agent analyzed
// the MAIN workspace instead of its own worktree.
//
// With Clone() returning a pointer copy, each registry clone gets an
// independent, settable instance.
func TestIssue2596SiblingClonesAreSettable(t *testing.T) {
	names := []string{"review_changes", "dep_graph", "desktop_control", "open_editor"}

	reg := NewRegistry()
	// Registration by value, mirroring builtin.go exactly.
	if err := reg.Register(ReviewChanges{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(DepGraphTool{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(DesktopControlTool{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(OpenEditorTool{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}

	clone := reg.Clone()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			orig, ok := reg.Get(name)
			if !ok {
				t.Fatalf("%s missing from registry", name)
			}
			got, ok := clone.Get(name)
			if !ok {
				t.Fatalf("%s missing from clone", name)
			}

			// Independent instance: the clone must not share the original.
			if got == orig {
				t.Fatalf("%s: clone shares the original instance (%#v)", name, orig)
			}

			// The exact #2596-sibling failure mode: syncToolWorkingDir requires
			// a settable string field. Value instances fail CanSet; the Clone()
			// pointer copy must satisfy it.
			v := reflect.ValueOf(got)
			if v.Kind() != reflect.Ptr {
				t.Fatalf("%s: clone is not a pointer (%T) - syncToolWorkingDir reflection cannot Set it", name, got)
			}
			f := v.Elem().FieldByName("WorkingDir")
			if !f.IsValid() || !f.CanSet() || f.Kind() != reflect.String {
				t.Fatalf("%s: clone WorkingDir not settable (valid=%v canSet=%v kind=%v)", name, f.IsValid(), f.CanSet(), f.Kind())
			}

			// Retarget the clone the way syncToolWorkingDir does.
			f.SetString("/wt/agent-a")

			// The original registry instance must be untouched (no
			// cross-agent bleed).
			ov := reflect.ValueOf(orig)
			var origDir string
			if ov.Kind() == reflect.Ptr {
				origDir = ov.Elem().FieldByName("WorkingDir").String()
			} else {
				origDir = ov.FieldByName("WorkingDir").String()
			}
			if origDir != "/orig-ws" {
				t.Fatalf("%s: original instance mutated to %q", name, origDir)
			}
		})
	}
}

// TestIssue2596DepGraphCloneResolvesRetargetedDir proves the behavioral fix
// end-to-end at the resolution layer: after a clone is retargeted (as
// syncToolWorkingDir does per agent), Execute's directory resolution
// (resolveDir(args.Path, t.WorkingDir)) must land on the agent's own dir, and
// a second clone must not bleed into the first.
func TestIssue2596DepGraphCloneResolvesRetargetedDir(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(DepGraphTool{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(ReviewChanges{WorkingDir: "/orig-ws"}); err != nil {
		t.Fatal(err)
	}

	cloneA := reg.Clone()
	cloneB := reg.Clone()

	for _, reg2 := range []*Registry{cloneA, cloneB} {
		got, _ := reg2.Get("dep_graph")
		reflect.ValueOf(got).Elem().FieldByName("WorkingDir").SetString("/wt/agent-a")
	}
	// cloneB gets a different dir, like a second teammate in another worktree.
	if got, ok := cloneB.Get("dep_graph"); ok {
		reflect.ValueOf(got).Elem().FieldByName("WorkingDir").SetString("/wt/agent-b")
	}

	// Resolution follows the per-agent WorkingDir (empty args.Path delegates
	// to t.WorkingDir, the exact Execute path).
	a, _ := cloneA.Get("dep_graph")
	b, _ := cloneB.Get("dep_graph")
	if got := resolveDir("", reflect.ValueOf(a).Elem().FieldByName("WorkingDir").String()); got != "/wt/agent-a" {
		t.Fatalf("cloneA resolves %q, want /wt/agent-a", got)
	}
	if got := resolveDir("", reflect.ValueOf(b).Elem().FieldByName("WorkingDir").String()); got != "/wt/agent-b" {
		t.Fatalf("cloneB resolves %q, want /wt/agent-b (cross-clone bleed)", got)
	}

	// Explicit args.Path still wins over WorkingDir (resolveDir semantics).
	if got := resolveDir("/explicit", reflect.ValueOf(a).Elem().FieldByName("WorkingDir").String()); got != "/explicit" {
		t.Fatalf("explicit path lost: %q", got)
	}

	// Original registry instance still points at the original workspace.
	orig, _ := reg.Get("dep_graph")
	if got := resolveDir("", reflect.ValueOf(orig).FieldByName("WorkingDir").String()); got != "/orig-ws" {
		t.Fatalf("original resolves %q, want /orig-ws", got)
	}
}
