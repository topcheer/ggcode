import React, { useState, useEffect, useCallback } from 'react'
import { ChevronRight, ChevronDown, File, Folder, FileCode, FileJson, Settings } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface FileNode {
  name: string
  isDir: boolean
  size: number
  expanded?: boolean
  children?: FileNode[]
}

const fallbackTree: FileNode[] = [
  {
    name: 'cmd', isDir: true, size: 0, children: [
      { name: 'root.go', isDir: false, size: 1200 },
    ]
  },
  {
    name: 'internal', isDir: true, size: 0, expanded: true, children: [
      { name: 'config', isDir: true, size: 0, children: [
        { name: 'config.go', isDir: false, size: 13200 },
        { name: 'config_test.go', isDir: false, size: 3400 },
      ]},
      { name: 'middleware', isDir: true, size: 0, children: [
        { name: 'auth.go', isDir: false, size: 4200 },
        { name: 'cors.go', isDir: false, size: 1800 },
      ]},
      { name: 'tui', isDir: true, size: 0, children: [
        { name: 'model.go', isDir: false, size: 8900 },
        { name: 'view.go', isDir: false, size: 5600 },
      ]},
      { name: 'server.go', isDir: false, size: 3200 },
    ]
  },
  {
    name: 'desktop', isDir: true, size: 0, children: [
      { name: 'ggcode-desktop', isDir: true, size: 0, children: [] },
      { name: 'ggcode-desktop-wails', isDir: true, size: 0, children: [] },
    ]
  },
  { name: 'go.mod', isDir: false, size: 1200 },
  { name: 'go.sum', isDir: false, size: 42000 },
  { name: 'Makefile', isDir: false, size: 2400 },
  { name: 'README.md', isDir: false, size: 8900 },
]

const fallbackCode = `package middleware

import (
\t"net/http"
\t"strings"
)

// AuthMiddleware validates JWT tokens
type AuthMiddleware struct {
\tsecret  []byte
\trevoker *TokenRevoker
}

func (m *AuthMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
\ttoken := extractBearer(r)
\tif token == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tclaims, err := m.validate(token)
\tif err != nil {
\t\thttp.Error(w, "forbidden", http.StatusForbidden)
\t\treturn
\t}
\tr.Header.Set("X-User-ID", claims.Subject)
}
`

function getFileIcon(name: string) {
  if (name.endsWith('.go')) return <FileCode size={14} style={{ color: '#00ADD8' }} />
  if (name.endsWith('.json') || name.endsWith('.yaml') || name.endsWith('.yml'))
    return <FileJson size={14} style={{ color: '#F85149' }} />
  if (name.endsWith('.md')) return <File size={14} style={{ color: '#58A6FF' }} />
  if (name === 'Makefile' || name.endsWith('.sh')) return <Settings size={14} style={{ color: '#D29922' }} />
  return <File size={14} style={{ color: 'var(--text-tertiary)' }} />
}

// Convert backend ListFiles response (array of {name, isDir, size, ...}) to FileNode tree
function buildTreeFromBackend(entries: Array<Record<string, any>>): FileNode[] {
  return entries.map(e => ({
    name: e.name || '',
    isDir: !!e.isDir,
    size: e.size || 0,
    expanded: false,
    children: e.isDir ? [] : undefined,
  }))
}

function FileTreeItem({ node, depth, activeFile, onSelect, onLoadDir }: {
  node: FileNode, depth: number, activeFile: string,
  onSelect: (path: string) => void,
  onLoadDir: (path: string, node: FileNode) => void,
}) {
  const [expanded, setExpanded] = useState(node.expanded ?? false)
  const path = node.name

  const handleClick = () => {
    if (node.isDir) {
      const next = !expanded
      setExpanded(next)
      if (next && (!node.children || node.children.length === 0)) {
        onLoadDir(path, node)
      }
    } else {
      onSelect(path)
    }
  }

  return (
    <>
      <div
        onClick={handleClick}
        style={{
          display: 'flex', alignItems: 'center', gap: 4,
          padding: '4px 12px', cursor: 'pointer',
          background: activeFile === path ? 'var(--color-card)' : 'transparent',
          paddingLeft: 12 + depth * 16,
        }}
      >
        {node.isDir ? (
          expanded ? <ChevronDown size={12} style={{ color: 'var(--text-tertiary)' }} /> :
            <ChevronRight size={12} style={{ color: 'var(--text-tertiary)' }} />
        ) : (
          <span style={{ width: 12, display: 'inline-flex', justifyContent: 'center' }} />
        )}
        {node.isDir ? <Folder size={14} style={{ color: '#D29922' }} /> : getFileIcon(node.name)}
        <span style={{
          fontFamily: 'var(--font-mono)', fontSize: 12,
          color: activeFile === path ? 'var(--text-primary)' : 'var(--text-secondary)',
        }}>{node.name}</span>
      </div>
      {node.isDir && expanded && node.children?.map((child, i) => (
        <FileTreeItem key={i} node={child} depth={depth + 1} activeFile={activeFile} onSelect={onSelect} onLoadDir={onLoadDir} />
      ))}
    </>
  )
}

export function FileBrowser({ onBack }: { onBack: () => void }) {
  const [activeFile, setActiveFile] = useState('')
  const [openTabs, setOpenTabs] = useState<string[]>([])
  const [tree, setTree] = useState<FileNode[]>(fallbackTree)
  const [code, setCode] = useState(fallbackCode)
  const [workDir, setWorkDir] = useState('my-project')
  const [loading, setLoading] = useState(true)

  // Load workdir and initial file listing
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        // Get working directory
        const dir = await App.GetWorkDir()
        if (cancelled) return
        if (dir) {
          const parts = dir.replace(/\\/g, '/').split('/')
          setWorkDir(parts[parts.length - 1] || dir)

          // Load top-level file listing
          const entries = await App.ListFiles(dir)
          if (cancelled) return
          if (Array.isArray(entries) && entries.length > 0) {
            const built = buildTreeFromBackend(entries)
            setTree(built)
          }
        }
      } catch {
        // Backend not ready, keep fallback
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  // Lazy-load directory contents when expanded
  const handleLoadDir = useCallback(async (dirPath: string, _node: FileNode) => {
    try {
      const entries = await App.ListFiles(dirPath)
      if (Array.isArray(entries) && entries.length > 0) {
        const children = buildTreeFromBackend(entries)
        setTree(prev => updateChildren(prev, dirPath, children))
      }
    } catch {
      // Directory listing failed, leave empty
    }
  }, [])

  // Load file content when selected
  const handleSelectFile = useCallback(async (filePath: string) => {
    setActiveFile(filePath)
    setOpenTabs(prev => prev.includes(filePath) ? prev : [...prev, filePath])
    try {
      const content = await App.ReadFileContent(filePath)
      if (content !== undefined && content !== null) {
        setCode(content)
      }
    } catch {
      // File read failed, keep previous content
    }
  }, [])

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* File tree */}
      <div style={{
        width: 240, background: 'var(--color-nav)',
        display: 'flex', flexDirection: 'column',
      }}>
        <div style={{
          padding: '4px 12px 8px 12px',
          fontFamily: 'var(--font-mono)', fontSize: 12, fontWeight: 600,
          color: 'var(--text-primary)',
        }}>
          {workDir}/
        </div>
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {loading && (
            <div style={{ padding: '8px 12px', fontSize: 11, color: 'var(--text-tertiary)' }}>
              Loading files...
            </div>
          )}
          {tree.map((node, i) => (
            <FileTreeItem key={i} node={node} depth={0} activeFile={activeFile} onSelect={handleSelectFile} onLoadDir={handleLoadDir} />
          ))}
        </div>
      </div>

      {/* Code preview */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        {/* Tab bar */}
        <div style={{
          height: 36, background: 'var(--color-nav)',
          display: 'flex', alignItems: 'center',
        }}>
          {openTabs.map(tab => {
            const tabName = tab.includes('/') ? tab.split('/').pop()! : tab
            return (
              <div key={tab} onClick={() => setActiveFile(tab)} style={{
                height: '100%', padding: '0 16px',
                display: 'flex', alignItems: 'center', gap: 6,
                background: activeFile === tab ? 'var(--color-bg)' : 'transparent',
                borderRight: '1px solid var(--color-border)',
                cursor: 'pointer',
              }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)' }}>{tabName}</span>
                <button onClick={e => { e.stopPropagation(); setOpenTabs(prev => prev.filter(t => t !== tab)) }}
                  style={{ background: 'none', border: 'none', color: 'var(--text-tertiary)', cursor: 'pointer', fontSize: 10 }}>
                  ✕
                </button>
              </div>
            )
          })}
        </div>

        {/* Code */}
        <div style={{
          flex: 1, padding: 'var(--spacing-md) var(--spacing-lg)',
          overflow: 'auto', background: 'var(--color-bg)',
          fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.8,
        }}>
          {code.split('\n').map((line, i) => (
            <div key={i} style={{ display: 'flex' }}>
              <span style={{
                width: 32, textAlign: 'right', color: 'var(--text-tertiary)',
                userSelect: 'none', paddingRight: 12, flexShrink: 0,
              }}>{i + 1}</span>
              <span style={{ color: line.startsWith('//') ? 'var(--text-tertiary)' :
                line.startsWith('func ') || line.startsWith('type ') || line.startsWith('import') ? '#D2A8FF' :
                  line.includes('"') ? '#A5D6FF' :
                    'var(--text-secondary)'
              }}>{line}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// Helper: recursively update children of a directory in the tree
function updateChildren(nodes: FileNode[], dirPath: string, newChildren: FileNode[]): FileNode[] {
  return nodes.map(n => {
    if (n.name === dirPath && n.isDir) {
      return { ...n, children: newChildren }
    }
    if (n.children) {
      return { ...n, children: updateChildren(n.children, dirPath, newChildren) }
    }
    return n
  })
}
