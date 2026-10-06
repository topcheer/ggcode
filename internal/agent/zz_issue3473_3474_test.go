package agent

// #3473: two self-answerable patterns matched PREFERENCE questions and
// hard-blocked legitimate asks ("which version would you like?" etc.).
// #3474: the duplicate fingerprint was recorded at GATE time (before
// execution) - a denied/cancelled ask consumed the dedup quota with a
// false-premise block message; it must only record after a successful run.

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
	// Gate check alone (simulating a later DENIED execution) must NOT record.
	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatalf("first ask must pass the gate, got %q", block)
	}
	// Retry after the (simulated) denial: still allowed - quota intact.
	if block, _ := a.checkAskQualityGate(args); block != "" {
		t.Fatalf("denied ask must not consume the dedup quota: %q", block)
	}
	// Only a SUCCESSFUL execution records; afterwards a re-ask is blocked.
	a.markAskAskedQG(args)
	if block, _ := a.checkAskQualityGate(args); block == "" {
		t.Fatal("post-success re-ask must be blocked as a duplicate")
	}
}
