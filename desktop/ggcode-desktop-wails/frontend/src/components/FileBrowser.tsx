import React, { useState, useEffect, useCallback } from 'react'
import {
  ChevronRight, ChevronDown, File, Folder, FileCode, FileJson,
  Settings, FileText, Image, FileTerminal, X
} from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'

interface FileNode {
  name: string
  path: string // full path from workdir root
  isDir: boolean
  size: number
  expanded?: boolean
  children?: FileNode[]
}

function getFileIcon(name: string) {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  switch (ext) {
    case 'go': return <FileCode size={14} style={{ color: '#00ADD8' }} />
    case 'ts': case 'tsx': return <FileCode size={14} style={{ color: '#3178C6' }} />
    case 'js': case 'jsx': return <FileCode size={14} style={{ color: '#F7DF1E' }} />
    case 'py': return <FileCode size={14} style={{ color: '#3572A5' }} />
    case 'rs': return <FileCode size={14} style={{ color: '#DEA584' }} />
    case 'json': case 'yaml': case 'yml': case 'toml': return <FileJson size={14} style={{ color: '#F85149' }} />
    case 'md': case 'txt': case 'rst': return <FileText size={14} style={{ color: '#58A6FF' }} />
    case 'png': case 'jpg': case 'jpeg': case 'gif': case 'svg': case 'webp':
      return <Image size={14} style={{ color: '#A371F7' }} />
    case 'sh': case 'bash': case 'zsh': return <FileTerminal size={14} style={{ color: '#D29922' }} />
    default:
      if (name === 'Makefile' || name === 'Dockerfile' || name === 'LICENSE')
        return <Settings size={14} style={{ color: '#D29922' }} />
      return <File size={14} style={{ color: 'var(--text-tertiary)' }} />
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function isImageFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'bmp', 'ico'].includes(ext)
}

function isBinaryFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['exe', 'dll', 'so', 'dylib', 'bin', 'dat', 'o', 'a', 'wasm', 'zip', 'tar', 'gz', 'bz2', '7z', 'rar'].includes(ext)
}

function isPDFFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ext === 'pdf'
}

function isOfficeFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt', 'odt', 'ods', 'odp'].includes(ext)
}

function isMediaFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['mp4', 'webm', 'mp3', 'wav', 'ogg', 'm4a', 'flac'].includes(ext)
}

function isTooLarge(size: number): boolean {
  return size > 2 * 1024 * 1024 // 2MB
}

function buildTreeFromBackend(entries: Array<Record<string, any>>, parentPath: string): FileNode[] {
  return entries.map(e => ({
    name: e.name || '',
    path: parentPath ? `${parentPath}/${e.name}` : e.name,
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

  const handleClick = () => {
    if (node.isDir) {
      const next = !expanded
      setExpanded(next)
      if (next && (!node.children || node.children.length === 0)) {
        onLoadDir(node.path, node)
      }
    } else {
      onSelect(node.path)
    }
  }

  return (
    <>
      <div
        onClick={handleClick}
        style={{
          display: 'flex', alignItems: 'center', gap: 4,
          padding: '3px 12px', cursor: 'pointer',
          background: activeFile === node.path ? 'var(--color-card)' : 'transparent',
          paddingLeft: 12 + depth * 16,
          borderRadius: 2,
        }}
        onMouseEnter={e => { if (activeFile !== node.path) e.currentTarget.style.background = 'rgba(255,255,255,0.04)' }}
        onMouseLeave={e => { if (activeFile !== node.path) e.currentTarget.style.background = 'transparent' }}
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
          color: activeFile === node.path ? 'var(--text-primary)' : 'var(--text-secondary)',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          flex: 1,
        }}>{node.name}</span>
        {!node.isDir && node.size > 0 && (
          <span style={{ fontSize: 10, color: 'var(--text-tertiary)', flexShrink: 0 }}>
            {formatSize(node.size)}
          </span>
        )}
      </div>
      {node.isDir && expanded && node.children?.map((child, i) => (
        <FileTreeItem key={child.path || i} node={child} depth={depth + 1}
          activeFile={activeFile} onSelect={onSelect} onLoadDir={onLoadDir} />
      ))}
    </>
  )
}

// Syntax highlighting by file extension
function highlightLine(line: string, ext: string): { color: string; bold?: boolean } {
  const trimmed = line.trimStart()
  if (!trimmed) return { color: 'var(--text-secondary)' }

  // Comments
  if (trimmed.startsWith('//') || trimmed.startsWith('#') && !['sh', 'bash', 'zsh', 'yaml', 'yml', 'toml'].includes(ext))
    return { color: 'var(--text-tertiary)' }
  if (['sh', 'bash', 'zsh'].includes(ext) && trimmed.startsWith('#'))
    return { color: 'var(--text-tertiary)' }
  if (trimmed.startsWith('/*') || trimmed.startsWith('*') || trimmed.startsWith('*/'))
    return { color: 'var(--text-tertiary)' }
  if (['yaml', 'yml', 'toml'].includes(ext) && trimmed.startsWith('#'))
    return { color: 'var(--text-tertiary)' }

  // Go keywords
  if (ext === 'go') {
    if (/^(func |type |var |const |package |import |return |if |for |switch |case |default |else |defer |go |range |select |struct |interface |map\[|chan )/.test(trimmed))
      return { color: '#D2A8FF' }
    if (/^(true|false|nil|break|continue|fallthrough|goto)/.test(trimmed))
      return { color: '#79C0FF' }
    if (trimmed.includes('"') || trimmed.includes('`'))
      return { color: '#A5D6FF' }
  }

  // TS/JS keywords
  if (['ts', 'tsx', 'js', 'jsx'].includes(ext)) {
    if (/^(export |import |const |let |var |function |class |interface |type |enum |return |if |for |while |switch |case |default |else |async |await |try |catch |throw |new |from )/.test(trimmed))
      return { color: '#D2A8FF' }
    if (/^(true|false|null|undefined|this|super)/.test(trimmed))
      return { color: '#79C0FF' }
    if (trimmed.includes("'") || trimmed.includes('"') || trimmed.includes('`'))
      return { color: '#A5D6FF' }
  }

  // Python keywords
  if (ext === 'py') {
    if (/^(def |class |import |from |return |if |for |while |with |try |except |finally |elif |else |async |await |yield |raise |pass |break |continue|lambda |global |nonlocal )/.test(trimmed))
      return { color: '#D2A8FF' }
    if (/^(True|False|None)/.test(trimmed))
      return { color: '#79C0FF' }
  }

  // YAML/TOML keys
  if (['yaml', 'yml', 'toml'].includes(ext)) {
    if (/^\S+:/.test(trimmed) || /^\[/.test(trimmed))
      return { color: '#D2A8FF' }
  }

  // Makefile
  if (ext === '' && trimmed.includes(':=')) return { color: '#D2A8FF' }
  if (ext === '' && trimmed.startsWith('$(')) return { color: '#A5D6FF' }

  return { color: 'var(--text-secondary)' }
}

export function FileBrowser({ onBack }: { onBack: () => void }) {
  const [activeFile, setActiveFile] = useState('')
  const [openTabs, setOpenTabs] = useState<string[]>([])
  const [tree, setTree] = useState<FileNode[]>([])
  const [code, setCode] = useState('')
  const [workDir, setWorkDir] = useState('')
  const [workDirName, setWorkDirName] = useState('')
  const [loading, setLoading] = useState(true)
  const [fileType, setFileType] = useState<'text' | 'image' | 'pdf' | 'media' | 'office' | 'binary' | 'too-large'>('text')
  const [imageSrc, setImageSrc] = useState('')
  const [mediaSrc, setMediaSrc] = useState('')

  // Load workdir and initial file listing
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const dir = await App.GetWorkDir() as string
        if (cancelled || !dir) return
        setWorkDir(dir)
        const parts = dir.replace(/\\/g, '/').split('/')
        setWorkDirName(parts[parts.length - 1] || dir)

        const entries = await App.ListFiles(dir)
        if (cancelled) return
        if (Array.isArray(entries) && entries.length > 0) {
          // Filter hidden files/dirs
          const filtered = entries.filter((e: any) => !e.name.startsWith('.'))
          setTree(buildTreeFromBackend(filtered, dir))
        }
      } catch {
        // Backend not ready
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
        const filtered = entries.filter((e: any) => !e.name.startsWith('.'))
        const children = buildTreeFromBackend(filtered, dirPath)
        setTree(prev => updateChildren(prev, dirPath, children))
      }
    } catch {}
  }, [])

  // Load file content when selected
  const loadFileContent = useCallback(async (filePath: string) => {
    const name = filePath.split('/').pop() || ''

    // Binary (exe, dll, zip, etc.)
    if (isBinaryFile(name)) {
      setFileType('binary'); setCode(''); setImageSrc(''); setMediaSrc('')
      return
    }

    // Office docs — can't render inline
    if (isOfficeFile(name)) {
      setFileType('office'); setCode(''); setImageSrc(''); setMediaSrc('')
      return
    }

    // Image — load as base64
    if (isImageFile(name)) {
      try {
        const result = await App.ReadFileAsBase64(filePath) as { mimeType: string; data: string }
        setFileType('image')
        setImageSrc(`data:${result.mimeType};base64,${result.data}`)
        setCode(''); setMediaSrc('')
      } catch {
        setFileType('binary'); setImageSrc(''); setCode('')
      }
      return
    }

    // PDF — load as base64
    if (isPDFFile(name)) {
      try {
        const result = await App.ReadFileAsBase64(filePath) as { mimeType: string; data: string }
        setFileType('pdf')
        setImageSrc(`data:${result.mimeType};base64,${result.data}`)
        setCode(''); setMediaSrc('')
      } catch {
        setFileType('binary'); setImageSrc(''); setCode('')
      }
      return
    }

    // Media (video/audio) — load as base64
    if (isMediaFile(name)) {
      try {
        const result = await App.ReadFileAsBase64(filePath) as { mimeType: string; data: string }
        setFileType('media')
        setMediaSrc(`data:${result.mimeType};base64,${result.data}`)
        setCode(''); setImageSrc('')
      } catch {
        setFileType('binary'); setMediaSrc(''); setCode('')
      }
      return
    }

    // Text files
    try {
      const content = await App.ReadFileContent(filePath) as string
      if (content !== undefined && content !== null) {
        if (isTooLarge(content.length)) {
          setFileType('too-large'); setCode('')
        } else {
          setFileType('text'); setCode(content)
        }
      }
    } catch {
      setFileType('text')
      setCode('// Unable to read file')
    }
  }, [])

  const handleSelectFile = useCallback(async (filePath: string) => {
    setActiveFile(filePath)
    setOpenTabs(prev => prev.includes(filePath) ? prev : [...prev, filePath])
    await loadFileContent(filePath)
  }, [loadFileContent])

  const handleTabSelect = async (path: string) => {
    setActiveFile(path)
    await loadFileContent(path)
  }

  const activeExt = activeFile.split('.').pop()?.toLowerCase() || ''

  return (
    <div style={{ display: 'flex', height: '100%', textAlign: 'left' }}>
      {/* File tree */}
      <div style={{
        width: 240, background: 'var(--color-nav)',
        display: 'flex', flexDirection: 'column',
        borderRight: '1px solid var(--color-border)',
        textAlign: 'left', alignItems: 'stretch',
      }}>
        <div style={{
          padding: '8px 12px',
          fontFamily: 'var(--font-mono)', fontSize: 12, fontWeight: 600,
          color: 'var(--text-primary)',
          borderBottom: '1px solid var(--color-border)',
          display: 'flex', alignItems: 'center', gap: 6,
        }}>
          <Folder size={14} style={{ color: 'var(--color-primary)' }} />
          {workDirName}/
        </div>
        <div style={{ flex: 1, overflowY: 'auto', textAlign: 'left' }}>
          {loading && (
            <div style={{ padding: '8px 12px', fontSize: 11, color: 'var(--text-tertiary)' }}>
              Loading files...
            </div>
          )}
          {!loading && tree.length === 0 && (
            <div style={{ padding: '8px 12px', fontSize: 11, color: 'var(--text-tertiary)' }}>
              Empty directory
            </div>
          )}
          {tree.map(node => (
            <FileTreeItem key={node.path} node={node} depth={0}
              activeFile={activeFile} onSelect={handleSelectFile} onLoadDir={handleLoadDir} />
          ))}
        </div>
      </div>

      {/* Code preview */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        {/* Tab bar */}
        <div style={{
          height: 36, background: 'var(--color-nav)',
          display: 'flex', alignItems: 'center',
          borderBottom: '1px solid var(--color-border)',
          overflowX: 'auto',
        }}>
          {openTabs.map(tab => {
            const tabName = tab.split('/').pop()!
            return (
              <div key={tab} onClick={() => handleTabSelect(tab)} style={{
                height: '100%', padding: '0 12px',
                display: 'flex', alignItems: 'center', gap: 6,
                background: activeFile === tab ? 'var(--color-bg)' : 'transparent',
                borderRight: '1px solid var(--color-border)',
                cursor: 'pointer', flexShrink: 0,
              }}>
                {getFileIcon(tabName)}
                <span style={{
                  fontFamily: 'var(--font-mono)', fontSize: 12,
                  color: activeFile === tab ? 'var(--text-primary)' : 'var(--text-secondary)',
                }}>{tabName}</span>
                <button onClick={e => {
                  e.stopPropagation()
                  const remaining = openTabs.filter(t => t !== tab)
                  setOpenTabs(remaining)
                  if (activeFile === tab) {
                    if (remaining.length > 0) {
                      handleTabSelect(remaining[remaining.length - 1])
                    } else {
                      setActiveFile('')
                      setCode('')
                      setImageSrc('')
                      setMediaSrc('')
                    }
                  }
                }} style={{
                  background: 'none', border: 'none', color: 'var(--text-tertiary)',
                  cursor: 'pointer', fontSize: 10, padding: 2, lineHeight: 1,
                }}>
                  <X size={10} />
                </button>
              </div>
            )
          })}
        </div>

        {/* Content area */}
        <div style={{ flex: 1, overflow: 'auto', background: 'var(--color-bg)' }}>
          {/* Empty state */}
          {!activeFile && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              color: 'var(--text-tertiary)', fontSize: 13,
            }}>
              Select a file to preview
            </div>
          )}

          {/* Image preview */}
          {activeFile && fileType === 'image' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              padding: 24, overflow: 'auto',
            }}>
              <img src={imageSrc} alt={activeFile.split('/').pop()}
                style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain', borderRadius: 4 }}
                onError={e => { (e.target as HTMLImageElement).style.display = 'none' }}
              />
            </div>
          )}

          {/* PDF preview */}
          {activeFile && fileType === 'pdf' && imageSrc && (
            <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
              <iframe src={imageSrc}
                style={{ flex: 1, border: 'none', width: '100%' }}
                title={activeFile.split('/').pop()}
              />
            </div>
          )}

          {/* Media preview (video/audio) */}
          {activeFile && fileType === 'media' && mediaSrc && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              padding: 24,
            }}>
              {mediaSrc.startsWith('data:video') ? (
                <video src={mediaSrc} controls style={{ maxWidth: '100%', maxHeight: '100%', borderRadius: 4 }} />
              ) : (
                <audio src={mediaSrc} controls style={{ width: '100%', maxWidth: 500 }} />
              )}
            </div>
          )}

          {/* Office document notice */}
          {activeFile && fileType === 'office' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexDirection: 'column', gap: 8, color: 'var(--text-tertiary)',
            }}>
              <FileText size={32} />
              <span style={{ fontSize: 13 }}>Office document</span>
              <span style={{ fontSize: 11 }}>{activeFile.split('/').pop()}</span>
              <span style={{ fontSize: 11 }}>Open with external application to view</span>
            </div>
          )}

          {/* Binary file notice */}
          {activeFile && fileType === 'binary' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexDirection: 'column', gap: 8, color: 'var(--text-tertiary)',
            }}>
              <File size={32} />
              <span style={{ fontSize: 13 }}>Binary file</span>
              <span style={{ fontSize: 11 }}>{activeFile.split('/').pop()}</span>
            </div>
          )}

          {/* Too large notice */}
          {activeFile && fileType === 'too-large' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexDirection: 'column', gap: 8, color: 'var(--text-tertiary)',
            }}>
              <FileText size={32} />
              <span style={{ fontSize: 13 }}>File too large to preview (&gt;2MB)</span>
              <span style={{ fontSize: 11 }}>{activeFile.split('/').pop()}</span>
            </div>
          )}

          {/* Code/text preview */}
          {activeFile && fileType === 'text' && (
            <div style={{
              padding: '12px 0',
              fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.7,
            }}>
              {code.split('\n').map((line, i) => {
                const hl = highlightLine(line, activeExt)
                return (
                  <div key={i} style={{
                    display: 'flex',
                    background: 'transparent',
                  }}
                    onMouseEnter={e => e.currentTarget.style.background = 'rgba(255,255,255,0.02)'}
                    onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
                  >
                    <span style={{
                      width: 48, textAlign: 'right', color: 'var(--text-tertiary)',
                      userSelect: 'none', paddingRight: 16, flexShrink: 0, fontSize: 11,
                    }}>{i + 1}</span>
                    <pre style={{
                      margin: 0, padding: 0,
                      color: hl.color,
                      fontWeight: hl.bold ? 600 : 400,
                      whiteSpace: 'pre', overflow: 'visible',
                    }}>{line || ' '}</pre>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// Helper: recursively update children of a directory in the tree
function updateChildren(nodes: FileNode[], dirPath: string, newChildren: FileNode[]): FileNode[] {
  return nodes.map(n => {
    if (n.path === dirPath && n.isDir) {
      return { ...n, children: newChildren }
    }
    if (n.children) {
      return { ...n, children: updateChildren(n.children, dirPath, newChildren) }
    }
    return n
  })
}
