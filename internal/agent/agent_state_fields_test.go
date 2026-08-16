package agent

import (
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestNewAgentInitializesAllStateFields is a regression guard for issue #341:
// detector state fields declared on the Agent struct but never initialized in
// the NewAgent literal silently short-circuit (all wrapper methods have
// nil guards), so the detector never fires in production. This test walks
// every Agent struct field via reflection and asserts that pointer- and
// interface-typed fields are non-nil after construction.
//
// Fields intentionally nil at construction belong in nilFieldsAllowed with a
// reason — they are lazily initialized, injected via setters, or are optional.
func TestNewAgentInitializesAllStateFields(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "Hello!"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "Be helpful", 5)
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}

	// nilFieldsAllowed lists fields that are legitimately nil right after
	// NewAgent, with the reason. Every entry must be justified — do not add
	// fields here just to make the test pass for a detector state.
	nilFieldsAllowed := map[string]string{
		"systemPromptInjector": "optional callback, injected via setter",
		"onVerifyProgress":     "optional callback, injected via setter",
		"onVerifyResult":       "optional callback, injected via setter",
		"onToolProgress":       "optional callback, injected via setter",
		"onApproval":           "optional approval callback, injected via SetApprovalFunc",
		"onCheckpoint":         "optional callback, injected via setter",
		"onInterrupt":          "optional interruption handler, injected via setter",
		"onMetric":             "optional metrics callback, injected via setter",
		"onRunHealth":          "optional callback, injected via setter",
		"onRunResult":          "optional run result handler, injected via setter",
		"onUsage":              "optional usage callback, injected via setter",
		"reflectionFunc":       "optional reflection callback, injected via setter",
		"diffConfirm":          "optional diff confirm callback, injected via setter",
		"policy":               "permission policy interface, injected via SetPermissionPolicy",
		"lastRunStats":         "populated only after a run completes",
		"perfBaseline":         "loaded lazily from disk on first access (guarded)",
		"guidancePromoter":     "loaded lazily from rule store (guarded)",
		"ruleStore":            "loaded lazily via SetRuleStore (guarded)",
		"ruleInjectCount":      "lazily initialized at first use (verify.go)",
		"checkpoints":          "checkpoint manager, injected via setter (guarded)",
		"codeIndex":            "code index manager, injected via setter (guarded)",
		"precompact":           "created lazily when precompaction starts (guarded)",
		"metadata":             "dead field — no read/write sites outside declaration",
	}

	// Dereference via the pointer (ValueOf(a).Elem()) instead of copying
	// the struct with ValueOf(*a): Agent embeds a sync.RWMutex and vet's
	// copylocks flags by-value reflection copies.
	v := reflect.ValueOf(a).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fv := v.Field(i)
		kind := fv.Kind()

		// Check pointer, interface, map, slice, func, and channel kinds.
		// Note: nil-able value kinds (maps, slices, funcs, channels) that are
		// core state should also be initialized; allow-list covers the rest.
		var isNilable bool
		switch kind {
		case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
			isNilable = true
		}
		if !isNilable {
			continue
		}
		if !fv.IsNil() {
			continue
		}
		if reason, ok := nilFieldsAllowed[field.Name]; ok {
			t.Logf("field %s (%s) is nil after NewAgent: allowed (%s)", field.Name, kind, reason)
			continue
		}
		t.Errorf("field %s (%s %s) is nil after NewAgent — detector or state not initialized; "+
			"add initialization in NewAgent or add to nilFieldsAllowed with a reason",
			field.Name, field.Type, kind)
	}
}
