// Command fakelsp is a minimal offline LSP server used by
// zz_covboost_test.go. It speaks Content-Length framed JSON-RPC over
// stdio and answers each request by method name with a canned result.
// The document URI used in location results is learned from the first
// textDocument/didOpen notification.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

type envelope struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func writeFrame(w io.Writer, body string) {
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func main() {
	var mu sync.Mutex
	uri := "file:///ws/main.go" // replaced by the first didOpen URI
	rng := `{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}`
	rng2 := `{"start":{"line":5,"character":0},"end":{"line":5,"character":9}}`

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for {
		header, err := in.ReadString('\n')
		if err != nil {
			return
		}
		header = strings.TrimRight(header, "\r\n")
		if !strings.HasPrefix(header, "Content-Length:") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:")))
		if err != nil || n <= 0 || n > 1<<20 {
			return
		}
		// consume the blank separator line
		if _, err := in.ReadString('\n'); err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(in, buf); err != nil {
			return
		}
		var env envelope
		if err := json.Unmarshal(buf, &env); err != nil {
			continue
		}
		if env.ID == nil {
			// notification: no response expected, but didOpen tells us
			// the real document URI to use in location results.
			if env.Method == "textDocument/didOpen" {
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				if json.Unmarshal(env.Params, &p) == nil && p.TextDocument.URI != "" {
					mu.Lock()
					uri = p.TextDocument.URI
					mu.Unlock()
				}
			}
			continue
		}
		mu.Lock()
		u := uri
		mu.Unlock()
		var result string
		switch env.Method {
		case "initialize":
			result = `{"capabilities":{}}`
		case "textDocument/hover":
			result = fmt.Sprintf(`{"contents":{"kind":"markdown","value":"func Foo() int"},"range":%s}`, rng)
		case "textDocument/definition":
			result = fmt.Sprintf(`[{"uri":%q,"range":%s}]`, u, rng)
		case "textDocument/references":
			result = fmt.Sprintf(`[{"uri":%q,"range":%s},{"uri":%q,"range":%s}]`, u, rng, u, rng2)
		case "textDocument/implementation":
			result = fmt.Sprintf(`[{"uri":%q,"range":%s}]`, u, rng)
		case "textDocument/documentHighlight":
			result = fmt.Sprintf(`[{"range":%s,"kind":2}]`, rng)
		case "textDocument/documentSymbol":
			result = fmt.Sprintf(`[{"name":"Foo","kind":12,"range":%s,"children":[{"name":"inner","kind":8,"range":%s,"children":[]}]}]`, rng, rng2)
		case "textDocument/prepareCallHierarchy":
			result = fmt.Sprintf(`[{"name":"main","kind":12,"detail":"func()","uri":%q,"range":%s,"selectionRange":%s}]`, u, rng, rng)
		case "callHierarchy/incomingCalls":
			result = fmt.Sprintf(`[{"from":{"name":"caller","kind":12,"uri":%q,"range":%s,"selectionRange":%s},"fromRanges":[%s]}]`, u, rng, rng, rng)
		case "callHierarchy/outgoingCalls":
			result = fmt.Sprintf(`[{"to":{"name":"callee","kind":12,"uri":%q,"range":%s,"selectionRange":%s},"fromRanges":[%s]}]`, u, rng, rng, rng)
		case "textDocument/diagnostic":
			result = fmt.Sprintf(`{"kind":"full","items":[{"range":%s,"severity":1,"message":"undefined: x","source":"fake"}]}`, rng)
		default:
			result = `null`
		}
		writeFrame(out, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *env.ID, result))
		if out.Flush() != nil {
			return
		}
	}
}
