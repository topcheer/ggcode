package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"
)

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNoteConfigSelfWrite_MatchesExactBytesOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	data := []byte("language: en\n")
	NoteConfigSelfWrite(path, data)

	if !MatchesRecentSelfWrite(path, sha256Hex(t, data)) {
		t.Fatal("mark should match the exact bytes just noted")
	}
	if MatchesRecentSelfWrite(path, sha256Hex(t, []byte("language: zh-CN\n"))) {
		t.Fatal("mark must not match different content (external edit)")
	}
	if MatchesRecentSelfWrite(filepath.Join(t.TempDir(), "other.yaml"), sha256Hex(t, data)) {
		t.Fatal("mark must not leak across paths")
	}
}

func TestNoteConfigSelfWrite_ExpiredMarkIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	data := []byte("language: en\n")
	sum := sha256.Sum256(data)
	selfWriteMarksMu.Lock()
	selfWriteMarks[path] = selfWriteMark{hash: hex.EncodeToString(sum[:]), at: time.Now().Add(-2 * selfWriteMarkTTL)}
	selfWriteMarksMu.Unlock()

	if MatchesRecentSelfWrite(path, hex.EncodeToString(sum[:])) {
		t.Fatal("expired mark must not match")
	}
}

func TestWriteSecureConfigFile_MarksSelfWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	data := []byte("language: en\n")

	if err := writeSecureConfigFile(path, data); err != nil {
		t.Fatalf("writeSecureConfigFile: %v", err)
	}
	// The choke point must attribute the write so the watcher can skip it.
	if !MatchesRecentSelfWrite(path, sha256Hex(t, data)) {
		t.Fatal("writeSecureConfigFile did not record a self-write mark")
	}

	// Simulating an external edit (different bytes, no fresh mark) must not
	// match: any byte difference means the file changed outside the session.
	ext := append([]byte(nil), data...)
	ext = append(ext, []byte("# trailing\n")...)
	if MatchesRecentSelfWrite(path, sha256Hex(t, ext)) {
		t.Fatal("mark must not match bytes the session never wrote")
	}
}
