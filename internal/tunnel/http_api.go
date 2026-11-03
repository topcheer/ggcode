package tunnel

func HTTPEndpoint(relayURL, path string) (string, error) {
	return shareEndpoint(relayURL, path)
}
