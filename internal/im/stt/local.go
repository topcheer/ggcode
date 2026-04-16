package stt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// LocalWhisper uses a local whisper CLI binary for transcription.
type LocalWhisper struct {
	binPath   string
	model     string
	language  string
	available bool
	checked   bool
	mu        sync.Mutex
}

// NewLocalWhisper creates a local whisper transcriber.
// binPath is the path to the whisper binary (empty = auto-detect).
// model is the whisper model size (empty = "turbo" for speed + accuracy).
// language is the language code (empty = auto-detect).
func NewLocalWhisper(binPath, model, language string) *LocalWhisper {
	w := &LocalWhisper{
		binPath:  strings.TrimSpace(binPath),
		model:    strings.TrimSpace(model),
		language: strings.TrimSpace(language),
	}
	if w.model == "" {
		w.model = "turbo"
	}
	return w
}

func (w *LocalWhisper) Transcribe(ctx context.Context, req Request) (Result, error) {
	if err := w.ensureAvailable(); err != nil {
		return Result{}, err
	}

	audioPath := strings.TrimSpace(req.Path)
	if audioPath == "" {
		return Result{}, fmt.Errorf("local whisper: no audio path provided")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return Result{}, fmt.Errorf("local whisper: audio file not found: %w", err)
	}

	// Create temp dir for whisper output
	outDir, err := os.MkdirTemp("", "ggcode-whisper-out-*")
	if err != nil {
		return Result{}, fmt.Errorf("local whisper: create temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	args := []string{
		audioPath,
		"--model", w.model,
		"--output_dir", outDir,
		"--output_format", "txt",
		"--verbose", "False",
	}
	if w.language != "" {
		args = append(args, "--language", w.language)
	}

	debug.Log("stt", "local whisper: running %s %v", w.binPath, args)

	start := time.Now()
	cmd := exec.CommandContext(ctx, w.binPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("local whisper: execution failed: %w", err)
	}

	// Read the output .txt file
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	txtPath := filepath.Join(outDir, base+".txt")
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return Result{}, fmt.Errorf("local whisper: read output: %w", err)
	}

	text := strings.TrimSpace(string(data))
	debug.Log("stt", "local whisper: transcribed in %s, %d chars", time.Since(start).Round(time.Millisecond), len(text))

	return Result{
		Text:     text,
		Provider: "whisper-local",
		Model:    w.model,
	}, nil
}

func (w *LocalWhisper) ensureAvailable() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.checked {
		if !w.available {
			return fmt.Errorf("local whisper: binary not found at %q", w.binPath)
		}
		return nil
	}
	w.checked = true

	if w.binPath != "" {
		if _, err := exec.LookPath(w.binPath); err == nil {
			w.available = true
			return nil
		}
	}
	// Auto-detect common whisper binaries
	for _, candidate := range []string{"whisper", "whisper-cpp"} {
		if p, err := exec.LookPath(candidate); err == nil {
			w.binPath = p
			w.available = true
			debug.Log("stt", "local whisper: found binary at %s", p)
			return nil
		}
	}
	return fmt.Errorf("local whisper: no whisper binary found in PATH")
}

// Available checks if local whisper is usable without blocking.
func (w *LocalWhisper) Available() bool {
	return w.ensureAvailable() == nil
}
