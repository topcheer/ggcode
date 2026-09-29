package usersim

import (
	"strings"
	"testing"
)

var elicitation = Persona{
	Name:    "return-customer",
	Opening: "Hi, I want a refund for my order.",
	Facts: map[string]string{
		"order_id": "T-4821",
		"email":    "user@example.com",
	},
	Cues: map[string]string{
		"order_id": "re:order",
		"email":    "re:email",
	},
	SuccessPattern: "re:refund.*T-4821",
	Fallbacks:      []string{"Please help me with my refund."},
	MaxTurns:       6,
}

// goodAgent asks about both facts in order, then produces the final
// refund answer. It is a closed-loop state machine keyed on its own
// progress, not on the user's message contents.
func goodAgent() Agent {
	step := 0
	return func(string) string {
		step++
		switch step {
		case 1:
			return "I can help with the refund. What is your order ID?"
		case 2:
			return "Thanks. What email is on the order?"
		default:
			return "Refund for order T-4821 has been approved."
		}
	}
}

func TestElicitationSuccess(t *testing.T) {
	s := New(elicitation)
	agent := goodAgent()
	reply := s.Open()
	var status Status
	for i := 0; i < 10; i++ {
		reply, status = s.Reply(agent(reply))
		if status != Ongoing {
			break
		}
	}
	if status != GoalAchieved {
		t.Fatalf("status = %v, want goal_achieved; transcript: %+v", status, s.Transcript())
	}
	r := s.Report()
	if len(r.Revealed) != 2 || len(r.Missing) != 0 {
		t.Fatalf("revealed = %v, missing = %v; want all facts elicited", r.Revealed, r.Missing)
	}
	if r.Score() != 1 {
		t.Fatalf("Score = %v, want 1", r.Score())
	}
	if r.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", r.Turns)
	}
}

func TestNeverAsksExhausted(t *testing.T) {
	r, turns := RunTrial(elicitation, func(string) string { return "I'll look into it right away." })
	if r.Status != Exhausted {
		t.Fatalf("status = %v, want exhausted", r.Status)
	}
	if r.Turns != elicitation.MaxTurns {
		t.Fatalf("Turns = %d, want %d", r.Turns, elicitation.MaxTurns)
	}
	if len(turns) != r.Turns {
		t.Fatalf("transcript length %d != report turns %d", len(turns), r.Turns)
	}
	if r.Score() != 0 {
		t.Fatalf("Score = %v, want 0 (nothing elicited)", r.Score())
	}
	want := []string{"email", "order_id"}
	if strings.Join(r.Missing, ",") != strings.Join(want, ",") {
		t.Fatalf("Missing = %v, want %v", r.Missing, want)
	}
}

func TestPartialCredit(t *testing.T) {
	s := New(elicitation)
	s.Open()
	// Only ever ask about the order; never the email; never conclude.
	for s.Report().Status == Ongoing {
		s.Reply("What is your order ID?")
	}
	r := s.Report()
	if got := r.Score(); got != 0.25 {
		t.Fatalf("Score = %v, want 0.5 * 1/2 = 0.25", got)
	}
	if len(r.Revealed) != 1 || r.Revealed[0] != "order_id" {
		t.Fatalf("Revealed = %v, want [order_id]", r.Revealed)
	}
}

func TestStallDetection(t *testing.T) {
	s := New(elicitation)
	s.Open()
	s.Reply("I'll look into it.")
	reply, _ := s.Reply("I'll look into it.")
	if !strings.Contains(reply, "already said") {
		t.Fatalf("reply = %q, want stall nudge", reply)
	}
	if s.Report().Stalls != 1 {
		t.Fatalf("Stalls = %d, want 1", s.Report().Stalls)
	}
}

func TestPassAtK(t *testing.T) {
	if got := PassAtK(elicitation, goodAgent, 5); got != 1 {
		t.Fatalf("PassAtK(good) = %v, want 1", got)
	}
	if got := PassAtK(elicitation, func() Agent { return func(string) string { return "..." } }, 5); got != 0 {
		t.Fatalf("PassAtK(fail) = %v, want 0", got)
	}
	if got := PassAtK(elicitation, nil, 3); got != 0 {
		t.Fatalf("PassAtK(nil agent) = %v, want 0", got)
	}
}

func TestClosedSimulatorStable(t *testing.T) {
	s := New(elicitation)
	s.Open()
	for s.Report().Status == Ongoing {
		s.Reply("Working on it.")
	}
	reply, status := s.Reply("anything")
	if status != Exhausted || reply != "" {
		t.Fatalf("after close: reply=%q status=%v; want empty/exhausted", reply, status)
	}
}

func TestFactlessPersona(t *testing.T) {
	p := Persona{Name: "direct", Opening: "hello", SuccessPattern: "re:done", MaxTurns: 3}
	r, _ := RunTrial(p, func(msg string) string {
		if strings.Contains(msg, "hello") {
			return "all done"
		}
		return "hmm"
	})
	if r.Status != GoalAchieved || r.Score() != 1 {
		t.Fatalf("factless persona: status=%v score=%v; want achieved/1", r.Status, r.Score())
	}
}

func TestInvalidRegexDegradesLiteral(t *testing.T) {
	p := Persona{
		Opening:        "hi",
		Facts:          map[string]string{"x": "1"},
		Cues:           map[string]string{"x": "re:(oops"},
		SuccessPattern: "re:([bad",
	}
	s := New(p) // must not panic
	s.Open()
	if reply, _ := s.Reply("re:(oops"); reply == "" {
		t.Fatal("literal cue should reveal fact")
	}
}
