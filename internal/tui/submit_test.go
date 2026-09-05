package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/image"
)

func TestBuildAgentSubmissionContentUsesLocalImagePathHint(t *testing.T) {
	imgs := []imageAttachedMsg{{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}}

	content := buildAgentSubmissionContent("帮我看看", imgs, false)
	if len(content) != 1 {
		t.Fatalf("expected text-only fallback content, got %d blocks", len(content))
	}
	if !strings.Contains(content[0].Text, "/tmp/ggcode-image-deadbeef.png") {
		t.Fatalf("expected prompt to include local image path, got %q", content[0].Text)
	}
}

func TestBuildAgentSubmissionContentAddsImageBlockWhenEnabled(t *testing.T) {
	imgs := []imageAttachedMsg{{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}}

	content := buildAgentSubmissionContent("帮我看看", imgs, true)
	if len(content) != 2 {
		t.Fatalf("expected text + image content, got %d blocks", len(content))
	}
	if content[1].Type != "image" {
		t.Fatalf("expected second block to be image, got %q", content[1].Type)
	}
	if !strings.Contains(content[0].Text, "Prefer native vision understanding first") {
		t.Fatalf("expected prompt to prefer native vision, got %q", content[0].Text)
	}
}

func TestBuildAgentSubmissionContentMultipleImages(t *testing.T) {
	imgs := []imageAttachedMsg{
		{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 10, Height: 10}, filename: "a.png", sourcePath: "/tmp/a.png"},
		{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 20, Height: 20}, filename: "b.png", sourcePath: "/tmp/b.png"},
	}

	content := buildAgentSubmissionContent("compare these", imgs, true)
	// Should be: 1 text block + 2 image blocks
	if len(content) != 3 {
		t.Fatalf("expected 3 blocks (text + 2 images), got %d", len(content))
	}
	if content[1].Type != "image" || content[2].Type != "image" {
		t.Fatalf("expected blocks 2,3 to be image type, got %q %q", content[1].Type, content[2].Type)
	}
}

func TestActiveEndpointSupportsVisionUsesResolvedConfig(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o"
	m.config = cfg

	if !m.activeEndpointSupportsVision() {
		t.Fatal("expected active endpoint to report vision support")
	}
}

func TestPersistAttachedImageWritesStableFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	img := image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG}

	path, err := persistAttachedImage("ggcode-image-test.png", img)
	if err != nil {
		t.Fatalf("persistAttachedImage() error = %v", err)
	}
	if filepath.Base(path) != "ggcode-image-test.png" {
		t.Fatalf("unexpected persisted filename %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != string(img.Data) {
		t.Fatal("expected persisted image data to match source bytes")
	}
}

// TestRestorePendingImagesMsgHandler pins #1393-A: the ExpandMentions
// failure path sends captured attachments home via restorePendingImagesMsg;
// Update appends them back to pendingImages. RunID-gated: a stale message
// from an aborted run must NOT interleave into a newer run's state.
func TestRestorePendingImagesMsgHandler(t *testing.T) {
	m := newTestModel()
	m.activeAgentRunID = 7
	imgs := []imageAttachedMsg{{sourcePath: "shot.png"}}

	// Same-run message restores.
	m2, _ := m.Update(restorePendingImagesMsg{RunID: 7, Images: imgs})
	if got := m2.(Model).pendingImages; len(got) != 1 || got[0].sourcePath != "shot.png" {
		t.Fatalf("images not restored: %#v", got)
	}

	// Stale-run message is dropped.
	m3, _ := m.Update(restorePendingImagesMsg{RunID: 6, Images: imgs})
	if got := m3.(Model).pendingImages; len(got) != 0 {
		t.Fatalf("stale-run restore interleaved: %#v", got)
	}
}

// TestVisionFallbackRestoresModelWhenActivationFails pins the leak fix in
// runAgentSubmission's vision fallback: when SetActiveSelection succeeds but
// tryActivateCurrentSelection fails (e.g. missing API key), the user's model
// must be restored synchronously AND via defer before the text-only
// fallthrough runs. Pre-fix, the vision model leaked into config.Model
// permanently on this path.
func TestVisionFallbackRestoresModelWhenActivationFails(t *testing.T) {
	m := newTestModel()
	cfg := &config.Config{
		Vendor:   "testv",
		Endpoint: "testep",
		Model:    "text-model",
		Vendors: map[string]config.VendorConfig{
			"testv": {
				Endpoints: map[string]config.EndpointConfig{
					"testep": {
						Protocol: "openai",
						BaseURL:  "https://example.com",
						// APIKey intentionally empty: ResolveCurrentSelection
						// fails with "no api key configured" while
						// SetActiveSelection still succeeds — the exact
						// half-switched state the fix guards against.
						Models: []string{"text-model", "gpt-4o"},
					},
				},
			},
		},
	}
	m.config = cfg

	// Drive the real function chain (everything except the
	// runAgentSubmission shell): real selection → real switch → real
	// activation attempt → synchronous restore → idempotent deferred restore.
	userModel := cfg.Model
	vm := m.temporaryVisionModelForTurn() // real selection, not a hardcoded name
	if vm != "gpt-4o" {
		t.Fatalf("temporaryVisionModelForTurn() = %q, want gpt-4o", vm)
	}
	if err := cfg.SetActiveSelection(cfg.Vendor, cfg.Endpoint, vm); err != nil {
		t.Fatalf("SetActiveSelection() error = %v", err)
	}
	defer m.restoreUserModelAfterVisionTurn(userModel)
	if err := m.tryActivateCurrentSelection(); err == nil {
		t.Fatal("expected activation failure with empty API key")
	}
	m.restoreUserModelAfterVisionTurn(userModel) // failure-branch synchronous restore

	if cfg.Model != userModel {
		t.Fatalf("vision model leaked: config.Model = %q, want %q", cfg.Model, userModel)
	}
	// Second (deferred) restore must be a safe no-op.
	m.restoreUserModelAfterVisionTurn(userModel)
	if cfg.Model != userModel {
		t.Fatalf("idempotent restore mutated model: %q", cfg.Model)
	}
}
