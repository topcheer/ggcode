// Package rexcache provides a process-wide cache of compiled regular
// expressions (#5831).
//
// Fleet-wide heap sampling showed regexp/syntax.(*compiler).inst as the #1
// allocation site (~10MB resident per instance): hot paths - the command
// gate, grep/search tools, and per-turn agent checks - recompiled the same
// patterns on every call. Compiling each distinct pattern once and reusing
// the *regexp.Regexp eliminates that churn. *regexp.Regexp is safe for
// concurrent use, so cached values can be shared across goroutines.
package rexcache

import (
	"regexp"
	"sync"
)

// maxEntries bounds the cache so unbounded user-supplied patterns (grep,
// batch_replace, ...) cannot turn the cache itself into a leak. When the
// cap is hit the map is reset; fixed call-site patterns re-warm within a
// few calls, and 512 compiled regexes already cover far more distinct
// patterns than any real session produces.
const maxEntries = 512

var (
	mu    sync.RWMutex
	cache = make(map[string]*regexp.Regexp)
)

// Compile compiles pattern once, caches it, and returns the cached value on
// later calls. Invalid patterns return the compile error uncached: rejected
// patterns never occupy cache space and re-rejecting them is cheap.
func Compile(pattern string) (*regexp.Regexp, error) {
	mu.RLock()
	re := cache[pattern]
	mu.RUnlock()
	if re != nil {
		return re, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	if len(cache) >= maxEntries {
		cache = make(map[string]*regexp.Regexp)
	}
	cache[pattern] = re
	mu.Unlock()
	return re, nil
}

// Must is the MustCompile flavor of Compile for patterns that are known
// valid (fixed literals or safely quoted dynamic input). It panics on
// invalid patterns exactly like regexp.MustCompile.
func Must(pattern string) *regexp.Regexp {
	re, err := Compile(pattern)
	if err != nil {
		panic("rexcache: " + err.Error())
	}
	return re
}

// Len reports the number of cached patterns (test hook).
func Len() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(cache)
}
