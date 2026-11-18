package restart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// intentFor captures the pre-exec identity of the exec target. Both
// production callers exec their own executable, so stat the target for
// dev/inode and take the revision from the running build.
func intentFor(binary string) Intent {
	id, ok := StatIdentity(binary)
	in := Intent{Path: binary, ArmedAt: time.Now()}
	if ok {
		in.Dev, in.Inode, in.Path = id.Dev, id.Inode, id.Path
	}
	in.Revision = SelfRevision()
	return in
}

func ExecRestart(binary string, args []string, env []string) error {
	if binary == "" {
		b, err := ResolveBinary()
		if err != nil {
			return fmt.Errorf("resolve binary: %w", err)
		}
		binary = b
	}
	if len(env) == 0 {
		env = os.Environ()
	}

	// #5367: record the exec target identity so the post-exec startup
	// self-check (StartupVerifyNotice) can prove the image actually swapped.
	// Drop any stale intent from an earlier arm first.
	idx := -1
	for i, kv := range env {
		if strings.HasPrefix(kv, IntentEnvKey+"=") {
			idx = i
			break
		}
	}
	intent := EncodeIntent(intentFor(binary))
	if idx >= 0 {
		env[idx] = IntentEnvKey + "=" + intent
	} else {
		env = append(env, IntentEnvKey+"="+intent)
	}

	// #5753: ensure a durable arm record exists before the exec attempt so
	// a failed execve leaves a trace the nudge-time/startup handshake can
	// alarm on. The tool arm path normally wrote it already; this covers
	// slash-command and programmatic exec restarts.
	if _, ok := LoadArmRecord(); !ok {
		if _, werr := WriteArmRecord(binary, ""); werr != nil {
			debug.Log("restart", "exec-time arm record write failed: %v", werr)
		}
	}

	debug.Log("restart", "exec: %s %v", binary, args)
	err := ExecSelf(binary, args, env)
	if err != nil {
		// The process is still alive on a failed execve: annotate the arm
		// record so the resume nudge (which will fire in THIS process)
		// reports ARMED-BUT-NOT-EXECUTED with the errno instead of
		// presenting as a successful restart (#5753).
		RecordExecFailure("exec", err)
	}
	return err
}

func ResolveBinary() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return execPath, nil
	}
	return resolved, nil
}
