package agent

import (
	"strings"
	"testing"
)

func TestCheckReceiverConsistency_NonGoFile(t *testing.T) {
	result := checkReceiverConsistency("test.py", "", "print('hello')")
	if len(result) != 0 {
		t.Errorf("expected no warnings for non-Go file, got %v", result)
	}
}

func TestCheckReceiverConsistency_EmptyContent(t *testing.T) {
	result := checkReceiverConsistency("test.go", "", "")
	if len(result) != 0 {
		t.Errorf("expected no warnings for empty content, got %v", result)
	}
}

func TestCheckReceiverConsistency_ConsistentReceivers(t *testing.T) {
	src := `package main

type Server struct{ addr string }

func (s *Server) Start() {}
func (s *Server) Stop()  {}
func (s *Server) Addr()  string { return s.addr }
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) != 0 {
		t.Errorf("expected no warnings for consistent receivers, got: %v", result)
	}
}

func TestCheckReceiverConsistency_InconsistentReceivers(t *testing.T) {
	// New file: methods on Server use "s", "srv", and "this".
	src := `package main

type Server struct{ addr string }

func (s *Server) Start() {}
func (srv *Server) Stop() {}
func (this *Server) Addr() string { return this.addr }
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for inconsistent receiver names, got none")
	}
	combined := strings.Join(result, " ")
	if !strings.Contains(combined, "Server") {
		t.Errorf("expected warning to mention type 'Server', got: %s", combined)
	}
	if !strings.Contains(combined, "s") || !strings.Contains(combined, "srv") {
		t.Errorf("expected warning to mention receiver names 's' and 'srv', got: %s", combined)
	}
	// "this" should be flagged as anti-pattern.
	if !strings.Contains(combined, "anti-pattern") {
		t.Errorf("expected anti-pattern mention for 'this', got: %s", combined)
	}
}

func TestCheckReceiverConsistency_DeltaAware_PreExistingInconsistency(t *testing.T) {
	// Old content already has inconsistency: (s *Foo) and (f *Foo).
	oldSrc := `package main
type Foo struct{}
func (s *Foo) A() {}
func (f *Foo) B() {}
`
	// New content has the same inconsistency -- should NOT warn (pre-existing).
	newSrc := `package main
type Foo struct{}
func (s *Foo) A() {}
func (f *Foo) B() {}
func (s *Foo) C() {}
`
	result := checkReceiverConsistency("test.go", oldSrc, newSrc)
	if len(result) != 0 {
		t.Errorf("expected no warnings for pre-existing inconsistency, got: %v", result)
	}
}

func TestCheckReceiverConsistency_DeltaAware_NewInconsistency(t *testing.T) {
	// Old content is consistent.
	oldSrc := `package main
type Foo struct{}
func (s *Foo) A() {}
func (s *Foo) B() {}
`
	// New content introduces inconsistency: method C uses "f" instead of "s".
	newSrc := `package main
type Foo struct{}
func (s *Foo) A() {}
func (s *Foo) B() {}
func (f *Foo) C() {}
`
	result := checkReceiverConsistency("test.go", oldSrc, newSrc)
	if len(result) == 0 {
		t.Fatal("expected warning for newly introduced inconsistency, got none")
	}
	combined := strings.Join(result, " ")
	if !strings.Contains(combined, "Foo") {
		t.Errorf("expected warning to mention type 'Foo', got: %s", combined)
	}
}

func TestCheckReceiverConsistency_MultipleTypes(t *testing.T) {
	src := `package main

type Bar struct{}
func (b *Bar) X() {}

type Baz struct{}
func (bz *Baz) Y() {}
func (z *Baz) Z() {}
`
	result := checkReceiverConsistency("test.go", "", src)
	// Only Baz is inconsistent; Bar is fine.
	if len(result) != 1 {
		t.Fatalf("expected exactly 1 warning (for Baz), got %d: %v", len(result), result)
	}
	if !strings.Contains(result[0], "Baz") {
		t.Errorf("expected warning for 'Baz', got: %s", result[0])
	}
}

func TestCheckReceiverConsistency_ValueReceiver(t *testing.T) {
	src := `package main

type Config struct{ port int }

func (c Config) GetPort() int { return c.port }
func (cfg Config) SetPort(p int) { cfg.port = p }
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for inconsistent value receivers, got none")
	}
	if !strings.Contains(result[0], "Config") {
		t.Errorf("expected warning to mention type 'Config', got: %s", result[0])
	}
}

func TestCheckReceiverConsistency_SingleMethod(t *testing.T) {
	src := `package main

type Solo struct{}

func (s *Solo) DoSomething() {}
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) != 0 {
		t.Errorf("expected no warnings for single-method type, got: %v", result)
	}
}

func TestCheckReceiverConsistency_BlankReceiver(t *testing.T) {
	src := `package main

type Foo struct{}

func (_ *Foo) A() {}
func (f *Foo) B() {}
`
	// Blank receiver should be skipped; only "f" is registered, so no inconsistency.
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) != 0 {
		t.Errorf("expected no warnings when blank receiver is used, got: %v", result)
	}
}

func TestCheckReceiverConsistency_GenericType(t *testing.T) {
	src := `package main

type Cache[K comparable, V any] struct{ data map[K]V }

func (c *Cache[K, V]) Get(k K) (V, bool) { v, ok := c.data[k]; return v, ok }
func (ch *Cache[K, V]) Set(k K, v V) { ch.data[k] = v }
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for inconsistent receivers on generic type, got none")
	}
	if !strings.Contains(result[0], "Cache") {
		t.Errorf("expected warning to mention type 'Cache', got: %s", result[0])
	}
}

func TestCheckReceiverConsistency_SyntaxError(t *testing.T) {
	// Garbage that won't parse -- should return nil gracefully.
	src := "package main\n\ntype Foo struct{\nfunc (s *"
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) != 0 {
		t.Errorf("expected no warnings for unparseable content, got: %v", result)
	}
}

func TestReceiverTypeName(t *testing.T) {
	// Test internal type extraction via the public check behavior.
	// We verify through findReceiverInconsistencies which uses receiverTypeName.
	groups := findReceiverInconsistencies(`package main

type Foo struct{}
func (f *Foo) M() {}

type Bar struct{}
func (b Bar) N() {}
`)
	// Should find 2 types, both consistent.
	if len(groups) != 2 {
		t.Fatalf("expected 2 type groups, got %d", len(groups))
	}
}

func TestCheckReceiverConsistency_ThisSelfAntiPattern(t *testing.T) {
	src := `package main

type Widget struct{ id int }

func (this *Widget) GetID() int { return this.id }
func (self *Widget) SetID(id int) { self.id = id }
`
	result := checkReceiverConsistency("test.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for this/self anti-pattern receivers")
	}
	combined := strings.Join(result, " ")
	if !strings.Contains(combined, "anti-pattern") {
		t.Errorf("expected anti-pattern mention, got: %s", combined)
	}
}
