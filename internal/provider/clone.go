package provider

// ClonableWithModel is implemented by providers that support creating a copy
// with a different model name. Used by named subagents for model overrides.
type ClonableWithModel interface {
	CloneWithModel(model string) Provider
}

// CloneProviderWithModel returns a copy of the provider with the given model.
// If the provider doesn't implement ClonableWithModel, the original is returned.
func CloneProviderWithModel(p Provider, model string) Provider {
	if c, ok := p.(ClonableWithModel); ok && model != "" {
		return c.CloneWithModel(model)
	}
	return p
}
