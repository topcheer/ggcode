package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

type precompactState struct {
	done      chan struct{}
	cancel    context.CancelFunc
	startedAt time.Time
	startTok  int
	snapshot  ctxpkg.CompactSnapshot
	result    ctxpkg.CompactResult
	err       error
	cancelled atomic.Bool
}

const (
	precompactBackgroundTimeout = 180 * time.Second
	precompactStartDelay        = 6 * time.Second
)

var precompactDelay = precompactStartDelay

func isRetryableCompactError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, keyword := range []string{
		"context deadline exceeded",
		"timeout awaiting response headers",
		"connection reset by peer",
		"unexpected eof",
		"broken pipe",
		"tls handshake timeout",
		"server closed idle connection",
		"temporary failure in name resolution",
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

type snapshotCompactManager interface {
	CompactSnapshot() ctxpkg.CompactSnapshot
	ApplyCompactResult(ctxpkg.CompactSnapshot, ctxpkg.CompactResult) (bool, int)
	LastCompactRejectReason() ctxpkg.CompactRejectReason
}

type PreCompactStatus struct {
	Running   bool
	StartedAt time.Time
	StartTok  int
}

func (a *Agent) StartPreCompact() {
	a.mu.Lock()
	if a.precompact != nil {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: already running")
		return
	}
	cm := a.contextManager
	prov := a.provider
	if cm == nil || prov == nil {
		a.mu.Unlock()
		return
	}
	snapshotMgr, ok := cm.(snapshotCompactManager)
	if !ok {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: context manager does not support snapshot compaction")
		return
	}
	threshold := cm.AutoCompactThreshold()
	tokens := cm.TokenCount()

	if threshold > 0 {
		if mgr, ok := cm.(*ctxpkg.Manager); ok {

			freed := mgr.CompactOldReasoningBlocks()
			if freed > 0 {
				debug.Log("precompact", "REASONING: freed %d tokens from old reasoning blocks", freed)
				tokens = cm.TokenCount()
			}
			freed = mgr.CompactSupersededReads()
			debug.Log("precompact", "SUPERSEDED: freed %d tokens from superseded reads, tokens now %d (threshold=%d)",
				freed, tokens, threshold)
			if freed > 0 {
				tokens = cm.TokenCount()
			}

			freed = mgr.CompactSupersededCommands()
			if freed > 0 {
				debug.Log("precompact", "SUPERSEDED-CMD: freed %d tokens from re-run commands", freed)
				tokens = cm.TokenCount()
			}
		}
	}

	if threshold <= 0 || tokens < threshold {
		a.mu.Unlock()
		debug.Log("precompact", "SKIP: tokens=%d threshold=%d", tokens, threshold)
		return
	}

	snapshot := snapshotMgr.CompactSnapshot()

	bgCtx, cancel := context.WithTimeout(a.shutdownCtx, precompactBackgroundTimeout)
	pc := &precompactState{
		done:      make(chan struct{}),
		cancel:    cancel,
		startedAt: time.Now(),
		startTok:  tokens,
		snapshot:  snapshot,
	}
	a.precompact = pc
	a.mu.Unlock()

	debug.Log("precompact", "START: tokens=%d threshold=%d ratio=%.2f snapshotMsgs=%d snapshotLastMsgID=%s snapshotVersion=%d",
		tokens, threshold, float64(tokens)/float64(threshold), len(snapshot.Messages), snapshot.LastMsgID, snapshot.Version)

	safego.Go("agent.precompact.run", func() {

		defer close(pc.done)
		defer cancel()

		if res := hooks.RunPreCompactHooks(a.GetHookConfig(), hooks.HookEnv{
			TokenBefore:    tokens,
			CompactTrigger: "auto",
		}); res.Err != nil {
			debug.Log("hooks", "pre_compact hook error (non-fatal): %v", res.Err)
		}

		a.preflushFacts("auto", snapshot.Messages)

		if d := precompactDelay; d > 0 {
			select {
			case <-bgCtx.Done():
				pc.err = bgCtx.Err()
				debug.Log("precompact", "CANCELLED before delay completed")
				return
			case <-time.After(d):
				debug.Log("precompact", "delay completed (%s), sending compaction request", d)
			}
		}

		result, err := snapshot.Compact(bgCtx, prov)
		if err != nil && isRetryableCompactError(err) && a.shutdownCtx.Err() == nil {
			debug.Log("precompact", "first attempt failed (%v), retrying...", err)

			retryCtx, retryCancel := context.WithTimeout(bgCtx, precompactBackgroundTimeout)
			defer retryCancel()
			result, err = snapshot.Compact(retryCtx, prov)
		}
		if err != nil {
			pc.err = err
			debug.Log("precompact", "FAILED: %v", err)
			return
		}
		pc.result = result
		debug.Log("precompact", "DONE: %d → %d tokens changed=%t (elapsed=%s)",
			tokens, result.TokenCount, result.Changed, time.Since(pc.startedAt).Round(time.Millisecond))
	})
}

var sa159RunCompactionHooks = hooks.RunCompactionHooks

func (a *Agent) consumeReadyPreCompact(onEvent func(provider.StreamEvent)) bool {
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()

	if pc == nil {
		return false
	}

	select {
	case <-pc.done:

		a.mu.Lock()
		if a.precompact == pc {
			a.precompact = nil
		}
		cm := a.contextManager
		a.mu.Unlock()
		if pc.err != nil || pc.cancelled.Load() {
			debug.Log("precompact", "READY but unusable err=%v cancelled=%v", pc.err, pc.cancelled.Load())
			if onEvent != nil {
				if pc.cancelled.Load() {
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Auto-compressing context... cancelled]"})
				} else {
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context... failed: %v]", pc.err)})
				}
			}
			return false
		}
		snapshotMgr, ok := cm.(snapshotCompactManager)
		if !ok {
			debug.Log("precompact", "READY but context manager cannot apply snapshots")
			return false
		}
		applied, newTokens := snapshotMgr.ApplyCompactResult(pc.snapshot, pc.result)
		debug.Log("precompact", "READY consumed applied=%t tokens=%d startTok=%d result.changed=%t result.msgs=%d", applied, newTokens, pc.startTok, pc.result.Changed, len(pc.result.Messages))
		if applied {

			a.resetGuidanceCounters()

			sa159RunCompactionHooks(a.GetHookConfig(), hooks.HookEnv{
				TokenBefore:    pc.startTok,
				TokenAfter:     newTokens,
				CompactTrigger: "auto",
			})
		}
		if !applied {
			reason := "unknown"
			liveShrunk := false

			if rr, ok := snapshotMgr.(interface {
				LastCompactRejectReason() ctxpkg.CompactRejectReason
			}); ok {
				switch rr.LastCompactRejectReason() {
				case ctxpkg.CompactRejectNoChange:
					reason = "summarization produced no change"
				case ctxpkg.CompactRejectEmpty:
					reason = "summarization produced empty result"
				case ctxpkg.CompactRejectUserReset:
					reason = "live context user-reset (clear/rewind) during compaction window"
					liveShrunk = true
				case ctxpkg.CompactRejectBenignTrim:
					reason = "benign internal cleanup (retry truncation/orphan removal) during compaction window"
					liveShrunk = false
				default:

				}
			}
			if reason == "unknown" {
				if !pc.result.Changed {
					reason = "summarization produced no change"
				} else if len(pc.result.Messages) == 0 {
					reason = "summarization produced empty result"
				} else {
					reason = "live messages shrunk below snapshot size"
					liveShrunk = true
				}
			}
			debug.Log("precompact", "RESULT DISCARDED: %s (snapshot.OrigLen=%d live=%d)", reason, pc.snapshot.OrigLen, len(cm.Messages()))

			if liveShrunk {
				if threshold := cm.AutoCompactThreshold(); threshold > 0 && pc.startTok >= threshold {
					a.mu.Lock()
					a.precompactCooldownUntil = time.Time{}
					a.mu.Unlock()
					debug.Log("precompact", "RESULT DISCARDED (live shrunk) but scheduled tokens %d still over threshold %d; cooldown refunded for immediate re-schedule", pc.startTok, threshold)
				}
			}

			if onEvent != nil {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Auto-compressing context... result discarded (messages changed)]"})
			}
			return false
		}
		a.maybeSaveCheckpoint(pc.snapshot.LastMsgID)
		if onEvent != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context... done (%d → %d tokens)] ", pc.startTok, newTokens)})
		}

		return true
	default:
		debug.Log("precompact", "still running; continuing without waiting")
		return false
	}
}

func (a *Agent) CancelPreCompact() {
	a.mu.Lock()
	pc := a.precompact
	a.precompact = nil
	a.mu.Unlock()
	if pc == nil {
		return
	}
	pc.cancelled.Store(true)
	if pc.cancel != nil {
		pc.cancel()
	}

	debug.Log("precompact", "CANCELLED externally")
}

func (a *Agent) PreCompactStatus() PreCompactStatus {
	a.mu.RLock()
	pc := a.precompact
	a.mu.RUnlock()
	if pc == nil {
		return PreCompactStatus{}
	}

	select {
	case <-pc.done:
		return PreCompactStatus{}
	default:
	}
	return PreCompactStatus{
		Running:   true,
		StartedAt: pc.startedAt,
		StartTok:  pc.startTok,
	}
}
