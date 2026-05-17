package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// skippedDirs are directory names to skip when building the file tree.
var skippedDirs = map[string]struct{}{
	".git":         {},
	".gradle":      {},
	".cxx":         {},
	".next":        {},
	".nuxt":        {},
	".cache":       {},
	".dart_tool":   {},
	".terraform":   {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"venv":         {},
	"pods":         {},
	"deriveddata":  {},
	"bin":          {},
	"dist":         {},
	"build":        {},
	"out":          {},
	"debug":        {},
	"release":      {},
	"target":       {},
	"coverage":     {},
	"__pycache__":  {},
}

// fileIconForExt returns an appropriate icon for a file based on its extension.
func fileIconForExt(ext string) fyne.Resource {
	switch strings.ToLower(ext) {
	case ".go":
		return theme.DocumentIcon()
	case ".md", ".rst", ".txt":
		return theme.DocumentIcon()
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp":
		return theme.MediaPhotoIcon()
	case ".yaml", ".yml", ".json", ".toml", ".xml":
		return theme.SettingsIcon()
	case ".go.mod", ".go.sum", ".lock":
		return theme.ContentAddIcon()
	default:
		return theme.FileIcon()
	}
}

// FileTree provides a lazy-loading file tree for the workspace.
type FileTree struct {
	root    string
	onOpen  func(absPath string)
	tree    *widget.Tree
	entries map[string][]string // uid -> sorted child names
}

// NewFileTree creates a new file tree rooted at the given workspace directory.
func NewFileTree(root string, onOpen func(absPath string)) *FileTree {
	ft := &FileTree{
		root:   root,
		onOpen: onOpen,
	}
	ft.tree = widget.NewTree(
		ft.childUIDs,
		ft.isBranch,
		ft.createNode,
		ft.updateNode,
	)
	ft.tree.OnSelected = ft.onSelected
	return ft
}

// Widget returns the Fyne tree widget.
func (ft *FileTree) Widget() fyne.CanvasObject {
	return ft.tree
}

// Refresh reloads the tree data and refreshes the widget.
func (ft *FileTree) Refresh() {
	ft.entries = nil
	ft.tree.Refresh()
}

// onSelected handles a node being selected in the tree.
func (ft *FileTree) onSelected(uid widget.TreeNodeID) {
	absPath := filepath.Join(ft.root, uid)
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return
	}
	if ft.onOpen != nil {
		ft.onOpen(absPath)
	}
}

// childUIDs returns the child UIDs for a given node (lazy loading).
func (ft *FileTree) childUIDs(uid widget.TreeNodeID) (children []widget.TreeNodeID) {
	if ft.entries == nil {
		ft.entries = make(map[string][]string)
	}
	dir := ft.root
	if uid != "" {
		dir = filepath.Join(ft.root, uid)
	}
	if cached, ok := ft.entries[uid]; ok {
		for _, name := range cached {
			if uid == "" {
				children = append(children, widget.TreeNodeID(name))
			} else {
				children = append(children, widget.TreeNodeID(filepath.Join(uid, name)))
			}
		}
		return children
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Separate dirs and files, sort each group
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") && uid == "" {
			// Skip hidden files at root level, allow in subdirs
			if name == ".git" || name == ".DS_Store" {
				continue
			}
		}
		if e.IsDir() {
			if _, skip := skippedDirs[name]; skip {
				continue
			}
			dirs = append(dirs, name)
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	names := append(dirs, files...)
	ft.entries[uid] = names
	for _, name := range names {
		if uid == "" {
			children = append(children, widget.TreeNodeID(name))
		} else {
			children = append(children, widget.TreeNodeID(filepath.Join(uid, name)))
		}
	}
	return children
}

// isBranch returns true if the node is a directory.
func (ft *FileTree) isBranch(uid widget.TreeNodeID) bool {
	absPath := filepath.Join(ft.root, uid)
	info, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// createNode creates a new tree node widget.
func (ft *FileTree) createNode(isBranch bool) fyne.CanvasObject {
	icon := widget.NewIcon(theme.FileIcon())
	label := widget.NewLabel("")
	return container.NewHBox(icon, label)
}

// updateNode updates a tree node's content.
func (ft *FileTree) updateNode(uid widget.TreeNodeID, isBranch bool, node fyne.CanvasObject) {
	hbox := node.(*fyne.Container)
	icon := hbox.Objects[0].(*widget.Icon)
	label := hbox.Objects[1].(*widget.Label)
	name := filepath.Base(uid)
	if uid == "" {
		name = filepath.Base(ft.root)
	}
	label.SetText(name)
	if isBranch {
		icon.SetResource(theme.FolderIcon())
	} else {
		ext := strings.ToLower(filepath.Ext(name))
		icon.SetResource(fileIconForExt(ext))
	}
}
