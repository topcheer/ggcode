package agent

import (
	"strings"
	"testing"
)

func Test1841SyncRemovalTriggers(t *testing.T) {
	old := "package a\nimport \"sync\"\nvar mu sync.Mutex\nfunc f(){ mu.Lock(); go work(); mu.Unlock() }\n"
	new := "package a\nfunc f(){ go work() }\n"
	if got := checkRaceVerifyHint("a.go", old, new); got == nil {
		t.Fatal("mutex removal with goroutine kept must trigger the race hint")
	}

	old2 := "package a\nimport \"sync\"\nvar mu sync.Mutex\nfunc f(){ mu.Lock(); go work(); mu.Unlock() }\n"
	new2 := "package a\nfunc f(){ ch := make(chan int); go func(){ <-ch }() }\n"
	if got := checkRaceVerifyHint("a.go", old2, new2); got == nil {
		t.Fatal("net-zero primitive rewrite must trigger")
	}

	old3 := old
	new3 := "package a\nfunc f(){}\n"
	if got := checkRaceVerifyHint("a.go", old3, new3); got != nil {
		t.Fatal("removing both sync and goroutines should not trigger")
	}
}

func Test1841ListingDoesNotClearFailure(t *testing.T) {
	if isRealTestExecution("make help") {
		t.Fatal("make help is not a real test execution")
	}
	if isRealTestExecution("task --list") {
		t.Fatal("task --list is not a real test execution")
	}
	if isRealTestExecution("gofmt -l .") {
		t.Fatal("gofmt -l is not a real test execution")
	}
	if !isRealTestExecution("go test ./...") {
		t.Fatal("go test must be a real test execution")
	}
	if !isRealTestExecution("make verify-ci") {
		t.Fatal("make <real-target> must be a real test execution")
	}
}

func Test1841MainstreamRunners(t *testing.T) {
	for _, cmd := range []string{"bazel test //...", "gradle test", "mvn test", "dotnet test", "yarn test", "pnpm test", "vitest run"} {
		if !isVerifyCommand(cmd) {
			t.Fatalf("%q must count as a verify command", cmd)
		}
	}
	_ = strings.TrimSpace
}

func Test1841RaceHintCritical(t *testing.T) {
	for _, c := range allChecks {
		if c.Name == "race-verify-hint" && c.Severity != SeverityCritical {
			t.Fatalf("race-verify-hint must be critical, got %v", c.Severity)
		}
	}
}
