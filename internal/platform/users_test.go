package platform

import (
	"path/filepath"
	"testing"
)

func testPassword() string { return "hunter2secure" }

func TestUserStoreAddAuthenticate(t *testing.T) {
	s, err := LoadUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("alice", testPassword(), true); err != nil {
		t.Fatalf("Add: %v", err)
	}
	u, ok := s.Authenticate("alice", testPassword())
	if !ok || !u.Admin {
		t.Fatalf("Authenticate(alice) = %v, %v; want ok+admin", u, ok)
	}
	if _, ok := s.Authenticate("alice", "wrong-password"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := s.Authenticate("mallory", testPassword()); ok {
		t.Fatal("unknown user accepted")
	}
}

func TestUserStoreRejectsShortPasswordAndDup(t *testing.T) {
	s, _ := LoadUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err := s.Add("a", "short", false); err == nil {
		t.Fatal("short password accepted")
	}
	if err := s.Add("", testPassword(), false); err == nil {
		t.Fatal("empty username accepted")
	}
	if err := s.Add("alice", testPassword(), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("alice", testPassword(), false); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestUserStoreRemoveAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	s, _ := LoadUserStore(path)
	_ = s.Add("bob", testPassword(), false)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// reload from disk
	s2, err := LoadUserStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("bob"); !ok {
		t.Fatal("persisted user not found after reload")
	}
	if err := s2.Remove("bob"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	s3, _ := LoadUserStore(path)
	if _, ok := s3.Get("bob"); ok {
		t.Fatal("removed user still present")
	}
	if err := s3.Remove("ghost"); err == nil {
		t.Fatal("removing unknown user should fail")
	}
}
