package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// fakeGatesAccess is a scriptable GatesAccess for CRUD-action tests.
type fakeGatesAccess struct {
	describeOut string
	setCalls    []string
	unsetCalls  []string
	testCalls   []string
	patterns    []*regexp.Regexp
	setErr      error
	unsetErr    error
}

func (f *fakeGatesAccess) Describe() (string, error) { return f.describeOut, nil }
func (f *fakeGatesAccess) Set(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.setCalls = append(f.setCalls, key+"="+value)
	return nil
}
func (f *fakeGatesAccess) Unset(key, value string) error {
	if f.unsetErr != nil {
		return f.unsetErr
	}
	f.unsetCalls = append(f.unsetCalls, key+"|"+value)
	return nil
}
func (f *fakeGatesAccess) Test(command string) (string, error) {
	f.testCalls = append(f.testCalls, command)
	return "test output", nil
}
func (f *fakeGatesAccess) HoldPatterns() ([]*regexp.Regexp, error) { return f.patterns, nil }

// ghScript routes gh invocations to canned outputs by matching the first
// meaningful argument tokens. This pins predicate logic without network.
type ghScript struct {
	repo    string
	prView  string // for --json number,headRefOid,statusCheckRollup,labels,comments
	pinView string // for --json headRefOid
	reviews string
	calls   []string
	repoErr error
}

func (s *ghScript) run(_ context.Context, _ string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	s.calls = append(s.calls, joined)
	switch {
	case strings.HasPrefix(joined, "repo view"):
		if s.repoErr != nil {
			return "", s.repoErr
		}
		return s.repo, nil
	case strings.Contains(joined, "statusCheckRollup"):
		return s.prView, nil
	case strings.Contains(joined, "headRefOid"):
		return s.pinView, nil
	case strings.HasPrefix(joined, "api repos/"):
		return s.reviews, nil
	}
	return "", fmt.Errorf("unexpected gh call: %s", joined)
}

func happyPRView(head string) string {
	return fmt.Sprintf(`{"number":5356,"headRefOid":%q,"statusCheckRollup":[`+
		`{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"SUCCESS"},`+
		`{"__typename":"StatusContext","context":"codecov","state":"SUCCESS"}`+
		`],"labels":[{"name":"bug"}],"comments":[{"body":"looks good"}]}`, head)
}

func runPreflight(t *testing.T, script *ghScript, pr int) Result {
	t.Helper()
	tool := MergePreflightTool{GHRun: script.run}
	res, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"pr_number":%d,"description":"x"}`, pr)))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return res
}

func parseReportLine(t *testing.T, content string) preflightReport {
	t.Helper()
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	var r preflightReport
	if err := json.Unmarshal([]byte(first), &r); err != nil {
		t.Fatalf("first line is not report JSON: %q: %v", first, err)
	}
	return r
}

func TestMergePreflightAllGreen(t *testing.T) {
	script := &ghScript{
		repo:    "acme/widgets\n",
		prView:  happyPRView("aaabbbcccddd000"),
		pinView: `{"headRefOid":"aaabbbcccddd000"}`,
		reviews: `[{"user":{"login":"alice"},"state":"CHANGES_REQUESTED","submitted_at":"2026-11-01T10:00:00Z"},{"user":{"login":"alice"},"state":"APPROVED","submitted_at":"2026-11-02T10:00:00Z"}]`,
	}
	res := runPreflight(t, script, 5356)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	r := parseReportLine(t, res.Content)
	if !r.HeadPinned || r.HeadSHA != "aaabbbcccddd000" {
		t.Errorf("head_pinned = %v %s", r.HeadPinned, r.HeadSHA)
	}
	if !r.ChecksGreen || !r.ReviewsResolved || !r.HoldsClear {
		t.Errorf("predicates = %v %v %v (want all true)", r.ChecksGreen, r.ReviewsResolved, r.HoldsClear)
	}
	if r.Verdict != "PASS" {
		t.Errorf("verdict = %s, reasons %v", r.Verdict, r.Reasons)
	}
	if !strings.Contains(res.Content, "PREFLIGHT: PASS") {
		t.Errorf("missing PASS conclusion line:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "--match-head-commit aaabbbcccddd000") {
		t.Errorf("missing pin guidance with full SHA:\n%s", res.Content)
	}
}

func TestMergePreflightChecksStaleHeadFails(t *testing.T) {
	// Rollup is all-SUCCESS, but the head moved between the two reads:
	// those checks belong to an older head -> checks_green must be false.
	script := &ghScript{
		repo:    "acme/widgets\n",
		prView:  happyPRView("aaabbbcccddd000"),
		pinView: `{"headRefOid":"fff000111222333"}`,
		reviews: `[]`,
	}
	res := runPreflight(t, script, 5356)
	r := parseReportLine(t, res.Content)
	if r.ChecksGreen {
		t.Error("checks_green must be false when the head moved (checks belong to the old head)")
	}
	if r.Verdict != "HOLD" {
		t.Errorf("verdict = %s", r.Verdict)
	}
	if !strings.Contains(res.Content, "PREFLIGHT: HOLD(") {
		t.Errorf("missing HOLD conclusion:\n%s", res.Content)
	}
	// Re-pin: report must carry the FRESH head for the retry.
	if r.HeadSHA != "fff000111222333" {
		t.Errorf("head must re-pin to fresh head, got %s", r.HeadSHA)
	}
}

func TestMergePreflightChecksNotSuccess(t *testing.T) {
	script := &ghScript{
		repo: "acme/widgets\n",
		prView: `{"number":1,"headRefOid":"abc","statusCheckRollup":[` +
			`{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"FAILURE"},` +
			`{"__typename":"CheckRun","name":"Lint","status":"IN_PROGRESS","conclusion":""}],"labels":[],"comments":[]}`,
		pinView: `{"headRefOid":"abc"}`,
		reviews: `[]`,
	}
	res := runPreflight(t, script, 1)
	r := parseReportLine(t, res.Content)
	if r.ChecksGreen {
		t.Error("checks_green must be false with FAILURE/IN_PROGRESS checks")
	}
	if len(r.Reasons) != 2 {
		t.Errorf("expected 2 check reasons, got %v", r.Reasons)
	}
}

func TestMergePreflightReviewUnresolved(t *testing.T) {
	script := &ghScript{
		repo:    "acme/widgets\n",
		prView:  happyPRView("abc"),
		pinView: `{"headRefOid":"abc"}`,
		reviews: `[{"user":{"login":"bob"},"state":"CHANGES_REQUESTED","submitted_at":"2026-11-03T09:00:00Z"}]`,
	}
	res := runPreflight(t, script, 5356)
	r := parseReportLine(t, res.Content)
	if r.ReviewsResolved {
		t.Error("reviews_resolved must be false with outstanding CHANGES_REQUESTED")
	}
	if r.Verdict != "HOLD" {
		t.Errorf("verdict = %s", r.Verdict)
	}
	found := false
	for _, why := range r.Reasons {
		if strings.Contains(why, "bob") && strings.Contains(why, "CHANGES_REQUESTED") {
			found = true
		}
	}
	if !found {
		t.Errorf("reason must name the reviewer, got %v", r.Reasons)
	}
}

func TestMergePreflightReviewSupersededResolves(t *testing.T) {
	// bob requested changes, then a later review superseded it.
	script := &ghScript{
		repo:    "acme/widgets\n",
		prView:  happyPRView("abc"),
		pinView: `{"headRefOid":"abc"}`,
		reviews: `[{"user":{"login":"bob"},"state":"CHANGES_REQUESTED","submitted_at":"2026-11-01T09:00:00Z"},{"user":{"login":"bob"},"state":"COMMENTED","submitted_at":"2026-11-05T09:00:00Z"}]`,
	}
	res := runPreflight(t, script, 5356)
	r := parseReportLine(t, res.Content)
	if !r.ReviewsResolved {
		t.Errorf("superseded CHANGES_REQUESTED must resolve, reasons %v", r.Reasons)
	}
}

func TestMergePreflightHoldHit(t *testing.T) {
	script := &ghScript{
		repo: "acme/widgets\n",
		prView: `{"number":9,"headRefOid":"abc","statusCheckRollup":[],"labels":[{"name":"waiting"}],` +
			`"comments":[{"body":"HOLD: awaiting owner sign-off"}]}`,
		pinView: `{"headRefOid":"abc"}`,
		reviews: `[]`,
	}
	pat := regexp.MustCompile(`HOLD: awaiting owner`)
	tool := MergePreflightTool{GHRun: script.run, Access: &fakeGatesAccess{patterns: []*regexp.Regexp{pat}}}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"pr_number":9,"description":"x"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	r := parseReportLine(t, res.Content)
	if r.HoldsClear {
		t.Error("holds_clear must be false when a comment matches the hold pattern")
	}
	if r.Verdict != "HOLD" {
		t.Errorf("verdict = %s", r.Verdict)
	}
}

func TestMergePreflightGhError(t *testing.T) {
	script := &ghScript{repoErr: fmt.Errorf("no auth")}
	res := runPreflight(t, script, 5)
	if !res.IsError {
		t.Errorf("expected error result, got %s", res.Content)
	}
}

func TestMergePreflightCRUDActions(t *testing.T) {
	fake := &fakeGatesAccess{describeOut: `{"enabled":true}`}
	tool := MergePreflightTool{Access: fake}

	// describe
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"describe","description":"x"}`))
	if err != nil || res.IsError {
		t.Fatalf("describe failed: %v %s", err, res.Content)
	}
	if res.Content != `{"enabled":true}` {
		t.Errorf("describe passthrough = %q", res.Content)
	}

	// set happy path
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"set","key":"preflight_ttl","value":"\"90s\"","description":"x"}`))
	if err != nil || res.IsError {
		t.Fatalf("set failed: %v %s", err, res.Content)
	}
	if len(fake.setCalls) != 1 || fake.setCalls[0] != `preflight_ttl="90s"` {
		t.Errorf("set calls = %v", fake.setCalls)
	}

	// set validation rejection propagates as an error result naming the key
	fake.setErr = fmt.Errorf("regex does not compile: [")
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"set","key":"hold_patterns","value":"[\"[\"]","description":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "gates.merge.hold_patterns") {
		t.Errorf("rejection must name the key: %s", res.Content)
	}

	// set without key/value rejected
	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"set","description":"x"}`))
	if !res.IsError {
		t.Error("set without key must be rejected")
	}

	// unset list entry by value
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"unset","key":"hold_patterns","value":"HOLD: awaiting owner","description":"x"}`))
	if err != nil || res.IsError {
		t.Fatalf("unset failed: %v %s", err, res.Content)
	}
	if len(fake.unsetCalls) != 1 || fake.unsetCalls[0] != "hold_patterns|HOLD: awaiting owner" {
		t.Errorf("unset calls = %v", fake.unsetCalls)
	}

	// test dry-run routes through access
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"test","command":"gh pr merge 5356","description":"x"}`))
	if err != nil || res.IsError {
		t.Fatalf("test failed: %v %s", err, res.Content)
	}
	if len(fake.testCalls) != 1 || fake.testCalls[0] != "gh pr merge 5356" {
		t.Errorf("test calls = %v", fake.testCalls)
	}

	// unknown action rejected
	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"zap","description":"x"}`))
	if !res.IsError {
		t.Error("unknown action must be rejected")
	}

	// CRUD unavailable without access
	bare := MergePreflightTool{}
	res, _ = bare.Execute(context.Background(), json.RawMessage(`{"action":"describe","description":"x"}`))
	if !res.IsError || !strings.Contains(res.Content, "unavailable") {
		t.Errorf("describe without access must degrade to an explicit error, got %s", res.Content)
	}
}

func TestMergePreflightRequiresPRNumber(t *testing.T) {
	tool := MergePreflightTool{}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"description":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("missing pr_number and action must be rejected")
	}
}
