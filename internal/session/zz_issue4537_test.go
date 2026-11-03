package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func zz4537Msg(origin, text string) provider.Message {
	return provider.Message{Role: "user", Origin: origin, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func zz4537Assistant(text string) provider.Message {
	return provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func TestIssue4537HumanConfirmationSurvivesAcrossTurns(t *testing.T) {
	in := []provider.Message{
		zz4537Msg("human", "yes"),
		zz4537Assistant("proceeding with step 1"),
		zz4537Msg("human", "yes"),
		zz4537Assistant("proceeding with step 2"),
	}
	out := dedupeUserMsgs(in)
	if len(out) != 4 {
		t.Fatalf("human re-typed confirmation must survive: got %d msgs, want 4: %+v", len(out), out)
	}
	yes := 0
	for _, m := range out {
		if m.Role == "user" && len(m.Content) == 1 && m.Content[0].Text == "yes" {
			yes++
		}
	}
	if yes != 2 {
		t.Fatalf("both human 'yes' turns must be kept, got %d", yes)
	}
}

func TestIssue4537MachineReFireStillCollapses(t *testing.T) {
	in := []provider.Message{
		zz4537Msg("", "run the sweep"),
		zz4537Assistant("partial"),
		zz4537Msg("", "run the sweep"),
		zz4537Assistant("partial 2"),
		zz4537Msg("", "run the sweep"),
	}
	out := dedupeUserMsgs(in)
	sweep := 0
	for _, m := range out {
		if m.Role == "user" && len(m.Content) == 1 && m.Content[0].Text == "run the sweep" {
			sweep++
		}
	}
	if sweep != 1 {
		t.Fatalf("machine re-fire must collapse globally to 1, got %d", sweep)
	}
}

func TestIssue4537MixedOriginsIndependent(t *testing.T) {
	in := []provider.Message{
		zz4537Msg("", "continue"),
		zz4537Assistant("ok"),
		zz4537Msg("", "continue"),
		zz4537Assistant("ok2"),
		zz4537Msg("human", "continue"),
	}
	out := dedupeUserMsgs(in)
	total := 0
	human := 0
	for _, m := range out {
		if m.Role == "user" && len(m.Content) == 1 && m.Content[0].Text == "continue" {
			total++
			if m.Origin == "human" {
				human++
			}
		}
	}
	if total != 2 || human != 1 {
		t.Fatalf("mixed origins: want total=2 (1 collapsed machine + 1 human), got total=%d human=%d", total, human)
	}
}
