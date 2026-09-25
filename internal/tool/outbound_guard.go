package tool

import (
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/security"
)

// Outbound-boundary secret guard.
//
// web_fetch url, web_search query and browser navigate url are the agent's
// scoped network egress points. Secrets read earlier in a session (config
// files, .env, source) — or steered into an argument by injected content —
// must not leave the machine embedded in those arguments. This guard checks
// the argument against the shared secret-pattern list and blocks the call
// with an agent-actionable error (pattern name + masked sample) so the model
// can correct the argument instead of retrying blind.
//
// Scope note: run_command remains guarded by the permission policy and
// dangerous-command detection, not by this pattern scan (arbitrary shell
// strings would false-positive heavily).
//
// Opt-out for credential-bearing endpoints the user intentionally targets
// (e.g. a webhook URL that embeds its own token):
//
//	GGCODE_OUTBOUND_SECRET_GUARD=off
var outboundSecretGuard = initOutboundSecretGuard()

func initOutboundSecretGuard() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_OUTBOUND_SECRET_GUARD"))) {
	case "off", "0", "false", "disabled":
		return false
	default:
		return true
	}
}

// guardOutboundSecrets inspects an outbound tool argument and returns a
// non-empty error message when it contains secret-shaped material.
// field is the argument name ("url", "query") for precise agent feedback.
func guardOutboundSecrets(field, value string) string {
	if !outboundSecretGuard {
		return ""
	}
	findings := security.CheckOutboundSecrets(value)
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "blocked: outbound guard found secret-shaped material in %s; the request was NOT sent ", field)
	b.WriteString("(OWASP LLM02 exfiltration guard). Findings:")
	for i, f := range findings {
		if i == 3 {
			fmt.Fprintf(&b, " … (+%d more)", len(findings)-3)
			break
		}
		fmt.Fprintf(&b, " [%s %s]", f.Name, f.Masked)
	}
	b.WriteString(". Remove the secret from the argument and retry (reference it indirectly instead of embedding it). If this is a credential-bearing endpoint you intend to call (e.g. a webhook URL embedding its own token), ask the user to set GGCODE_OUTBOUND_SECRET_GUARD=off for this session.")
	return b.String()
}
