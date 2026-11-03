package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4538_EnsureMetaSerializedWithAppenders(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("zai", "ep", "model")
	ses.Title = "4538"

	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}}
	path := filepath.Join(dir, ses.ID+".jsonl")

	if err := store.EnsureMeta(ses); err != nil {
		t.Fatalf("EnsureMeta: %v", err)
	}
	const appenders = 8
	var wg sync.WaitGroup
	for i := 0; i < appenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := jsonlRecord{Type: "message", Message: &provider.Message{
				ID:   fmt.Sprintf("m-%d", i),
				Role: "user",
				Content: []provider.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("msg %d", i)},
				},
			}}
			if err := appendRecordLine(path, rec); err != nil {
				t.Errorf("appender %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"type":"meta"`) {
		t.Fatalf("meta record missing after concurrent appends:\n%s", content)
	}
	for i := 0; i < appenders; i++ {
		if !strings.Contains(content, fmt.Sprintf(`"id":"m-%d"`, i)) {
			t.Fatalf("appender %d record lost:\n%s", i, content)
		}
	}
}
