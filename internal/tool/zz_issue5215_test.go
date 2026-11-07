package tool

import (
	"path/filepath"
	"strings"
	"testing"
)

// #5215: the browser tool's screenshot output_path (arbitrary write) and
// upload path (arbitrary read handed to a page) bypassed the sandbox
// architecture entirely. Both faces must be gated; nil checkers fail open
// (matches readSandboxFor semantics without a policy).

func TestIssue5215_WriteFaceGatesScreenshotOutputPath(t *testing.T) {
	denyAll := func(string) bool { return false }
	b := NewBrowserWithSandbox(denyAll, nil)
	msg, blocked := b.checkScreenshotPath("/etc/hostconfig")
	if !blocked {
		t.Fatal("write face must block deny-all checker")
	}
	for _, pin := range []string{"workspace sandbox", "#5215/#712", "/etc/hostconfig"} {
		if !strings.Contains(msg, pin) {
			t.Errorf("block message missing %q: %s", pin, msg)
		}
	}
}

func TestIssue5215_ReadFaceGatesUploadPath(t *testing.T) {
	denyAll := func(string) bool { return false }
	b := NewBrowserWithSandbox(nil, denyAll)
	msg, blocked := b.checkUploadPath("/Users/x/.ssh/id_rsa")
	if !blocked {
		t.Fatal("read face must block deny-all checker")
	}
	for _, pin := range []string{"workspace sandbox", "#5215/#712", "/Users/x/.ssh/id_rsa"} {
		if !strings.Contains(msg, pin) {
			t.Errorf("block message missing %q: %s", pin, msg)
		}
	}
}

func TestIssue5215_AllowedPathsPassBothGates(t *testing.T) {
	allow := func(string) bool { return true }
	b := NewBrowserWithSandbox(allow, allow)
	if _, blocked := b.checkScreenshotPath(filepath.Join("out", "shot.png")); blocked {
		t.Error("allowed path must pass the write gate")
	}
	if _, blocked := b.checkUploadPath(filepath.Join("data", "f.csv")); blocked {
		t.Error("allowed path must pass the read gate")
	}
}

func TestIssue5215_NilCheckersFailOpen(t *testing.T) {
	b := NewBrowser() // no sandbox wiring: legacy behavior preserved
	if _, blocked := b.checkScreenshotPath("/anywhere/else"); blocked {
		t.Error("nil write checker must fail open")
	}
	if _, blocked := b.checkUploadPath("/anywhere/else"); blocked {
		t.Error("nil read checker must fail open")
	}
	if b.SandboxCheck != nil || b.ReadSandboxCheck != nil {
		t.Error("NewBrowser must leave checkers nil")
	}
}
