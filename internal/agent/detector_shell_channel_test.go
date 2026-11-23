package agent

import (
	"testing"
)

func TestPlanAbandon_EvidenceGateMatrix(t *testing.T) {
	plan := "Here's my plan:\n" +
		"1. I'll read the auth module\n" +
		"2. Next, I'll fix the token refresh logic\n" +
		"3. Finally, I'll run the tests to verify"
	completion := "The task is now complete."

	cases := []struct {
		name string
		rs   *RunStats
		want bool
	}{
		{"nil stats = no evidence channel = warn (true abandonment shape)", nil, true},
		{"full evidence = faithful completion = silent", &RunStats{
			FilesEdited: []string{"token.go"},
			CommandsRun: []string{"go test ./..."},
		}, false},
		{"edit evidence only, but plan also had run step = gap = warn", &RunStats{
			FilesEdited: []string{"token.go"},
		}, true},
		{"command evidence only, but plan also had edit step = gap = warn", &RunStats{
			CommandsRun: []string{"go test ./..."},
		}, true},
		{"zero evidence = warn", &RunStats{}, true},
	}
	for _, tc := range cases {
		a := &Agent{planAbandon: newPlanAbandonState()}
		if h := a.maybeWarnPlanAbandon(plan, tc.rs); h != "" {
			t.Errorf("%s: plan declaration must not warn", tc.name)
		}
		h := a.maybeWarnPlanAbandon(completion, tc.rs)
		if tc.want && h == "" {
			t.Errorf("%s: expected warning, got none", tc.name)
		}
		if !tc.want && h != "" {
			t.Errorf("%s: unexpected warning: %s", tc.name, h)
		}
	}
}

func TestPlanAbandon_PureReadPlanSilent(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	plan := "1. I'll read the auth module\n2. Next, I'll inspect the callers\n3. Finally, I'll examine the handler wiring"
	rs := &RunStats{}
	if h := a.maybeWarnPlanAbandon(plan, rs); h != "" {
		t.Fatalf("plan declaration must not warn: %s", h)
	}
	if h := a.maybeWarnPlanAbandon("The task is now complete.", rs); h != "" {
		t.Fatalf("pure-read plan has no evidence channel; completion must stay silent, got: %s", h)
	}
}
