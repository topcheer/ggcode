// Package usersim provides deterministic, LLM-free simulated users for
// evaluating multi-turn agent conversations.
//
// It adapts the tau-bench tool-agent-user methodology (Sierra Research,
// 2024-2026) to an offline harness: the simulated user holds hidden facts
// the agent must actively elicit through questioning; the conversation is
// half-duplex (strictly turn-based); success is measured by goal
// achievement, and reliability by pass^k over repeated trials (PassAtK).
//
// Determinism matters: unlike LLM-based user simulators, replies are
// script-driven so evaluation runs are reproducible and cheap enough to
// gate every build.
package usersim

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// defaultMaxTurns bounds a conversation when Persona.MaxTurns is unset.
const defaultMaxTurns = 8

// Persona describes a simulated user with a goal and hidden information.
type Persona struct {
	// Name identifies the persona in reports.
	Name string
	// Opening is the user's first message; it starts every conversation.
	Opening string
	// Facts are the hidden pieces of information the agent must elicit.
	// Keys are canonical fact names (e.g. "order_id"); values are the
	// secret contents the user only discloses when asked. A persona may
	// have zero facts, in which case success depends solely on
	// SuccessPattern.
	Facts map[string]string
	// Cues map fact keys to trigger patterns: when the agent's message
	// matches the pattern, the user reveals that fact. Use the prefix
	// "re:" for a regular expression; anything else is a case-insensitive
	// substring match. Facts without a cue are never revealed.
	Cues map[string]string
	// Reveals optionally map fact keys to the user's reply line when the
	// fact is disclosed. Missing entries use a standard template.
	Reveals map[string]string
	// SuccessPattern: once all facts are revealed, an agent message
	// matching this pattern achieves the goal. Same "re:" convention.
	SuccessPattern string
	// Fallbacks are replies used when no cue matches and the goal is not
	// achieved. They rotate deterministically across turns.
	Fallbacks []string
	// MaxTurns bounds user turns; 0 defaults to 8.
	MaxTurns int
}

// Status is the outcome of a conversation in progress.
type Status int

const (
	// Ongoing means the conversation may continue.
	Ongoing Status = iota
	// GoalAchieved means the agent satisfied the persona's goal.
	GoalAchieved
	// Exhausted means MaxTurns user turns elapsed without success.
	Exhausted
)

// String implements fmt.Stringer.
func (s Status) String() string {
	switch s {
	case GoalAchieved:
		return "goal_achieved"
	case Exhausted:
		return "exhausted"
	default:
		return "ongoing"
	}
}

// Turn is one half-duplex exchange: an agent message and the user reply.
type Turn struct {
	N     int
	Agent string
	User  string
}

// Report summarizes a finished (or in-progress) conversation.
type Report struct {
	Persona  string
	Turns    int
	Revealed []string // fact keys in reveal order
	Missing  []string // fact keys never elicited, sorted
	Stalls   int      // consecutive identical agent messages seen
	Status   Status
}

// Score returns partial credit: 1.0 on goal achievement, otherwise half
// of the fraction of hidden facts successfully elicited (0 facts: 0.5
// only if the goal was achieved, else 0).
func (r Report) Score() float64 {
	if r.Status == GoalAchieved {
		return 1
	}
	total := len(r.Revealed) + len(r.Missing)
	if total == 0 {
		return 0
	}
	return 0.5 * float64(len(r.Revealed)) / float64(total)
}

// Simulator is a deterministic half-duplex simulated user.
type Simulator struct {
	persona   Persona
	factKeys  []string // sorted fact keys for deterministic iteration
	cueRes    map[string]*regexp.Regexp
	successRe *regexp.Regexp
	revealed  map[string]bool
	order     []string
	turns     []Turn
	lastAgent string
	stalls    int
	fallbackN int
	maxTurns  int
	done      bool
	status    Status
}

// New builds a simulator for p. Invalid cue/success regexes degrade to
// literal matches so a malformed persona never panics mid-evaluation.
func New(p Persona) *Simulator {
	s := &Simulator{
		persona:  p,
		revealed: map[string]bool{},
		cueRes:   map[string]*regexp.Regexp{},
		maxTurns: p.MaxTurns,
	}
	if s.maxTurns <= 0 {
		s.maxTurns = defaultMaxTurns
	}
	for k := range p.Facts {
		s.factKeys = append(s.factKeys, k)
	}
	sort.Strings(s.factKeys)
	for _, k := range s.factKeys {
		s.cueRes[k] = compile(p.Cues[k])
	}
	s.successRe = compile(p.SuccessPattern)
	return s
}

// compile turns a cue/success pattern into a matcher: "re:..." is a
// case-insensitive regexp, anything else a case-insensitive substring.
func compile(pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}
	if expr, ok := strings.CutPrefix(pattern, "re:"); ok {
		re, err := regexp.Compile("(?i)" + expr)
		if err == nil {
			return re
		}
		// fall through: treat whole pattern as literal
	}
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(pattern))
}

// Open returns the user's opening message and starts the transcript.
func (s *Simulator) Open() string { return s.persona.Opening }

// MaxTurns reports the effective turn budget.
func (s *Simulator) MaxTurns() int { return s.maxTurns }

// Reply feeds one agent message and returns the user's response together
// with the current status. After GoalAchieved or Exhausted the simulator
// is closed and keeps returning the terminal status.
func (s *Simulator) Reply(agentMsg string) (string, Status) {
	if s.done {
		return "", s.status
	}
	if strings.TrimSpace(agentMsg) != "" && strings.TrimSpace(agentMsg) == strings.TrimSpace(s.lastAgent) {
		s.stalls++
	}
	s.lastAgent = agentMsg

	reply := ""
	for _, k := range s.factKeys {
		if s.revealed[k] || s.cueRes[k] == nil || !s.cueRes[k].MatchString(agentMsg) {
			continue
		}
		s.revealed[k] = true
		s.order = append(s.order, k)
		if line, ok := s.persona.Reveals[k]; ok && line != "" {
			reply = line
		} else {
			reply = fmt.Sprintf("Sure, it's %s.", s.persona.Facts[k])
		}
	}

	if len(s.order) == len(s.factKeys) && s.successRe != nil && s.successRe.MatchString(agentMsg) {
		s.close(GoalAchieved)
		return s.record(agentMsg, "Perfect, that's everything I needed. Thank you!"), s.status
	}
	if reply == "" {
		reply = s.nextFallback()
	}
	reply = s.record(agentMsg, reply)
	if len(s.turns) >= s.maxTurns {
		s.close(Exhausted)
	}
	return reply, s.status
}

// Transcript returns a copy of the turns so far.
func (s *Simulator) Transcript() []Turn {
	out := make([]Turn, len(s.turns))
	copy(out, s.turns)
	return out
}

// Report snapshots the conversation state.
func (s *Simulator) Report() Report {
	missing := make([]string, 0, len(s.factKeys)-len(s.order))
	for _, k := range s.factKeys {
		if !s.revealed[k] {
			missing = append(missing, k)
		}
	}
	return Report{
		Persona:  s.persona.Name,
		Turns:    len(s.turns),
		Revealed: append([]string(nil), s.order...),
		Missing:  missing,
		Stalls:   s.stalls,
		Status:   s.status,
	}
}

func (s *Simulator) nextFallback() string {
	if s.stalls > 0 {
		return "You already said that. Could you look into my request?"
	}
	fallbacks := s.persona.Fallbacks
	if len(fallbacks) == 0 {
		fallbacks = []string{"Please help me with my request."}
	}
	reply := fallbacks[s.fallbackN%len(fallbacks)]
	s.fallbackN++
	return reply
}

func (s *Simulator) record(agentMsg, userReply string) string {
	s.turns = append(s.turns, Turn{N: len(s.turns) + 1, Agent: agentMsg, User: userReply})
	return userReply
}

func (s *Simulator) close(status Status) {
	s.done = true
	s.status = status
}
