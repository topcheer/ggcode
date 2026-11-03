package tool

import (
	"strings"
	"testing"
)

func Test1694AndroidRefResolution(t *testing.T) {
	a := &androidBackend{}

	_, _, err := a.resolveRef("dev1", "@e1")
	if err == nil || !strings.Contains(err.Error(), "prior snapshot") {
		t.Fatalf("no-snapshot ref must error, got %v", err)
	}

	root := &uiElement{Type: "root", Children: []*uiElement{
		{Type: "button", Label: "OK", Rect: &uiRect{X: 100, Y: 200, Width: 40, Height: 20}},
	}}
	formatted := formatSnapshot(root, "")
	if !strings.Contains(formatted, "@e1") {
		t.Fatal("snapshot output must advertise @e1")
	}
	a.mu.Lock()
	if a.snapshots == nil {
		a.snapshots = map[string]*uiElement{}
	}
	a.snapshots["dev1"] = root
	a.mu.Unlock()

	cx, cy, err := a.resolveRef("dev1", "@e1")
	if err != nil {
		t.Fatal(err)
	}
	if cx != 120 || cy != 210 {
		t.Fatalf("ref must resolve to element center, got (%d,%d)", cx, cy)
	}

	if _, _, err := a.resolveRef("dev1", "@e9"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("stale ref must error, got %v", err)
	}

	if _, _, err := a.resolveRef("dev1", "e1"); err == nil {
		t.Fatal("malformed ref must error")
	}

	if _, _, err := a.resolveRef("dev2", "@e1"); err == nil {
		t.Fatal("other device must not see dev1's snapshot")
	}
}

func Test1694LspSeverityLabels(t *testing.T) {
	cases := map[int]string{1: "Error", 2: "Warning", 3: "Info", 4: "Hint", 9: "severity-9"}
	for sev, want := range cases {
		if got := lspSeverityLabel(sev); got != want {
			t.Fatalf("sev %d: got %q want %q", sev, got, want)
		}
	}
}
