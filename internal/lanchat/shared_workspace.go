package lanchat

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SharedWorkspaceAlert returns a system-prompt section warning that other
// online ggcode agents are working in the SAME workspace directory on this
// machine, together with the iron rules that keep concurrent agents from
// clobbering each other. Returns "" when no collision exists (single-agent
// workspace, empty hub, or no lanchat at all) so the common case pays nothing.
//
// hasVCS selects the rule set: version-controlled workspaces get the
// worktree+PR discipline; non-VCS workspaces get coordinate-first rules
// because nothing there can be reverted.
//
// lang follows the runtime language ("zh-CN"/"en"; empty = English).
func SharedWorkspaceAlert(h *Hub, selfNodeID, workspace string, hasVCS bool, lang string) string {
	if h == nil || workspace == "" {
		return ""
	}
	peers := sharedWorkspacePeers(h, selfNodeID, workspace)
	if len(peers) == 0 {
		return ""
	}
	zh := strings.HasPrefix(strings.ToLower(lang), "zh")
	names := make([]string, 0, len(peers))
	for _, p := range peers {
		n := p.AgentNick
		if n == "" {
			n = p.HumanNick
		}
		if n == "" {
			n = p.NodeID
		}
		names = append(names, n)
	}

	var b strings.Builder
	if zh {
		fmt.Fprintf(&b, "## 共享工作区警示（并发 agent 在线）\n\n")
		fmt.Fprintf(&b, "当前工作区 %s 上另有 %d 个在线 ggcode agent 实例正在工作：%s。\n", workspace, len(peers), strings.Join(names, "、"))
		fmt.Fprintf(&b, "其他 agent 可能正在读写同一批文件。共享工作区铁律：\n")
		if hasVCS {
			b.WriteString("- 严禁在共享主树上执行 git stash/checkout/reset——会摧毁其他 agent 的未提交改动。\n")
			b.WriteString("- 修改代码必须走独立 git worktree 隔离，经 PR 流程合入（merge commit，不删分支）。\n")
			b.WriteString("- 只 stage 自己创建/修改的文件；严禁 git add -A / git add . 扫全树。\n")
		} else {
			b.WriteString("- 本工作区不受 git/svn 版本控制，改动无法回滚——动手前先协调。\n")
			b.WriteString("- 优先新建文件而非整体改写共享文件（write_file 会整文件覆盖）；大改前在 lanchat 通告。\n")
			b.WriteString("- 避免批量移动/删除；触及他人可能在用的路径前先 DM 确认。\n")
		}
		b.WriteString("- 编辑前重读文件；发现他人近期改动时，DM 归属席确认而不是直接覆盖。\n")
	} else {
		fmt.Fprintf(&b, "## SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE\n\n")
		fmt.Fprintf(&b, "%d other online ggcode agent instance(s) are working in this same workspace (%s): %s.\n", len(peers), workspace, strings.Join(names, ", "))
		b.WriteString("Other agents may be reading and writing the same files right now. Iron rules:\n")
		if hasVCS {
			b.WriteString("- NEVER run git stash/checkout/reset on the shared tree — it clobbers other agents' uncommitted work.\n")
			b.WriteString("- Make code changes in an isolated git worktree and land them via PR (merge commit, keep branch).\n")
			b.WriteString("- Stage only files you created or edited; NEVER `git add -A` / `git add .` over the shared tree.\n")
		} else {
			b.WriteString("- This workspace is NOT under git/svn version control — changes cannot be reverted. Coordinate before acting.\n")
			b.WriteString("- Prefer creating new files over rewriting shared ones (write_file overwrites whole files); announce large edits in lanchat first.\n")
			b.WriteString("- Avoid bulk move/delete; DM the owner before touching paths others may be using.\n")
		}
		b.WriteString("- Re-read files before editing; when you see recent foreign changes, DM the owning agent instead of overwriting.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// sharedWorkspacePeers returns online participants whose workspace path
// resolves to the same directory as ours, excluding ourselves. Path
// equality on the local filesystem implies the same machine (a remote node
// cannot mount the identical absolute path in practice; the alert is
// advisory anyway).
func sharedWorkspacePeers(h *Hub, selfNodeID, workspace string) []Participant {
	want := filepath.Clean(workspace)
	var peers []Participant
	for _, p := range h.Participants() {
		if !p.Online || p.NodeID == selfNodeID || p.NodeID == "" {
			continue
		}
		if p.Workspace == "" {
			continue
		}
		if filepath.Clean(p.Workspace) == want {
			peers = append(peers, p)
		}
	}
	return peers
}
