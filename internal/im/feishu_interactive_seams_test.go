package im

import (
	"encoding/json"
	"testing"
)

// Seam truth-table pins for the feishuAdapter.SendInteractive split (r211).
// Expected values were derived verbatim from the pre-split inline card code
// (feishu_adapter.go:2025-2100 @ c76ac9378) and lock the wire-visible card
// layout: button style mapping, callback wiring, Done column and column_set
// assembly. Intentionally independent of map iteration order: assertions go
// through re-marshalled JSON only where ordering is guaranteed by the builder.

func feishuFindColumnSet(t *testing.T, card map[string]any) map[string]any {
	t.Helper()
	body := card["body"].(map[string]any)
	elements := body["elements"].([]any)
	for _, e := range elements {
		m := e.(map[string]any)
		if m["tag"] == "column_set" {
			return m
		}
	}
	t.Fatal("card has no column_set element")
	return nil
}

func TestFeishuInteractiveButtonColumnStyles(t *testing.T) {
	cases := []struct {
		style string
		want  string
	}{
		{"primary", "primary"},
		{"danger", "danger"},
		{"", "default"},
		{"unknown", "default"},
	}
	for _, tc := range cases {
		col := feishuInteractiveButtonColumn(InteractiveButton{Label: "L", Value: "v", Style: tc.style})
		if col["tag"] != "column" {
			t.Fatalf("style %q: column tag = %v", tc.style, col["tag"])
		}
		btn := col["elements"].([]any)[0].(map[string]any)
		if got := btn["type"]; got != tc.want {
			t.Fatalf("style %q: button type = %v, want %v", tc.style, got, tc.want)
		}
		text := btn["text"].(map[string]any)
		if text["content"] != "L" || text["tag"] != "plain_text" {
			t.Fatalf("style %q: text = %v", tc.style, text)
		}
		beh := btn["behaviors"].([]any)[0].(map[string]any)
		if beh["type"] != "callback" {
			t.Fatalf("style %q: behavior type = %v", tc.style, beh["type"])
		}
		if got := beh["value"].(map[string]any)["choice"]; got != "v" {
			t.Fatalf("style %q: choice = %v, want v", tc.style, got)
		}
	}
}

func TestFeishuInteractiveDoneColumn(t *testing.T) {
	col := feishuInteractiveDoneColumn()
	if col["tag"] != "column" {
		t.Fatalf("column tag = %v", col["tag"])
	}
	btn := col["elements"].([]any)[0].(map[string]any)
	if btn["type"] != "primary" {
		t.Fatalf("done button type = %v, want primary", btn["type"])
	}
	if got := btn["text"].(map[string]any)["content"]; got != "✅ Done" {
		t.Fatalf("done label = %q", got)
	}
	if got := btn["behaviors"].([]any)[0].(map[string]any)["value"].(map[string]any)["choice"]; got != "__done__" {
		t.Fatalf("done choice = %v, want __done__", got)
	}
}

func TestFeishuInteractiveCardShape(t *testing.T) {
	card := buildFeishuInteractiveCard(InteractiveMessage{Text: "hello"})
	if card["schema"] != "2.0" {
		t.Fatalf("schema = %v, want 2.0", card["schema"])
	}
	if got := card["config"].(map[string]any)["wide_screen_mode"]; got != true {
		t.Fatalf("wide_screen_mode = %v, want true", got)
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	if len(elements) != 1 {
		t.Fatalf("text-only card must have exactly 1 element, got %d", len(elements))
	}
	first := elements[0].(map[string]any)
	if first["tag"] != "markdown" || first["content"] != "hello" {
		t.Fatalf("first element = %v", first)
	}

	// Buttons + multiselect: column_set with button columns then Done column.
	card = buildFeishuInteractiveCard(InteractiveMessage{
		Text:        "pick",
		MultiSelect: true,
		Buttons: []InteractiveButton{
			{Label: "A", Value: "a", Style: "primary"},
			{Label: "B", Value: "b"},
		},
	})
	cs := feishuFindColumnSet(t, card)
	if cs["flex_mode"] != "bisect" {
		t.Fatalf("flex_mode = %v, want bisect", cs["flex_mode"])
	}
	cols := cs["columns"].([]map[string]any)
	if len(cols) != 3 {
		t.Fatalf("columns = %d, want 2 buttons + 1 done", len(cols))
	}
	lastBtn := cols[2]["elements"].([]any)[0].(map[string]any)
	if lastBtn["type"] != "primary" ||
		lastBtn["text"].(map[string]any)["content"] != "✅ Done" {
		t.Fatalf("last column must be the Done button, got %v", lastBtn)
	}

	// Wire-visible JSON must carry the same shape (bytes-level sanity).
	cardBytes, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(cardBytes, &round); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	if round["schema"] != "2.0" {
		t.Fatalf("round-trip schema = %v", round["schema"])
	}

	// Empty buttons + no multiselect: no column_set at all.
	card = buildFeishuInteractiveCard(InteractiveMessage{Text: "plain"})
	for _, e := range card["body"].(map[string]any)["elements"].([]any) {
		if e.(map[string]any)["tag"] == "column_set" {
			t.Fatal("text-only card must not contain column_set")
		}
	}
}

func TestFeishuInteractiveRequestBodyShape(t *testing.T) {
	b := feishuInteractiveRequestBody("ch-9", []byte(`{"schema":"2.0"}`))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if m["receive_id"] != "ch-9" || m["msg_type"] != "interactive" {
		t.Fatalf("body fields = %v", m)
	}
	if got := m["content"]; got != `{"schema":"2.0"}` {
		t.Fatalf("content = %q, want raw card JSON string", got)
	}
}
