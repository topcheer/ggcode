package agent

import (
	"strings"
	"testing"
)

func TestSQLInjection_StringConcat(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB, name string) {
	db.Query("SELECT * FROM users WHERE name = '" + name + "'")
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "string concatenation") {
		t.Errorf("expected concat mention, got: %s", warnings[0])
	}
}

func TestSQLInjection_FmtSprintf(t *testing.T) {
	src := `package main
import ("database/sql"; "fmt")
func f(db *sql.DB, role string) {
	db.Exec(fmt.Sprintf("DELETE FROM orders WHERE role = '%s'", role))
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "fmt.Sprintf") {
		t.Errorf("expected Sprintf mention, got: %s", warnings[0])
	}
}

func TestSQLInjection_SafeParam(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB, name string) {
	db.Query("SELECT * FROM users WHERE name = ?", name)
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for parameterized query, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_QueryRowContext(t *testing.T) {
	src := `package main
import ("database/sql"; "context")
func f(ctx context.Context, db *sql.DB, id string) {
	db.QueryRowContext(ctx, "SELECT * FROM t WHERE id = " + id)
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for context method, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_ExecContextSafe(t *testing.T) {
	src := `package main
import ("database/sql"; "context")
func f(ctx context.Context, db *sql.DB, id int) {
	db.ExecContext(ctx, "UPDATE t SET active = 1 WHERE id = ?", id)
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for safe ExecContext, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_NonDBMethodIgnored(t *testing.T) {
	src := `package main
func f(s string) string {
	return "prefix" + s
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for non-DB method, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_PrepareSprintf(t *testing.T) {
	src := `package main
import ("database/sql"; "fmt")
func f(db *sql.DB, table string) {
	db.Prepare(fmt.Sprintf("SELECT * FROM %s WHERE id = ?", table))
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for Prepare+Sprintf, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_MultipleMethods(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB, a, b string) {
	db.Query("SELECT 1 WHERE x = '" + a + "'")
	db.Exec("DELETE FROM t WHERE y = '" + b + "'")
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_CappedWarnings(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB) {
	db.Query("x" + "a")
	db.Query("x" + "b")
	db.Query("x" + "c")
	db.Query("x" + "d")
	db.Query("x" + "e")
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != maxSQLInjectionWarnings+1 {
		t.Fatalf("expected %d warnings (capped + notice), got %d", maxSQLInjectionWarnings+1, len(warnings))
	}
}

func TestSQLInjection_NonGoFile(t *testing.T) {
	warnings := checkSQLInjection("test.py", "", "print('hello')")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for non-Go file, got %d", len(warnings))
	}
}

func TestSQLInjection_EmptyContent(t *testing.T) {
	warnings := checkSQLInjection("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for empty content, got %d", len(warnings))
	}
}

func TestSQLInjection_SyntaxError(t *testing.T) {
	src := `package main
func f(db) {
	db.Query("x" +`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for syntax error, got %d", len(warnings))
	}
}

func TestSQLInjection_NamedExec(t *testing.T) {
	src := `package main
func f(db interface{ NamedExec(string, interface{}) interface{} }, name string) {
	db.NamedExec("SELECT * FROM u WHERE n = '" + name + "'", nil)
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 for NamedExec concat, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_LiteralOnly(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB) {
	db.Query("SELECT * FROM users")
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 for literal query, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_DeltaSuppressesPreexisting(t *testing.T) {
	src := `package main
import "database/sql"
func f(db *sql.DB, name string) {
	db.Query("SELECT * FROM users WHERE name = '" + name + "'")
}`
	warnings := checkSQLInjection("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for pre-existing concat query, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_DeltaNewInstanceReported(t *testing.T) {
	old := `package main
import "database/sql"
func f(db *sql.DB, name string) {
	db.Query("SELECT * FROM users WHERE name = '" + name + "'")
}`
	newSrc := `package main
import "database/sql"
func f(db *sql.DB, name string) {
	db.Query("SELECT * FROM users WHERE name = '" + name + "'")
	db.Exec("DELETE FROM t WHERE id = '" + name + "'")
}`
	warnings := checkSQLInjection("test.go", old, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 new warning (pre-existing suppressed), got %d: %v", len(warnings), warnings)
	}
	// The warning text carries the position; the new Exec call is on line 5.
	if !strings.Contains(warnings[0], "test.go:5") {
		t.Errorf("expected the NEW instance (line 5) to be reported, got: %s", warnings[0])
	}
}

func TestSQLInjection_CacheGetNotFlagged(t *testing.T) {
	// cache.Get / fmt.Sprintf is a redis/cache idiom, not SQL. Get was removed
	// from sqlInjMethods.
	src := `package main
import "fmt"
func f(cache *Cache, key string) string {
	return cache.Get(fmt.Sprintf("user:%s", key))
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for cache.Get with Sprintf, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_MapSelectNotFlagged(t *testing.T) {
	src := `package main
func f(m *Mapper, key string) string {
	return m.Select("prefix-" + key)
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for m.Select concat, got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_MustExecNotFlagged(t *testing.T) {
	src := `package main
import "fmt"
func f(tx *Tx, table string) {
	tx.MustExec(fmt.Sprintf("TRUNCATE %s", table))
}`
	warnings := checkSQLInjection("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for MustExec (removed from method set), got %d: %v", len(warnings), warnings)
	}
}

// Note: `contains` helper is defined in reflection_test.go

func TestSQLInjection_MultilineDeltaNotSuppressed(t *testing.T) {
	oldSrc := "package main\n" +
		"import \"database/sql\"\n" +
		"func f(db *sql.DB, id string) {\n" +
		"	rows, err := db.Query(\n" +
		"		\"SELECT * FROM users WHERE id = \" + id)\n" +
		"	_ = rows\n" +
		"	_ = err\n" +
		"}\n"
	// Same first line, but query changed SELECT -> DELETE on the arg line.
	newSrc := "package main\n" +
		"import \"database/sql\"\n" +
		"func f(db *sql.DB, id string) {\n" +
		"	rows, err := db.Query(\n" +
		"		\"DELETE FROM users WHERE id = \" + id)\n" +
		"	_ = rows\n" +
		"	_ = err\n" +
		"}\n"
	warnings := checkSQLInjection("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (new vuln must not be delta-suppressed), got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "DELETE") && !contains(warnings[0], "concatenation") {
		t.Logf("warning: %s", warnings[0])
	}
}

func TestSQLInjection_MultilineDeltaSameQuerySuppressed(t *testing.T) {
	src := "package main\n" +
		"import \"database/sql\"\n" +
		"func f(db *sql.DB, id string) {\n" +
		"	rows, err := db.Query(\n" +
		"		\"SELECT * FROM users WHERE id = \" + id)\n" +
		"	_ = rows\n" +
		"	_ = err\n" +
		"}\n"
	warnings := checkSQLInjection("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for identical content (delta suppress), got %d: %v", len(warnings), warnings)
	}
}

func TestSQLInjection_SingleLineDeltaStillSuppressed(t *testing.T) {
	oldSrc := "package main\n" +
		"import \"database/sql\"\n" +
		"func f(db *sql.DB, id string) {\n" +
		"	db.Query(\"SELECT * FROM t WHERE id = \" + id)\n" +
		"}\n"
	// Same single-line call with an unrelated edit elsewhere.
	newSrc := "package main\n" +
		"import \"database/sql\"\n" +
		"func f(db *sql.DB, id string) {\n" +
		"	db.Query(\"SELECT * FROM t WHERE id = \" + id)\n" +
		"	_ = id\n" +
		"}\n"
	warnings := checkSQLInjection("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unchanged single-line call, got %d: %v", len(warnings), warnings)
	}
}
