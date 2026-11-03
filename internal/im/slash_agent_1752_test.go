package im

import (
	"reflect"
	"testing"
)

func Test1752SanitizeGitDiffArgs(t *testing.T) {

	got := SanitizeGitDiffArgs([]string{"--output=~/.bashrc", "HEAD"})
	want := []string{"HEAD"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output-injection: got %v want %v", got, want)
	}

	got = SanitizeGitDiffArgs([]string{"--ext-diff", "-Oorderfile", "--no-index", "--stat"})
	want = []string{"--stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("widening options: got %v want %v", got, want)
	}

	got = SanitizeGitDiffArgs([]string{"--cached", "--", "--output=x", "file.go"})
	want = []string{"--cached", "--", "--output=x", "file.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-separator paths: got %v want %v", got, want)
	}

	got = SanitizeGitDiffArgs([]string{"--stat", "--numstat"})
	want = []string{"--stat", "--numstat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("display flags: got %v want %v", got, want)
	}
}
