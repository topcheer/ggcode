package mcp

func (t ToolDefinition) DeclaredReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

func (t ToolDefinition) DeclaredDestructive() bool {
	if t.Annotations == nil {
		return true
	}
	if t.DeclaredReadOnly() {

		return false
	}
	if t.Annotations.DestructiveHint != nil {
		return *t.Annotations.DestructiveHint
	}
	return true
}

func (t ToolDefinition) ContradictoryHints() bool {
	return t.DeclaredReadOnly() &&
		t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
}

func (t ToolDefinition) annotationAllowsReadOnly() bool {
	return t.DeclaredReadOnly() && !t.ContradictoryHints()
}
