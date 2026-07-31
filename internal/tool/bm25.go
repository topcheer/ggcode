package tool

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// ── BM25 ranking engine ──────────────────────────────────────────
//
// BM25 (Best Matching 25) is the ranking function used by Lucene,
// Elasticsearch, and Solr. It ranks documents by term frequency and
// inverse document frequency, making it far more effective than plain
// keyword matching for natural-language queries like "where do we
// handle authentication errors".
//
// This is a lightweight, in-memory implementation that builds an index
// from file contents and scores them against a query. No persistence,
// no external dependencies — pure computation.

const (
	bm25K1 = 1.2  // term frequency saturation parameter
	bm25B  = 0.75 // length normalization parameter
)

// bm25Doc represents a single indexed document (file).
type bm25Doc struct {
	path   string
	tf     map[string]int // term frequency in this document
	length int            // total number of terms
}

// bm25Index is an ephemeral index built from a set of files.
type bm25Index struct {
	docs      []bm25Doc
	df        map[string]int // document frequency per term
	avgLength float64
}

// tokenizeForSearch splits source code into searchable terms.
// It handles camelCase, snake_case, kebab-case, and PascalCase splitting,
// removes common programming-language stopwords, and lowercases everything.
// tokenizeForSearch splits source code into searchable terms (with query
// expansion). Used at query time so that searching for "auth" also matches
// "authentication", "authenticate", etc.
func tokenizeForSearch(text string) []string {
	return tokenizeRaw(text, true)
}

// tokenizeForIndex splits source code into index terms WITHOUT expansion.
// The index stores only terms that actually appear in the file; query-side
// expansion handles synonym matching. This prevents index bloat.
func tokenizeForIndex(text string) []string {
	return tokenizeRaw(text, false)
}

// tokenizeRaw is the core tokenizer. When expand is true, each term is
// also checked against the synonym map and stemmer for better recall.
func tokenizeRaw(text string, expand bool) []string {
	var terms []string
	var current strings.Builder

	flush := func() {
		if current.Len() == 0 {
			return
		}
		word := current.String()
		current.Reset()

		// Split camelCase / PascalCase: "HttpRequest" → "http", "request"
		for _, part := range splitCamelCase(word) {
			part = strings.ToLower(part)
			if len(part) < 2 {
				continue
			}
			if codeStopwords[part] {
				continue
			}
			terms = append(terms, part)
			if expand {
				terms = append(terms, expandTerm(part)...)
			}
		}
	}

	for _, r := range text {
		if isTokenRune(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()

	return terms
}

// isTokenRune returns true for characters that form part of an identifier
// or keyword: letters, digits, and underscore.
func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// splitCamelCase splits a word at camelCase boundaries.
// e.g., "BM25Index" → ["bm25", "index"], "httpClient" → ["http", "client"]
func splitCamelCase(word string) []string {
	if word == "" {
		return nil
	}

	var parts []string
	var current strings.Builder
	runes := []rune(word)

	for i, r := range runes {
		// Transition: lowercase → uppercase (camelCase boundary)
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		}
		// Transition: uppercase → uppercase followed by lowercase (acronym boundary)
		// e.g., "HTTPRequest" → "HTTP", "Request"
		if i > 0 && i < len(runes)-1 && unicode.IsUpper(r) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		}
		// Transition: digit → letter (e.g., "BM25Index" → "BM25", "Index")
		if i > 0 && unicode.IsLetter(r) && unicode.IsDigit(runes[i-1]) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// codeStopwords are common tokens that carry little meaning in code search.
var codeStopwords = map[string]bool{
	// Language keywords
	"func": true, "var": true, "const": true, "type": true, "struct": true,
	"interface": true, "package": true, "import": true, "return": true,
	"if": true, "else": true, "for": true, "range": true, "switch": true,
	"case": true, "default": true, "break": true, "continue": true,
	"defer": true, "go": true, "chan": true, "select": true,
	"public": true, "private": true, "protected": true, "class": true,
	"void": true, "int": true, "string": true, "bool": true, "byte": true,
	"def": true, "this": true, "nil": true, "null": true,
	"true": true, "false": true, "new": true, "from": true, "let": true,
	"function": true, "export": true, "module": true, "require": true,

	// Common identifiers / boilerplate
	"err": true, "error": true, "ctx": true, "context": true,
	"args": true, "params": true, "opts": true,
	"result": true, "res": true, "req": true, "request": true,
	"response": true, "data": true, "value": true, "val": true,
	"name": true, "test": true, "mock": true, "stub": true,
	"todo": true, "fixme": true, "hack": true,

	// Common English stopwords
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "should": true, "could": true,
	"may": true, "might": true, "must": true, "can": true,
	"in": true, "on": true, "at": true, "to": true, "of": true,
	"by": true, "as": true, "and": true,
	"then": true, "when": true, "where": true,
	"what": true, "which": true, "how": true, "why": true,
	"that": true, "these": true, "those": true,
	"it": true, "its": true, "i": true, "you": true, "we": true,
	"they": true, "them": true, "their": true, "our": true,
	"my": true, "your": true, "his": true, "her": true,
	"about": true, "into": true, "out": true, "up": true, "down": true,
	"get": true, "set": true, "put": true, "run": true,
	"use": true, "used": true, "using": true,
}

// expandTerm returns synonym and stem variants of a single token for
// query-side expansion. This dramatically improves recall: searching for
// "auth" also matches "authentication", "db" matches "database", "configs"
// matches "config". The returned slice does NOT include the original term
// (the caller already has it); it contains only the additional variants.
func expandTerm(term string) []string {
	var variants []string
	seen := map[string]bool{term: true}

	add := func(s string) {
		if s != "" && len(s) >= 2 && !seen[s] && !codeStopwords[s] {
			seen[s] = true
			variants = append(variants, s)
		}
	}

	// 1. Synonym / abbreviation expansion.
	if syns, ok := codeSynonyms[term]; ok {
		for _, s := range syns {
			add(s)
		}
	}
	// Reverse lookup: if the term is a value in the synonym map, add the key.
	for key, syns := range codeSynonyms {
		for _, s := range syns {
			if s == term {
				add(key)
			}
		}
	}

	// 2. Light stemming (suffix stripping) for English word forms.
	add(stem(term))

	return variants
}

// stem performs lightweight suffix stripping to normalise word forms.
// e.g., "authentication" → "authenticat(e)", "running" → "run",
// "configs" → "config". NOT a full Porter stemmer — just common suffixes.
func stem(word string) string {
	if len(word) <= 3 {
		return word
	}

	// Order matters: longer suffixes first.
	suffixes := []string{
		"ization", "ational", "fulness", "ousness", "iveness",
		"ation", "ments", "tions", "ement", "ables", "ibles",
		"tion", "ment", "ness", "ions", "able", "ible",
		"ized", "ful", "ous", "ive", "ies", "ied",
		"ing", "ers", "est", "ed", "er", "es", "ly", "s",
	}

	for _, suf := range suffixes {
		if strings.HasSuffix(word, suf) && len(word)-len(suf) >= 3 {
			base := word[:len(word)-len(suf)]
			// Restore common verb forms: "authenticat" → "authenticate".
			if strings.HasSuffix(base, "at") || strings.HasSuffix(base, "iz") {
				base += "e"
			}
			return base
		}
	}

	return word
}

// codeSynonyms maps abbreviations and short forms to their expanded forms.
// Used for query-side expansion only — the index stores terms verbatim.
// When a user searches for "auth", we also score against "authentication",
// "authenticate", "authorize", etc.
var codeSynonyms = map[string][]string{
	// Authentication / authorization
	"auth":   {"authentication", "authenticate", "authorize", "authorization"},
	"login":  {"signin", "logon", "authenticate"},
	"logout": {"signout", "logoff"},
	"signup": {"register", "registration"},
	"pwd":    {"password"},
	"cred":   {"credential", "credentials"},
	"jwt":    {"token"},
	"oauth":  {"openid", "oidc", "sso"},
	"perm":   {"permission", "permissions"},
	"rbac":   {"role", "roles"},

	// Database
	"db":   {"database", "databases", "sql"},
	"repo": {"repository", "repositories"},
	"crud": {"create", "read", "update", "delete"},
	"ddl":  {"schema", "migration"},

	// Configuration
	"config": {"configuration", "configure", "settings", "preference"},
	"env":    {"environment"},

	// Infrastructure / DevOps
	"k8s":    {"kubernetes"},
	"deploy": {"deployment", "deployments"},
	"svc":    {"service", "services"},
	"ns":     {"namespace", "namespaces"},
	"cron":   {"schedule", "scheduler"},

	// API / networking
	"api":  {"endpoint", "endpoints"},
	"rest": {"api", "resource"},
	"grpc": {"rpc", "proto"},
	"ws":   {"websocket", "websockets"},
	"url":  {"uri", "link"},
	"req":  {"request", "requests"},
	"resp": {"response", "responses"},

	// Testing
	"spec":   {"specification", "specifications"},
	"assert": {"assertion", "expect"},

	// Misc development
	"impl":  {"implementation", "implement"},
	"init":  {"initialize", "initialization", "setup"},
	"util":  {"utility", "helper", "helpers"},
	"mgr":   {"manager", "managers"},
	"opts":  {"options", "option"},
	"fmt":   {"format", "formatting"},
	"regex": {"regexp", "pattern"},
	"async": {"asynchronous", "concurrent", "concurrency", "goroutine"},
	"sync":  {"synchronous", "synchronized"},
	"priv":  {"private"},
	"pub":   {"public", "publish"},
	"sub":   {"subscribe", "subscription"},
	"temp":  {"temporary"},
	"info":  {"information"},
	"msg":   {"message", "messages"},
	"num":   {"number", "numbers"},
	"idx":   {"index", "indexes"},
	"len":   {"length"},
	"ref":   {"reference", "references"},
	"ptr":   {"pointer", "pointers"},
	"dir":   {"directory", "directories"},
	"fs":    {"filesystem"},
	"pkg":   {"package", "packages"},
	"mod":   {"module", "modules", "model", "models"},
	"ctrl":  {"controller", "controllers"},
	"gen":   {"generate", "generation", "generator"},
	"cache": {"caching", "cached", "memoize", "memoization"},
	"retry": {"retries", "backoff"},
}

// buildBM25Index creates an ephemeral index from the given file contents.
// fileContents is a map of relative path → raw file content.
func buildBM25Index(fileContents map[string]string) *bm25Index {
	idx := &bm25Index{
		df: make(map[string]int),
	}

	var totalLength int

	// Process files in sorted order for deterministic behavior.
	paths := make([]string, 0, len(fileContents))
	for p := range fileContents {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		content := fileContents[path]
		terms := tokenizeForIndex(content) // index without expansion
		if len(terms) == 0 {
			continue
		}

		tf := make(map[string]int, len(terms))
		for _, term := range terms {
			tf[term]++
		}

		doc := bm25Doc{
			path:   path,
			tf:     tf,
			length: len(terms),
		}
		idx.docs = append(idx.docs, doc)
		totalLength += len(terms)

		// Update document frequency
		for term := range tf {
			idx.df[term]++
		}
	}

	if len(idx.docs) > 0 {
		idx.avgLength = float64(totalLength) / float64(len(idx.docs))
	}

	return idx
}

// bm25Result holds the score for a single document.
type bm25Result struct {
	path  string
	score float64
}

// score ranks all documents against the query terms using BM25.
func (idx *bm25Index) score(queryTerms []string, topK int) []bm25Result {
	if len(idx.docs) == 0 || len(queryTerms) == 0 {
		return nil
	}

	N := len(idx.docs)
	results := make([]bm25Result, 0, len(idx.docs))

	for _, doc := range idx.docs {
		var score float64
		for _, term := range queryTerms {
			tf, ok := doc.tf[term]
			if !ok || tf == 0 {
				continue
			}

			df := idx.df[term]
			// IDF with smoothing to prevent negative scores for very common terms
			idf := math.Log(1 + (float64(N-df)+0.5)/(float64(df)+0.5))

			// BM25 term score
			tfNorm := float64(tf) * (bm25K1 + 1)
			denom := float64(tf) + bm25K1*(1-bm25B+bm25B*float64(doc.length)/idx.avgLength)
			score += idf * tfNorm / denom
		}

		if score > 0 {
			results = append(results, bm25Result{path: doc.path, score: score})
		}
	}

	// Sort by score descending, then by path for determinism
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].path < results[j].path
	})

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	return results
}
