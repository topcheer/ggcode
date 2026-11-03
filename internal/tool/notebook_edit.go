package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type NotebookEdit struct {
	SandboxCheck AllowedPathChecker
}

func (t NotebookEdit) Name() string { return "notebook_edit" }

func (t NotebookEdit) Description() string {
	return "Edit a Jupyter Notebook (.ipynb) file by replacing, adding, or deleting cells. " +
		"Always use this tool for editing .ipynb files instead of edit_file or write_file. " +
		"Replacing or adding code cells creates fresh cells with no outputs and null execution_count; it does not preserve prior execution results."
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
			"enum": [
				"replace",
				"add",
				"delete"
			],
			"description": "Operation to perform: replace cell source, add a new cell, or delete a cell"
		},
		"source": {
			"type": "string",
			"description": "New cell source content (for replace and add operations). Replaced or added code cells are written without outputs and with null execution_count."
		},
		"cell_type": {
			"type": "string",
			"enum": [
				"code",
				"markdown",
				"raw"
			],
			"description": "Cell type for add operation (default: code)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"notebook_path",
		"operation",
		"description"
	]
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

	unlockNB := LockWritePath(args.NotebookPath)
	defer unlockNB()

	if stale, since := defaultFileTracker.CheckStale(args.NotebookPath); stale {
		return Result{IsError: true, Content: fmt.Sprintf(
			"notebook was modified externally since last read (changed after %s). "+
				"Re-read the notebook with read_file before editing to avoid overwriting external changes.",
			since.Format("2006-01-02 15:04:05"),
		)}, nil
	}

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

		if args.Source == "" {
			return Result{IsError: true, Content: "source is required for replace operation (omitting it would wipe the cell content)"}, nil
		}
		sourceJSON, _ := json.Marshal(sourceToLines(args.Source))
		cell["source"] = sourceJSON
		var cellType string
		if ctRaw, ok := cell["cell_type"]; ok {
			_ = json.Unmarshal(ctRaw, &cellType)
		}
		if cellType == "code" || cellType == "" {
			cell["outputs"] = json.RawMessage("[]")
			cell["execution_count"] = json.RawMessage("null")
		} else {
			delete(cell, "outputs")
			delete(cell, "execution_count")
		}
		updatedCell, _ := json.Marshal(cell)
		cells[idx] = updatedCell

	case "add":

		if args.Source == "" {
			return Result{IsError: true, Content: "source is required for add operation"}, nil
		}
		cellType := args.CellType
		if cellType == "" {
			cellType = "code"
		}

		switch cellType {
		case "code", "markdown", "raw":
		default:
			return Result{IsError: true, Content: fmt.Sprintf("invalid cell_type %q for add (must be code, markdown, or raw)", cellType)}, nil
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

	cellsJSON, _ := json.Marshal(cells)
	notebook["cells"] = cellsJSON

	output, err := json.MarshalIndent(notebook, "", " ")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error serializing notebook: %v", err)}, nil
	}

	output = append(output, '\n')

	if err := atomicWriteFile(args.NotebookPath, output, 0644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error writing notebook: %v", err)}, nil
	}

	defaultFileTracker.RecordWrite(args.NotebookPath)

	return Result{Content: fmt.Sprintf("Notebook %s: %s operation completed (%d cells)", args.NotebookPath, args.Operation, len(cells))}, nil
}

func sourceToLines(source string) []string {
	if source == "" {
		return []string{}
	}
	lines := strings.Split(source, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {

		if i < len(lines)-1 {
			result[i] = line + "\n"
		} else {
			result[i] = line
		}
	}

	if len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}
