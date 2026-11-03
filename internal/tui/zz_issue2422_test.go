package tui

import (
	"reflect"

	"charm.land/bubbles/v2/textarea"
	"strings"
	"testing"
)

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

func TestPanelRegistryEmptyModel(t *testing.T) {
	m := &Model{}
	if m.hasActivePanel() {
		t.Error("empty model reports active panel")
	}
	if m.closeActivePanel() {
		t.Error("empty model closeActivePanel() returned true")
	}
}

func TestPanelRegistryWechatWecomCascade(t *testing.T) {
	m := &Model{
		wechatPanel: &wechatPanelState{},
		imPanel:     &imPanelState{},
	}

	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel()==false with wechat+im open")
	}
	if m.imPanel != nil {
		t.Error("imPanel should close first (registry order precedes wechat/wecom)")
	}
	if m.wechatPanel == nil {
		t.Error("wechatPanel must remain open after first close - only the FIRST hit closes")
	}

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
