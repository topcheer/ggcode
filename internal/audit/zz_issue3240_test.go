package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #3240 regression: InvariantID must participate in the chain hash.
// Three tamper shapes that used to pass Verify: (1) rewrite the
// invariant_id to a different rule, (2) erase it, (3) inject one onto a
// normal entry. Plus the compatibility anchor: pre-r454 entries
// (InvariantID="") must hash IDENTICALLY to the old 9-field computation
// so existing chains keep verifying after the upgrade.

func TestIssue3240_HashSealsInvariantID(t *testing.T) {
	e := Entry{Seq: 1, Time: "t", Tool: "file_ops", Status: "invalid",
		InputHash: "aa", DurationMS: 5, InvariantID: "no-delete-env", PrevHash: GenesisPrevHash}
	h1 := hashEntry(e)
	e.InvariantID = "other-rule"
	if h2 := hashEntry(e); h1 == h2 {
		t.Fatal("rewriting InvariantID must change the chain hash (#3240 blind spot)")
	}
	e.InvariantID = ""
	if h3 := hashEntry(e); h1 == h3 {
		t.Fatal("erasing InvariantID must change the chain hash")
	}
	e.InvariantID = "no-delete-env"
	if h4 := hashEntry(e); h1 != h4 {
		t.Fatal("restore must round-trip")
	}
}

func TestIssue3240_EmptyInvariantIDMatchesPreR454(t *testing.T) {
	// The OLD (pre-#3236/#3240) 9-field writeField sequence, reproduced
	// verbatim: the conditional append must be invisible for empty IDs.
	e := Entry{Seq: 2, Time: "2026-10-03T00:00:00Z", Session: "s", Tool: "run_command",
		Status: "ok", InputHash: "ff", DurationMS: 42, Err: "", PrevHash: GenesisPrevHash}
	var b strings.Builder
	wf := func(s string) {
		var lenBuf [8]byte
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(s)))
		b.Write(lenBuf[:])
		b.WriteString(s)
	}
	wf(fmt.Sprintf("%d", e.Seq))
	wf(e.Time)
	wf(e.Session)
	wf(e.Tool)
	wf(e.Status)
	wf(e.InputHash)
	wf(fmt.Sprintf("%d", e.DurationMS))
	wf(e.Err)
	wf(e.PrevHash)
	sum := sha256.Sum256([]byte(b.String()))
	oldStyle := hex.EncodeToString(sum[:])
	if got := hashEntry(e); got != oldStyle {
		t.Fatalf("empty-InvariantID hash drifts from pre-r454 computation:\nnew=%s\nold=%s", got, oldStyle)
	}
}

func TestIssue3240_TamperTripsVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")

	seal := func(ev Event) {
		l, err := Open(path, "sess")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(ev); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	raw := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	overwrite := func(content string) {
		os.WriteFile(path, []byte(content), 0o644)
	}
	mustTrip := func(label string) {
		t.Helper()
		rep, err := Verify(path)
		if err == nil && rep.OK() {
			t.Fatalf("tamper[%s] passed Verify - blind spot (#3240)", label)
		}
	}

	// 1: rewrite the invariant_id to another rule.
	seal(Event{Tool: "file_ops", Status: "invalid", InputHash: "aa", DurationMS: 5, InvariantID: "no-delete-env"})
	overwrite(strings.Replace(raw(), "no-delete-env", "some-other", 1))
	mustTrip("rewrite invariant_id")

	// 2: erase the field.
	os.Remove(path)
	seal(Event{Tool: "file_ops", Status: "invalid", InputHash: "aa", DurationMS: 5, InvariantID: "no-delete-env"})
	overwrite(strings.Replace(raw(), `,"invariant_id":"no-delete-env"`, "", 1))
	mustTrip("erase invariant_id")

	// 3: inject an invariant_id onto a NORMAL entry.
	os.Remove(path)
	seal(Event{Tool: "read_file", Status: "ok", InputHash: "bb", DurationMS: 1})
	overwrite(strings.Replace(raw(), `"tool":"read_file"`, `"tool":"read_file","invariant_id":"framed"`, 1))
	mustTrip("inject invariant_id onto normal entry")

	// Control: a freshly sealed chain (normal + invariant entries mixed)
	// must verify clean under the new hash.
	os.Remove(path)
	seal(Event{Tool: "read_file", Status: "ok", InputHash: "bb", DurationMS: 1})
	seal(Event{Tool: "file_ops", Status: "invalid", InputHash: "aa", DurationMS: 5, InvariantID: "no-delete-env"})
	if rep, err := Verify(path); err != nil || !rep.OK() {
		t.Fatalf("clean mixed chain must Verify: err=%v ok=%v", err, rep.OK())
	}
	_ = time.Now
}
