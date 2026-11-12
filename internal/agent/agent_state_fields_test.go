package agent

import (
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

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

	nilFieldsAllowed := map[string]string{
		"systemPromptInjector":      "optional callback, injected via setter",
		"systemPromptLayers":        "optional named prompt layers (e.g. resume reconciliation), registered via AddSystemPromptLayer",
		"onVerifyProgress":          "optional callback, injected via setter",
		"onVerifyResult":            "optional callback, injected via setter",
		"onToolProgress":            "optional callback, injected via setter",
		"onApproval":                "optional approval callback, injected via SetApprovalFunc",
		"onCheckpoint":              "optional callback, injected via setter",
		"idleMaint":                 "optional sleep-time-compute watcher (r373), injected via SetIdleMaintainer; nil = idle maintenance disabled (default)",
		"onInterrupt":               "optional interruption handler, injected via setter",
		"subagentCompletionFeed":    "optional P1-1 completion feed, injected via SetSubagentCompletionFeed; nil = push disabled",
		"subagentNotified":          "P1-1 seen-set, lazily initialized on first injectSubagentCompletions poll (baseline swallow)",
		"onMetric":                  "optional metrics callback, injected via setter",
		"onRunHealth":               "optional callback, injected via setter",
		"onRunResult":               "optional run result handler, injected via setter",
		"onUsage":                   "optional usage callback, injected via setter",
		"reflectionFunc":            "optional reflection callback, injected via setter",
		"diffConfirm":               "optional diff confirm callback, injected via setter",
		"policy":                    "permission policy interface, injected via SetPermissionPolicy",
		"lastRunStats":              "populated only after a run completes",
		"experienceInjectedCaseIDs": "populated only by the run-start experience recall (#3072); nil = nothing injected yet",
		"runToolNames":              "appended only as tools execute during a run (r484 toolflow hint input); nil = no tools run yet",
		"researchPlanSubs":          "populated only when the r343/sa-130 research planner fires (research-mode run); nil = no plan yet",
		"perfBaseline":              "loaded lazily from disk on first access (guarded)",
		"guidancePromoter":          "loaded lazily from rule store (guarded)",
		"ruleStore":                 "loaded lazily via SetRuleStore (guarded)",
		"selfModel":                 "lazily bound to the workspace in SetWorkingDir (#5498: NewAgent runs before the workspace is known)",
		"anchorStore":               "lazily bound to the workspace in SetWorkingDir (#5498: NewAgent runs before the workspace is known)",
		"userEditObs":               "r444 user-edit observer, built lazily on the rule store via getUserEditObserver (guarded)",
		"ruleInjectCount":           "lazily initialized at first use (verify.go)",
		"checkpoints":               "checkpoint manager, injected via setter (guarded)",
		"codeIndex":                 "code index manager, injected via setter (guarded)",
		"precompact":                "created lazily when precompaction starts (guarded)",
		"auxResolved":               "task-tier model routing config, injected via SetAuxModel; nil = routing disabled (default)",
		"auxProvider":               "aux provider built lazily by auxProviderFor() on first auxiliary call",
		"cascadeSavedProvider":      "r485 turn-tier cascade: non-nil only while an exploratory turn is executing on the aux model; nil otherwise (default)",
		"metadata":                  "dead field — no read/write sites outside declaration",
		"invEngine":                 "r454 declarative invariants engine, built lazily by invariantEngineLazy once workingDir is known (guarded; nil = no invariants file, inert)",
		"wfEngine":                  "r26 workflow-spec engine, built lazily by workflowEngineLazy once workingDir is known (guarded; nil = no workflow-spec.json, inert)",
	}

	v := reflect.ValueOf(a).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fv := v.Field(i)
		kind := fv.Kind()

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
