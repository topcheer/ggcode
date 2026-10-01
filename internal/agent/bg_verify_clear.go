package agent

// Background verify-job registry (#2992 case 2).
//
// The inline debt-clearing path at the agent.go wiring site only recognizes
// run_command. But the recommended long-test workflow is start_command +
// wait_command / read_command_output (#595/#1152/#1153): those completions
// never reached verifyDebt / editPropagation / fileChurn, so background-test
// workflows accumulated fake "N unverified edits" warnings for the entire
// run. This registry mirrors prematureSuccess's job registry (#1153) but is
// deliberately separate: that one is consumed-once for its own grading and
// must not be drained by other consumers.

import (
	"encoding/json"
	"sync"
)

type bgVerifyRegistry struct {
	mu   sync.Mutex
	jobs map[string]string // job_id -> command line
}

func newBgVerifyRegistry() *bgVerifyRegistry {
	return &bgVerifyRegistry{jobs: make(map[string]string)}
}

// register records a successfully started background job's command.
func (r *bgVerifyRegistry) register(jobID, cmd string) {
	if r == nil || jobID == "" || cmd == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Bounded like psMaxTrackedJobs: stale entries from abandoned jobs must
	// not accumulate across a long run.
	for len(r.jobs) >= 64 {
		for k := range r.jobs {
			delete(r.jobs, k)
			break
		}
	}
	r.jobs[jobID] = cmd
}

// take removes and returns the command for jobID (consumed-once so repeated
// waits/polls do not re-clear debt for the same finished job).
func (r *bgVerifyRegistry) take(jobID string) (string, bool) {
	if r == nil || jobID == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd, ok := r.jobs[jobID]
	if ok {
		delete(r.jobs, jobID)
	}
	return cmd, ok
}

// bgVerifyExtractJobID pulls "job_id" (falling back to "task_id", mirroring
// prematureSuccess) from raw tool arguments.
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
