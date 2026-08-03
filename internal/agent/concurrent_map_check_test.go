package agent

import (
	"testing"
)

func TestCheckConcurrentMapAccess_BasicDetection(t *testing.T) {
	src := `package main

func worker(m map[string]int) {
	go func() {
		m["key"] = 42
	}()
	m["other"] = 1
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result == "" {
		t.Fatal("expected concurrent map access warning")
	}
	if !contains(result, "m") {
		t.Errorf("warning should mention map variable 'm': %s", result)
	}
}

func TestCheckConcurrentMapAccess_StructField(t *testing.T) {
	src := `package main

type Cache struct {
	items map[string]int
}

func (c *Cache) run() {
	go func() {
		c.items["x"] = 1
	}()
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result == "" {
		t.Fatal("expected concurrent map access warning for struct field")
	}
	if !contains(result, "c.items") {
		t.Errorf("warning should mention 'c.items': %s", result)
	}
}

func TestCheckConcurrentMapAccess_SyncMutexOK(t *testing.T) {
	src := `package main

import "sync"

type Safe struct {
	mu    sync.RWMutex
	items map[string]int
}

func (s *Safe) run() {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.items["x"] = 1
	}()
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning when sync.RWMutex is used, got: %s", result)
	}
}

func TestCheckConcurrentMapAccess_SyncMapOK(t *testing.T) {
	src := `package main

import "sync"

func worker(m *sync.Map) {
	go func() {
		m.Store("key", 42)
	}()
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for sync.Map, got: %s", result)
	}
}

func TestCheckConcurrentMapAccess_NoGoOK(t *testing.T) {
	src := `package main

func worker(m map[string]int) {
	m["key"] = 42
	m["other"] = 1
	delete(m, "old")
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning when no goroutines are spawned, got: %s", result)
	}
}

func TestCheckConcurrentMapAccess_DeltaAware(t *testing.T) {
	old := `package main

func worker(m map[string]int) {
	go func() {
		m["key"] = 42
	}()
}
`
	newSrc := `package main

func worker(m map[string]int) {
	go func() {
		m["key"] = 42
	}()
}

func worker2(m map[string]int) {
	go func() {
		m["other"] = 99
	}()
}
`
	result := checkConcurrentMapAccess("test.go", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for newly added concurrent map access")
	}
}

func TestCheckConcurrentMapAccess_DeleteOperation(t *testing.T) {
	src := `package main

func worker(m map[string]int) {
	go func() {
		delete(m, "key")
	}()
}
`
	result := checkConcurrentMapAccess("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for delete in goroutine context")
	}
}

func TestCheckConcurrentMapAccess_NonGoFile(t *testing.T) {
	result := checkConcurrentMapAccess("test.py", "", "some python code")
	if result != "" {
		t.Errorf("expected no warning for non-Go file")
	}
}

func TestCheckConcurrentMapAccess_EmptyContent(t *testing.T) {
	result := checkConcurrentMapAccess("test.go", "", "")
	if result != "" {
		t.Errorf("expected no warning for empty content")
	}
}
