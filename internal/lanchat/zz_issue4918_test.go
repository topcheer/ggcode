package lanchat

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalWorkspaceComparableSymlink: a symlink to a real dir must
// compare EQUAL to its target spelling.
func TestCanonicalWorkspaceComparableSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "wslink")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if canonicalWorkspaceComparable(link) != canonicalWorkspaceComparable(dir) {
		t.Fatalf("#4918: symlink spelling must compare equal: link=%q dir=%q",
			canonicalWorkspaceComparable(link), canonicalWorkspaceComparable(dir))
	}
}

// TestCanonicalWorkspaceComparableFallback: an unresolvable path must fall
// back to lexical Clean, not empty-string or panic.
func TestCanonicalWorkspaceComparableFallback(t *testing.T) {
	got := canonicalWorkspaceComparable("/definitely/not/here/../here")
	if got == "" {
		t.Fatal("#4918: fallback must return a lexical Clean path, got empty")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute fallback, got %q", got)
	}
}

// TestSharedWorkspacePeersSeesSymlinkPeer: end-to-end - a peer reporting via
// a symlink spelling must be detected for the real-path workspace.
func TestSharedWorkspacePeersSeesSymlinkPeer(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "wslink2")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	h2 := &Hub{peers: map[string]*Participant{
		"peer":    {NodeID: "peer", Online: true, Workspace: link},
		"other":   {NodeID: "other", Online: true, Workspace: t.TempDir()},
		"offline": {NodeID: "offline", Online: false, Workspace: link},
	}}
	peers := sharedWorkspacePeers(h2, "somebody-else", real)
	if len(peers) != 1 || peers[0].NodeID != "peer" {
		t.Fatalf("#4918: symlink-spelling peer not detected, got %d peers", len(peers))
	}

}
