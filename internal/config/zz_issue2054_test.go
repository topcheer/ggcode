package config

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

		{"reference line-leading", "build:\n\t-tags $(TAGS) go build .\n", true},
		{"reference equals form", "check:\n\tgo vet -tags=$(TAGS) ./...\n", true},

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
