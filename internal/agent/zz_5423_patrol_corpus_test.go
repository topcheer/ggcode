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
		// watchdog_dev 22:33Z sweep, verbatim shape (export prefix, timeout
		// wrapper, backslash continuations, || fallback, EXIT capture)
		"cd /Volumes/new/ggai/ggcode; export GH_PAGER=cat; echo \"== utc ==\"; date -u +%H:%M:%SZ; echo \"== open pulls ==\"; timeout 25 gh api 'repos/topcheer/ggcode/pulls?state=open&per_page=20&sort=updated&direction=desc' --jq '.[] | [.number, .updated_at, .head.sha[0:10], .head.ref[:32], .title[:52]] | @tsv'; timeout 40 gh api 'repos/topcheer/ggcode/issues?state=all&per_page=20&sort=updated&direction=desc' --jq '.[] | select(.updated_at > \"2026-11-11T22:18:00Z\") | [.number, .updated_at, (.pull_request != null | if . then \"PR\" else \"IS\" end), .state, .title[:55]] | @tsv' || echo DELTA_QUERY_FAILED; timeout 25 gh api 'repos/topcheer/ggcode/actions/runs?per_page=8' --jq '.workflow_runs[] | select(.status != \"completed\") | [.id, .name, .status, .created_at] | @tsv' | head -4",
		// watchdog_dev 23:00Z retry shape (grep && tail | cut)
		"grep -c 'watchdog_dev' .ggcode/self-observations.md && tail -3 .ggcode/self-observations.md | cut -c1-80",
		// unit isolation: the suspicious atoms
		"export GH_PAGER=cat",
		"timeout 25 gh api 'repos/topcheer/ggcode/actions/runs?per_page=8' --jq '.workflow_runs[0].status'",
		"date -u +%H:%M:%SZ",
		// original corpus below
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
