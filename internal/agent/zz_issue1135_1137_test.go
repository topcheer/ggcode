package agent

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const issue1135SrcBase = `package main

import (
	"context"
	"net/http"
)

func processItems(ids []int, httpClient *http.Client, rdb *Redis, repo *Repo, cache *Cache, registry *Registry, group *ErrGroup) ([]byte, error) {
	ctx := context.Background()
	var out []byte
	for _, id := range ids {
`

const issue1135SrcTail = `
	}
	return out, nil
}
`

func TestIssue1135_MemoryCacheGetNotFlagged(t *testing.T) {
	src := `package main

func work(cache *Cache, m *KVStore) []byte {
	out := make([]byte, 0)
	for i := 0; i < 100; i++ {
		_ = cache.Get(i)
		m.Delete("k")
		out = append(out, byte(i))
	}
	return out
}
`
	warnings := checkNPlus1Loop("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("in-memory cache.Get/m.Delete should not warn, got %d warnings: %v", len(warnings), warnings)
	}
}

func TestIssue1135_RegistryFindAndGroupDoNotFlagged(t *testing.T) {
	src := `package main

import "context"

func lookup(registry *Registry, group *Group, ids []string) error {
	ctx := context.Background()
	for _, id := range ids {
		_, _ = registry.Find(ctx, id)
		if err := group.Do(func() error { return nil }); err != nil {
			return err
		}
	}
	return nil
}
`
	warnings := checkNPlus1Loop("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("registry.Find/group.Do should not warn, got %d warnings: %v", len(warnings), warnings)
	}
}

func TestIssue1135_DBReceiverFindStillFlagged(t *testing.T) {
	src := `package main

func loadUsers(userDB *DB, ids []int) []*User {
	users := make([]*User, 0)
	for _, id := range ids {
		u := userDB.Find(id)
		users = append(users, u)
	}
	return users
}
`
	warnings := checkNPlus1Loop("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("userDB.Find should warn exactly once, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "N+1") {
		t.Errorf("warning should mention N+1, got: %s", warnings[0])
	}
}

func TestIssue1135_RedisHGetWithSignalFlagged(t *testing.T) {
	src := `package main

func loadFields(rdb *redis.Client, ids []int) {
	for _, id := range ids {
		_, _ = rdb.HGet("hash", id)
	}
}
`
	warnings := checkNPlus1Loop("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("rdb.HGet should warn exactly once, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1135_HttpClientDoWithSignalFlagged(t *testing.T) {
	src := `package main

import "net/http"

func pingAll(httpClient *http.Client, urls []string) {
	for _, u := range urls {
		req, _ := http.NewRequest("GET", u, nil)
		resp, _ := httpClient.Do(req)
		_ = resp
	}
}
`
	warnings := checkNPlus1Loop("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("httpClient.Do should warn exactly once, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1135_GenericVsPreciseReceivers(t *testing.T) {
	generic := issue1135SrcBase + "\t\t_ = opts.Get(\"mode\")\n" + issue1135SrcTail
	warnings := checkNPlus1Loop("main.go", "", generic)
	if len(warnings) != 0 {
		t.Fatalf("opts.Get without receiver signal should be silent, got %d: %v", len(warnings), warnings)
	}

	precise := issue1135SrcBase + "\t\trow := repo.QueryRow(\"SELECT 1\")\n\t\t_ = row\n" + issue1135SrcTail
	warnings = checkNPlus1Loop("main.go", "", precise)
	if len(warnings) != 1 {
		t.Fatalf("repo.QueryRow is a precise SQL verb and should warn, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1136_InsertionAboveTargetNoReWarn(t *testing.T) {
	oldSrc := `package main

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		row := db.Query("SELECT 1", id)
		res = append(res, row)
	}
	return res
}
`

	newSrc := `package main

func padHelper(n int) int {
	x := n + 1
	y := x * 2
	z := y - x
	return z
}

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		row := db.Query("SELECT 1", id)
		res = append(res, row)
	}
	return res
}
`
	warnings := checkNPlus1Loop("main.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("identical call after insertion above target should not re-warn (#1136), got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1136_CommentInsideLoopNoReWarn(t *testing.T) {
	oldSrc := `package main

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		row := db.Query("SELECT 1", id)
		res = append(res, row)
	}
	return res
}
`
	newSrc := `package main

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		// refreshed docs below
		row := db.Query("SELECT 1", id)
		res = append(res, row)
	}
	return res
}
`
	warnings := checkNPlus1Loop("main.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("comment-only change should not re-warn (#1136), got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1136_DeltaKeyPositionIndependent(t *testing.T) {
	srcA := `package main

func f(db *DB, xs []int) {
	for range xs {
		db.Query("q", 1)
	}
}
`
	srcB := `package main

func padding(a, b, c int) int {
	s := a + b
	s += c
	return s
}

func f(db *DB, xs []int) {
	for range xs {
		db.Query("q", 1)
	}
}
`
	fsetA := token.NewFileSet()
	fA, errA := parser.ParseFile(fsetA, "a.go", srcA, 0)
	fsetB := token.NewFileSet()
	fB, errB := parser.ParseFile(fsetB, "b.go", srcB, 0)
	if errA != nil || errB != nil {
		t.Fatalf("parse failed: %v / %v", errA, errB)
	}
	pA := findIOInLoops(fA)
	pB := findIOInLoops(fB)
	if len(pA) != 1 || len(pB) != 1 {
		t.Fatalf("expected 1 pattern each, got %d / %d", len(pA), len(pB))
	}
	if pA[0].String() != pB[0].String() {
		t.Errorf("keys must be position-independent:\n A=%q\n B=%q", pA[0].String(), pB[0].String())
	}
	if pA[0].pos == pB[0].pos {
		t.Errorf("display positions are expected to differ across shifted parses")
	}
}

func TestIssue1136_SecondIdenticalCallReportsOnce(t *testing.T) {
	oldSrc := `package main

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		row := db.Query("SELECT 1", id)
		res = append(res, row)
	}
	return res
}
`
	newSrc := `package main

func fetch(db *DB, ids []int) []int {
	res := make([]int, 0)
	for _, id := range ids {
		row := db.Query("SELECT 1", id)
		extra := db.Query("SELECT 1", id)
		res = append(res, row+extra)
	}
	return res
}
`
	warnings := checkNPlus1Loop("main.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("exactly one new identical call should yield one warning (#1136), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "database query") {
		t.Errorf("unexpected warning text: %s", warnings[0])
	}
}

func TestIssue1137_NestedLoopSingleCount(t *testing.T) {
	newSrc := `package main

func matrixScan(db *DB, n int) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			row := db.Query("SELECT cell", i, j)
			_ = row
		}
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "m.go", newSrc, 0)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	patterns := findIOInLoops(f)
	if len(patterns) != 1 {
		t.Fatalf("nested loop must contain exactly 1 distinct call site (#1137), got %d", len(patterns))
	}

	warnings := checkNPlus1Loop("main.go", "", newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning for 1 nested call site, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1137_TripleNestingSingleCount(t *testing.T) {
	newSrc := `package main

func cubeScan(db *DB, n int) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				db.Exec("UPDATE cell SET v = 1 WHERE i = ? AND j = ? AND k = ?", i, j, k)
			}
		}
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "c.go", newSrc, 0)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	patterns := findIOInLoops(f)
	if len(patterns) != 1 {
		t.Fatalf("triple nesting must still be 1 distinct call site (#1137), got %d", len(patterns))
	}

	warnings := checkNPlus1Loop("main.go", "", newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue1137_TwoDistinctCallsTwoWarnings(t *testing.T) {
	newSrc := `package main

func mixed(db *DB, n int) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r := db.Query("SELECT cell", i, j)
			w := db.Exec("WRITE", i, j)
			_, _ = r, w
		}
	}
}
`
	warnings := checkNPlus1Loop("main.go", "", newSrc)
	if len(warnings) != 2 {
		t.Fatalf("two distinct call sites should give 2 warnings, got %d: %v", len(warnings), warnings)
	}
}
