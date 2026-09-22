package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

type stubOutcomeRecorder struct {
	stubSkillLookup
	successes map[string]bool
}

func (s *stubOutcomeRecorder) RecordOutcome(name string, success bool) {
	s.successes[name] = success
}

func TestSkillOutcomeRecordedOnInlineSuccess(t *testing.T) {
	rec := &stubOutcomeRecorder{
		stubSkillLookup: stubSkillLookup{
			"deploy": {Name: "deploy", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Enabled: true, Template: "echo deploy"},
		},
		successes: map[string]bool{},
	}
	skillTool := SkillTool{Skills: rec}
	input := json.RawMessage(`{"skill":"deploy"}`)
	result, err := skillTool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Content)
	}
	if got, ok := rec.successes["deploy"]; !ok || !got {
		t.Fatalf("outcome = %v/%v, want recorded success for deploy", got, ok)
	}
}

func TestSkillOutcomeRecordedOnMCPUnavailableFailure(t *testing.T) {
	rec := &stubOutcomeRecorder{
		stubSkillLookup: stubSkillLookup{
			"remote:prompt": {Name: "remote:prompt", Source: commands.SourceMCP, LoadedFrom: commands.LoadedFromMCP, Enabled: true},
		},
		successes: map[string]bool{},
	}
	// No Runtime injected: MCP-backed skill must fail and record a failure
	// so the skill gets health-demoted in future prompt listings.
	skillTool := SkillTool{Skills: rec}
	result, err := skillTool.Execute(context.Background(), json.RawMessage(`{"skill":"remote:prompt"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for MCP skill without runtime")
	}
	if got, ok := rec.successes["remote:prompt"]; !ok || got {
		t.Fatalf("outcome = %v/%v, want recorded failure for remote:prompt", got, ok)
	}
}

func TestSkillOutcomeNotRecordedForUnknownSkill(t *testing.T) {
	rec := &stubOutcomeRecorder{
		stubSkillLookup: stubSkillLookup{},
		successes:       map[string]bool{},
	}
	skillTool := SkillTool{Skills: rec}
	skillTool.Execute(context.Background(), json.RawMessage(`{"skill":"missing"}`))
	if len(rec.successes) != 0 {
		t.Fatalf("outcomes recorded for unknown skill: %v", rec.successes)
	}
}

func TestSkillOutcomeNotRecordedWithoutRecorder(t *testing.T) {
	// Plain lookup without RecordOutcome must not panic.
	skillTool := SkillTool{Skills: stubSkillLookup{
		"deploy": {Name: "deploy", Source: commands.SourceUser, LoadedFrom: commands.LoadedFromSkills, Template: "echo deploy"},
	}}
	if _, err := skillTool.Execute(context.Background(), json.RawMessage(`{"skill":"deploy"}`)); err != nil {
		t.Fatalf("Execute error = %v", err)
	}
}
