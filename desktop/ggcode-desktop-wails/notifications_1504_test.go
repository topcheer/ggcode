package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func Test1504DeliverUnixBounded(t *testing.T) {
	done := make(chan struct{})
	go func() {
		nm := &NotificationManager{}
		nm.deliverUnix("t", "b")
		close(done)
	}()
	select {
	case <-done:

	case <-time.After(15 * time.Second):
		t.Fatal("deliverUnix ran unbounded - worker can be wedged by a hung notifier")
	}
}

func Test1504SourceContracts(t *testing.T) {
	srcs := map[string]string{
		"main.go":          "runtime.WindowShow(app.ctx)",
		"app.go":           "a.dc.SetGlobalHotkey(false)",
		"notifications.go": "exec.CommandContext(ctx",
	}
	for f, needle := range srcs {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(data), needle) {
			t.Errorf("%s lost the #1504 contract (%q not found)", f, needle)
		}
	}
}
