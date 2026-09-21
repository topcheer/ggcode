# Session Transfer (跨设备会话迁移)

Move a ggcode session from one machine to another and resume it where you
left off — full message history, compaction checkpoints, pinned/tags, and
usage metadata travel with it.

This implements the "session handoff / portable agent state" pattern: the
session store (`~/.ggcode/sessions/`) is machine-local, so sessions are
normally stuck on the device that created them. Bundles make a session a
portable artifact.

## Export

```bash
# Export by session ID (see `ggcode daemon --resume-picker` or the TUI
# session list for IDs):
ggcode daemon session export <session-id>

# Custom output path:
ggcode daemon session export <session-id> -o ~/backups/my-session.ggsession
```

The bundle is a single gzip-compressed JSON file containing the raw session
records plus the index metadata. Copy it anywhere (scp, cloud drive, USB).

## Import

```bash
ggcode daemon session import ~/backups/my-session.ggsession
```

On import:

- The session is installed into the local store and shows up in the resume
  picker and session list immediately.
- **Workspace remap**: by default the session is remapped to the current
  working directory, because the original absolute workspace path usually
  does not exist on the new machine. Use `--workspace /path` to choose a
  different directory, or `--keep-workspace` to preserve the original path.
- **No overwrites**: if a session with the same ID already exists locally,
  the import gets a fresh ID (both copies coexist).
- Resume hint is printed: `ggcode --resume <session-id>`.

## Guarantees

- Export is a lossless raw-record copy (including pre-compaction history).
- Import rewrites each record's session ID and remaps the workspace of meta
  records; malformed bundles are rejected atomically (no partial files).
- Bundle IDs are validated — a hostile bundle cannot write outside the
  session store directory.
- Both gzip-compressed and plain-JSON bundles are accepted (magic-byte
  detection).

## Notes

- The bundle carries conversation content in plain text (inside the gzip
  stream). Treat bundle files with the same care as session files.
- Bundles are forward-compatible: `import` rejects bundles written by newer
  schema versions instead of mis-parsing them.
