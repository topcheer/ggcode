# Code Search and the Background Code Index

`code_search` is the agent's semantic code search: it ranks files by BM25
relevance to a natural-language query ("OAuth token refresh logic"), then
re-ranks the candidates, so it finds conceptually related code rather than
exact keyword matches. Use `grep` for exact patterns, `code_search` when you
only know *what* the code does.

The TUI composer's `@file` mention uses the same index for fuzzy file-name
completion once it is ready (`Code index ready: N files indexed - @ fuzzy
search enabled`).

## What gets indexed

- **Files**: source, config and doc files - `.go .js .jsx .ts .tsx .py .rb .rs
  .java .kt .swift .c .h .cpp .cc .hpp .cs .php .scala .clj .ex .exs .erl .vim
  .lua .pl .sh .bash .zsh .ps1 .sql .graphql .proto .thrift .dart .r .yaml .yml
  .toml .json .xml .html .css .scss .md .txt .rst` - up to **256 KB** each.
- **Skipped**: VCS and IDE dirs (`.git`, `.svn`, `.hg`, `.idea`, `.vscode`,
  `.ggcode`), dependency and build output (`node_modules`, `vendor`,
  `target`, `build`, `dist`, `bin`, `.cache`, `coverage`, `__pycache__`,
  `.venv`, ...), and **nested git repositories** - any sub-directory with its
  own `.git` is left to its own ggcode session.
- **Not indexed at all** when ggcode is started in your **home directory**
  (not a project tree): `code_search` then answers that it is disabled and
  points at `grep` / `search_files` with an explicit path.

## Budgets

| Limit | Value | What happens past it |
|---|---|---|
| Files | 50,000 | The walk stops; the rest is not indexed |
| Total distinct terms | 1,500,000 (`GGCODE_CODE_INDEX_MAX_TERMS`) | The index is truncated; the remaining files are remembered as *excluded* and stay out until the budget is raised |
| Single build | 5 minutes | The indexed subset is published |

If you see `term budget reached ... set GGCODE_CODE_INDEX_MAX_TERMS to
adjust, or start ggcode inside a single project`, either raise the variable
(a negative value removes the limit) or start ggcode in the sub-project you
work on instead of the monorepo root.

## How the index stays fresh

The index is built once in the background at startup and cached per working
directory under `~/.ggcode/cache/codeindex/` (reused by file mtime, so a
restart skips re-reading and re-tokenizing unchanged files). After that:

- **Edits made through ggcode** (`edit_file`, `write_file`, `apply_patch`,
  `batch_replace`, `file_ops`, `lsp_rename`, `notebook_edit`, whole-tree git
  tools) mark the touched files dirty and trigger an incremental rebuild
  after a **3-second debounce** - in every mode: TUI, `--print`/pipe, daemon,
  ACP and A2A sessions alike.
- **External changes** (your editor, `git checkout`, builds) are picked up by
  a scan every **5 minutes**: changed and new files are re-indexed, deleted
  files dropped, and files that grew past the size cap are removed.
- **Idle**: after 60 minutes without searches the in-memory index is
  released; the next `code_search` reloads it from the disk cache.
- **Several ggcode instances in one directory** share the cache file; one
  builds (file lock), the others read.

## Tool parameters

| Parameter | Description |
|---|---|
| `query` | Natural-language description of what you are looking for (required) |
| `max_results` | 1-50, default 10 |
| `path`, `type` | Only used in legacy mode (no background index) |

The first call on a cold start waits up to **2 seconds** for an in-flight
build before answering `code index is being built in the background, please
try again in a few seconds` - with a warm cache you normally never see that
message.

Design notes and invariants: [docs/design/code-index.md](../design/code-index.md).
