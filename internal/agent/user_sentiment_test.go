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

// TestDetectNegativeFeedback_OpenerWindowWordBoundary pins #4174: the
// opener-window position check must use word-boundary matching (wordIndexIn),
// the same semantics as the wordContains gate. strings.Index finds a substring
// embedded in an unrelated opening word ("no" inside "Now that..."), and that
// borrowed position passes the 12-rune window even though the word-bounded
// match that actually fired is mid-message - so neutral long messages whose
// only standalone negative word appears mid-text must NOT be classified as
// course-correction feedback.
func TestDetectNegativeFeedback_OpenerWindowWordBoundary(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{
			// Issue repro: "Now" embeds "no" at index 0; the standalone
			// "no" is ~100 runes in ("the answer is no for now").
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
			// Control proving causality: same mid-message negative word,
			// but the opener ("Knowledge...") embeds nothing - passed both
			// before and after the fix.
			name: "Knowledge-opener control",
			msg:  "Knowledge of the retry semantics is spread across three files, which is why the fix is non-trivial; honestly the earlier approach was wrong and the new one is better documented.",
		},
	}
	for _, tt := range tests {
		if got := detectNegativeFeedback(tt.msg); got != "" {
			t.Errorf("%s: detectNegativeFeedback = %q, want empty", tt.name, got)
		}
	}

	// Positive controls: genuine openers on long messages must still fire.
	rej := "no, that is not what I meant at all. I asked for the dry-run report first and the full migration after, because the staging data is not representative of production yet."
	if got := detectNegativeFeedback(rej); got != negCatRejection {
		t.Errorf("genuine no-opener: detectNegativeFeedback = %q, want %q", got, negCatRejection)
	}
	// "wrong" belongs to the rejection group and is checked before the
	// redirection group, so a "wrong ... instead" opener classifies as
	// rejection (pre-existing precedence, unaffected by #4174).
	redir := "wrong approach - use the batch endpoint instead. Looping one request per item is what blew the rate limit last time, and the batch call has been available since API v2 launched."
	if got := detectNegativeFeedback(redir); got != negCatRejection {
		t.Errorf("genuine wrong-opener: detectNegativeFeedback = %q, want %q", got, negCatRejection)
	}
}

// TestDetectNegativeFeedback_PositivePhrases pins #1223: affirmative
// acknowledgements that contain bare rejection words ("no problem", "not bad")
// and technical how-to questions ("how do I stop...") must NOT be classified
// as negative feedback - consecutive misreads used to force an unnecessary
// ask_user interruption and wipe legitimate monitoring state.
func TestDetectNegativeFeedback_PositivePhrases(t *testing.T) {
	positives := []string{
		"no problem, looks good to me",
		"not bad at all",
		"no worries",
		"looks good, no issues",
		"all good, well done",
		"great work, thank you",
		// Technical questions, not frustration.
		"why are you using a mutex here?",
		"how do I stop the daemon?",
	}
	for _, msg := range positives {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", msg, got)
		}
	}
}

// TestDetectNegativeFeedback_MixedSentiment pins #1227: positive phrases mask
// only themselves; genuine negative signals elsewhere on the same line are
// still detected. The previous whole-line short-circuit hid "thanks, but
// still broken" and wrongly reset the escalation counter.
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
