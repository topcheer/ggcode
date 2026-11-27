// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { sanitizeMarkdownHTML, sanitizeSVGHTML } from './sanitize'

describe('HTML sanitisation of model / tool / LAN output (D11)', () => {
  it('strips event handlers and script tags from markdown HTML', () => {
    const out = sanitizeMarkdownHTML('<p>hi</p><img src="x" onerror="window.go.main.App.Quit()"><script>window.x=1</script><a href="https://a.b" data-external="true">l</a>')
    expect(out).not.toContain('onerror')
    expect(out).not.toContain('<script')
    expect(out).toContain('<p>hi</p>')
    expect(out).toContain('data-external="true"')
  })

  it('keeps GFM task-list checkboxes and highlight classes', () => {
    const out = sanitizeMarkdownHTML('<ul><li><input type="checkbox" disabled checked> done</li></ul><pre><code class="language-go hljs"><span class="hljs-keyword">func</span></code></pre>')
    expect(out).toContain('type="checkbox"')
    expect(out).toContain('hljs-keyword')
  })

  it('strips javascript: URLs', () => {
    const out = sanitizeMarkdownHTML('<a href="javascript:alert(1)">x</a>')
    expect(out).not.toContain('javascript:')
  })

  it('keeps inline SVG shapes but drops scripts inside them', () => {
    const out = sanitizeSVGHTML('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><script>alert(1)</script><circle cx="5" cy="5" r="4" fill="red"/></svg>')
    expect(out).toContain('<circle')
    expect(out).not.toContain('<script')
  })
})
