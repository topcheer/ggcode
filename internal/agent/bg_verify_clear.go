package agent

import (
	"encoding/json"
	"sync"
)

type bgVerifyRegistry struct {
	mu   sync.Mutex
	jobs map[string]string
}

func newBgVerifyRegistry() *bgVerifyRegistry {
	return &bgVerifyRegistry{jobs: make(map[string]string)}
}

func (r *bgVerifyRegistry) register(jobID, cmd string) {
	if r == nil || jobID == "" || cmd == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for len(r.jobs) >= 64 {
		for k := range r.jobs {
			delete(r.jobs, k)
			break
		}
	}
	r.jobs[jobID] = cmd
}

func (r *bgVerifyRegistry) peek(jobID string) (string, bool) {
	if r == nil || jobID == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd, ok := r.jobs[jobID]
	return cmd, ok
}

func (r *bgVerifyRegistry) remove(jobID string) {
	if r == nil || jobID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, jobID)
}

func bgVerifyExtractJobID(args json.RawMessage) string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	if s := extractStringArg(m, "job_id"); s != "" {
		return s
	}
	return extractStringArg(m, "task_id")
}
