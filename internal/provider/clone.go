package provider

type ClonableWithModel interface {
	CloneWithModel(model string) Provider
}

func CloneProviderWithModel(p Provider, model string) Provider {
	if c, ok := p.(ClonableWithModel); ok && model != "" {
		return c.CloneWithModel(model)
	}
	return p
}
