package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// #4774 (S-02): a broadcast timeout must be reported as UNKNOWN delivery -
// distinct from both success and definite failure - and the report must
// surface per-recipient outcomes. lanchat is fire-and-forget UDP; a nil
// return only means "handed to transport", never "delivered".
type zz4774TimeoutErr struct{}

func (zz4774TimeoutErr) Error() string   { return "i/o timeout" }
func (zz4774TimeoutErr) Timeout() bool   { return true }
func (zz4774TimeoutErr) Temporary() bool { return false }

func TestIssue4774TimeoutClassifiedUnknown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"ctx deadline", context.DeadlineExceeded, true},
		{"wrapped ctx deadline", fmt.Errorf("send: %w", context.DeadlineExceeded), true},
		{"net timeout", zz4774TimeoutErr{}, true},
		{"plain error", fmt.Errorf("connection refused"), false},
	}
	for _, c := range cases {
		if got := isUnknownDeliveryErr(c.err); got != c.want {
			t.Errorf("%s: isUnknownDeliveryErr=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestIssue4774UnknownAndFailedReport(t *testing.T) {
	unknown := []string{"seat-a: context deadline exceeded", "seat-b: i/o timeout"}
	failed := []string{"seat-c: connection refused"}
	var b strings.Builder
	appendUnknownAndFailed(&b, unknown, failed, 5)
	out := b.String()
	for _, want := range []string{
		"Delivery UNKNOWN for 2/5", "neither failure nor confirmed success",
		"seat-a: context deadline exceeded", "seat-b: i/o timeout",
		"Definitely failed 1/5", "seat-c: connection refused",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestIssue4774OutcomeListCap(t *testing.T) {
	entries := make([]string, 20)
	for i := range entries {
		entries[i] = fmt.Sprintf("seat-%02d: timeout", i)
	}
	var b strings.Builder
	appendOutcomeLines(&b, entries)
	out := b.String()
	if got := strings.Count(out, "\n"); got != outcomeListCap+1 { // capped lines + the "and N more" line
		t.Fatalf("cap not enforced: %d lines rendered (cap %d):\n%s", got-1, outcomeListCap, out)
	}
	if !strings.Contains(out, "...and 12 more") {
		t.Fatalf("overflow line missing:\n%s", out)
	}
}
