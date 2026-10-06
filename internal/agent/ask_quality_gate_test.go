package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func askArgs(t *testing.T, title, prompt string, labels []string) string {
	t.Helper()
	type choice struct {
		Label string `json:"label"`
	}
	choices := make([]choice, 0, len(labels))
	for _, l := range labels {
		choices = append(choices, choice{Label: l})
	}
	b, err := json.Marshal(map[string]any{
		"title": title,
		"questions": []map[string]any{{
			"title":   title,
			"prompt":  prompt,
			"kind":    "single",
			"choices": choices,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAskGateBlocksSelfAnswerable: codebase-fact questions must not spend
// a user interruption.
func TestAskGateBlocksSelfAnswerable(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	cases := [][2]string{
		// #3473: the bare "which version" form is gone (lexically
		// indistinguishable from preference asks); the explicit file
		// mention is the lookupable form that stays blocked.
		{"Version", "Check go.mod: which version is declared for the module?"},
		{"Existence", "Does the file config.go exist?"},
		{"Signature", "What is the signature of NewManager?"},
		{"Branch", "Which branch should I target?"},
	}
	for _, c := range cases {
		block, _ := a.checkAskQualityGate(askArgs(t, c[0], c[1], nil))
		if block == "" {
			t.Errorf("case %q must be blocked as self-answerable", c[0])
		} else if !strings.Contains(block, "Ask Gate") {
			t.Errorf("block message malformed: %s", block)
		}
	}
}

// TestAskGateBlocksDuplicate: the same question twice in one run is zero
// information gain; different wording that differs only in case/whitespace
// counts as the same question.
func TestAskGateBlocksDuplicate(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	args := askArgs(t, "Deploy target", "Deploy to staging or production?", []string{"staging", "production"})
	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatalf("first ask must pass, got block: %s", block)
	}
	// #3474: the gate is read-only now - the fingerprint is recorded only
	// after a SUCCESSFUL execution (markAskAskedQG, wired in agent.go's
	// result pipeline). Simulate that success, then re-asks are duplicates.
	a.markAskAskedQG(args)
	if block, _ := a.checkAskQualityGate(args); block == "" {
		t.Fatal("identical re-ask must be blocked")
	}
	// Case/whitespace variant is still a duplicate.
	variant := askArgs(t, "  deploy   TARGET ", "Deploy  to staging or production?", []string{"production", "Staging"})
	if block, _ := a.checkAskQualityGate(variant); block == "" {
		t.Fatal("normalized variant re-ask must be blocked")
	}
	// A genuinely different question passes.
	other := askArgs(t, "Rollback plan", "Should we keep a rollback checkpoint?", []string{"yes", "no"})
	if block, _ := a.checkAskQualityGate(other); block != "" {
		t.Fatalf("genuinely new question must pass, got: %s", block)
	}
}

// TestAskGateSafeDefaultAdvisory: a safe-default choice yields an advisory,
// never a block.
func TestAskGateSafeDefaultAdvisory(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	args := askArgs(t, "Migration", "Run the destructive migration?", []string{"Skip (safe)", "Run now"})
	block, adv := a.checkAskQualityGate(args)
	if block != "" {
		t.Fatalf("safe-default ask must not be blocked, got: %s", block)
	}
	if adv == "" || !strings.Contains(adv, "safe default") {
		t.Fatalf("expected safe-default advisory, got: %q", adv)
	}
}

// TestAskGateResetAndPassThrough: reset re-opens asking; nil gate and
// malformed JSON are pass-throughs (schema validation is the tool's job).
func TestAskGateResetAndPassThrough(t *testing.T) {
	g := newAskQualityGateState()
	a := &Agent{askQualityGate: g}
	args := askArgs(t, "Q", "Preference A or B?", []string{"A", "B"})
	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatal("first ask must pass")
	}
	g.reset()
	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatal("after reset the same ask must pass again")
	}

	nilGate := &Agent{}
	if block, adv := nilGate.checkAskQualityGate(args); block != "" || adv != "" {
		t.Fatal("nil gate must pass through")
	}
	if block, adv := a.checkAskQualityGate("{not json"); block != "" || adv != "" {
		t.Fatal("malformed args must pass through to schema validation")
	}
}
