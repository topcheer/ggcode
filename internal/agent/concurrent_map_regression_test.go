package agent

import "testing"

func TestConcurrentMap_WaitGroupNotSync(t *testing.T) {
	src := `package main
import "sync"
func fanout(items []int, results map[int]int) {
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(it int) { defer wg.Done(); results[it] = it }(item)
	}
	wg.Wait()
}`
	out := checkConcurrentMapAccess("cm.go", "", src)
	if out == "" {
		t.Fatal("expected warning for WaitGroup-guarded map writes")
	}
}

func TestConcurrentMap_MutexSuppresses(t *testing.T) {
	src := `package main
import "sync"
func f(m map[int]int, mu *sync.Mutex) {
	mu.Lock()
	m[1] = 2
	mu.Unlock()
}`
	out := checkConcurrentMapAccess("cm.go", "", src)
	if out != "" {
		t.Fatalf("expected no warning with Mutex, got: %s", out)
	}
}

func TestConcurrentMap_DeltaSuppressesPreexisting(t *testing.T) {
	src := `package main
func f(results map[int]int, items []int) {
	for _, item := range items {
		go func(it int) { results[it] = it }(item)
	}
}`
	edited := `package main
// unrelated comment
func f(results map[int]int, items []int) {
	for _, item := range items {
		go func(it int) { results[it] = it }(item)
	}
}`
	if out := checkConcurrentMapAccess("cm.go", "", src); out == "" {
		t.Fatal("sanity: new pattern should warn")
	}
	if out := checkConcurrentMapAccess("cm.go", src, edited); out != "" {
		t.Fatalf("pre-existing pattern re-reported: %s", out)
	}
}
