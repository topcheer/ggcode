package restart

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// #5367: "/restart reports OK but the process never exec-s the rebuilt
// binary". The livelock half (re-arm starvation behind a continuously active
// turn) was fixed by the restart arm hard cap. This file closes the remaining
// acceptance gap: "OK" must mean the image actually swapped, with the
// actually-running identity reported after the exec.
//
// Mechanism: ExecRestart stamps the intended exec target's identity (path,
// device, inode, vcs revision) into GGCODE_RESTART_INTENT, which survives the
// in-process syscall.Exec. On the next startup, StartupVerifyNotice compares
// the running binary's identity against the intent and reports the actually
// running vcs revision - a loud banner on mismatch (wrong-path exec silently
// succeeding is invisible without this), and a quiet confirmation line when
// the swap matched.
//
// Revision source note: both production exec sites (TUI execRestart and the
// daemon restart path) exec the current process's own executable via
// ResolveBinary(), so the pre-exec revision is the running process's own
// runtime/debug vcs.revision; no buildinfo file read is required.

// IntentEnvKey carries the restart intent across the exec boundary.
const IntentEnvKey = "GGCODE_RESTART_INTENT"

// Intent is the recorded pre-exec identity of the intended exec target.
type Intent struct {
	Path     string
	Dev      uint64
	Inode    uint64
	Revision string // vcs.revision from runtime/debug, first 12 chars, "" if unknown
	ArmedAt  time.Time
}

// SelfRevision returns the running process's own vcs.revision (short form),
// or "" when the binary was built without VCS stamping.
func SelfRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi == nil {
		return ""
	}
	for _, kv := range bi.Settings {
		if kv.Key == "vcs.revision" && kv.Value != "" {
			if len(kv.Value) > 12 {
				return kv.Value[:12]
			}
			return kv.Value
		}
	}
	return ""
}

// EncodeIntent serializes an Intent into the env-var payload form
// path|dev|inode|revision|unixNano.
func EncodeIntent(in Intent) string {
	return strings.Join([]string{
		in.Path,
		strconv.FormatUint(in.Dev, 10),
		strconv.FormatUint(in.Inode, 10),
		in.Revision,
		strconv.FormatInt(in.ArmedAt.UnixNano(), 10),
	}, "|")
}

// DecodeIntent parses a GGCODE_RESTART_INTENT payload. It returns ok=false
// for malformed input rather than erroring so callers can degrade to silence
// on legacy/corrupted values.
func DecodeIntent(s string) (in Intent, ok bool) {
	parts := strings.Split(s, "|")
	if len(parts) != 5 || parts[0] == "" {
		return Intent{}, false
	}
	dev, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return Intent{}, false
	}
	inode, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return Intent{}, false
	}
	nanos, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return Intent{}, false
	}
	return Intent{Path: parts[0], Dev: dev, Inode: inode, Revision: parts[3], ArmedAt: time.Unix(0, nanos)}, true
}

// Identity captures the current identity of a binary path.
type Identity struct {
	Path     string
	Dev      uint64
	Inode    uint64
	Revision string
}

// StatIdentity resolves the current identity of path (symlinks resolved for
// the display path; dev/inode read from the file). ok=false when the file
// cannot be stat'ed.
func StatIdentity(path string) (id Identity, ok bool) {
	if path == "" {
		return Identity{}, false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved == "" {
		resolved = path
	}
	st, err := os.Stat(path)
	if err != nil {
		return Identity{Path: resolved}, false
	}
	id.Path = resolved
	id.Dev, id.Inode = devInode(st)
	id.Revision = SelfRevision()
	return id, true
}

// CurrentIdentity captures the running process's own binary identity.
func CurrentIdentity() (id Identity, ok bool) {
	execPath, err := os.Executable()
	if err != nil {
		return Identity{}, false
	}
	return StatIdentity(execPath)
}

// StartupVerifyNotice implements the post-exec self-check. It is called once
// per process start. Behavior:
//
//   - no intent env (fresh start, not a restart): "" and consumed=false
//   - intent present and identity matches (dev+inode; on rebuild-in-place
//     inode churn, vcs revision decides): confirmation line reporting the
//     running revision
//   - intent present but running image is a DIFFERENT binary (wrong-path
//     exec silently succeeded): loud restart-warn banner naming both paths -
//     this is the #5367 acceptance criterion
//
// The caller must clear the intent env after acting on the notice
// (ConsumeIntent) so a later manual restart-without-exec does not re-show a
// stale banner.
func StartupVerifyNotice() (notice string, matched bool) {
	raw := os.Getenv(IntentEnvKey)
	if raw == "" {
		return "", false
	}
	in, ok := DecodeIntent(raw)
	if !ok {
		return "", false
	}
	cur, ok := CurrentIdentity()
	if !ok {
		return fmt.Sprintf("restart-warn: intended exec target %s could not be compared - running binary identity unavailable (running revision %s)", in.Path, revisionOrUnknown(SelfRevision())), false
	}
	sameFile := in.Dev != 0 && cur.Dev == in.Dev && cur.Inode == in.Inode
	// Rebuild-in-place: same path, new inode - judge by vcs revision when
	// both sides carry one.
	if !sameFile && cur.Path == in.Path && cur.Revision != "" && in.Revision != "" {
		sameFile = cur.Revision == in.Revision
	}
	if sameFile {
		return fmt.Sprintf("Restart verified: running image %s (revision %s) matches the restart intent", cur.Path, revisionOrUnknown(cur.Revision)), true
	}
	return fmt.Sprintf(
		"restart-warn: RESTART DID NOT SWAP THE INTENDED IMAGE - running %s (revision %s), intended %s (revision %s, armed %s ago). The previous restart armed OK but exec-ed a different binary; the rebuilt image never went live. Verify how this instance was launched (installed copy, wrapper, PATH shadow) and restart from the intended binary.",
		cur.Path, revisionOrUnknown(cur.Revision), in.Path, revisionOrUnknown(in.Revision), time.Since(in.ArmedAt).Round(time.Second)), false
}

// ConsumeIntent clears the intent env so the notice is shown exactly once.
func ConsumeIntent() {
	_ = os.Unsetenv(IntentEnvKey)
}

// StampIntent records the pre-exec identity of the exec target. Both
// production callers exec their own executable, so stat the target for
// dev/inode and take the revision from the running build.
func StampIntent(targetPath string) {
	id, ok := StatIdentity(targetPath)
	in := Intent{Path: targetPath, ArmedAt: time.Now()}
	if ok {
		in.Dev, in.Inode, in.Path = id.Dev, id.Inode, id.Path
	}
	in.Revision = SelfRevision()
	_ = os.Setenv(IntentEnvKey, EncodeIntent(in))
}

func revisionOrUnknown(rev string) string {
	if rev == "" {
		return "unknown"
	}
	return rev
}
