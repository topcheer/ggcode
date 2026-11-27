import DOMPurify from 'dompurify'

// D11 (desktop review 2026-11-27): markdown produced from model output, tool
// results and LAN peers used to reach dangerouslySetInnerHTML unsanitised, so
// an injected <img onerror> ran inside the WebView where window.go.main.App is
// reachable. Every HTML sink now goes through here.
//
// Markdown HTML: default profile plus the data-external marker used by the
// link handler. Inline <svg> blocks: SVG profile (scripts/event handlers are
// still stripped).
const markdownPolicy = { ADD_ATTR: ['data-external'], KEEP_CONTENT: true }
const svgPolicy = { USE_PROFILES: { svg: true, svgFilters: true, html: true }, ADD_ATTR: ['data-external'] }

export function sanitizeMarkdownHTML(html: string): string {
  return DOMPurify.sanitize(html, markdownPolicy) as string
}

export function sanitizeSVGHTML(svg: string): string {
  return DOMPurify.sanitize(svg, svgPolicy) as string
}
