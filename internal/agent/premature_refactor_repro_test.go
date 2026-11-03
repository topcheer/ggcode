package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrematureRefactor_WiringOrderBug_IsDead(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}

	a.prematureRefactorRecordVerifyForTool(json.RawMessage(`{"path":"foo.go"}`))
	if a.prematureRefactor.hasVerified {
		t.Fatal("read_file must NOT set hasVerified (#487): detector was 100% dead")
	}

	old1 := strings.Repeat("some existing code line that is fairly long\n", 6)
	new1 := strings.Repeat("renamed existing code line fairly long\n", 6)
	a.prematureRefactorRecordEdit("edit_file", mustMarshalEdit(old1, new1))
	a.prematureRefactorRecordEdit("edit_file", mustMarshalEdit(old1, new1))

	if msg := a.prematureRefactorCheck(); msg == "" {
		t.Fatal("detector must fire after 2 refactoring edits with NO verify command (#487)")
	}
}

func TestPrematureRefactor_RealVerifyCommandSilences(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}

	a.prematureRefactorRecordVerifyForTool(json.RawMessage(`{"command":"go test ./..."}`))
	if !a.prematureRefactor.hasVerified {
		t.Fatal("go test must set hasVerified")
	}

	old1 := strings.Repeat("some existing code line that is fairly long\n", 6)
	new1 := strings.Repeat("renamed existing code line fairly long\n", 6)
	a.prematureRefactorRecordEdit("edit_file", mustMarshalEdit(old1, new1))
	a.prematureRefactorRecordEdit("edit_file", mustMarshalEdit(old1, new1))

	if msg := a.prematureRefactorCheck(); msg != "" {
		t.Fatalf("after a real verify command the detector must stay silent, got: %s", msg)
	}
}

func TestPrematureRefactor_LocalizedFixNotRefactoring(t *testing.T) {
	old := "if len(x) > 3 {\n\tdoSomething(a, b)\n}\n" + strings.Repeat("// padding to exceed the min edit length threshold for classification\n", 4)
	new := "if len(x) >= 3 {\n\tdoSomething(a; b)\n}\n" + strings.Repeat("// padding to exceed the min edit length threshold for classification\n", 4)
	if isRefactoringContent(old, new) {
		t.Fatal("localized `>`→`>=` fix with tiny core delta must NOT classify as refactoring (#487 F1)")
	}
}

func TestPrematureRefactor_KeywordWordBoundary(t *testing.T) {
	base := strings.Repeat("unrelated code line content here padding\n", 6)
	for _, tc := range []struct{ name, newText string }{
		{"identifier extractTargets", base + "result := extractTargets(args)\n"},
		{"identifier abstractHandler", base + "h := abstractHandler{}\n"},
		{"comment optimize later", base + "// TODO: optimize later after profiling\n"},
		{"comment refactor intent", base + "// refactor: extract the helper next\n"},
	} {
		if isRefactoringContent(base, tc.newText) {
			t.Errorf("%s: identifier/comment hit must NOT classify as refactoring keyword (#487 F2)", tc.name)
		}
	}

	ro := "func process(items []string) {\n\tfor i := range items {\n\t\thandleOne(items[i])\n\t}\n}\n" + strings.Repeat("// pad line to keep sizes similar here\n", 5)
	rn := "func process(items []string) {\n\tbatch := collectAll(items)\n\tfor _, it := range batch {\n\t\thandleOneItem(it)\n\t}\n}\n" + strings.Repeat("// pad line to keep sizes similar here\n", 5)
	if !isRefactoringContent(ro, rn) {
		t.Error("whole-line restructuring with similar sizes must still classify as refactoring")
	}
}

func mustMarshalEdit(old, new string) []byte {
	b, _ := json.Marshal(map[string]string{"old_text": old, "new_text": new})
	return b
}
