# Duplicate Code Detection (Check #42)

## Overview

Post-edit AST-based detection of structurally similar function bodies (code clones) introduced by AI agent edits.

## Background

AI agents frequently introduce copy-paste code patterns: near-identical function bodies, duplicated logic blocks, or structurally repeated code fragments. Studies show 5-15% of code in typical projects is duplicated (Bellon et al.), and LLM-generated code has higher duplication rates because models generate by pattern matching rather than refactoring.

No major coding agent (Claude Code, Cursor, Cline, Aider, Windsurf) detects code duplication at write-time. They rely on external linters (jscpd, dupl, SonarQube) run post-hoc.

## Implementation

**File**: `internal/agent/duplicate_code_check.go`
**Registration**: Check #42 in `internal/agent/write_integrity.go`

### Detection Strategy

1. **AST Parsing**: Parse the edited Go file to extract all function/method declarations
2. **Token Normalization**: Convert function bodies to normalized token sequences:
   - Identifiers replaced with `v` (unexported) or `E` (exported)
   - Literals replaced with type placeholders (`STR`, `INT`, `FLOAT`)
   - Structural tokens preserved (`if`, `for`, `return`, `call`, etc.)
3. **Similarity Computation**: Jaccard-like metric using token frequency maps
4. **Threshold**: 85% similarity for near-duplicates, 100% for exact clones

### Clone Types Detected

- **Type 1 (Exact)**: Functions with identical normalized token sequences
- **Type 2 (Renamed)**: Functions with same structure but different identifiers

### Delta-Awareness

Only flags pairs where at least one function was introduced or modified by the current edit. Uses `extractFuncNames()` on old content to determine which functions pre-existed.

### Thresholds

| Parameter | Value | Purpose |
|-----------|-------|---------|
| `duplicateMinStmts` | 3 | Minimum body statements (excludes getters/setters) |
| `duplicateMinTokens` | 20 | Minimum normalized tokens (excludes trivial functions) |
| `duplicateSimilarityThreshold` | 0.85 | Minimum token overlap ratio |
| `duplicateCodeMaxWarnings` | 3 | Cap per write |

### Performance

- O(n^2) pairwise comparison where n = number of functions in file
- Typical files have <30 functions, so <900 comparisons
- Each comparison is O(m) where m = unique token types
- Total: <1ms for typical files, pure in-memory, no external dependencies

### Multi-Language Support

Currently Go-only (uses `go/parser` and `go/ast`). The normalization approach generalizes to other languages but requires language-specific token extraction.
