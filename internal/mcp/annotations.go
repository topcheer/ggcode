package mcp

// Tool annotation semantics (MCP 2025-06-18, "Tool Annotations"):
//
//	annotations tell the client how a tool BEHAVES so it can make UI and
//	scheduling decisions: readOnlyHint (default false), destructiveHint
//	(default true, meaningful only when readOnlyHint is false),
//	idempotentHint (default false), openWorldHint (default true).
//
// They are self-declared hints, NOT security guarantees - the spec
// explicitly says clients should not blindly trust them. Our trust model:
//
//   - An explicit readOnlyHint=true REFINES the read-only-server blocking
//     decision (#996/#998 name heuristic): the heuristic is a guess from
//     the tool NAME; the annotation is the server's own declaration. A
//     server that opted into `read_only: true` mode and declares a tool
//     read-only gets that tool allowed even when the name fragment-matches
//     a write keyword ("deploy_status", "set_config_cache", ...).
//   - The override is refused when the declarations CONTRADICT: an explicit
//     destructiveHint=true alongside readOnlyHint=true means the server
//     cannot decide what it offers - the more dangerous hint wins and the
//     name heuristic stays in force.
//   - Everything else (approval prompts, non-read-only servers) is
//     unchanged: hints never AUTO-ALLOW a tool on a server the user has
//     not marked read-only.
func (t ToolDefinition) DeclaredReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

// DeclaredDestructive reports whether the annotations explicitly declare
// the tool destructive. Per spec destructiveHint defaults to true and is
// only meaningful when readOnlyHint is false, so "absent" also reports
// true only when the tool is not declared read-only.
func (t ToolDefinition) DeclaredDestructive() bool {
	if t.Annotations == nil {
		return true
	}
	if t.DeclaredReadOnly() {
		// readOnlyHint=true makes destructiveHint irrelevant per spec;
		// contradicting declarations are resolved as dangerous by the
		// caller via ContradictoryHints below.
		return false
	}
	if t.Annotations.DestructiveHint != nil {
		return *t.Annotations.DestructiveHint
	}
	return true
}

// ContradictoryHints reports a server that declared readOnlyHint=true AND
// destructiveHint=true at once. We treat the more dangerous hint as
// authoritative: such a tool never gets the annotation override.
func (t ToolDefinition) ContradictoryHints() bool {
	return t.DeclaredReadOnly() &&
		t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
}

// annotationAllowsReadOnly reports whether the annotation override should
// lift a read-only-server name-heuristic block for this tool.
func (t ToolDefinition) annotationAllowsReadOnly() bool {
	return t.DeclaredReadOnly() && !t.ContradictoryHints()
}
