package agent

import (
	"encoding/json"
	"testing"
)

// TestIrrevPatrolCorpusReadOnly pins the standard patrol command corpus as
// read-only. Grew out of the #5423(b) investigation: watchdog_dev reported
// medium-impact hints on read-only gh api patrols at 22:33Z/23:00Z on
// 6b6110acf, but all eight real-world shapes below classify read-only on
// current main - the gate is not the cause. This corpus now guards the
// classification against regression, including the tricky shapes: jq pipes
// inside quotes, for-loop compound probes, and shell pipes with whitelist
// members on both sides.
func TestIrrevPatrolCorpusReadOnly(t *testing.T) {
	corpus := []string{
		// B2.1 main-CI step-1 shape
		`gh run list --repo topcheer/ggcode --branch main --limit 3 --json databaseId,status,conclusion,createdAt,displayTitle`,
		// run-id probes (escalation shape)
		`gh api repos/topcheer/ggcode/actions/runs/43105798349 --jq '.status'`,
		`gh api repos/topcheer/ggcode/actions/runs/43105216189 --jq '"\(.id) \(.status)/\(.conclusion // "-")"'`,
		// for-loop multi-run probe (patrol standard)
		`cd /Volumes/new/ggai/ggcode && for id in 43105216189 43105787313; do gh api repos/topcheer/ggcode/actions/runs/$id --jq '"\(.status)/\(.conclusion // "-")"'; done`,
		// git one-liners
		`cd /Volumes/new/ggai/ggcode && git log --oneline -1`,
		// jq pipe inside quotes (aggregate shapes)
		`gh run list --repo topcheer/ggcode --branch main --limit 3 --json status,conclusion --jq '[.[] | "\(.status)/\(.conclusion // "-")"] | join(" | ")'`,
		`gh pr list --repo topcheer/ggcode --state open --json number --jq '"pool: " + ([.[] | "#\(.number)"] | join(" "))'`,
		// shell pipe, whitelist both sides
		`gh api repos/topcheer/ggcode/actions/runs/43105221967 --json jobs --jq '.jobs[] | .name' | head -3`,
	}
	for _, cmd := range corpus {
		if !irrevIsReadOnlyCommand(mustMarshalCmd5423b(t, cmd)) {
			t.Errorf("patrol corpus regression: expected read-only, got gated: %s", cmd)
		}
	}
}

func mustMarshalCmd5423b(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
