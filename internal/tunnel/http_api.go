package tunnel

// HTTPEndpoint converts a relay ws/wss/http/https base URL into an HTTP API
// endpoint under the same relay host.
func HTTPEndpoint(relayURL, path string) (string, error) {
	return shareEndpoint(relayURL, path)
}
