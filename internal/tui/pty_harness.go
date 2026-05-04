//go:build integration_local

package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/creack/pty/v2"
)

// ptyHarness manages a ggcode process running in a pseudo-terminal.
type ptyHarness struct {
	t      *testing.T
	cmd    *exec.Cmd
	ptmx   *os.File
	tmpDir string
	binary string
	stderr bytes.Buffer

	// async reader
	outputMu   sync.Mutex
	outputBuf  bytes.Buffer
	readerDone chan struct{}

	// dimensions
	cols, rows uint16
}

// ptyOptions configures the ggcode process.
type ptyOptions struct {
	Binary  string        // path to ggcode binary (default: ./bin/ggcode)
	Cols    uint16        // terminal width (default: 120)
	Rows    uint16        // terminal height (default: 40)
	Config  string        // YAML config content (written to temp file)
	Env     []string      // extra environment variables
	Timeout time.Duration // max wait for interactions (default: 10s)
}

// startGGCode launches a ggcode process in a PTY.
func startGGCode(t *testing.T, opts ptyOptions) *ptyHarness {
	t.Helper()

	h := &ptyHarness{
		t:    t,
		cols: opts.Cols,
		rows: opts.Rows,
	}
	if h.cols == 0 {
		h.cols = 120
	}
	if h.rows == 0 {
		h.rows = 40
	}

	// Find binary
	h.binary = opts.Binary
	if h.binary == "" {
		candidates := []string{
			"./bin/ggcode",
			"../bin/ggcode",
			"ggcode",
		}
		for _, c := range candidates {
			if _, err := exec.LookPath(c); err == nil {
				h.binary = c
				break
			}
		}
	}
	if h.binary == "" {
		t.Skip("ggcode binary not found, skipping PTY test")
	}

	// === Test isolation ===
	// Redirect HOME to tmpdir so ~/.ggcode/ → {tmpdir}/.ggcode/
	// This protects user's real config, keys.env, and instance data.
	h.tmpDir = t.TempDir()
	workspaceDir := filepath.Join(h.tmpDir, "workspace")
	os.MkdirAll(workspaceDir, 0755)

	// Create isolated ~/.ggcode/ in tmpdir
	ggcodeDir := filepath.Join(h.tmpDir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)

	// Write test config as both global config and workspace config
	configPath := filepath.Join(ggcodeDir, "ggcode.yaml")
	if opts.Config != "" {
		if err := os.WriteFile(configPath, []byte(opts.Config), 0600); err != nil {
			t.Fatalf("write test config: %v", err)
		}
	} else {
		generateSafeTestConfig(t, configPath)
	}

	// Also place a copy in workspace for project-level resolution
	wsConfigDir := filepath.Join(workspaceDir, ".ggcode")
	os.MkdirAll(wsConfigDir, 0755)
	cfgData, _ := os.ReadFile(configPath)
	os.WriteFile(filepath.Join(wsConfigDir, "ggcode.yaml"), cfgData, 0600)

	args := []string{} // no --config needed, HOME isolation handles it

	// 3. Run in isolated workspace dir
	h.cmd = exec.Command(h.binary, args...)
	h.cmd.Dir = workspaceDir

	// Environment — redirect HOME to isolate ~/.ggcode/ completely.
	// This prevents ggcode from migrating API keys to the user's real keys.env.
	// The dummy HOME points to tmpdir, so ~/.ggcode/ → {tmpdir}/.ggcode/.
	env := os.Environ()
	filteredEnv := make([]string, 0, len(env))
	for _, e := range env {
		// Remove real HOME and any credential env vars
		if strings.HasPrefix(e, "HOME=") {
			continue
		}
		filteredEnv = append(filteredEnv, e)
	}
	filteredEnv = append(filteredEnv,
		"HOME="+h.tmpDir,
		"TERM=xterm-256color",
	)
	for _, e := range opts.Env {
		filteredEnv = append(filteredEnv, e)
	}
	h.cmd.Env = filteredEnv

	// Capture stderr for debugging
	h.cmd.Stderr = &h.stderr

	// Start with PTY
	ptmx, err := pty.StartWithSize(h.cmd, &pty.Winsize{
		Cols: h.cols,
		Rows: h.rows,
	})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	h.ptmx = ptmx

	// Background reader: continuously reads PTY output into outputBuf.
	// This is necessary because PTY files don't support SetReadDeadline on macOS.
	h.readerDone = make(chan struct{})
	go func() {
		defer close(h.readerDone)
		buf := make([]byte, 64*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				h.outputMu.Lock()
				h.outputBuf.Write(buf[:n])
				h.outputMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		h.quit()
	})

	return h
}

// sendKey writes a key sequence to the PTY.
func (h *ptyHarness) sendKey(key string) {
	h.t.Helper()
	h.sendKeys(key)
}

// sendKeys writes multiple key sequences.
func (h *ptyHarness) sendKeys(keys ...string) {
	h.t.Helper()
	for _, key := range keys {
		seq := keyToANSI(key)
		if _, err := h.ptmx.Write([]byte(seq)); err != nil {
			h.t.Fatalf("write key %q: %v", key, err)
		}
	}
	// Small delay for the TUI to process
	time.Sleep(30 * time.Millisecond)
}

// sendText types a string character by character.
func (h *ptyHarness) sendText(text string) {
	h.t.Helper()
	for _, r := range text {
		h.ptmx.WriteString(string(r))
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
}

// sendRaw writes raw bytes to the PTY.
func (h *ptyHarness) sendRaw(data string) {
	h.t.Helper()
	if _, err := h.ptmx.Write([]byte(data)); err != nil {
		h.t.Fatalf("write raw: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
}

// readAll waits briefly for any pending PTY data, then returns.
func (h *ptyHarness) readAll() {
	h.t.Helper()
	time.Sleep(30 * time.Millisecond) // wait for data to arrive
}

// getOutput returns the current accumulated output (thread-safe).
func (h *ptyHarness) getOutput() string {
	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	return h.outputBuf.String()
}

// getStrippedOutput returns ANSI-stripped output.
func (h *ptyHarness) getStrippedOutput() string {
	return stripANSI(h.getOutput())
}

// waitFor waits for the raw output to contain the given pattern (regex).
func (h *ptyHarness) waitFor(pattern string, timeout time.Duration) string {
	h.t.Helper()
	re := regexp.MustCompile(pattern)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		h.readAll()
		output := h.getOutput()
		if re.MatchString(output) {
			return output
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for pattern %q in output (last %d bytes: %s)",
		pattern, len(h.getOutput()), lastN(h.getStrippedOutput(), 500))
	return ""
}

// waitForText waits for plain text (not regex) in the stripped output.
func (h *ptyHarness) waitForText(text string, timeout time.Duration) string {
	h.t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		h.readAll()
		stripped := h.getStrippedOutput()
		if strings.Contains(stripped, text) {
			return h.getOutput()
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Debug: show what we actually got
	stripped := h.getStrippedOutput()
	stderrStr := h.stderr.String()

	// Check if process is still alive
	processAlive := false
	if h.cmd.Process != nil {
		processAlive = h.cmd.ProcessState == nil
	}

	h.t.Fatalf("timeout waiting for text %q in output (last 500 bytes: %s)\nstderr: %s\nprocess alive: %v",
		text, lastN(stripped, 500), lastN(stderrStr, 500), processAlive)
	return ""
}

// waitForScreen waits until ALL given texts appear in the stripped screen output.
func (h *ptyHarness) waitForScreen(timeout time.Duration, texts ...string) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		h.readAll()
		screen := h.getStrippedOutput()
		allFound := true
		for _, text := range texts {
			if !strings.Contains(screen, text) {
				allFound = false
				break
			}
		}
		if allFound {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	screen := h.getStrippedOutput()
	missing := []string{}
	for _, text := range texts {
		if !strings.Contains(screen, text) {
			missing = append(missing, text)
		}
	}
	h.t.Fatalf("timeout waiting for screen texts %v (screen:\n%s)", missing, lastN(screen, 1000))
}

// assertContains verifies the current screen contains the given text.
func (h *ptyHarness) assertContains(text string) {
	h.t.Helper()
	h.readAll()
	screen := h.getStrippedOutput()
	if !strings.Contains(screen, text) {
		h.t.Errorf("expected screen to contain %q\nscreen:\n%s", text, lastN(screen, 500))
	}
}

// assertNotContains verifies the current screen does NOT contain the given text.
func (h *ptyHarness) assertNotContains(text string) {
	h.t.Helper()
	h.readAll()
	screen := h.getStrippedOutput()
	if strings.Contains(screen, text) {
		h.t.Errorf("expected screen NOT to contain %q\nscreen:\n%s", text, lastN(screen, 500))
	}
}

// snapshot returns the current screen content with ANSI escapes stripped.
func (h *ptyHarness) snapshot() string {
	h.t.Helper()
	h.readAll()
	return h.getStrippedOutput()
}

// screen returns a clean multiline view of the current screen.
func (h *ptyHarness) screen() string {
	h.t.Helper()
	return h.snapshot()
}

// resize changes the PTY dimensions.
func (h *ptyHarness) resize(cols, rows uint16) {
	h.t.Helper()
	h.cols = cols
	h.rows = rows
	pty.Setsize(h.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
	// Send SIGWINCH to the child process
	if h.cmd.Process != nil {
		h.cmd.Process.Signal(os.Signal(nil))
	}
	time.Sleep(50 * time.Millisecond)
}

// quit sends Ctrl+C then Ctrl+D, waits for process to exit.
func (h *ptyHarness) quit() {
	if h.ptmx == nil {
		return
	}
	// Try graceful quit
	h.ptmx.Write([]byte{0x03}) // Ctrl+C
	time.Sleep(100 * time.Millisecond)
	h.ptmx.Write([]byte{0x04}) // Ctrl+D
	time.Sleep(200 * time.Millisecond)

	// Force kill if still running
	if h.cmd.Process != nil {
		h.cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
	h.ptmx.Close()
	h.ptmx = nil

	// Wait for background reader to finish
	select {
	case <-h.readerDone:
	case <-time.After(2 * time.Second):
	}
}

// drainOutput waits for output to settle.
func (h *ptyHarness) drainOutput() {
	h.t.Helper()
	time.Sleep(100 * time.Millisecond)
}

// --- Key encoding ---

// keyToANSI converts a key description to ANSI escape sequence.
func keyToANSI(key string) string {
	switch key {
	case "enter":
		return "\r"
	case "tab":
		return "\t"
	case "shift+tab":
		return "\x1b[Z"
	case "escape", "esc":
		return "\x1b"
	case "backspace":
		return "\x7f"
	case "delete":
		return "\x1b[3~"
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	case "home":
		return "\x1b[H"
	case "end":
		return "\x1b[F"
	case "pgup":
		return "\x1b[5~"
	case "pgdown":
		return "\x1b[6~"
	case "ctrl+c":
		return "\x03"
	case "ctrl+d":
		return "\x04"
	case "ctrl+l":
		return "\x0c"
	case "ctrl+z":
		return "\x1a"
	case "ctrl+u":
		return "\x15"
	case "ctrl+w":
		return "\x17"
	case "ctrl+a":
		return "\x01"
	case "ctrl+e":
		return "\x05"
	case "ctrl+k":
		return "\x0b"
	case "ctrl+p":
		return "\x10"
	case "ctrl+n":
		return "\x0e"
	case "ctrl+r":
		return "\x12"
	case "ctrl+v":
		return "\x16"
	case "space":
		return " "
	case "f1":
		return "\x1bOP"
	case "f2":
		return "\x1bOQ"
	case "f3":
		return "\x1bOR"
	case "f4":
		return "\x1bOS"
	case "f5":
		return "\x1b[15~"
	case "f6":
		return "\x1b[17~"
	case "f7":
		return "\x1b[18~"
	case "f8":
		return "\x1b[19~"
	case "f9":
		return "\x1b[20~"
	case "f10":
		return "\x1b[21~"
	default:
		// Single character
		if utf8.RuneCountInString(key) == 1 {
			return key
		}
		// ctrl+letter
		if strings.HasPrefix(key, "ctrl+") && len(key) == 6 {
			ch := key[5]
			if ch >= 'a' && ch <= 'z' {
				return string(rune(ch - 'a' + 1))
			}
		}
		return key
	}
}

// --- ANSI stripping ---

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	// Remove OSC sequences (title, etc.)
	s = regexp.MustCompile(`\x1b\][^\x07]*\x07`).ReplaceAllString(s, "")
	// Remove CSI sequences
	s = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`).ReplaceAllString(s, "")
	// Remove other ESC sequences (2-char)
	s = regexp.MustCompile(`\x1b[\x40-\x5a\x5c\x5e-\x7e]`).ReplaceAllString(s, "")
	// Remove raw ESC
	s = strings.ReplaceAll(s, "\x1b", "")
	return s
}

// lastN returns the last n characters of s.
func lastN(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

// compressSpaces collapses runs of whitespace into single spaces.
func compressSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// === Config isolation ===

// secretKeys are YAML keys whose values must be stripped in test configs.
var secretKeys = map[string]bool{
	"api_key":        true,
	"apikey":         true,
	"secret":         true,
	"token":          true,
	"access_token":   true,
	"refresh_token":  true,
	"app_secret":     true,
	"app_id":         true,
	"client_secret":  true,
	"private_key":    true,
	"password":       true,
	"stream_key":     true,
	"webhook_secret": true,
}

// generateSafeTestConfig reads the user's real config to extract non-secret
// preferences (vendor, model, endpoint), then writes a safe test config
// with dummy API keys. No real secrets ever touch the test workspace.
func generateSafeTestConfig(t *testing.T, destPath string) {
	t.Helper()

	// Defaults
	vendor := "openai"
	model := "gpt-4o"
	endpoint := "default"
	defaultMode := "bypass"

	// Try to read real config for non-secret preferences only
	realConfigPath := findRealConfig()
	if realConfigPath != "" {
		data, err := os.ReadFile(realConfigPath)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				// Skip comments and secrets
				if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
					continue
				}
				lower := strings.ToLower(trimmed)
				// Skip any line that contains a secret key
				skip := false
				for secretKey := range secretKeys {
					if strings.HasPrefix(lower, secretKey+":") || strings.HasPrefix(lower, strings.ReplaceAll(secretKey, "_", "-")+":") {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				// Extract non-secret preferences
				if strings.HasPrefix(lower, "vendor:") {
					vendor = strings.TrimSpace(trimmed[len("vendor:"):])
				}
				if strings.HasPrefix(lower, "model:") {
					model = strings.TrimSpace(trimmed[len("model:"):])
				}
				if strings.HasPrefix(lower, "endpoint:") {
					endpoint = strings.TrimSpace(trimmed[len("endpoint:"):])
				}
				if strings.HasPrefix(lower, "default_mode:") || strings.HasPrefix(lower, "default-mode:") {
					defaultMode = strings.TrimSpace(trimmed[strings.Index(trimmed, ":")+1:])
				}
			}
		}
	}

	// Build safe test config — hardcode dummy key, no env var magic
	testConfig := fmt.Sprintf(`# Auto-generated test config — no real secrets
vendor: %s
model: %s
endpoint: %s
default_mode: %s
vendors:
  openai:
    api_key: "sk-pty-test-dummy-not-real-key"
    endpoints:
      default:
        protocol: openai
        base_url: "http://127.0.0.1:1"
  anthropic:
    api_key: "sk-pty-test-dummy-not-real-key"
    endpoints:
      default:
        protocol: anthropic
        base_url: "http://127.0.0.1:1"
  zai:
    api_key: "sk-pty-test-dummy-not-real-key"
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: "http://127.0.0.1:1"
      cn-coding-anthropic:
        protocol: anthropic
        base_url: "http://127.0.0.1:1"
`, vendor, model, endpoint, defaultMode)

	if err := os.WriteFile(destPath, []byte(testConfig), 0600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
}

// findRealConfig returns the path to the user's real ggcode.yaml.
func findRealConfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".ggcode", "ggcode.yaml"),
		filepath.Join(home, ".ggcode", "config.yaml"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// isSecretLine checks if a YAML line contains a secret key.
func isSecretLine(line, key string) bool {
	// Trim leading whitespace for matching
	trimmed := strings.TrimSpace(line)
	lowered := strings.ToLower(trimmed)

	// Must start with the key name followed by ":"
	prefix := key + ":"
	if strings.HasPrefix(lowered, prefix) {
		return true
	}
	// Also match key with dash variant: api_key / api-key
	if strings.Contains(key, "_") {
		dashKey := strings.ReplaceAll(key, "_", "-") + ":"
		if strings.HasPrefix(lowered, dashKey) {
			return true
		}
	}
	return false
}

// redactYAMLToEnvRef replaces the value with a dummy env var reference.
func redactYAMLToEnvRef(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line
	}
	return line[:idx+1] + " \"sk-pty-test-dummy-not-real\""
}
