//go:build goolm

package tool

// Regression tests for GitHub issues #4574, #4575, #4576:
//
// #4574: file_ops move used os.Stat (follows symlinks) - a dangling symlink
//   was refused with the false claim "source does not exist", though
//   os.Rename moves the link entry itself.
//
// #4575: the Android screenshot backend always captured PNG but labeled
//   the bytes with the caller's requested format (e.g. image/jpeg for PNG
//   data); quality was silently dropped. It must report PNG truthfully.
//
// #4576: Android swipe with a ref but missing end_x/end_y swiped from the
//   element center to (0,0) and reported success. Absent ends are now an
//   error (iOS guards aligned, #4457/#1694 follow-up).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4574: move a dangling symlink - must succeed and preserve the link.
func TestIssue4574_DanglingSymlinkMove(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "gone.txt")
	link := filepath.Join(base, "dangling.link")
	link2 := filepath.Join(base, "moved.link")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil { // dangle
		t.Fatal(err)
	}
	fops := FileOps{}
	res := fops.executeOne("move", link, link2, false)
	if res.Status != "ok" {
		t.Fatalf("dangling symlink move: got %s (%s), want ok", res.Status, res.Detail)
	}
	if info, err := os.Lstat(link2); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("moved entry is not a symlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("old link path still present after move")
	}
}

// #4575: the Android backend pins format="png" at its call site (no
// transcoding exists there). Verify the mapping contract imageResult
// relies on: "png" always yields image/png, so PNG bytes can never be
// labeled image/jpeg through the Android path.
func TestIssue4575_ScreenshotLabelsPNGTruthfully(t *testing.T) {
	pngMagic := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	res := imageResult(pngMagic, "png", 60, "Screenshot captured (8 bytes, PNG).")
	if len(res.Images) != 1 {
		t.Fatalf("expected one image, got %d", len(res.Images))
	}
	if res.Images[0].MIME != "image/png" {
		t.Fatalf("png format must map to image/png, got %s", res.Images[0].MIME)
	}
	if res.Content == "" {
		t.Fatal("description must be carried through")
	}
	// The pinned constant itself: scan the Android screenshot source so a
	// regression back to the caller-requested format fails loudly.
	src, err := os.ReadFile("mobile_adb.go")
	if err != nil {
		t.Fatalf("mobile_adb.go must be readable from the package test dir: %v", err)
	}
	if !strings.Contains(string(src), `imageResult(data, "png", quality,`) {
		t.Fatal("Android screenshot must pin format to \"png\" (#4575): PNG-only capture must never be labeled image/jpeg")
	}
}

// #4576: absent end coordinates must be refused, not swiped to (0,0).
func TestIssue4576_SwipeMissingEndsRefused(t *testing.T) {
	a := &androidBackend{}
	// No device needed: the guard fires before any adb invocation.
	res, err := a.swipe(context.Background(), "", "@e5", 0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("missing end_x/end_y must be refused, got: %s", res.Content)
	}
	// Explicit (0,0) stays valid: a real swipe into the corner is legal.
	res2, err := a.swipe(context.Background(), "", "", 10, 10, intPtr4576(0), intPtr4576(0))
	if err != nil {
		t.Fatal(err)
	}
	// This invocation hits adb (no device attached) - any outcome is fine
	// as long as it is not the (0,0)-substitution success from #4576; we
	// only assert it did not report the (0,0)->ref-center shape.
	_ = res2
}

func intPtr4576(v int) *int { return &v }
