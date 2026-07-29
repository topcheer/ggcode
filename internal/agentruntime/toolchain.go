package agentruntime

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// toolchainTimeout is the per-command timeout for version probes. It is short
// because these are local commands that should complete near-instantly; a slow
// or hung process must not block prompt construction.
const toolchainTimeout = 3 * time.Second

// toolchainCache stores results of version probes so we shell out at most once
// per binary per process lifetime. The cache is keyed by the binary name.
var (
	toolchainCache   = make(map[string]string)
	toolchainCacheMu sync.RWMutex
)

// toolchainProbe describes a version command to run and the project markers
// that must be present for it to be relevant.
type toolchainProbe struct {
	binary  string   // command to execute
	args    []string // arguments (typically ["--version"] or ["version"])
	markers []string // project file markers; at least one must exist
	label   string   // human-friendly label for the output line
}

// toolchainProbes lists the toolchains ggcode can detect. Only probes whose
// markers exist in the working directory are executed, keeping the system prompt
// compact and avoiding useless shell-outs.
var toolchainProbes = []toolchainProbe{
	{
		binary:  "go",
		args:    []string{"version"},
		markers: []string{"go.mod", "go.work"},
		label:   "Go",
	},
	{
		binary:  "node",
		args:    []string{"--version"},
		markers: []string{"package.json"},
		label:   "Node",
	},
	{
		binary:  "npm",
		args:    []string{"--version"},
		markers: []string{"package.json"},
		label:   "npm",
	},
	{
		binary:  "python3",
		args:    []string{"--version"},
		markers: []string{"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"},
		label:   "Python",
	},
	{
		binary:  "cargo",
		args:    []string{"--version"},
		markers: []string{"Cargo.toml"},
		label:   "Rust/Cargo",
	},
	{
		binary:  "java",
		args:    []string{"-version"},
		markers: []string{"pom.xml", "build.gradle", "build.gradle.kts"},
		label:   "Java",
	},
}

// runVersionCmd executes a version command with a short timeout. It returns the
// trimmed stdout/stderr output, or "" if the command fails or is not found.
// Results are cached per binary for the process lifetime.
func runVersionCmd(binary string, args []string) string {
	toolchainCacheMu.RLock()
	if v, ok := toolchainCache[binary]; ok {
		toolchainCacheMu.RUnlock()
		return v
	}
	toolchainCacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), toolchainTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	// java -version writes to stderr; capture both.
	out, err := cmd.CombinedOutput()
	cancel() // release timer immediately

	result := ""
	if err == nil && len(out) > 0 {
		result = strings.TrimSpace(string(out))
	}

	toolchainCacheMu.Lock()
	toolchainCache[binary] = result
	toolchainCacheMu.Unlock()
	return result
}

// resetToolchainCache clears the cached version results. Used by tests to
// ensure deterministic behaviour.
func resetToolchainCache() {
	toolchainCacheMu.Lock()
	toolchainCache = make(map[string]string)
	toolchainCacheMu.Unlock()
}

// detectToolchain probes installed toolchain versions for the languages used
// in the given working directory. It returns a compact, single-line-per-tool
// summary suitable for the system prompt.
//
// Only toolchains relevant to the project (based on marker files) are probed,
// so a pure Go project won't shell out to `python3 --version`. Each probe is
// cached for the process lifetime, so repeated prompt constructions don't
// re-run version commands.
func detectToolchain(workingDir string) []string {
	if workingDir == "" {
		return nil
	}

	var results []string
	for _, probe := range toolchainProbes {
		if !toolchainHasMarker(workingDir, probe.markers) {
			continue
		}
		ver := runVersionCmd(probe.binary, probe.args)
		if ver == "" {
			continue
		}
		// Keep only the first line of multi-line output (e.g. java -version
		// prints three lines; go version prints one line with platform info).
		if idx := strings.IndexByte(ver, '\n'); idx >= 0 {
			ver = ver[:idx]
		}
		results = append(results, probe.label+": "+ver)
	}
	return results
}

// toolchainHasMarker returns true if any of the marker files exist in the
// working directory.
func toolchainHasMarker(workingDir string, markers []string) bool {
	for _, m := range markers {
		if _, err := os.Stat(workingDir + string(os.PathSeparator) + m); err == nil {
			return true
		}
	}
	return false
}

// toolchainSection formats detected toolchain versions as a compact system
// prompt section. Returns an empty string if no toolchains were detected.
func toolchainSection(workingDir string) string {
	versions := detectToolchain(workingDir)
	if len(versions) == 0 {
		return ""
	}
	return "\n\n## Toolchain\nInstalled toolchain versions (for writing compatible code):\n- " +
		strings.Join(versions, "\n- ")
}
