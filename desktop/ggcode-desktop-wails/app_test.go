package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

func TestEmitStreamEventAlsoQueuesChatUIEvent(t *testing.T) {
	app := &App{
		ctx:          context.Background(),
		streamEvents: make(chan uiEvent, 4),
	}

	raw := json.RawMessage(`{"message":"boom"}`)
	app.emitStreamEvent("error", raw)

	select {
	case ev := <-app.streamEvents:
		if ev.name != "chat:stream" {
			t.Fatalf("expected chat:stream UI event, got %q", ev.name)
		}
		payload, ok := ev.payload.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map payload, got %T", ev.payload)
		}
		if payload["type"] != "error" {
			t.Fatalf("expected payload type error, got %#v", payload["type"])
		}
		if payload["data"] != string(raw) {
			t.Fatalf("expected payload data %q, got %#v", string(raw), payload["data"])
		}
	default:
		t.Fatal("expected chat:stream UI event to be queued")
	}
}

func TestGetSessionHistoryUsesAppChat(t *testing.T) {
	app := &App{chat: &wailskit.ChatBridge{}}

	history, err := app.GetSessionHistory()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected empty history from zero-value chat bridge, got %+v", history)
	}
}

func TestIsWorkingUsesAppChat(t *testing.T) {
	app := &App{chat: &wailskit.ChatBridge{}}

	if app.IsWorking() {
		t.Fatal("expected zero-value chat bridge to report not working")
	}
}
