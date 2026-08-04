# Dependency Graph Analysis (`dep_graph`)

The `dep_graph` tool provides package-level dependency intelligence for Go projects. It builds an import graph from source files and supports architectural queries.

## Actions

### `overview` (default)
Returns a summary of all packages in the module with their import counts, dependent counts, and file counts. Use to understand the overall architecture before making changes.

### `reverse_deps`
Finds all packages that depend on a target package (directly or transitively). Use before modifying a package to understand blast radius.

**Parameters:**
- `target` (required): Package import path suffix, last path component, or full path. Matches any package whose import path ends with this value.

### `cycles`
Detects import cycles in the module. Import cycles cause Go compilation errors and indicate architecture violations.

### `hotspots`
Returns packages ranked by fan-in (number of packages that import them). High fan-in packages are architectural bottlenecks - changes to them have the broadest impact.

## Example Usage

```
# Get overview of the entire module
dep_graph(action="overview")

# What packages depend on internal/util?
dep_graph(action="reverse_deps", target="util")

# Check for import cycles
dep_graph(action="cycles")

# Find the most depended-upon packages
dep_graph(action="hotspots")
```

## Scope

- Only analyzes **intra-module** imports (packages within the same Go module)
- Skips `_test.go` files (test dependencies are ephemeral)
- Uses `parser.ImportsOnly` mode for fast parsing
- No external dependencies beyond `golang.org/x/mod/modfile`

## Competitor Comparison

| Feature | ggcode | Cursor | Sourcegraph Cody | Aider |
|---------|--------|--------|-------------------|-------|
| Package-level dependency graph | Yes | Implicit (indexing) | Yes (via search) | No |
| Import cycle detection | Yes | No | No | No |
| Reverse dependency lookup | Yes | No | Yes (via search) | No |
| Fan-in/fan-out metrics | Yes | No | No | No |
