package agentruntime

import "testing"

// Verifier model selection (heterogeneous verification, arXiv:2512.02304).
func TestVerifierModelFor(t *testing.T) {
	mk := func(m string) CandidateOutcome { return CandidateOutcome{Model: m} }
	cases := []struct {
		name string
		opts BestOfNOptions
		a, b CandidateOutcome
		want string
	}{
		{
			name: "empty list inherits (r439 behavior)",
			opts: BestOfNOptions{},
			a:    mk("glm-5.3"), b: mk("glm-5.3"),
			want: "",
		},
		{
			name: "prefers model differing from both tied candidates",
			opts: BestOfNOptions{VerifierModels: []string{"glm-5.3", "glm-5.3-flash"}},
			a:    mk("glm-5.3"), b: mk("glm-5.3"),
			want: "glm-5.3-flash",
		},
		{
			name: "skips a verifier model used by one candidate",
			opts: BestOfNOptions{VerifierModels: []string{"glm-5.3-flash", "glm-5.3-air"}},
			a:    mk("glm-5.3"), b: mk("glm-5.3-flash"),
			want: "glm-5.3-air",
		},
		{
			name: "explicit config wins when all verifier models are also candidates",
			opts: BestOfNOptions{VerifierModels: []string{"glm-5.3"}},
			a:    mk("glm-5.3"), b: mk("glm-5.3"),
			want: "glm-5.3",
		},
		{
			name: "candidates on different models still find a third",
			opts: BestOfNOptions{VerifierModels: []string{"glm-5.3", "glm-5.3-air"}},
			a:    mk("glm-5.3"), b: mk("glm-5.3-air"),
			want: "glm-5.3", // no third available: explicit config wins over ""
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.opts.verifierModelFor(c.a, c.b); got != c.want {
				t.Fatalf("verifierModelFor(%q,%q) = %q, want %q", c.a.Model, c.b.Model, got, c.want)
			}
		})
	}
}

func TestVmOrInherited(t *testing.T) {
	if vmOrInherited("") != "inherited" {
		t.Fatal("empty model must render as inherited")
	}
	if vmOrInherited("glm-5.3") != "glm-5.3" {
		t.Fatal("non-empty model must pass through")
	}
}
