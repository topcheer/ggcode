package agent

import "testing"

func Test1800JSCommentedOutCodeQuiet(t *testing.T) {
	src := "package main\n// console.log(\"token\", token)\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 0 {
		t.Fatalf("commented-out console.log must stay quiet, got %d", len(got))
	}
}

func Test1800JSRedactedWrapperQuiet(t *testing.T) {
	src := "package main\nconsole.log(user, redact(token))\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 0 {
		t.Fatalf("redact(token) must stay quiet, got %d", len(got))
	}

	src = "package main\nconsole.log(user, token)\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 1 {
		t.Fatalf("bare token must fire, got %d", len(got))
	}
}

func Test1800ScreamingSnakeEnvFires(t *testing.T) {
	src := "package main\nconsole.log(GITHUB_TOKEN)\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 1 {
		t.Fatalf("GITHUB_TOKEN must fire, got %d", len(got))
	}
}

func Test1800CamelCaseFires(t *testing.T) {
	src := "package main\nconsole.log(authToken)\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 1 {
		t.Fatalf("authToken must fire, got %d", len(got))
	}

	src = "package main\nconsole.log(tokenCount)\n"
	if got := findJSSensitiveLogArgs(src); len(got) != 0 {
		t.Fatalf("tokenCount must stay quiet, got %d", len(got))
	}
}

func Test1800JSParenNestingKept(t *testing.T) {
	src := "package main\nconsole.log(fmt(x), token)\n"
	got := findJSSensitiveLogArgs(src)
	if len(got) != 1 {
		t.Fatalf("nested-paren args must fire, got %d", len(got))
	}
}
