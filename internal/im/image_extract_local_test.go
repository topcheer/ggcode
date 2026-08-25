package im

import "testing"

// Local-path extraction (4th source). Paths are emitted as Kind "url":
// every adapter's sendExtractedImage branches on IsLocalFilePath inside
// the "url" case, so local files route to upload with zero adapter changes.
func TestExtractImagesFromText_LocalPaths(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantPaths []string
	}{
		{"absolute unix path in prose", "截图已保存到 /tmp/shot_2026.png 请查收", []string{"/tmp/shot_2026.png"}},
		{"deep absolute path", "见 /Users/zhanju/work/out/diagram.jpeg", []string{"/Users/zhanju/work/out/diagram.jpeg"}},
		{"relative ./ path", "./out/chart.png generated", []string{"./out/chart.png"}},
		{"relative ../ path", "saved to ../reports/fig1.gif", []string{"../reports/fig1.gif"}},
		{"windows path", `C:\Users\zj\shots\screen.png done`, []string{`C:\Users\zj\shots\screen.png`}},
		{"trailing punctuation not captured", "output at /tmp/a.png.", []string{"/tmp/a.png"}},
		{"multiple paths", "/tmp/x.png and /tmp/y.webp", []string{"/tmp/x.png", "/tmp/y.webp"}},
		{"dedupe repeated path", "/tmp/x.png ... /tmp/x.png", []string{"/tmp/x.png"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			images, _ := ExtractImagesFromText(tc.text)
			if len(images) != len(tc.wantPaths) {
				t.Fatalf("got %d images (%v), want %d", len(images), images, len(tc.wantPaths))
			}
			for i, want := range tc.wantPaths {
				img := images[i]
				if img.Kind != "url" {
					t.Fatalf("image[%d] kind = %q, want %q (adapters route local paths via IsLocalFilePath)", i, img.Kind, "url")
				}
				if img.Data != want {
					t.Fatalf("image[%d] data = %q, want %q", i, img.Data, want)
				}
				if !IsLocalFilePath(img.Data) {
					t.Fatalf("image[%d] %q rejected by IsLocalFilePath", i, img.Data)
				}
			}
		})
	}
}

// False-positive guards: prose mentions that must NOT be extracted.
func TestExtractImagesFromText_LocalPathFalsePositives(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"bare filename without directory", "the file logo.png is missing"},
		{"extension mentioned as word", "convert to PNG then GIF"},
		{"url not double-extracted", "see https://example.com/cat.png now"},
		{"version-like token", "release 1.2.png notes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			images, _ := ExtractImagesFromText(tc.text)
			for _, img := range images {
				if IsLocalFilePath(img.Data) {
					t.Fatalf("false positive: %q extracted as local path", img.Data)
				}
			}
		})
	}
}
