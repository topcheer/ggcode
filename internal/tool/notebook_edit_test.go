package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func makeTestNotebook(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	nb := `{
 "cells": [
  {
   "cell_type": "code",
   "source": ["print('hello')\n"],
   "metadata": {},
   "execution_count": 1,
   "outputs": []
  },
  {
   "cell_type": "markdown",
   "source": ["# Title\n"],
   "metadata": {}
  }
 ],
 "metadata": {
  "kernelspec": {"display_name": "Python 3", "language": "python", "name": "python3"}
 },
 "nbformat": 4,
 "nbformat_minor": 5
}`
	fp := filepath.Join(dir, "test.ipynb")
	os.WriteFile(fp, []byte(nb), 0644)
	return fp
}

func TestNotebookEdit_Replace(t *testing.T) {
	fp := makeTestNotebook(t)
	ne := NotebookEdit{}

	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": fp,
		"cell_number":   0,
		"operation":     "replace",
		"source":        "print('world')",
	})

	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if !containsAny(string(data), "world") {
		t.Errorf("expected 'world' in notebook, got: %s", string(data))
	}
}

func TestNotebookEdit_Add(t *testing.T) {
	fp := makeTestNotebook(t)
	ne := NotebookEdit{}

	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": fp,
		"cell_number":   1,
		"operation":     "add",
		"source":        "x = 42",
		"cell_type":     "code",
	})

	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "3 cells") {
		t.Errorf("expected 3 cells, got: %s", result.Content)
	}
}

func TestNotebookEdit_Delete(t *testing.T) {
	fp := makeTestNotebook(t)
	ne := NotebookEdit{}

	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": fp,
		"cell_number":   1,
		"operation":     "delete",
	})

	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "1 cell") {
		t.Errorf("expected 1 cell, got: %s", result.Content)
	}
}

func TestNotebookEdit_OutOfRange(t *testing.T) {
	fp := makeTestNotebook(t)
	ne := NotebookEdit{}

	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": fp,
		"cell_number":   99,
		"operation":     "replace",
		"source":        "x",
	})

	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for out of range cell_number")
	}
}

func TestNotebookEdit_NotIpynb(t *testing.T) {
	ne := NotebookEdit{}
	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": "/tmp/test.txt",
		"operation":     "replace",
	})
	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-.ipynb file")
	}
}

func TestNotebookEdit_MissingCellNumber(t *testing.T) {
	fp := makeTestNotebook(t)
	ne := NotebookEdit{}

	input, _ := json.Marshal(map[string]interface{}{
		"notebook_path": fp,
		"operation":     "replace",
		"source":        "x",
	})

	result, err := ne.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing cell_number on replace")
	}
}

func TestSourceToLines(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello", []string{"hello"}},
		{"line1\nline2", []string{"line1\n", "line2"}},
		{"a\nb\nc", []string{"a\n", "b\n", "c"}},
		{"", []string{}},
		{"trail\n", []string{"trail\n"}},
	}

	for _, tt := range tests {
		got := sourceToLines(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("sourceToLines(%q): expected %v, got %v", tt.input, tt.expected, got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("sourceToLines(%q)[%d]: expected %q, got %q", tt.input, i, tt.expected[i], got[i])
			}
		}
	}
}
