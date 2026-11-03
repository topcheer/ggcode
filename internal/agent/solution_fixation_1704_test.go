package agent

import "testing"

func TestAgentMutationEditToolsIncludesFileOps1704(t *testing.T) {
	if !agentMutationEditTools["file_ops"] {
		t.Fatal("file_ops must be in agentMutationEditTools")
	}
}
