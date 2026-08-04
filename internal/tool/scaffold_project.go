package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/safego"
)

const maxScaffoldFiles = 40

// ScaffoldProject generates a multi-file project skeleton from a set of
// built-in templates (Go, Node/TypeScript, Python, Rust). This eliminates the
// tedious, error-prone pattern of the agent calling write_file 8-15 times to
// scaffold a new project, each call consuming a full LLM round-trip.
//
// Templates include best-practice defaults: lint configs, CI workflows,
// .gitignore, README, and entry points -- the same structure the agent would
// eventually create manually, but in a single deterministic call.
type ScaffoldProject struct {
	SandboxCheck AllowedPathChecker
	WorkingDir   string
}

func (t ScaffoldProject) Name() string { return "scaffold_project" }

func (t ScaffoldProject) Description() string {
	return "Generate a multi-file project skeleton (Go, TypeScript/Node, Python, or Rust) in one call. " +
		"Creates directory structure, config files (.gitignore, lint config, CI workflow), entry point, " +
		"README, and test file with best-practice defaults. Use this instead of multiple write_file calls " +
		"when starting a new project or module. Returns a file manifest. Does not overwrite existing files."
}

func (t ScaffoldProject) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"language": {
				"type": "string",
				"enum": ["go", "typescript", "python", "rust"],
				"description": "Target language/runtime for the scaffold."
			},
			"project_name": {
				"type": "string",
				"description": "Project name (used for module path, package.json name, etc.)."
			},
			"output_dir": {
				"type": "string",
				"description": "Target directory for the project root. Defaults to current working directory."
			},
			"options": {
				"type": "object",
				"description": "Optional template toggles.",
				"properties": {
					"ci": { "type": "boolean", "description": "Generate CI workflow file (default true)." },
					"docker": { "type": "boolean", "description": "Generate Dockerfile (default false)." },
					"module_path": { "type": "string", "description": "Go module path (Go only, e.g. github.com/user/repo)." }
				}
			},
			"description": {
				"type": "string",
				"description": "Optional. Brief activity label shown in the UI."
			}
		},
		"required": ["language", "project_name"]
	}`)
}

// scaffoldFile represents a single file in a template.
type scaffoldFile struct {
	Path    string
	Content string
}

// scaffoldResult is the structured output returned to the LLM.
type scaffoldResult struct {
	Summary     string               `json:"summary"`
	Language    string               `json:"language"`
	ProjectName string               `json:"project_name"`
	OutputDir   string               `json:"output_dir"`
	Total       int                  `json:"total"`
	Created     int                  `json:"created"`
	Skipped     int                  `json:"skipped"`
	Files       []scaffoldFileResult `json:"files"`
}

type scaffoldFileResult struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "created", "skipped"
}

func (t ScaffoldProject) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	_ = ctx
	defer safego.Recover("tool.scaffold_project")

	args, errMsg := parseScaffoldArgs(input)
	if errMsg != "" {
		return Result{IsError: true, Content: errMsg}, nil
	}

	// Resolve output directory (defaults to WorkingDir).
	outDir := args.OutputDir
	if outDir == "" {
		outDir = t.WorkingDir
	}
	outDir, err := cleanAbsolutePath(outDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid output_dir: %v", err)}, nil
	}
	args.OutputDir = outDir

	// Sandbox check on the output directory.
	if t.SandboxCheck != nil && !t.SandboxCheck(args.OutputDir) {
		return Result{IsError: true, Content: fmt.Sprintf("output_dir not allowed by sandbox policy: %s", args.OutputDir)}, nil
	}

	files := resolveScaffoldFiles(args)
	if len(files) > maxScaffoldFiles {
		return Result{IsError: true, Content: fmt.Sprintf("template too large: %d files (max %d)", len(files), maxScaffoldFiles)}, nil
	}

	result := t.writeScaffoldFiles(args, files)
	content, err := json.Marshal(result)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
	}
	return Result{Content: string(content)}, nil
}

// scaffoldArgs holds validated parameters for a scaffold operation.
type scaffoldArgs struct {
	Language    string
	ProjectName string
	OutputDir   string
	CI          bool
	Docker      bool
	ModulePath  string
}

// parseScaffoldArgs unmarshals and validates tool input.
// Returns a non-empty errMsg on failure.
func parseScaffoldArgs(input json.RawMessage) (scaffoldArgs, string) {
	var raw struct {
		Language    string `json:"language"`
		ProjectName string `json:"project_name"`
		OutputDir   string `json:"output_dir"`
		Options     struct {
			CI         bool   `json:"ci"`
			Docker     bool   `json:"docker"`
			ModulePath string `json:"module_path"`
		} `json:"options"`
	}
	if err := json.Unmarshal(input, &raw); err != nil {
		return scaffoldArgs{}, fmt.Sprintf("invalid input: %v", err)
	}
	if raw.ProjectName == "" {
		return scaffoldArgs{}, "project_name is required"
	}
	lang := strings.ToLower(raw.Language)
	switch lang {
	case "go", "typescript", "python", "rust":
	default:
		return scaffoldArgs{}, fmt.Sprintf("unsupported language %q (use go, typescript, python, or rust)", lang)
	}
	return scaffoldArgs{
		Language:    lang,
		ProjectName: raw.ProjectName,
		OutputDir:   raw.OutputDir,
		CI:          raw.Options.CI || !inputHasField(input, "ci"),
		Docker:      raw.Options.Docker,
		ModulePath:  raw.Options.ModulePath,
	}, ""
}

// resolveScaffoldFiles dispatches to the appropriate template generator.
func resolveScaffoldFiles(args scaffoldArgs) []scaffoldFile {
	switch args.Language {
	case "go":
		return goTemplate(args.ProjectName, args.OutputDir, args.ModulePath, args.CI, args.Docker)
	case "typescript":
		return tsTemplate(args.ProjectName, args.OutputDir, args.CI, args.Docker)
	case "python":
		return pythonTemplate(args.ProjectName, args.OutputDir, args.CI, args.Docker)
	case "rust":
		return rustTemplate(args.ProjectName, args.OutputDir, args.CI, args.Docker)
	}
	return nil
}

// writeScaffoldFiles writes each file to disk, skipping existing files.
func (t ScaffoldProject) writeScaffoldFiles(args scaffoldArgs, files []scaffoldFile) scaffoldResult {
	result := scaffoldResult{
		Language:    args.Language,
		ProjectName: args.ProjectName,
		OutputDir:   args.OutputDir,
		Total:       len(files),
		Files:       make([]scaffoldFileResult, 0, len(files)),
	}

	for _, f := range files {
		fullPath := filepath.Join(args.OutputDir, f.Path)
		if t.tryWriteFile(fullPath, f.Content) {
			defaultFileTracker.RecordRead(fullPath)
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "created"})
			result.Created++
		} else {
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "skipped"})
			result.Skipped++
		}
	}

	if result.Skipped > 0 {
		result.Summary = fmt.Sprintf("Scaffolded %s project %q: %d files created, %d skipped (already exist)",
			args.Language, args.ProjectName, result.Created, result.Skipped)
	} else {
		result.Summary = fmt.Sprintf("Scaffolded %s project %q: %d files created",
			args.Language, args.ProjectName, result.Created)
	}
	return result
}

// tryWriteFile attempts to write a single file. Returns false if the file
// already exists, is outside the sandbox, or any I/O error occurs.
func (t ScaffoldProject) tryWriteFile(fullPath, content string) bool {
	if t.SandboxCheck != nil && !t.SandboxCheck(fullPath) {
		return false
	}
	if _, err := os.Stat(fullPath); err == nil {
		return false // file already exists
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return false
	}
	return os.WriteFile(fullPath, []byte(content), 0644) == nil
}

// inputHasField checks whether a JSON input has a specific field set.
func inputHasField(input json.RawMessage, field string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return false
	}
	opts, ok := m["options"]
	if !ok {
		return false
	}
	var om map[string]json.RawMessage
	if err := json.Unmarshal(opts, &om); err != nil {
		return false
	}
	_, ok = om[field]
	return ok
}
