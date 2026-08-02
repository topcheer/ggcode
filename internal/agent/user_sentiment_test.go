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
		// "now" should NOT match "no"
		{"now let's try something else", "", true},
		// "nobody" should NOT match "no" (word boundary)
		{"nobody knows", "", true},
		// Long messages with rejection words should NOT trigger (too detailed)
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
		// Redirection applies to short messages only
		{"actually, use JWT tokens instead", negCatRedirection, false},
		{"instead of that approach, try using a map", negCatRedirection, false},
		{"I meant to say something different", negCatRedirection, false},
		{"start over", negCatRedirection, false},
		// Non-redirection
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
		// "stopwatch" should NOT match "stop"
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
	// First line is the reactive sentiment; second line is context.
	msg := "wrong\nI need the function to return an error instead of a boolean"
	got := detectNegativeFeedback(msg)
	if got != negCatRejection {
		t.Errorf("detectNegativeFeedback multiline = %q, want %q", got, negCatRejection)
	}

	// First line neutral, second line has rejection word -- should not trigger
	msg2 := "Here's the function I'm working on:\nwrong return type on line 5"
	got2 := detectNegativeFeedback(msg2)
	if got2 != "" {
		t.Errorf("detectNegativeFeedback neutral first line = %q, want empty", got2)
	}
}

func TestUserSentimentState_Escalation(t *testing.T) {
	s := newUserSentimentState()

	// First negative message -> level 1
	fb := s.analyzeAndUpdate("no, that's wrong")
	if fb.Level != sentimentEscalationSoft {
		t.Errorf("first negative: level=%d, want %d", fb.Level, sentimentEscalationSoft)
	}

	// Second consecutive negative -> level 2
	fb = s.analyzeAndUpdate("stop doing that")
	if fb.Level != sentimentEscalationStrong {
		t.Errorf("second negative: level=%d, want %d", fb.Level, sentimentEscalationStrong)
	}

	// Third consecutive -> level 3
	fb = s.analyzeAndUpdate("wrong again")
	if fb.Level != sentimentEscalationMax {
		t.Errorf("third negative: level=%d, want %d", fb.Level, sentimentEscalationMax)
	}

	// Fourth consecutive -> still level 3 (capped)
	fb = s.analyzeAndUpdate("still broken")
	if fb.Level != sentimentEscalationMax {
		t.Errorf("fourth negative: level=%d, want %d", fb.Level, sentimentEscalationMax)
	}

	// Non-negative message resets
	fb = s.analyzeAndUpdate("great, now let's add tests")
	if fb.Level != 0 {
		t.Errorf("after reset: level=%d, want 0", fb.Level)
	}

	// Next negative starts fresh at level 1
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
	// Level 0 -> empty
	g := buildSentimentGuidance(SentimentFeedback{Level: 0})
	if g != "" {
		t.Errorf("level 0 guidance should be empty, got: %s", g)
	}

	// Level 1 -> contains "different strategy"
	g = buildSentimentGuidance(SentimentFeedback{Level: sentimentEscalationSoft, Category: negCatRejection})
	if g == "" {
		t.Error("level 1 guidance should not be empty")
	}

	// Level 2 -> contains "STOP"
	g = buildSentimentGuidance(SentimentFeedback{Level: sentimentEscalationStrong, Category: negCatFrustration})
	if g == "" {
		t.Error("level 2 guidance should not be empty")
	}

	// Level 3 -> contains "ask_user"
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
		{"nope", "no", false}, // "no" + "pe" - 'p' is alnum, not a word boundary
		{"hello no world", "no", true},
		{"no!", "no", true},     // '!' is not alnum, valid boundary
		{"nobody", "no", false}, // "no" + "body" - 'b' is alnum, not a word boundary
		{"know", "no", false},   // 'k' before 'n' -> false
		{"now", "no", false},    // "no" + "w" - 'w' is alnum, not a word boundary
	}
	for _, tt := range tests {
		got := wordContains(tt.text, tt.pat)
		if got != tt.want {
			t.Errorf("wordContains(%q, %q) = %v, want %v", tt.text, tt.pat, got, tt.want)
		}
	}
}
