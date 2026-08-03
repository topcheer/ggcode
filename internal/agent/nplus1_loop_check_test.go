package agent

import (
	"strings"
	"testing"
)

func TestCheckNPlus1Loop_DBQueryInForLoop(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

import "database/sql"

func processItems(db *sql.DB, ids []int) {
	for _, id := range ids {
		db.Query("SELECT * FROM items WHERE id = ?", id)
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected N+1 warning for db.Query in for loop")
	}
	if !strings.Contains(warnings[0], "N+1") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckNPlus1Loop_DBQueryInRangeLoop(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

import "database/sql"

func process(db *sql.DB, rows []int) {
	for i := range rows {
		db.Query("SELECT 1")
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected N+1 warning for db.Query in range loop")
	}
}

func TestCheckNPlus1Loop_HTTPGetInLoop(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

import "net/http"

func fetchAll(urls []string) {
	for _, u := range urls {
		http.Get(u)
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected N+1 warning for http.Get in loop")
	}
}

func TestCheckNPlus1Loop_NoIOTInLoop(t *testing.T) {
	new := `package main

import "database/sql"

func process(db *sql.DB) {
	db.Query("SELECT * FROM items")
}
`
	warnings := checkNPlus1Loop("test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestCheckNPlus1Loop_DeltaAware(t *testing.T) {
	// The I/O in loop pattern already exists in old content.
	old := `package main

import "database/sql"

func process(db *sql.DB, ids []int) {
	for _, id := range ids {
		db.Query("SELECT 1 WHERE id=?", id)
	}
}
`
	new := old + `
func extra() {}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no delta warnings for pre-existing pattern, got: %v", warnings)
	}
}

func TestCheckNPlus1Loop_FileReadInLoop(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

import "os"

func readAll(paths []string) {
	for _, p := range paths {
		os.ReadFile(p)
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected N+1 warning for os.ReadFile in loop")
	}
}

func TestCheckNPlus1Loop_GORMInLoop(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

type DB struct{}

func (db *DB) Where(query string, args ...interface{}) {}

func fetch(db *DB, ids []int) {
	for _, id := range ids {
		db.Where("id = ?", id)
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected N+1 warning for db.Where in loop")
	}
}

func TestCheckNPlus1Loop_NonGoFile(t *testing.T) {
	warnings := checkNPlus1Loop("test.py", "", "for x in items: pass")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckNPlus1Loop_FuncLitExclusion(t *testing.T) {
	// I/O inside a function literal within a loop should NOT trigger
	// (it's likely a goroutine or callback, which is a different pattern).
	new := `package main

import "sync"

func process(urls []string) {
	var wg sync.WaitGroup
	for _, u := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			_ = url
		}(u)
	}
	wg.Wait()
}
`
	warnings := checkNPlus1Loop("test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for func lit in loop, got: %v", warnings)
	}
}

func TestCheckNPlus1Loop_SyntaxError(t *testing.T) {
	new := `package main
func broken(
`
	warnings := checkNPlus1Loop("test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for syntax-error file, got: %v", warnings)
	}
}

func TestCheckNPlus1Loop_MultiplePatterns(t *testing.T) {
	old := `package main
func main() {}
`
	new := `package main

import (
	"database/sql"
	"net/http"
)

func bad(db *sql.DB, urls []string) {
	for _, u := range urls {
		db.Query("SELECT 1")
		http.Get(u)
	}
}
`
	warnings := checkNPlus1Loop("test.go", old, new)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
}
