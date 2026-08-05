# Struct Tag Consistency Intelligence

## Problem

AI coding agents generating Go structs for JSON/API serialization frequently
produce struct tags that cause runtime API inconsistencies:

1. **PascalCase json tags** (`json:"FieldName"`): JSON convention uses
   camelCase or snake_case. A capitalized tag almost always means the
   developer forgot to lowercase it.

2. **Redundant json tags** (`json:"Name"` on field `Name`): Go's default
   JSON serialization already uses the field name verbatim. The tag is
   useless and likely masks a typo or forgotten rename.

3. **Inconsistent tag coverage**: When most exported fields in a struct
   have json tags but one or two don't, the untagged fields serialize with
   their PascalCase Go names, producing mixed-case JSON keys
   (e.g. `{"user_name":"bob", "Email":"bob@x.com"}`).

## Competitor Analysis

| Tool | Detects struct tag issues? | At write time? |
|------|--------------------------|----------------|
| Claude Code | No | N/A |
| Cursor | No | N/A |
| Cline/OpenHands | No | N/A |
| Aider | No | N/A |
| Windsurf | No | N/A |
| Devin | No | N/A |
| `go vet` | No | N/A |
| staticcheck | No (does not flag these patterns) | N/A |

**Gap**: No tool or AI agent detects struct tag inconsistencies at write
time. The bugs only surface during API integration testing or production.

## Design

### Detection approach

AST-based scan of Go struct declarations using `go/parser` and `go/ast`.

1. Parse the file into an AST.
2. Walk all `*ast.StructType` nodes.
3. For each struct, collect field metadata (name, json tag value, exported
   status, line number).
4. Apply three independent checks.

### Check 1: PascalCase tag names

For each field with a json tag, extract the base name (before comma). If
the first character is uppercase ASCII, flag it with a suggested
lowercased alternative.

**False positive rate**: Zero. JSON field names are never PascalCase by
convention.

### Check 2: Redundant tags

If the json tag base name exactly matches the Go field name, the tag is
redundant. Flag it.

**False positive rate**: Near zero. The only legitimate use is explicit
documentation, but even then the warning is useful.

### Check 3: Inconsistent coverage

Count exported fields with json tags vs without. If >= 2 fields have tags
and >= 1 exported field lacks a tag, flag the untagged field(s).

**False positive mitigation**:
- Structs with < 2 tagged fields are skipped (may not be JSON models).
- `json:"-"` (explicit omission) is respected.
- Embedded/anonymous fields are skipped.
- Unexported fields are skipped.

### Constraints

- **Zero LLM cost**: Pure AST/regex analysis.
- **No external dependencies**: Uses only `go/ast`, `go/parser`, `reflect`,
  `strconv`, `strings` from the standard library.
- **Cyclomatic complexity**: All functions under 15.
- **Warning cap**: Maximum 8 warnings per file, with truncation notice.
- **Test files excluded**: Struct tags in tests are often fixtures.

## Files

- `internal/agent/struct_tag_check.go` -- check implementation (~200 lines)
- `internal/agent/struct_tag_check_test.go` -- 20 test cases
- `internal/agent/write_integrity.go` -- registration (1 line added)

## Registration

```go
{Name: "struct-tag-consistency", Langs: []Language{LangGo}, Run: sliceCheck(checkStructTagConsistency)},
```
