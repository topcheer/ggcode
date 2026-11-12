package agent

import "os"

// readAgentAndProcessingSources returns agent.go concatenated with the
// #5543 extraction files, so source-structure pins keep scanning moved
// bodies (same contract, shape-independent file list).
func readAgentAndProcessingSources() ([]byte, error) {
	a, err := os.ReadFile("agent.go")
	if err != nil {
		return nil, err
	}
	for _, extra := range []string{"chat_response_processing.go", "chat_tool_loop.go", "chat_turn_error.go", "conversation_loop.go", "run_stream_setup.go", "stream_chat.go", "watcher_registry.go", "edited_paths.go"} {
		b, err := os.ReadFile(extra)
		if err != nil {
			continue // optional until later knives populate them
		}
		a = append(a, '\n')
		a = append(a, b...)
	}
	return a, nil
}
