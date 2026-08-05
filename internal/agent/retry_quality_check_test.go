package agent

import (
	"strings"
	"testing"
)

func TestRetryQuality_MissingBackoff(t *testing.T) {
	src := `package main
import "net/http"
func fetch(url string) {
	for {
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		_ = resp
		break
	}
}`
	w := checkRetryQuality("retry.go", "", src)
	if !hasWarning(w, "no backoff delay") {
		t.Fatalf("expected missing-backoff warning, got %v", w)
	}
}

func TestRetryQuality_UnboundedRetry(t *testing.T) {
	src := `package main
import "net/http"
func fetch(url string) {
	for {
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		_ = resp
		break
	}
}`
	w := checkRetryQuality("retry.go", "", src)
	if !hasWarning(w, "no attempt cap") {
		t.Fatalf("expected unbounded-retry warning, got %v", w)
	}
}

func TestRetryQuality_HasBackoffAndCap_OK(t *testing.T) {
	src := `package main
import ("net/http"; "time")
func fetch(url string) {
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		_ = resp
		return
	}
}`
	if w := checkRetryQuality("retry.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for well-formed retry, got %v", w)
	}
}

func TestRetryQuality_UnboundedButHasCounterCheckInBody(t *testing.T) {
	src := `package main
import ("net/http"; "time")
var maxRetries = 3
func fetch(url string) {
	attempt := 0
	for {
		resp, err := http.Get(url)
		if err != nil {
			if attempt >= maxRetries {
				return
			}
			attempt++
			time.Sleep(time.Second)
			continue
		}
		_ = resp
		return
	}
}`
	w := checkRetryQuality("retry.go", "", src)
	if hasWarning(w, "no attempt cap") {
		t.Fatalf("unexpected unbounded-retry warning: %v", w)
	}
}

func TestRetryQuality_NotRetryLoop_NoWarning(t *testing.T) {
	src := `package main
import "net/http"
func fetch(url string) (*http.Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}`
	if w := checkRetryQuality("retry.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for non-retry code, got %v", w)
	}
}

func TestRetryQuality_DeltaAware(t *testing.T) {
	src := `package main
import "net/http"
func fetch(url string) {
	for {
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		_ = resp
		break
	}
}`
	if w := checkRetryQuality("retry.go", src, src); len(w) != 0 {
		t.Fatalf("expected 0 delta warnings, got %v", w)
	}
}

func TestRetryQuality_NonGoFile(t *testing.T) {
	if w := checkRetryQuality("retry.py", "", "for x in range(10):\n    pass"); w != nil {
		t.Fatalf("expected nil for non-Go file, got %v", w)
	}
}

func TestRetryQuality_EmptyContent(t *testing.T) {
	if w := checkRetryQuality("retry.go", "", ""); w != nil {
		t.Fatalf("expected nil for empty content, got %v", w)
	}
}

func TestRetryQuality_SyntaxError(t *testing.T) {
	if w := checkRetryQuality("retry.go", "", "package main\nfunc broken("); w != nil {
		t.Fatalf("expected nil for syntax error, got %v", w)
	}
}

func TestRetryQuality_DBRetryMissingBackoff(t *testing.T) {
	src := `package main
import "database/sql"
func query(db *sql.DB) {
	for {
		err := db.Ping()
		if err != nil {
			continue
		}
		return
	}
}`
	w := checkRetryQuality("retry.go", "", src)
	if !hasWarning(w, "no backoff delay") {
		t.Fatalf("expected missing-backoff for DB retry, got %v", w)
	}
}

func TestRetryQuality_HasBackoffViaTimeAfter(t *testing.T) {
	src := `package main
import ("net/http"; "time")
func fetch(url string, ch chan struct{}) {
	for {
		resp, err := http.Get(url)
		if err != nil {
			select {
			case <-time.After(time.Second):
				continue
			case <-ch:
				return
			}
		}
		_ = resp
		return
	}
}`
	w := checkRetryQuality("retry.go", "", src)
	if hasWarning(w, "no backoff delay") {
		t.Fatalf("unexpected missing-backoff with time.After: %v", w)
	}
}

func TestRetryQuality_BoundedLoopNoErrCheck_NotRetry(t *testing.T) {
	src := `package main
import "net/http"
func poll(url string) {
	for i := 0; i < 100; i++ {
		_, _ = http.Get(url)
	}
}`
	if w := checkRetryQuality("retry.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for non-retry loop, got %v", w)
	}
}

// hasWarning checks if any warning contains the given substring.
func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
