package tool

import (
	"reflect"
	"testing"
)

func TestIssue2596SiblingClonesAreSettable(t *testing.T) {
	names := []string{"review_changes", "dep_graph", "desktop_control", "open_editor"}

	reg := NewRegistry()

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

			if got == orig {
				t.Fatalf("%s: clone shares the original instance (%#v)", name, orig)
			}

			v := reflect.ValueOf(got)
			if v.Kind() != reflect.Ptr {
				t.Fatalf("%s: clone is not a pointer (%T) - syncToolWorkingDir reflection cannot Set it", name, got)
			}
			f := v.Elem().FieldByName("WorkingDir")
			if !f.IsValid() || !f.CanSet() || f.Kind() != reflect.String {
				t.Fatalf("%s: clone WorkingDir not settable (valid=%v canSet=%v kind=%v)", name, f.IsValid(), f.CanSet(), f.Kind())
			}

			f.SetString("/wt/agent-a")

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

	if got, ok := cloneB.Get("dep_graph"); ok {
		reflect.ValueOf(got).Elem().FieldByName("WorkingDir").SetString("/wt/agent-b")
	}

	a, _ := cloneA.Get("dep_graph")
	b, _ := cloneB.Get("dep_graph")
	if got := resolveDir("", reflect.ValueOf(a).Elem().FieldByName("WorkingDir").String()); got != "/wt/agent-a" {
		t.Fatalf("cloneA resolves %q, want /wt/agent-a", got)
	}
	if got := resolveDir("", reflect.ValueOf(b).Elem().FieldByName("WorkingDir").String()); got != "/wt/agent-b" {
		t.Fatalf("cloneB resolves %q, want /wt/agent-b (cross-clone bleed)", got)
	}

	if got := resolveDir("/explicit", reflect.ValueOf(a).Elem().FieldByName("WorkingDir").String()); got != "/explicit" {
		t.Fatalf("explicit path lost: %q", got)
	}

	orig, _ := reg.Get("dep_graph")
	if got := resolveDir("", reflect.ValueOf(orig).FieldByName("WorkingDir").String()); got != "/orig-ws" {
		t.Fatalf("original resolves %q, want /orig-ws", got)
	}
}
