package agent

import (
	"strings"
	"testing"
)

func issue2733ScopeConstraints(s *constraintViolationState) []cvConstraint {
	var out []cvConstraint
	for _, c := range s.constraints {
		if c.constraintT == "scope" {
			out = append(out, c)
		}
	}
	return out
}

func TestIssue2733LaterScopeSupersedesEarlier(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth/ directory.", 1)
	s.recordReasoning("I'll limit changes to the docs/ folder.", 5)

	scopes := issue2733ScopeConstraints(s)
	if len(scopes) != 1 {
		t.Fatalf("expected exactly 1 scope constraint after supersede, got %d (%+v)", len(scopes), scopes)
	}
	if scopes[0].pattern != "docs/" {
		t.Fatalf("expected surviving scope pattern 'docs/', got %q", scopes[0].pattern)
	}

	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "docs/readme.md"}, 6); msg != "" {
		t.Fatalf("edit inside current scope must not warn, got: %s", msg)
	}

	if s.warnings != 0 {
		t.Fatalf("quota must be intact after in-scope edit, warnings=%d", s.warnings)
	}

	msg := s.checkToolCall("edit_file", map[string]any{"file_path": "cmd/main.go"}, 7)
	if msg == "" {
		t.Fatal("real out-of-scope edit (cmd/main.go vs docs/) must warn")
	}
	if want := "docs/"; !strings.Contains(msg, want) {
		t.Fatalf("warning should cite the current scope %q, got: %s", want, msg)
	}
}

func TestIssue2733AvoidStaysAdditive(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I won't modify the config/ directory.", 1)
	s.recordReasoning("I'll limit changes to the docs/ folder.", 3)

	var avoids []cvConstraint
	for _, c := range s.constraints {
		if c.constraintT == "avoid" {
			avoids = append(avoids, c)
		}
	}
	if len(avoids) != 1 {
		t.Fatalf("avoid constraint must survive scope supersede, got %d", len(avoids))
	}

	msg := s.checkToolCall("edit_file", map[string]any{"file_path": "config/app.yaml"}, 4)
	if msg == "" || !strings.Contains(msg, "avoid") {
		t.Fatalf("edit into avoided config/ must warn via avoid arm, got: %s", msg)
	}
}

func TestIssue2733SameScopeRedeclareNoDuplicate(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth/ directory.", 1)
	s.recordReasoning("Staying within auth/ as planned.", 4)

	scopes := issue2733ScopeConstraints(s)
	if len(scopes) != 1 {
		t.Fatalf("re-declared same scope must dedup, got %d scope constraints", len(scopes))
	}
	if scopes[0].iter != 4 {
		t.Fatalf("surviving entry should be the latest declaration (iter=4), got iter=%d", scopes[0].iter)
	}
}

func TestIssue2733NoScopeDeclarationKeepsExisting(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth/ directory.", 1)
	s.recordReasoning("I won't touch the vendor/ directory.", 2)

	scopes := issue2733ScopeConstraints(s)
	if len(scopes) != 1 || scopes[0].pattern != "auth/" {
		t.Fatalf("scope without a competing declaration must survive, got %+v", scopes)
	}
	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "docs/readme.md"}, 3); msg == "" {
		t.Fatal("out-of-scope edit must still warn when scope not superseded")
	}
}
