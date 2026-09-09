package agent

import "testing"

// #1704 case 5: file_ops is a mutation - failed file_ops counts as an
// edit failure for solution fixation.
func TestAgentMutationEditToolsIncludesFileOps1704(t *testing.T) {
	if !agentMutationEditTools["file_ops"] {
		t.Fatal("file_ops must be in agentMutationEditTools")
	}
}
