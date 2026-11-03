package im

import "testing"

func Test1833QuestionnaireWinsWhenBothPending(t *testing.T) {
	r := RouteInboundText("y", true, true)
	if r.Kind != InboundRouteAskUser {
		t.Fatalf("confirm word must answer the questionnaire when both pending, got %v", r.Kind)
	}

	r2 := RouteInboundText("y", true, false)
	if r2.Kind != InboundRouteApproval {
		t.Fatalf("approval-only must route approval, got %v", r2.Kind)
	}

	r3 := RouteInboundText("anything free text", false, true)
	if r3.Kind != InboundRouteAskUser {
		t.Fatalf("questionnaire-only must route ask-user, got %v", r3.Kind)
	}

	r4 := RouteInboundText("hello", false, false)
	if r4.Kind != InboundRouteMessage {
		t.Fatalf("no pending must route message, got %v", r4.Kind)
	}
}
