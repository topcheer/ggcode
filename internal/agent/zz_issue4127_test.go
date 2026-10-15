package agent

import "testing"

// #4127: "why do you keep"/"you keep"/"why are you doing" followed by a NOUN
// phrase is a neutral technical question (keep = hold/retain, doing =
// executing), the same false-positive family #1223 removed ("why are you
// using a mutex here?"). Only gerund/deixis frustration forms stay.
func TestIssue4127_TechnicalQuestionsNotFrustration(t *testing.T) {
	questions := []string{
		"why do you keep a mutex here?",
		"why do you keep the config in memory",
		"why are you doing a bounds check here?",
		"the cache you keep in memory is stale",
		"why are you doing the retry in the handler",
	}
	for _, q := range questions {
		if got := detectNegativeFeedback(q); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (neutral technical question)", q, got)
		}
	}
}

func TestIssue4127_GenuineFrustrationFormsStillFire(t *testing.T) {
	genuine := []string{
		"why are you doing this",
		"why are you doing that to the config",
		"you keep making the same mistake",
		"you keep trying to parse it with regex",
		"you keep doing the opposite of what I asked",
	}
	for _, msg := range genuine {
		if got := detectNegativeFeedback(msg); got != negCatFrustration {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", msg, got, negCatFrustration)
		}
	}
}
