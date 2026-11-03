package agent

import (
	"testing"
)

func TestDetectNegativeFeedback_Rejection(t *testing.T) {
	tests := []struct {
		msg       string
		want      string
		wantEmpty bool
	}{
		{"no", negCatRejection, false},
		{"No, that's wrong", negCatRejection, false},
		{"wrong", negCatRejection, false},
		{"that's not right", negCatRejection, false},
		{"this is broken", negCatRejection, false},
		{"still not working", negCatRejection, false},
		{"hello world", "", true},
		{"can you help me with this function?", "", true},

		{"now let's try something else", "", true},

		{"nobody knows", "", true},

		{"I was thinking about how the wrong approach might actually be the right one if we consider the edge cases carefully", "", true},
	}
	for _, tt := range tests {
		got := detectNegativeFeedback(tt.msg)
		if tt.wantEmpty && got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", tt.msg, got)
		}
		if !tt.wantEmpty && got != tt.want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestDetectNegativeFeedback_Redirection(t *testing.T) {
	tests := []struct {
		msg       string
		want      string
		wantEmpty bool
	}{

		{"actually, use JWT tokens instead", negCatRedirection, false},
		{"instead of that approach, try using a map", negCatRedirection, false},
		{"I meant to say something different", negCatRedirection, false},
		{"start over", negCatRedirection, false},

		{"let's go", "", true},
	}
	for _, tt := range tests {
		got := detectNegativeFeedback(tt.msg)
		if tt.wantEmpty && got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", tt.msg, got)
		}
		if !tt.wantEmpty && got != tt.want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestDetectNegativeFeedback_Frustration(t *testing.T) {
	tests := []struct {
		msg       string
		want      string
		wantEmpty bool
	}{
		{"stop", negCatFrustration, false},
		{"stop doing that", negCatFrustration, false},
		{"ugh", negCatFrustration, false},
		{"why are you doing this", negCatFrustration, false},
		{"you keep making the same mistake", negCatFrustration, false},

		{"stopwatch timer implementation", "", true},
	}
	for _, tt := range tests {
		got := detectNegativeFeedback(tt.msg)
		if tt.wantEmpty && got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", tt.msg, got)
		}
		if !tt.wantEmpty && got != tt.want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestDetectNegativeFeedback_MultiLine(t *testing.T) {

	msg := "wrong\nI need the function to return an error instead of a boolean"
	got := detectNegativeFeedback(msg)
	if got != negCatRejection {
		t.Errorf("detectNegativeFeedback multiline = %q, want %q", got, negCatRejection)
	}

	msg2 := "Here's the function I'm working on:\nwrong return type on line 5"
	got2 := detectNegativeFeedback(msg2)
	if got2 != "" {
		t.Errorf("detectNegativeFeedback neutral first line = %q, want empty", got2)
	}
}

func TestDetectNegativeFeedback_OpenerWindowWordBoundary(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{

			name: "Now-opener with mid-message standalone no",
			msg:  "Now that we've validated the parser, let's discuss the deployment plan. I reviewed the checklist with the team yesterday and we agreed the answer is no for now, because the staging cluster is not ready yet.",
		},
		{
			name: "Nobody-opener with trailing standalone wrong",
			msg:  "Nobody has reviewed the migration plan yet, so I took a pass this morning and left comments on the pull request; overall the approach looks right, but the rollback section is wrong.",
		},
		{
			name: "Note-opener with mid-message standalone wrong",
			msg:  "Note that some tests still fail on the CI runner; the first failure is a flake we have seen before, but the second one is definitely wrong and needs a fix before we merge.",
		},
		{

			name: "Knowledge-opener control",
			msg:  "Knowledge of the retry semantics is spread across three files, which is why the fix is non-trivial; honestly the earlier approach was wrong and the new one is better documented.",
		},
	}
	for _, tt := range tests {
		if got := detectNegativeFeedback(tt.msg); got != "" {
			t.Errorf("%s: detectNegativeFeedback = %q, want empty", tt.name, got)
		}
	}

	rej := "no, that is not what I meant at all. I asked for the dry-run report first and the full migration after, because the staging data is not representative of production yet."
	if got := detectNegativeFeedback(rej); got != negCatRejection {
		t.Errorf("genuine no-opener: detectNegativeFeedback = %q, want %q", got, negCatRejection)
	}

	redir := "wrong approach - use the batch endpoint instead. Looping one request per item is what blew the rate limit last time, and the batch call has been available since API v2 launched."
	if got := detectNegativeFeedback(redir); got != negCatRejection {
		t.Errorf("genuine wrong-opener: detectNegativeFeedback = %q, want %q", got, negCatRejection)
	}
}

func TestDetectNegativeFeedback_PositivePhrases(t *testing.T) {
	positives := []string{
		"no problem, looks good to me",
		"not bad at all",
		"no worries",
		"looks good, no issues",
		"all good, well done",
		"great work, thank you",

		"why are you using a mutex here?",
		"how do I stop the daemon?",
	}
	for _, msg := range positives {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", msg, got)
		}
	}
}

func TestDetectNegativeFeedback_MixedSentiment(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"thanks, but still broken", negCatRejection},
		{"no problem, but wrong file", negCatRejection},
		{"looks good, but tests are failing", negCatRejection},
		{"thanks for nothing, this is garbage", negCatFrustration},
	}
	for _, tt := range tests {
		got := detectNegativeFeedback(tt.msg)
		if got != tt.want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestUserSentimentState_Escalation(t *testing.T) {
	s := newUserSentimentState()

	fb := s.analyzeAndUpdate("no, that's wrong")
	if fb.Level != sentimentEscalationSoft {
		t.Errorf("first negative: level=%d, want %d", fb.Level, sentimentEscalationSoft)
	}

	fb = s.analyzeAndUpdate("stop doing that")
	if fb.Level != sentimentEscalationStrong {
		t.Errorf("second negative: level=%d, want %d", fb.Level, sentimentEscalationStrong)
	}

	fb = s.analyzeAndUpdate("wrong again")
	if fb.Level != sentimentEscalationMax {
		t.Errorf("third negative: level=%d, want %d", fb.Level, sentimentEscalationMax)
	}

	fb = s.analyzeAndUpdate("still broken")
	if fb.Level != sentimentEscalationMax {
		t.Errorf("fourth negative: level=%d, want %d", fb.Level, sentimentEscalationMax)
	}

	fb = s.analyzeAndUpdate("great, now let's add tests")
	if fb.Level != 0 {
		t.Errorf("after reset: level=%d, want 0", fb.Level)
	}

	fb = s.analyzeAndUpdate("nope, that's not it")
	if fb.Level != sentimentEscalationSoft {
		t.Errorf("after reset+neg: level=%d, want %d", fb.Level, sentimentEscalationSoft)
	}
}

func TestUserSentimentState_Reset(t *testing.T) {
	s := newUserSentimentState()
	s.analyzeAndUpdate("wrong")
	s.analyzeAndUpdate("still wrong")
	if s.consecutiveNegatives != 2 {
		t.Fatalf("consecutiveNegatives=%d, want 2", s.consecutiveNegatives)
	}

	s.reset()
	if s.consecutiveNegatives != 0 {
		t.Errorf("after reset: consecutiveNegatives=%d, want 0", s.consecutiveNegatives)
	}
}

func TestShouldResetMonitoringOnFeedback(t *testing.T) {
	tests := []struct {
		level int
		want  bool
	}{
		{0, false},
		{sentimentEscalationSoft, false},
		{sentimentEscalationStrong, true},
		{sentimentEscalationMax, true},
	}
	for _, tt := range tests {
		fb := SentimentFeedback{Level: tt.level, Category: negCatRejection}
		got := shouldResetMonitoringOnFeedback(fb)
		if got != tt.want {
			t.Errorf("shouldResetMonitoring(level=%d) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestBuildSentimentGuidance(t *testing.T) {

	g := buildSentimentGuidance(SentimentFeedback{Level: 0})
	if g != "" {
		t.Errorf("level 0 guidance should be empty, got: %s", g)
	}

	g = buildSentimentGuidance(SentimentFeedback{Level: sentimentEscalationSoft, Category: negCatRejection})
	if g == "" {
		t.Error("level 1 guidance should not be empty")
	}

	g = buildSentimentGuidance(SentimentFeedback{Level: sentimentEscalationStrong, Category: negCatFrustration})
	if g == "" {
		t.Error("level 2 guidance should not be empty")
	}

	g = buildSentimentGuidance(SentimentFeedback{Level: sentimentEscalationMax, Category: negCatRedirection})
	if g == "" {
		t.Error("level 3 guidance should not be empty")
	}
}

func TestWordContains(t *testing.T) {
	tests := []struct {
		text string
		pat  string
		want bool
	}{
		{"no", "no", true},
		{"nope", "no", false},
		{"hello no world", "no", true},
		{"no!", "no", true},
		{"nobody", "no", false},
		{"know", "no", false},
		{"now", "no", false},
	}
	for _, tt := range tests {
		got := wordContains(tt.text, tt.pat)
		if got != tt.want {
			t.Errorf("wordContains(%q, %q) = %v, want %v", tt.text, tt.pat, got, tt.want)
		}
	}
}
