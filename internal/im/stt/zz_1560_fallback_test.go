package stt

// #1560-D: an empty SUCCESS from the primary transcriber must stand -
// silent audio legitimately transcribes to "". The old Text != ""
// condition treated it as failure and ran the local whisper fallback,
// whose hallucinated filler replaced the correct empty transcript.

import (
	"context"
	"errors"
	"testing"
)

type stub1560 struct {
	text  string
	err   error
	calls int
}

func (s *stub1560) Transcribe(ctx context.Context, req Request) (Result, error) {
	s.calls++
	return Result{Text: s.text, Provider: "stub"}, s.err
}

func TestIssue1560DEmptySuccessDoesNotFallThrough(t *testing.T) {
	primary := &stub1560{text: ""}
	secondary := &stub1560{text: "Thank you."} // whisper hallucination
	f := NewFallback(primary, secondary)
	res, err := f.Transcribe(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "" {
		t.Fatalf("empty success must stand, got hallucination %q", res.Text)
	}
	if secondary.calls != 0 {
		t.Fatal("secondary must not run on primary success")
	}
}

func TestIssue1560DRealErrorFallsThrough(t *testing.T) {
	primary := &stub1560{err: errors.New("boom")}
	secondary := &stub1560{text: "rescued"}
	f := NewFallback(primary, secondary)
	res, err := f.Transcribe(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "rescued" || secondary.calls != 1 {
		t.Fatalf("real error must fall through, got %q calls=%d", res.Text, secondary.calls)
	}
}
