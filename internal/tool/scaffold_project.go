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

	var args struct {
		Language    string `json:"language"`
		ProjectName string `json:"project_name"`
		OutputDir   string `json:"output_dir"`
		Options     struct {
			CI         bool   `json:"ci"`
			Docker     bool   `json:"docker"`
			ModulePath string `json:"module_path"`
		} `json:"options"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.ProjectName == "" {
		return Result{IsError: true, Content: "project_name is required"}, nil
	}

	lang := strings.ToLower(args.Language)
	switch lang {
	case "go", "typescript", "python", "rust":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported language %q (use go, typescript, python, or rust)", lang)}, nil
	}

	// Resolve output directory.
	outDir := args.OutputDir
	if outDir == "" {
		outDir = t.WorkingDir
	}
	outDir, err := cleanAbsolutePath(outDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid output_dir: %v", err)}, nil
	}

	// Sandbox check.
	if t.SandboxCheck != nil && !t.SandboxCheck(outDir) {
		return Result{IsError: true, Content: fmt.Sprintf("output_dir not allowed by sandbox policy: %s", outDir)}, nil
	}

	// Defaults.
	if args.Options.CI || !inputHasField(input, "ci") {
		args.Options.CI = true
	}

	// Generate template files.
	var files []scaffoldFile
	switch lang {
	case "go":
		files = goTemplate(args.ProjectName, outDir, args.Options.ModulePath, args.Options.CI, args.Options.Docker)
	case "typescript":
		files = tsTemplate(args.ProjectName, outDir, args.Options.CI, args.Options.Docker)
	case "python":
		files = pythonTemplate(args.ProjectName, outDir, args.Options.CI, args.Options.Docker)
	case "rust":
		files = rustTemplate(args.ProjectName, outDir, args.Options.CI, args.Options.Docker)
	}

	if len(files) > maxScaffoldFiles {
		return Result{IsError: true, Content: fmt.Sprintf("template too large: %d files (max %d)", len(files), maxScaffoldFiles)}, nil
	}

	// Write files (never overwrite existing).
	result := scaffoldResult{
		Language:    lang,
		ProjectName: args.ProjectName,
		OutputDir:   outDir,
		Total:       len(files),
		Files:       make([]scaffoldFileResult, 0, len(files)),
	}

	for _, f := range files {
		fullPath := filepath.Join(outDir, f.Path)

		// Sandbox check each file path.
		if t.SandboxCheck != nil && !t.SandboxCheck(fullPath) {
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "skipped"})
			result.Skipped++
			continue
		}

		// Don't overwrite existing files.
		if _, err := os.Stat(fullPath); err == nil {
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "skipped"})
			result.Skipped++
			continue
		}

		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "skipped"})
			result.Skipped++
			continue
		}

		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "skipped"})
			result.Skipped++
			continue
		}

		defaultFileTracker.RecordRead(fullPath)
		result.Files = append(result.Files, scaffoldFileResult{Path: f.Path, Status: "created"})
		result.Created++
	}

	if result.Skipped > 0 {
		result.Summary = fmt.Sprintf("Scaffolded %s project %q: %d files created, %d skipped (already exist)", lang, args.ProjectName, result.Created, result.Skipped)
	} else {
		result.Summary = fmt.Sprintf("Scaffolded %s project %q: %d files created", lang, args.ProjectName, result.Created)
	}

	content, err := json.Marshal(result)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
	}
	return Result{Content: string(content)}, nil
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
