package im

import "testing"

// #1833 case 2 pin: when both are pending, a confirm word routes to the
// questionnaire, not the approval.
func Test1833QuestionnaireWinsWhenBothPending(t *testing.T) {
	r := RouteInboundText("y", true, true)
	if r.Kind != InboundRouteAskUser {
		t.Fatalf("confirm word must answer the questionnaire when both pending, got %v", r.Kind)
	}
	// Approval-only still parses.
	r2 := RouteInboundText("y", true, false)
	if r2.Kind != InboundRouteApproval {
		t.Fatalf("approval-only must route approval, got %v", r2.Kind)
	}
	// Questionnaire-only unaffected.
	r3 := RouteInboundText("anything free text", false, true)
	if r3.Kind != InboundRouteAskUser {
		t.Fatalf("questionnaire-only must route ask-user, got %v", r3.Kind)
	}
	// No pending: message.
	r4 := RouteInboundText("hello", false, false)
	if r4.Kind != InboundRouteMessage {
		t.Fatalf("no pending must route message, got %v", r4.Kind)
	}
}
