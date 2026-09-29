package agent

// #2879 regression: prefix-phrase ambiguity patterns used to fire on
// fully-disambiguated instructions ("sort the users by last_login descending"
// got "clarify ascending or descending" - contradicting the prompt itself).
// The sentence-level suppressors must silence exactly those shapes while
// genuinely vague variants keep firing. Also pins the fired-once semantics:
// a suppressed signal must NOT consume the run's single firing budget.

import "testing"

func TestAmbiguityPointDisambiguatedPrefixesSuppressed(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
	}{
		{"sort with direction", "sort the users by last_login descending"},
		{"sort ascending", "sort the users by last_login ascending"},
		{"order by with desc", "order by created_at desc, id asc"},
		{"rename with new name", "rename the file to config.yaml"},
		{"improve with metric and lever", "improve the latency of the hot path by adding a cache"},
		{"sort with direction in later sentence", "review the list first. sort the items, descending order"},
		{"cjk sort with direction", "帮我排序，按创建时间降序排"},
		{"cjk rename with new name", "重命名一下这个文件，改成 config.yaml"},
		{"cjk short rename with target", "改个名，改为 runner"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{ambiguityPoint: newAmbiguityPointState()}
			if msg := a.checkAmbiguityPoints(tt.prompt); msg != "" {
				t.Errorf("expected no message, got: %s", msg[:min(80, len(msg))])
			}
			// A suppressed detection must not mark the detector as fired:
			// the run's only firing budget stays intact for real ambiguity.
			if a.ambiguityPoint.fired {
				t.Error("suppressed signal consumed the fired-once budget")
			}
		})
	}
}

func TestAmbiguityPointVaguePrefixesStillFire(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
	}{
		{"bare sort", "sort the list please"},
		{"order by no direction", "order by those results"},
		{"rename no target name", "rename the file"},
		{"improve no metric", "improve the code"},
		{"cjk bare sort", "帮我排序"},
		{"cjk bare rename", "重命名一下"},
		{"cjk short rename", "帮我改个名"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{ambiguityPoint: newAmbiguityPointState()}
			if msg := a.checkAmbiguityPoints(tt.prompt); msg == "" {
				t.Error("expected guidance message, got empty")
			}
		})
	}
}
