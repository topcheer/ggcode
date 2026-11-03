package permission

import (
	"encoding/json"
	"testing"
)

func TestIssue4260_EscalationGateCaseInsensitive(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"Bypass"}`, `{"mode":"BYPASS"}`, `{"mode":"ByPass"}`,
		`{"mode":"Autopilot"}`, `{"mode":"AUTOPILOT"}`, `{"mode":"autopilot"}`,
		`{"mode":"  bypass  "}`,
	} {
		if !switchModeEscalates(json.RawMessage(raw)) {
			t.Fatalf("%s must escalate (Ask gate), got false - privilege-bypass regression", raw)
		}
	}
	for _, raw := range []string{
		`{"mode":"supervised"}`, `{"mode":"Supervised"}`, `{"mode":"auto"}`, `{"mode":"Auto"}`, `{"mode":"plan"}`,
	} {
		if switchModeEscalates(json.RawMessage(raw)) {
			t.Fatalf("%s must NOT escalate, got true", raw)
		}
	}
}
