package im

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// ToolManagerAdapter wraps *Manager to satisfy tool.IMManager.
// This bridges the IM runtime to the tool layer without creating an import cycle.
type ToolManagerAdapter struct {
	Mgr *Manager
}

// NewToolManagerAdapter creates an adapter that satisfies tool.IMManager.
func NewToolManagerAdapter(mgr *Manager) *ToolManagerAdapter {
	return &ToolManagerAdapter{Mgr: mgr}
}

func (a *ToolManagerAdapter) Snapshot() tool.IMSnapshot {
	if a == nil || a.Mgr == nil {
		return tool.IMSnapshot{}
	}
	snap := a.Mgr.Snapshot()
	return convertSnapshot(snap)
}

func (a *ToolManagerAdapter) MuteBinding(adapterName string) error {
	return a.Mgr.MuteBinding(adapterName)
}

func (a *ToolManagerAdapter) UnmuteBinding(adapterName string) error {
	return a.Mgr.UnmuteBinding(adapterName)
}

func (a *ToolManagerAdapter) DisableBinding(adapterName string) error {
	return a.Mgr.DisableBinding(adapterName)
}

func (a *ToolManagerAdapter) EnableBinding(adapterName string) error {
	return a.Mgr.EnableBinding(adapterName)
}

func (a *ToolManagerAdapter) IsBindingMuted(adapterName string) bool {
	return a.Mgr.IsBindingMuted(adapterName)
}

func (a *ToolManagerAdapter) IsBindingDisabled(adapterName string) bool {
	return a.Mgr.IsBindingDisabled(adapterName)
}

func (a *ToolManagerAdapter) Emit(ctx context.Context, event tool.IMOutboundEvent) error {
	return a.Mgr.Emit(ctx, OutboundEvent{
		Kind:      OutboundEventKind(event.Kind),
		Text:      event.Text,
		CreatedAt: time.Now(),
	})
}

func (a *ToolManagerAdapter) SendDirect(ctx context.Context, adapter string, event tool.IMOutboundEvent) error {
	// Find the binding for this adapter
	snap := a.Mgr.Snapshot()
	for _, b := range snap.CurrentBindings {
		if b.Adapter == adapter {
			return a.Mgr.SendDirect(ctx, b, OutboundEvent{
				Kind:      OutboundEventKind(event.Kind),
				Text:      event.Text,
				CreatedAt: time.Now(),
			})
		}
	}
	// Also check disabled bindings
	for _, b := range snap.DisabledBindings {
		if b.Adapter == adapter {
			return a.Mgr.SendDirect(ctx, b, OutboundEvent{
				Kind:      OutboundEventKind(event.Kind),
				Text:      event.Text,
				CreatedAt: time.Now(),
			})
		}
	}
	return ErrNoChannelBound
}

// OtherInstancesHaveActiveChannels returns true if other instances in the
// same workspace have active (non-muted) IM channel bindings.
func (a *ToolManagerAdapter) OtherInstancesHaveActiveChannels() bool {
	d := a.Mgr.InstanceDetect()
	if d == nil {
		return false
	}
	for _, info := range d.ListInstances() {
		if info.PID == a.Mgr.InstanceDetect().Info().PID {
			continue // skip self
		}
		if info.HasActiveChannels {
			return true
		}
	}
	return false
}

func convertSnapshot(snap StatusSnapshot) tool.IMSnapshot {
	out := tool.IMSnapshot{
		CurrentBindings:  make([]tool.IMChannelBinding, 0, len(snap.CurrentBindings)),
		DisabledBindings: make([]tool.IMChannelBinding, 0, len(snap.DisabledBindings)),
		Adapters:         make([]tool.IMAdapterState, 0, len(snap.Adapters)),
	}
	for _, b := range snap.CurrentBindings {
		out.CurrentBindings = append(out.CurrentBindings, tool.IMChannelBinding{
			Adapter:   b.Adapter,
			Platform:  string(b.Platform),
			ChannelID: b.ChannelID,
			Muted:     b.Muted,
		})
	}
	for _, b := range snap.DisabledBindings {
		out.DisabledBindings = append(out.DisabledBindings, tool.IMChannelBinding{
			Adapter:   b.Adapter,
			Platform:  string(b.Platform),
			ChannelID: b.ChannelID,
			Muted:     b.Muted,
		})
	}
	for _, a := range snap.Adapters {
		out.Adapters = append(out.Adapters, tool.IMAdapterState{
			Name:      a.Name,
			Platform:  string(a.Platform),
			Healthy:   a.Healthy,
			Status:    a.Status,
			LastError: a.LastError,
		})
	}
	return out
}
