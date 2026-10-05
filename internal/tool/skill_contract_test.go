package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

func TestBuildContractHintFull(t *testing.T) {
	cmd := &commands.Command{
		Precondition:  "tree clean",
		Postcondition: "tests green",
		StateContract: "sha stored in .release-state",
		FailureModes: []commands.SkillFailureMode{
			{Name: "smoke-5xx", Detect: "endpoint 5xx", Recover: "rollback via .release-state"},
			{Name: ""},
		},
	}
	hint := buildContractHint(cmd)
	for _, want := range []string{
		"Precondition", "tree clean",
		"Postcondition", "tests green",
		"State contract", ".release-state",
		`Failure mode "smoke-5xx"`, "detect: endpoint 5xx", "recover: rollback via .release-state",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint missing %q:\n%s", want, hint)
		}
	}
	if strings.Contains(hint, "[skill contract] \n") {
		t.Error("unnamed failure mode should be skipped")
	}
}

func TestBuildContractHintEmpty(t *testing.T) {
	if got := buildContractHint(&commands.Command{}); got != "" {
		t.Errorf("empty command should yield empty hint, got %q", got)
	}
	if got := buildContractHint(nil); got != "" {
		t.Errorf("nil command should yield empty hint, got %q", got)
	}
}

func TestBuildSkillMarkdownContractRoundTrip(t *testing.T) {
	contract := skillContract{
		Precondition:  "on release branch",
		Postcondition: "tag pushed",
		StateContract: "sha in .release-state",
		FailureModes: []commands.SkillFailureMode{
			{Name: "smoke-5xx", Detect: "5xx", Recover: "rollback"},
		},
	}
	md := buildSkillMarkdown("deployx", "deploy safely", "", nil, nil, nil, "", contract, "Body text.")
	for _, want := range []string{
		"precondition: on release branch",
		"postcondition: tag pushed",
		"state-contract: sha in .release-state",
		"name: smoke-5xx", "recover: rollback",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBuildSkillMarkdownNoContract(t *testing.T) {
	md := buildSkillMarkdown("plainx", "plain skill", "", nil, nil, nil, "", skillContract{}, "Body.")
	if strings.Contains(md, "precondition") || strings.Contains(md, "failure-modes") {
		t.Errorf("omitted contract fields must not be serialized:\n%s", md)
	}
}
