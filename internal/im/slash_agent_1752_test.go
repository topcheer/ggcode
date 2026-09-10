package im

import (
	"reflect"
	"testing"
)

// #1752 case 1: the /diff allowlist must drop every dangerous git option
// and keep only display flags plus post-separator paths.
func Test1752SanitizeGitDiffArgs(t *testing.T) {
	// Dangerous options are dropped.
	got := SanitizeGitDiffArgs([]string{"--output=~/.bashrc", "HEAD"})
	want := []string{"HEAD"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output-injection: got %v want %v", got, want)
	}
	// --ext-diff / -O / --no-index dropped.
	got = SanitizeGitDiffArgs([]string{"--ext-diff", "-Oorderfile", "--no-index", "--stat"})
	want = []string{"--stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("widening options: got %v want %v", got, want)
	}
	// After --, everything passes (paths).
	got = SanitizeGitDiffArgs([]string{"--cached", "--", "--output=x", "file.go"})
	want = []string{"--cached", "--", "--output=x", "file.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-separator paths: got %v want %v", got, want)
	}
	// Repeated allowlisted flags are fine.
	got = SanitizeGitDiffArgs([]string{"--stat", "--numstat"})
	want = []string{"--stat", "--numstat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("display flags: got %v want %v", got, want)
	}
}
