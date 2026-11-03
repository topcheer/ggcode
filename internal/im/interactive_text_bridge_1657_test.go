package im

import (
	"context"
	"testing"
)

func Test1657ApprovalIndexFromText(t *testing.T) {
	if idx, ok := approvalIndexFromText("y 2", 3); !ok || idx != 2 {
		t.Fatalf(`"y 2" -> (%d,%v), want (2,true)`, idx, ok)
	}
	if idx, ok := approvalIndexFromText("2 y", 3); !ok || idx != 2 {
		t.Fatalf(`"2 y" -> (%d,%v), want (2,true)`, idx, ok)
	}
	if idx, ok := approvalIndexFromText("n 1", 2); !ok || idx != 1 {
		t.Fatalf(`"n 1" -> (%d,%v), want (1,true)`, idx, ok)
	}

	if _, ok := approvalIndexFromText("y", 3); ok {
		t.Fatal(`"y" with 3 pending must NOT carry an index`)
	}

	if _, ok := approvalIndexFromText("y 9", 3); ok {
		t.Fatal(`"y 9" out of range must not match`)
	}
	if _, ok := approvalIndexFromText("y 02", 3); ok {
		t.Fatal(`leading-zero token must not match (strconv round-trip guard)`)
	}

	if _, ok := approvalIndexFromText("y 1", 1); ok {
		t.Fatal("n<=1 must never consume an index")
	}
}

func Test1657MultiPendingDisambiguation(t *testing.T) {
	mkMsg := func(text string) InboundMessage {
		return InboundMessage{Envelope: Envelope{Adapter: "qq"}, Text: text}
	}

	submitted := ""
	b := &InteractiveTextBridge{
		Submit: func(_ context.Context, text, _ string) error {
			submitted = text
			return nil
		},
		PendingApprovals: func() []string { return []string{"id-1", "id-2"} },
		CurrentApproval:  func() (string, string, bool) { return "id-1", "run_command", true },
		ResolveApproval: func(requestID, decision string) {
			t.Fatalf("bare y with 2 pending must not resolve, resolved %s=%s", requestID, decision)
		},
	}
	if err := b.SubmitInboundMessage(nil, mkMsg("y")); err != nil {
		t.Fatal(err)
	}
	if submitted != "y" {
		t.Fatalf("bare y must fall through to Submit, got %q", submitted)
	}

	resolved := ""
	b2 := &InteractiveTextBridge{
		Submit: func(context.Context, string, string) error {
			t.Fatal("indexed reply must not fall through")
			return nil
		},
		PendingApprovals: func() []string { return []string{"id-1", "id-2"} },
		CurrentApproval:  func() (string, string, bool) { return "id-1", "run_command", true },
		ResolveApproval:  func(requestID, decision string) { resolved = requestID + ":" + decision },
	}
	if err := b2.SubmitInboundMessage(nil, mkMsg("y 2")); err != nil {
		t.Fatal(err)
	}
	if resolved != "id-2:allow" {
		t.Fatalf(`"y 2" must resolve id-2:allow, got %q`, resolved)
	}

	resolved = ""
	b3 := &InteractiveTextBridge{
		Submit:           func(context.Context, string, string) error { return nil },
		PendingApprovals: func() []string { return []string{"only"} },
		CurrentApproval:  func() (string, string, bool) { return "only", "run_command", true },
		ResolveApproval:  func(requestID, decision string) { resolved = requestID + ":" + decision },
	}
	if err := b3.SubmitInboundMessage(nil, mkMsg("y")); err != nil {
		t.Fatal(err)
	}
	if resolved != "only:allow" {
		t.Fatalf("single pending bare y must resolve, got %q", resolved)
	}
}
