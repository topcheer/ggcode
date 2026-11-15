package memreclaim

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Stats sampling: default OFF; GGCODE_MEM_SAMPLE=1 writes one JSONL record
// with MemStats summary + variable-level top-sites.
func TestSamplingDefaultOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_MEM_SAMPLE_JSONL_DIR", dir)
	os.Unsetenv("GGCODE_MEM_SAMPLE")
	var ms runtime.MemStats
	writeStatsLine(&ms, false)
	if _, err := os.Stat(filepath.Join(dir, "memstats-test.jsonl")); !os.IsNotExist(err) {
		t.Fatal("stats record written despite sampling disabled")
	}
}

func TestSamplingEnabledWritesRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_MEM_SAMPLE_JSONL_DIR", dir)
	t.Setenv("GGCODE_MEM_SAMPLE", "1")
	var ms runtime.MemStats
	ms.HeapInuse = 100 << 20
	ms.HeapIdle = 700 << 20
	writeStatsLine(&ms, false)

	path := filepath.Join(dir, fmt.Sprintf("memstats-%d.jsonl", os.Getpid()))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no stats file: %v", err)
	}
	var rec statsRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("bad JSONL: %v\n%s", err, b)
	}
	if rec.HeapInuseB != 100<<20 || rec.HeapIdleB != 700<<20 {
		t.Fatalf("summary fields wrong: %+v", rec)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatal("record must be newline-terminated")
	}
}

// heapTopSites must aggregate real profile blocks into ranked sites with
// non-zero bytes and a symbolized site name.
func TestHeapTopSitesReal(t *testing.T) {
	// allocate distinctively so the site shows up
	payload := make([][]byte, 64)
	for i := range payload {
		payload[i] = make([]byte, 1<<20)
	}
	runtime.GC()
	sites := heapTopSites(10)
	if len(sites) == 0 {
		t.Fatal("no sites aggregated from live heap profile")
	}
	if len(sites) > 10 {
		t.Fatalf("top-N not honored: %d sites", len(sites))
	}
	var total uint64
	for _, s := range sites {
		if s.Bytes == 0 || s.Site == "" {
			t.Fatalf("bad site entry: %+v", s)
		}
		total += s.Bytes
	}
	if total == 0 {
		t.Fatal("aggregated zero bytes")
	}
	// descending order
	for i := 1; i < len(sites); i++ {
		if sites[i].Bytes > sites[i-1].Bytes {
			t.Fatalf("sites not sorted desc: %d > %d at %d", sites[i].Bytes, sites[i-1].Bytes, i)
		}
	}
	_ = payload // keep alive
}

func TestTopSitesLimitEnv(t *testing.T) {
	if got := topSitesLimit(); got != 10 {
		t.Fatalf("default top = %d, want 10", got)
	}
	t.Setenv("GGCODE_MEM_SAMPLE_TOP", "3")
	if got := topSitesLimit(); got != 3 {
		t.Fatalf("env top = %d, want 3", got)
	}
}

// Marker file: ~/.ggcode/mem-sample existing turns sampling on (fleet-wide
// switch, same pattern as ~/.ggcode/pprof-port). HOME is redirected to a
// temp dir so the real machine state cannot leak into the test.
func TestSamplingMarkerFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("GGCODE_MEM_SAMPLE")
	os.Unsetenv("GGCODE_MEM_SAMPLE_JSONL_DIR")

	if samplingEnabled() {
		t.Fatal("marker absent: sampling must be off")
	}
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, ".ggcode", "mem-sample")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !samplingEnabled() {
		t.Fatal("marker present: sampling must be on")
	}
	os.Remove(marker)
	if samplingEnabled() {
		t.Fatal("marker removed: sampling must be off again")
	}
}
