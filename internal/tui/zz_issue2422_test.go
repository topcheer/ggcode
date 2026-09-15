package tui

import (
	"reflect"

	"charm.land/bubbles/v2/textarea"
	"strings"
	"testing"
)

// TestPanelRegistryMirrorsModelFields is the structural pin for #2422's
// closeActivePanel/hasActivePanel mirror-table: every *Panel pointer field on
// Model MUST be registered in panelRegistry exactly once, and every registry
// entry must reference a real field. #904 (hooksPanel) and #2363 (usagePanel)
// were both born as drift between the two hand-maintained lists - a future
// panel field added without a registry entry fails here before it can drift.
func TestPanelRegistryMirrorsModelFields(t *testing.T) {
	m := &Model{}
	regNames := map[string]bool{}
	for _, p := range m.panelRegistry() {
		if regNames[p.name] {
			t.Errorf("duplicate registry entry %q", p.name)
		}
		regNames[p.name] = true
		if p.open == nil || p.close == nil {
			t.Errorf("registry entry %q has nil open/close", p.name)
		}
	}

	modelType := reflect.TypeOf(*m)
	for i := 0; i < modelType.NumField(); i++ {
		f := modelType.Field(i)
		if !strings.HasSuffix(f.Name, "Panel") || f.Type.Kind() != reflect.Ptr {
			continue
		}
		if !regNames[f.Name] {
			t.Errorf("Model field %s (pointer, *Panel suffix) missing from panelRegistry - hasActivePanel/closeActivePanel will drift (#904/#2363 pattern)", f.Name)
		} else {
			delete(regNames, f.Name)
		}
	}
	if !regNames["langOptions"] {
		t.Error("langOptions slot missing from panelRegistry")
	} else {
		delete(regNames, "langOptions")
	}
	for leftover := range regNames {
		t.Errorf("registry entry %q does not match any Model *Panel field (or the reserved langOptions slot)", leftover)
	}
}

// TestPanelRegistryMirrorsLangOptions pins the non-pointer slot: langOptions
// is the one active-panel state without a *Panel field, so it gets its own
// case in both predicates.
func TestPanelRegistryMirrorsLangOptions(t *testing.T) {
	m := &Model{langOptions: []languageOption{{}}}
	if !m.hasActivePanel() {
		t.Error("langOptions non-empty but hasActivePanel()==false")
	}
	if !m.closeActivePanel() {
		t.Error("langOptions non-empty but closeActivePanel()==false")
	}
	if len(m.langOptions) != 0 {
		t.Error("langOptions not cleared by closeActivePanel()")
	}
}

// TestPanelRegistryPriorityOrder pins the resolution order: the original
// switch (now the registry slice order) resolved modelPanel before
// hooksPanel when both were open. Behavioral parity requires the first
// entry to win. closeModelPanel refocuses the main input, so the Model must
// carry an initialized textarea (zero-value cursor panics on Focus).
func TestPanelRegistryPriorityOrder(t *testing.T) {
	m := &Model{
		modelPanel: &modelPanelState{},
		hooksPanel: &hooksPanelState{},
	}
	m.input = textarea.New()
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with two panels open")
	}
	if m.modelPanel != nil {
		t.Error("modelPanel should have been closed first (registry priority order)")
	}
	if m.hooksPanel == nil {
		t.Error("hooksPanel must remain open - only the FIRST registry hit closes per call")
	}
}

// TestPanelRegistryEmptyModel pins the no-panel default branch.
func TestPanelRegistryEmptyModel(t *testing.T) {
	m := &Model{}
	if m.hasActivePanel() {
		t.Error("empty model reports active panel")
	}
	if m.closeActivePanel() {
		t.Error("empty model closeActivePanel() returned true")
	}
}

// TestPanelRegistryWechatWecomCascade pins the two IM cascade slots: closing
// wechat or wecom must ALSO close the shared IM panel (original switch ran
// both closers). Note the registry order: imPanel itself sorts BEFORE the
// wechat/wecom slots, so when both are open Ctrl+C closes im first and the
// channel panel on the next press - this table order mirrors the original
// switch exactly.
func TestPanelRegistryWechatWecomCascade(t *testing.T) {
	m := &Model{
		wechatPanel: &wechatPanelState{},
		imPanel:     &imPanelState{},
	}
	// First close: imPanel wins (earlier in the table).
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with wechat+im open")
	}
	if m.imPanel != nil {
		t.Error("imPanel should close first (registry order precedes wechat/wecom)")
	}
	if m.wechatPanel == nil {
		t.Error("wechatPanel must remain open after first close - only the FIRST hit closes")
	}
	// Second close: wechat slot fires the cascade.
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with wechat open")
	}
	if m.wechatPanel != nil || m.imPanel != nil {
		t.Errorf("wechat cascade incomplete: wechat=%v im=%v", m.wechatPanel != nil, m.imPanel != nil)
	}

	m = &Model{
		wecomPanel: &wecomPanelState{},
		imPanel:    &imPanelState{},
	}
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with wecom+im open")
	}
	// imPanel first again.
	if m.imPanel != nil || m.wecomPanel == nil {
		t.Errorf("expected im closed, wecom still open: im=%v wecom=%v", m.imPanel != nil, m.wecomPanel != nil)
	}
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with wecom open")
	}
	if m.wecomPanel != nil || m.imPanel != nil {
		t.Errorf("wecom cascade incomplete: wecom=%v im=%v", m.wecomPanel != nil, m.imPanel != nil)
	}
}
