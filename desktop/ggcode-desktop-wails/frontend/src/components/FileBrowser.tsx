import React, { useState, useEffect, useCallback } from 'react'
import { ChevronRight, ChevronDown, File, Folder, FileCode, FileJson, Settings } from 'lucide-react'

interface FileNode {
  name: string
  isDir: boolean
  size: number
  expanded?: boolean
  children?: FileNode[]
}

const mockTree: FileNode[] = [
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

function getFileIcon(name: string) {
  if (name.endsWith('.go')) return <FileCode size={14} style={{ color: '#00ADD8' }} />
  if (name.endsWith('.json') || name.endsWith('.yaml') || name.endsWith('.yml'))
    return <FileJson size={14} style={{ color: '#F85149' }} />
  if (name.endsWith('.md')) return <File size={14} style={{ color: '#58A6FF' }} />
  if (name === 'Makefile' || name.endsWith('.sh')) return <Settings size={14} style={{ color: '#D29922' }} />
  return <File size={14} style={{ color: 'var(--text-tertiary)' }} />
}

function FileTreeItem({ node, depth, activeFile, onSelect }: {
  node: FileNode, depth: number, activeFile: string,
  onSelect: (path: string) => void,
}) {
  const [expanded, setExpanded] = useState(node.expanded ?? false)
  const path = node.name

  return (
    <>
      <div
        onClick={() => node.isDir ? setExpanded(!expanded) : onSelect(path)}
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
        <FileTreeItem key={i} node={child} depth={depth + 1} activeFile={activeFile} onSelect={onSelect} />
      ))}
    </>
  )
}

const mockCode = `package middleware

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

export function FileBrowser({ onBack }: { onBack: () => void }) {
  const [activeFile, setActiveFile] = useState('auth.go')
  const [openTabs, setOpenTabs] = useState(['auth.go'])

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
          my-project/
        </div>
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {mockTree.map((node, i) => (
            <FileTreeItem key={i} node={node} depth={0} activeFile={activeFile} onSelect={f => {
              setActiveFile(f)
              setOpenTabs(prev => prev.includes(f) ? prev : [...prev, f])
            }} />
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
          {openTabs.map(tab => (
            <div key={tab} onClick={() => setActiveFile(tab)} style={{
              height: '100%', padding: '0 16px',
              display: 'flex', alignItems: 'center', gap: 6,
              background: activeFile === tab ? 'var(--color-bg)' : 'transparent',
              borderRight: '1px solid var(--color-border)',
              cursor: 'pointer',
            }}>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)' }}>{tab}</span>
              <button onClick={e => { e.stopPropagation(); setOpenTabs(prev => prev.filter(t => t !== tab)) }}
                style={{ background: 'none', border: 'none', color: 'var(--text-tertiary)', cursor: 'pointer', fontSize: 10 }}>
                ✕
              </button>
            </div>
          ))}
        </div>

        {/* Code */}
        <div style={{
          flex: 1, padding: 'var(--spacing-md) var(--spacing-lg)',
          overflow: 'auto', background: 'var(--color-bg)',
          fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.8,
        }}>
          {mockCode.split('\n').map((line, i) => (
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
