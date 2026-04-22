package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NotebookEdit edits Jupyter Notebook (.ipynb) cells.
type NotebookEdit struct {
	SandboxCheck AllowedPathChecker
}

func (t NotebookEdit) Name() string { return "notebook_edit" }

func (t NotebookEdit) Description() string {
	return "Edit a Jupyter Notebook (.ipynb) file by replacing, adding, or deleting cells. " +
		"Always use this tool for editing .ipynb files instead of edit_file or write_file."
}

func (t NotebookEdit) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"notebook_path": {
				"type": "string",
				"description": "Path to the .ipynb file"
			},
			"cell_number": {
				"type": "integer",
				"description": "Cell index (0-based) to edit. For 'add' operation, inserts before this index."
			},
			"operation": {
				"type": "string",
				"enum": ["replace", "add", "delete"],
				"description": "Operation to perform: replace cell source, add a new cell, or delete a cell"
			},
			"source": {
				"type": "string",
				"description": "New cell source content (for replace and add operations)"
			},
			"cell_type": {
				"type": "string",
				"enum": ["code", "markdown", "raw"],
				"description": "Cell type for add operation (default: code)"
			}
		},
		"required": ["notebook_path", "operation"]
	}`)
}

func (t NotebookEdit) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		NotebookPath string `json:"notebook_path"`
		CellNumber   *int   `json:"cell_number"`
		Operation    string `json:"operation"`
		Source       string `json:"source"`
		CellType     string `json:"cell_type"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.NotebookPath == "" {
		return Result{IsError: true, Content: "notebook_path is required"}, nil
	}
	if ext := filepath.Ext(args.NotebookPath); ext != ".ipynb" {
		return Result{IsError: true, Content: fmt.Sprintf("file must be a .ipynb file, got %q", ext)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.NotebookPath) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	// Read and parse notebook
	data, err := os.ReadFile(args.NotebookPath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error reading notebook: %v", err)}, nil
	}

	var notebook map[string]json.RawMessage
	if err := json.Unmarshal(data, &notebook); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error parsing notebook JSON: %v", err)}, nil
	}

	var cells []json.RawMessage
	if err := json.Unmarshal(notebook["cells"], &cells); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error parsing cells: %v", err)}, nil
	}

	switch args.Operation {
	case "replace":
		if args.CellNumber == nil {
			return Result{IsError: true, Content: "cell_number is required for replace operation"}, nil
		}
		idx := *args.CellNumber
		if idx < 0 || idx >= len(cells) {
			return Result{IsError: true, Content: fmt.Sprintf("cell_number %d out of range (0-%d)", idx, len(cells)-1)}, nil
		}
		var cell map[string]json.RawMessage
		if err := json.Unmarshal(cells[idx], &cell); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error parsing cell %d: %v", idx, err)}, nil
		}
		sourceJSON, _ := json.Marshal(sourceToLines(args.Source))
		cell["source"] = sourceJSON
		cell["outputs"] = json.RawMessage("[]")
		cell["execution_count"] = json.RawMessage("null")
		updatedCell, _ := json.Marshal(cell)
		cells[idx] = updatedCell

	case "add":
		cellType := args.CellType
		if cellType == "" {
			cellType = "code"
		}
		newCell := map[string]interface{}{
			"cell_type":       cellType,
			"source":          sourceToLines(args.Source),
			"metadata":        map[string]interface{}{},
			"execution_count": nil,
			"outputs":         []interface{}{},
		}
		if cellType != "code" {
			delete(newCell, "execution_count")
			delete(newCell, "outputs")
		}
		newCellJSON, _ := json.Marshal(newCell)

		insertAt := len(cells)
		if args.CellNumber != nil {
			insertAt = *args.CellNumber
			if insertAt < 0 {
				insertAt = 0
			}
			if insertAt > len(cells) {
				insertAt = len(cells)
			}
		}
		// Insert at position
		cells = append(cells[:insertAt], append([]json.RawMessage{newCellJSON}, cells[insertAt:]...)...)

	case "delete":
		if args.CellNumber == nil {
			return Result{IsError: true, Content: "cell_number is required for delete operation"}, nil
		}
		idx := *args.CellNumber
		if idx < 0 || idx >= len(cells) {
			return Result{IsError: true, Content: fmt.Sprintf("cell_number %d out of range (0-%d)", idx, len(cells)-1)}, nil
		}
		cells = append(cells[:idx], cells[idx+1:]...)

	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown operation %q — use replace, add, or delete", args.Operation)}, nil
	}

	// Rebuild notebook
	cellsJSON, _ := json.Marshal(cells)
	notebook["cells"] = cellsJSON

	// Use compact but readable formatting
	output, err := json.MarshalIndent(notebook, "", " ")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error serializing notebook: %v", err)}, nil
	}
	// Ensure trailing newline
	output = append(output, '\n')

	if err := atomicWriteFile(args.NotebookPath, output, 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing notebook: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Notebook %s: %s operation completed (%d cells)", args.NotebookPath, args.Operation, len(cells))}, nil
}

// sourceToLines converts a string source to the ipynb format: []string with trailing newlines.
func sourceToLines(source string) []string {
	if source == "" {
		return []string{}
	}
	lines := strings.Split(source, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		// All lines except the last get a trailing newline
		if i < len(lines)-1 {
			result[i] = line + "\n"
		} else {
			result[i] = line
		}
	}
	// If the source ends with a newline, the last element is just "" — trim it
	if len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}
