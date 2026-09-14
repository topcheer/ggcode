package tui

// #2377: the async usage-probe goroutine captured the whole *Model and
// re-read m.imEmitter cross-goroutine (emitIMText) while Update-side
// writers swap it - a data race. The goroutine must hold the emitter
// VALUE snapshotted on the Update side and call EmitText directly.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2377ProbeGoroutineHoldsEmitterValue(t *testing.T) {
	b, err := os.ReadFile("remote_commands.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	anchor := "#2377: snapshot the EMITTER VALUE"
	i := strings.Index(src, anchor)
	if i < 0 {
		t.Fatal("the #2377 snapshot block must exist in SessionUsageSummary")
	}
	j := strings.Index(src[i:], "\n\t\t\t}\n") // end of the if-block region
	region := src[i : i+j+10]
	if strings.Contains(region, "emitter := d.m\n") {
		t.Fatal("must not capture the whole *Model (#2377 race source)")
	}
	if !strings.Contains(region, "emitter := d.m.imEmitter") {
		t.Fatal("must snapshot the emitter FIELD value on the Update side")
	}
	if strings.Contains(region, "emitter.emitIMText") {
		t.Fatal("the goroutine must not call emitIMText (re-reads m.imEmitter)")
	}
	if !strings.Contains(region, "emitter.EmitText") {
		t.Fatal("the goroutine must call EmitText on the snapshot directly")
	}
}
