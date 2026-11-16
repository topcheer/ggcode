package tool

// #5737: spawn_agent must expose the reread-breaker calibration so a
// dispatcher can mark read-heavy research runs explicitly (the issue ask),
// and the Explore subagent type must imply the research calibration.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func issue5737Spawn(t *testing.T) SpawnAgentTool {
	t.Helper()
	mgr := subagent.NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 5 * time.Second})
	t.Cleanup(mgr.Shutdown)
	return SpawnAgentTool{Manager: mgr, Provider: nil}
}

func issue5737Execute(t *testing.T, s SpawnAgentTool, payload map[string]interface{}) string {
	t.Helper()
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result.Content
}

func TestIssue5737SpawnModeResearchAccepted(t *testing.T) {
	s := issue5737Spawn(t)
	content := issue5737Execute(t, s, map[string]interface{}{
		"task": "study the emitter call graph",
		"mode": "research",
	})
	if !strings.Contains(content, "Sub-agent spawned") {
		t.Fatalf("expected spawn message: %s", content)
	}
	if !strings.Contains(content, "research calibration") {
		t.Fatalf("research mode must disclose the calibration in the spawn result: %s", content)
	}
	warnAt, tripAt := subagent.ResearchRereadLimits()
	if !strings.Contains(content, strconv.Itoa(warnAt)+" same-file reads") || !strings.Contains(content, strconv.Itoa(tripAt)) {
		t.Fatalf("disclosure must carry warn/trip thresholds: %s", content)
	}
}

func TestIssue5737SpawnModeInvalidRejected(t *testing.T) {
	s := issue5737Spawn(t)
	content := issue5737Execute(t, s, map[string]interface{}{
		"task": "task",
		"mode": "banana",
	})
	if !strings.Contains(content, "invalid mode") || !strings.Contains(content, "research") {
		t.Fatalf("invalid mode must be rejected with the valid values: %s", content)
	}
}

func TestIssue5737SpawnExploreTypeImpliesResearch(t *testing.T) {
	s := issue5737Spawn(t)
	content := issue5737Execute(t, s, map[string]interface{}{
		"task":          "explore task",
		"subagent_type": "Explore",
	})
	if !strings.Contains(content, "research calibration") {
		t.Fatalf("Explore type must imply the research calibration: %s", content)
	}
}

func TestIssue5737SpawnModeDefaultKeepsStrict(t *testing.T) {
	s := issue5737Spawn(t)
	content := issue5737Execute(t, s, map[string]interface{}{
		"task": "edit task",
		"mode": "default",
	})
	if !strings.Contains(content, "Sub-agent spawned") {
		t.Fatalf("expected spawn message: %s", content)
	}
	if strings.Contains(content, "research calibration") {
		t.Fatalf("explicit default must keep the strict breaker silently: %s", content)
	}
}

func TestIssue5737SpawnParametersDocumentMode(t *testing.T) {
	params := string(SpawnAgentTool{}.Parameters())
	if !strings.Contains(params, "\"mode\"") {
		t.Fatalf("spawn_agent parameters must document the mode option: %s", params)
	}
	if !strings.Contains(params, "research") || !strings.Contains(params, "reread") {
		t.Fatalf("mode description must explain the reread calibration: %s", params)
	}
}
