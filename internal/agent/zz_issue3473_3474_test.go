package agent

import "testing"

func TestIssue3473_PreferenceAsksNotBlocked(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	cases := [][2]string{
		{"target", "Which version would you like to target for the release?"},
		{"cadence", "What release cadence do you prefer?"},
		{"layout", "Does the new file layout work for you?"},
		{"naming", "Does the config file naming style look good?"},
	}
	for _, c := range cases {
		if block, _ := a.checkAskQualityGate(askArgs(t, c[0], c[1], nil)); block != "" {
			t.Errorf("preference ask %q wrongly blocked: %s", c[1], block)
		}
	}
}

func TestIssue3473_LookupableAsksStillBlocked(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	if block, _ := a.checkAskQualityGate(askArgs(t, "exists",
		"Does the config file exist in the repo root?", nil)); block == "" {
		t.Error("existence lookup must still be blocked")
	}
	if block, _ := a.checkAskQualityGate(askArgs(t, "branch",
		"Which branch contains the last release tag?", nil)); block == "" {
		t.Error("branch lookup must still be blocked")
	}
}

func TestIssue3474_GateCheckDoesNotConsumeQuota(t *testing.T) {
	a := &Agent{askQualityGate: newAskQualityGateState()}
	args := askArgs(t, "mode", "Run in strict or lax mode?", nil)

	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatalf("first ask must pass the gate, got %q", block)
	}

	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatalf("denied ask must not consume the dedup quota: %q", block)
	}

	a.markAskAskedQG(args)
	if block, _ := a.checkAskQualityGate(args); block == "" {
		t.Fatal("post-success re-ask must be blocked as a duplicate")
	}
}
