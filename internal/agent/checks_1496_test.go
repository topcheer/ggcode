package agent

import (
	"testing"
)

func Test1496GitNestedNotFlat(t *testing.T) {
	if isReadOnlySearchCommand(`git commit -m "remove t.Skip from tests"`) {
		t.Fatal("git commit must not be exempted as a read-only search")
	}
	if !isReadOnlySearchCommand("git grep -n t.Skip -- tests/") {
		t.Fatal("git grep must keep the investigation exemption")
	}
}
