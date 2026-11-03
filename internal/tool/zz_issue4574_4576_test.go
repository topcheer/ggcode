//go:build goolm

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := os.Remove(target); err != nil {
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

	src, err := os.ReadFile("mobile_adb.go")
	if err != nil {
		t.Fatalf("mobile_adb.go must be readable from the package test dir: %v", err)
	}
	if !strings.Contains(string(src), `imageResult(data, "png", quality,`) {
		t.Fatal("Android screenshot must pin format to \"png\" (#4575): PNG-only capture must never be labeled image/jpeg")
	}
}

func TestIssue4576_SwipeMissingEndsRefused(t *testing.T) {
	a := &androidBackend{}

	res, err := a.swipe(context.Background(), "", "@e5", 0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("missing end_x/end_y must be refused, got: %s", res.Content)
	}

	res2, err := a.swipe(context.Background(), "", "", 10, 10, intPtr4576(0), intPtr4576(0))
	if err != nil {
		t.Fatal(err)
	}

	_ = res2
}

func intPtr4576(v int) *int { return &v }
