package tui

import (
	"os"
	"strings"
	"testing"
)

func wecomSrc2337(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("wecom_panel.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wecomBindTailSrc(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "func (m *Model) wecomBindTail")
	if start < 0 {
		t.Fatal("bind tail must exist as its own method")
	}
	end := strings.Index(src[start+1:], "\nfunc ")
	return src[start : start+1+end]
}

func TestIssue2333BindFlowInterceptsEnableSentinel(t *testing.T) {
	src := wecomSrc2337(t)
	tail := wecomBindTailSrc(t, src)
	if !strings.Contains(tail, "errors.Is(err, errWecomEnableNeeded)") {
		t.Fatal("bind tail must intercept the enable sentinel")
	}
	if !strings.Contains(tail, "SetIMAdapterEnabled(entry.Adapter, true)") {
		t.Fatal("interception must auto-enable the adapter")
	}
	if !strings.Contains(tail, "wecomBindTail(entry, ws)") {
		t.Fatal("post-enable continuation must retry the bind tail")
	}

	entryRaw := src[strings.Index(src, "func (m *Model) bindWeComEntry"):strings.Index(src, "func (m *Model) wecomBindTail")]
	var entryCode []string
	for _, l := range strings.Split(entryRaw, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			entryCode = append(entryCode, l)
		}
	}
	entry := strings.Join(entryCode, "\n")
	if strings.Contains(entry, "errWecomEnableNeeded") || strings.Contains(entry, "err: err}") {
		t.Fatal("bind entry must not pass start errors through raw")
	}
}

func TestIssue2337BindEnableIsBindOnly(t *testing.T) {
	src := wecomSrc2337(t)
	tail := wecomBindTailSrc(t, src)

	if strings.Contains(tail, "wecomEnableMutation(") {
		t.Fatal("bind tail must not chain wecomEnableMutation: its restart failure rolls back (deletes) the adapter")
	}
	if strings.Contains(tail, "rollbackWecomCreate(") {
		t.Fatal("bind tail must never roll back (delete) a pre-existing adapter")
	}

	if !strings.Contains(tail, "StartNamedAdapter") {
		t.Fatal("bind-only continuation must restart the adapter inline")
	}
}

func TestIssue2337EntriesUseSnapshotLen(t *testing.T) {
	src := wecomSrc2337(t)
	if strings.Contains(src, "len(m.config.IM.Adapters)") {
		t.Fatal("live-map len read must be replaced by the snapshot length")
	}
	if !strings.Contains(src, "snapAdapters := m.config.IMSnapshot().Adapters") {
		t.Fatal("entries must snapshot the adapter map once")
	}
}
