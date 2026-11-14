package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

// #5642 (C2): the env-face mount fields (skills, mobile) must serialize
// verbatim and stay omitempty-clean when absent - pins the peer contract
// for the P2 additions that have no wiring-level tests.
func TestRuntimeEnvMountsSerialization(t *testing.T) {
	ev := RuntimeEnv{
		Workspace: "/w",
		Provider:  "test",
		Skills: []RuntimeEnvService{
			{Name: "deploy-web", State: "project"},
			{Name: "draft", State: "staging"},
		},
		MCPServers: []RuntimeEnvService{{Name: "gitea", State: "connected", Tools: 12}},
		IMAdapters: []RuntimeEnvService{{Name: "ggcode03", State: "connected"}},
		Mobile:     &RuntimeMobileState{Connected: true, RelayURL: "https://r.example", SessionID: "s-1"},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"skills":[{"name":"deploy-web","state":"project"}`,
		`"name":"draft","state":"staging"`,
		`"mcp_servers":[{"name":"gitea","state":"connected","tools":12}]`,
		`"im_adapters":[{"name":"ggcode03","state":"connected"}]`,
		`"mobile":{"connected":true,"relay_url":"https://r.example","session_id":"s-1"}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("env JSON missing %q\n got: %s", want, s)
		}
	}

	// omitempty cleanliness: empty env must not emit empty mount arrays.
	bare, _ := json.Marshal(RuntimeEnv{Workspace: "/w"})
	for _, absent := range []string{"skills", "mcp_servers", "im_adapters", "mobile"} {
		if strings.Contains(string(bare), absent) {
			t.Errorf("empty env must omit %q, got: %s", absent, string(bare))
		}
	}
}
