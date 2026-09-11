package config

// #2054 regression: goolmTagUsage's bare-TAGS branch matched any
// TAGS-prefixed line (HasPrefix(t, "TAGS")), so release-image labels like
// `TAGS := v1.2.3 production` or `TAGS ?= docker` injected `-tags goolm`
// into projects that never use it - the same #1523 false-positive class
// (DOCKER_TAGS/IMAGE_TAGS) in a narrower form. A bare assignment now only
// counts when its VALUE names goolm; the `-tags $(TAGS)` reference branch
// is unchanged.

import "testing"

func TestGoolmTagUsage(t *testing.T) {
	cases := []struct {
		name     string
		makefile string
		want     bool
	}{
		{"value names goolm", "TAGS := goolm\n", true},
		{"conditional goolm", "TAGS ?= goolm\n", true},
		{"append goolm", "TAGS += goolm\n", true},
		{"plain equals", "TAGS = goolm\n", true},
		{"posix assignment", "TAGS ::= goolm\n", true},
		// Reference branch preserved as-is from #1523: line-leading
		// "-tags $(TAGS)" (e.g. a shell recipe line) and the
		// -tags=$(TAGS) contains form.
		{"reference line-leading", "build:\n\t-tags $(TAGS) go build .\n", true},
		{"reference equals form", "check:\n\tgo vet -tags=$(TAGS) ./...\n", true},
		// #2054: the false-positive class the bare HasPrefix let through.
		{"release image labels", "TAGS := v1.2.3 production\n", false},
		{"docker tags value", "TAGS ?= docker\n", false},
		{"suffixed variable name", "TAGS_APPEND := goolm-ish\n", false},
		{"prefixed variable name", "DOCKER_TAGS := goolm\n", false},
		{"comment mention", "# TAGS := goolm\n", false},
		{"unrelated", "build:\n\tgo build ./...\n", false},
	}
	for _, c := range cases {
		if got := goolmTagUsage(c.makefile); got != c.want {
			t.Errorf("%s: goolmTagUsage(%q) = %v, want %v", c.name, c.makefile, got, c.want)
		}
	}
}
