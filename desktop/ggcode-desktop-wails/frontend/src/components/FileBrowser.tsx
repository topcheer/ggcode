import React, { useState, useEffect, useCallback, useRef } from 'react'
import { SkeletonList } from './Skeleton'
import {
  ChevronRight, ChevronDown, File, Folder, FileCode, FileJson,
  Settings, FileText, Image, FileTerminal, X, Search
} from 'lucide-react'
import { useTranslation } from '../i18n'
import * as App from '../../wailsjs/go/main/App'
import hljs from 'highlight.js'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
// mermaid imported dynamically to enable code-splitting (see MermaidBlock)

// highlight.js dark theme CSS
import 'highlight.js/styles/github-dark-dimmed.css'

interface FileNode {
  name: string
  path: string
  isDir: boolean
  size: number
  expanded?: boolean
  children?: FileNode[]
}

// ─── File type detection ─────────────────────────────────

function getFileIcon(name: string) {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  switch (ext) {
    case 'go': return <FileCode size={14} style={{ color: '#00ADD8' }} />
    case 'ts': case 'tsx': return <FileCode size={14} style={{ color: '#3178C6' }} />
    case 'js': case 'jsx': return <FileCode size={14} style={{ color: '#F7DF1E' }} />
    case 'py': return <FileCode size={14} style={{ color: '#3572A5' }} />
    case 'rs': return <FileCode size={14} style={{ color: '#DEA584' }} />
    case 'json': case 'yaml': case 'yml': case 'toml': return <FileJson size={14} style={{ color: '#F85149' }} />
    case 'md': return <FileText size={14} style={{ color: '#58A6FF' }} />
    case 'html': case 'htm': return <FileCode size={14} style={{ color: '#E34C26' }} />
    case 'css': return <FileCode size={14} style={{ color: '#563D7C' }} />
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
  return ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'bmp', 'ico', 'avif', 'tiff', 'tif'].includes(ext)
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

function isMarkdownFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['md', 'markdown', 'mdx'].includes(ext)
}

function isHTMLFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  return ['html', 'htm', 'xhtml'].includes(ext)
}

function isTooLarge(size: number): boolean {
  return size > 2 * 1024 * 1024
}

// Map file extension to highlight.js language name
function extToHljsLang(name: string): string | undefined {
  const ext = name.split('.').pop()?.toLowerCase() || ''
  const map: Record<string, string> = {
    go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
    py: 'python', rs: 'rust', java: 'java', kt: 'kotlin', swift: 'swift',
    rb: 'ruby', cpp: 'cpp', cc: 'cpp', c: 'c', h: 'c', hpp: 'cpp',
    cs: 'csharp', scala: 'scala', r: 'r', lua: 'lua', perl: 'perl', pl: 'perl',
    php: 'php', sql: 'sql', sh: 'bash', bash: 'bash', zsh: 'bash',
    yaml: 'yaml', yml: 'yaml', toml: 'ini', json: 'json', xml: 'xml',
    html: 'xml', htm: 'xml', css: 'css', scss: 'scss', less: 'less',
    dockerfile: 'dockerfile', makefile: 'makefile',
    md: 'markdown', markdown: 'markdown',
    diff: 'diff', patch: 'diff',
    graphql: 'graphql', gql: 'graphql',
    proto: 'protobuf', tf: 'hcl',
  }
  if (map[ext]) return map[ext]
  // Special filenames
  const base = name.toLowerCase()
  if (base === 'makefile') return 'makefile'
  if (base === 'dockerfile') return 'dockerfile'
  if (base === 'jenkinsfile') return 'groovy'
  if (base === 'vagrantfile') return 'ruby'
  return undefined
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

// ─── File Tree Item ───────────────────────────────────────

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

// ─── Mermaid Renderer ─────────────────────────────────────

let mermaidCounter = 0
let mermaidInitDone = false

function MermaidBlock({ chart }: { chart: string }) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDivElement>(null)
  const [svg, setSvg] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    const id = `mermaid-${++mermaidCounter}`
    ;(async () => {
      const mermaid = (await import('mermaid')).default
      if (cancelled) return
      if (!mermaidInitDone) {
        mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose' })
        mermaidInitDone = true
      }
      const { svg: resultSvg } = await mermaid.render(id, chart)
      if (!cancelled) setSvg(resultSvg)
    })().catch((err: any) => {
      if (!cancelled) setError(err?.message || 'Mermaid render error')
    })
    return () => { cancelled = true }
  }, [chart])

  if (error) {
    return <pre style={{ color: '#f87171', fontSize: 12, padding: 12, background: 'rgba(220,38,38,0.1)', borderRadius: 6 }}>{error}</pre>
  }
  if (!svg) return <div style={{ padding: 12, color: 'var(--text-tertiary)', fontSize: 12 }}>{t('files.renderingDiagram')}</div>
  return (
    <div ref={ref} style={{ padding: 12, overflow: 'auto', textAlign: 'center' }}
      dangerouslySetInnerHTML={{ __html: svg }} />
  )
}

// ─── Source Code Preview (highlight.js) ───────────────────

function CodePreview({ code, fileName }: { code: string; fileName: string }) {
  const lang = extToHljsLang(fileName)
  const lines = code.split('\n')

  // Try to highlight whole block at once for accuracy
  let highlighted = ''
  if (lang) {
    try {
      highlighted = hljs.highlight(code, { language: lang }).value
    } catch {}
  }
  if (!highlighted) {
    try {
      highlighted = hljs.highlightAuto(code).value
    } catch {}
  }

  const highlightedLines = highlighted ? highlighted.split('\n') : lines

  return (
    <div style={{
      padding: '12px 0',
      fontFamily: 'var(--font-mono)', fontSize: 12, lineHeight: 1.7,
    }}>
      {highlightedLines.map((line, i) => (
        <div key={i} style={{ display: 'flex' }}
          onMouseEnter={e => e.currentTarget.style.background = 'rgba(255,255,255,0.02)'}
          onMouseLeave={e => e.currentTarget.style.background = 'transparent'}
        >
          <span style={{
            width: 48, textAlign: 'right', color: 'var(--text-tertiary)',
            userSelect: 'none', paddingRight: 16, flexShrink: 0, fontSize: 11,
          }}>{i + 1}</span>
          {highlighted ? (
            <pre style={{
              margin: 0, padding: 0,
              color: 'var(--text-secondary)',
              whiteSpace: 'pre', overflow: 'visible',
            }} dangerouslySetInnerHTML={{ __html: line || ' ' }} />
          ) : (
            <pre style={{
              margin: 0, padding: 0,
              color: 'var(--text-secondary)',
              whiteSpace: 'pre', overflow: 'visible',
            }}>{lines[i] || ' '}</pre>
          )}
        </div>
      ))}
    </div>
  )
}

// ─── Markdown Preview ─────────────────────────────────────

function MarkdownPreview({ content, workDir }: { content: string; workDir: string }) {
  return (
    <div style={{
      padding: '20px 32px',
      color: 'var(--text-primary)',
      lineHeight: 1.7,
      fontSize: 14,
      maxWidth: 900,
    }}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw]}
        components={{
          // Mermaid code blocks
          code({ className, children, ...props }) {
            const match = /language-mermaid/.exec(className || '')
            const text = String(children).replace(/\n$/, '')
            if (match) {
              return <MermaidBlock chart={text} />
            }
            // Inline code
            if (!className) {
              return <code style={{
                background: 'var(--color-card)', padding: '2px 6px',
                borderRadius: 3, fontSize: 12, fontFamily: 'var(--font-mono)',
                color: 'var(--color-info)',
              }} {...props}>{children}</code>
            }
            // Fenced code blocks with syntax highlighting
            const langMatch = /language-(\w+)/.exec(className || '')
            const lang = langMatch ? langMatch[1] : undefined
            let html = text
            if (lang) {
              try { html = hljs.highlight(text, { language: lang }).value } catch {}
            }
            return (
              <pre style={{
                background: '#161b22', padding: 16, borderRadius: 8,
                overflow: 'auto', margin: '12px 0',
              }}>
                <code style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}
                  dangerouslySetInnerHTML={{ __html: html }} />
              </pre>
            )
          },
          // SVG images rendered inline
          img({ src, alt, ...props }) {
            if (src?.endsWith('.svg') || src?.startsWith('data:image/svg')) {
              return <img src={src} alt={alt} style={{ maxWidth: '100%', borderRadius: 4 }} {...props} />
            }
            return <img src={src} alt={alt} style={{ maxWidth: '100%', borderRadius: 4 }} {...props} />
          },
          // Style tables
          table({ children }) {
            return <table style={{
              borderCollapse: 'collapse', width: '100%', margin: '12px 0',
            }}>{children}</table>
          },
          th({ children }) {
            return <th style={{
              border: '1px solid var(--color-border)', padding: '6px 12px',
              background: 'var(--color-card)', fontWeight: 600, fontSize: 13,
              textAlign: 'left',
            }}>{children}</th>
          },
          td({ children }) {
            return <td style={{
              border: '1px solid var(--color-border)', padding: '6px 12px', fontSize: 13,
            }}>{children}</td>
          },
          // Headings
          h1({ children }) { return <h1 style={{ fontSize: 24, fontWeight: 700, borderBottom: '1px solid var(--color-border)', paddingBottom: 8, marginTop: 24 }}>{children}</h1> },
          h2({ children }) { return <h2 style={{ fontSize: 20, fontWeight: 600, borderBottom: '1px solid var(--color-border)', paddingBottom: 6, marginTop: 20 }}>{children}</h2> },
          h3({ children }) { return <h3 style={{ fontSize: 16, fontWeight: 600, marginTop: 16 }}>{children}</h3> },
          // Links
          a({ href, children }) { return <a href={href} target="_blank" rel="noopener noreferrer" style={{ color: 'var(--color-info)', textDecoration: 'none' }}>{children}</a> },
          // Blockquote
          blockquote({ children }) { return <blockquote style={{ borderLeft: '3px solid var(--color-primary)', paddingLeft: 12, color: 'var(--text-secondary)', margin: '8px 0' }}>{children}</blockquote> },
          // Lists
          ul({ children }) { return <ul style={{ paddingLeft: 20 }}>{children}</ul> },
          ol({ children }) { return <ol style={{ paddingLeft: 20 }}>{children}</ol> },
          // Horizontal rule
          hr() { return <hr style={{ border: 'none', borderTop: '1px solid var(--color-border)', margin: '16px 0' }} /> },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}

// ─── HTML Preview ─────────────────────────────────────────

function HTMLPreview({ content }: { content: string }) {
  const { t } = useTranslation()
  return (
    <iframe
      srcDoc={content}
      sandbox="allow-same-origin"
      style={{
        width: '100%', height: '100%', border: 'none',
        background: '#fff',
      }}
      title={t('files.htmlPreview')}
    />
  )
}

// ─── Main Component ───────────────────────────────────────

export function FileBrowser({ onBack }: { onBack: () => void }) {
  const { t } = useTranslation()
  const [activeFile, setActiveFile] = useState('')
  const [openTabs, setOpenTabs] = useState<string[]>([])
  const [tree, setTree] = useState<FileNode[]>([])
  const [code, setCode] = useState('')
  const [workDir, setWorkDir] = useState('')
  const [workDirName, setWorkDirName] = useState('')
  const [loading, setLoading] = useState(true)
  const [fileType, setFileType] = useState<'text' | 'markdown' | 'html' | 'image' | 'pdf' | 'media' | 'office' | 'binary' | 'too-large'>('text')
  const [imageSrc, setImageSrc] = useState('')
  const [mediaSrc, setMediaSrc] = useState('')
  const [searchQuery, setSearchQuery] = useState('')

  // Flatten tree to file-only list for search results
  const flattenTree = useCallback((nodes: FileNode[]): FileNode[] => {
    const result: FileNode[] = []
    const walk = (items: FileNode[]) => {
      for (const item of items) {
        if (!item.isDir) result.push(item)
        if (item.children) walk(item.children)
      }
    }
    walk(nodes)
    return result
  }, [])

  const filteredFiles = searchQuery.trim()
    ? flattenTree(tree).filter(f =>
        f.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        f.path.toLowerCase().includes(searchQuery.toLowerCase())
      )
    : []

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
          const filtered = entries.filter((e: any) => !e.name.startsWith('.'))
          setTree(buildTreeFromBackend(filtered, dir))
        }
      } catch {
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

    if (isBinaryFile(name)) {
      setFileType('binary'); setCode(''); setImageSrc(''); setMediaSrc('')
      return
    }
    if (isOfficeFile(name)) {
      setFileType('office'); setCode(''); setImageSrc(''); setMediaSrc('')
      return
    }
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

    // Text-based files (code, markdown, html, etc.)
    try {
      const content = await App.ReadFileContent(filePath) as string
      if (content !== undefined && content !== null) {
        if (isTooLarge(content.length)) {
          setFileType('too-large'); setCode('')
        } else if (isMarkdownFile(name)) {
          setFileType('markdown'); setCode(content)
        } else if (isHTMLFile(name)) {
          setFileType('html'); setCode(content)
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

  const clearPreview = () => {
    setActiveFile(''); setCode(''); setImageSrc(''); setMediaSrc('')
  }

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
        {/* File search/filter */}
        <div style={{
          padding: '6px 8px',
          borderBottom: '1px solid var(--color-border)',
          position: 'relative',
        }}>
          <Search size={12} style={{
            position: 'absolute', left: 16, top: '50%',
            transform: 'translateY(-50%)',
            color: 'var(--text-tertiary)',
          }} />
          <input
            type="text"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder={t('files.searchPlaceholder')}
            style={{
              width: '100%', height: 26,
              padding: '0 8px 0 26px',
              background: 'var(--color-bg)',
              border: '1px solid var(--color-border)',
              borderRadius: 'var(--radius-sm)',
              color: 'var(--text-primary)',
              fontFamily: 'var(--font-mono)', fontSize: 12,
              outline: 'none',
            }}
          />
        </div>
        <div style={{ flex: 1, overflowY: 'auto', textAlign: 'left' }}>
          {loading && (
            <SkeletonList count={8} variant="row" />
          )}
          {!loading && tree.length === 0 && (
            <div style={{ padding: '8px 12px', fontSize: 11, color: 'var(--text-tertiary)' }}>
              Empty directory
            </div>
          )}
          {/* Search results (flat list) */}
          {searchQuery.trim() && (
            filteredFiles.length === 0 ? (
              <div style={{ padding: '8px 12px', fontSize: 11, color: 'var(--text-tertiary)' }}>
                No matching files
              </div>
            ) : (
              filteredFiles.slice(0, 100).map(f => {
                const relPath = f.path.replace(workDir + '/', '')
                const parts = relPath.split('/')
                const fileName = parts[parts.length - 1]
                const dirPath = parts.slice(0, -1).join('/')
                return (
                  <div key={f.path} onClick={() => handleSelectFile(f.path)} style={{
                    padding: '4px 12px', display: 'flex', alignItems: 'center', gap: 6,
                    background: activeFile === f.path ? 'var(--color-surface)' : 'transparent',
                    cursor: 'pointer',
                  }}>
                    <span style={{ flexShrink: 0 }}>{getFileIcon(fileName)}</span>
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <div style={{
                        fontFamily: 'var(--font-mono)', fontSize: 12,
                        color: activeFile === f.path ? 'var(--text-primary)' : 'var(--text-secondary)',
                        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                      }}>{fileName}</div>
                      {dirPath && (
                        <div style={{
                          fontFamily: 'var(--font-mono)', fontSize: 10,
                          color: 'var(--text-tertiary)',
                          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                        }}>{dirPath}</div>
                      )}
                    </div>
                  </div>
                )
              })
            )
          )}
          {/* Normal tree view */}
          {!searchQuery.trim() && tree.map(node => (
            <FileTreeItem key={node.path} node={node} depth={0}
              activeFile={activeFile} onSelect={handleSelectFile} onLoadDir={handleLoadDir} />
          ))}
        </div>
      </div>

      {/* Preview area */}
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
                      clearPreview()
                    }
                  }
                }} title={`Close ${tabName}`} aria-label={`Close ${tabName}`} style={{
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
            <iframe src={imageSrc}
              style={{ width: '100%', height: '100%', border: 'none' }}
              title={activeFile.split('/').pop()}
            />
          )}

          {/* Media preview */}
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

          {/* Markdown preview */}
          {activeFile && fileType === 'markdown' && code && (
            <MarkdownPreview content={code} workDir={workDir} />
          )}

          {/* HTML preview */}
          {activeFile && fileType === 'html' && code && (
            <HTMLPreview content={code} />
          )}

          {/* Source code preview (highlight.js) */}
          {activeFile && fileType === 'text' && code && (
            <CodePreview code={code} fileName={activeFile.split('/').pop() || ''} />
          )}

          {/* Office document notice */}
          {activeFile && fileType === 'office' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexDirection: 'column', gap: 8, color: 'var(--text-tertiary)',
            }}>
              <FileText size={32} />
              <span style={{ fontSize: 13 }}>{t('files.officeDocument')}</span>
              <span style={{ fontSize: 11 }}>{activeFile.split('/').pop()}</span>
              <span style={{ fontSize: 11 }}>{t('files.openExternal')}</span>
            </div>
          )}

          {/* Binary file notice */}
          {activeFile && fileType === 'binary' && (
            <div style={{
              height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexDirection: 'column', gap: 8, color: 'var(--text-tertiary)',
            }}>
              <File size={32} />
              <span style={{ fontSize: 13 }}>{t('files.binaryFile')}</span>
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
              <span style={{ fontSize: 13 }}>{t('files.fileTooLarge')}</span>
              <span style={{ fontSize: 11 }}>{activeFile.split('/').pop()}</span>
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
