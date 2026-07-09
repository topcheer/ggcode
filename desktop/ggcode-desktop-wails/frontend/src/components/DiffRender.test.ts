import { describe, expect, it } from 'vitest'
import { parseDiff, type DiffLine } from './DiffRender'

describe('parseDiff', () => {
  it('returns single empty context for empty string', () => {
    // ''.split('\n') = [''], which doesn't match any prefix → context
    expect(parseDiff('')).toEqual<DiffLine[]>([
      { type: 'context', content: '' },
    ])
  })

  it('parses addition lines (+ prefix, not +++)', () => {
    const result = parseDiff('+new line')
    expect(result).toEqual<DiffLine[]>([
      { type: 'add', content: 'new line' },
    ])
  })

  it('parses removal lines (- prefix, not ---)', () => {
    const result = parseDiff('-old line')
    expect(result).toEqual<DiffLine[]>([
      { type: 'remove', content: 'old line' },
    ])
  })

  it('parses context lines (space prefix)', () => {
    const result = parseDiff(' unchanged')
    expect(result).toEqual<DiffLine[]>([
      { type: 'context', content: 'unchanged' },
    ])
  })

  it('parses hunk headers (@@)', () => {
    const result = parseDiff('@@ -1,3 +1,4 @@')
    expect(result).toEqual<DiffLine[]>([
      { type: 'header', content: '@@ -1,3 +1,4 @@' },
    ])
  })

  it('does not treat +++ file header as addition', () => {
    const result = parseDiff('+++ b/newfile.ts')
    expect(result).toEqual<DiffLine[]>([
      { type: 'context', content: '+++ b/newfile.ts' },
    ])
  })

  it('does not treat --- file header as removal', () => {
    const result = parseDiff('--- a/oldfile.ts')
    expect(result).toEqual<DiffLine[]>([
      { type: 'context', content: '--- a/oldfile.ts' },
    ])
  })

  it('parses lines without space prefix as context', () => {
    const result = parseDiff('some text')
    expect(result).toEqual<DiffLine[]>([
      { type: 'context', content: 'some text' },
    ])
  })

  it('strips leading + from addition content', () => {
    const result = parseDiff('+console.log("hello")')
    expect(result[0].content).toBe('console.log("hello")')
  })

  it('strips leading - from removal content', () => {
    const result = parseDiff('-const x = 1')
    expect(result[0].content).toBe('const x = 1')
  })

  it('strips leading space from context content', () => {
    const result = parseDiff(' const x = 2')
    expect(result[0].content).toBe('const x = 2')
  })

  it('parses a complete multi-line diff', () => {
    const diff = [
      '@@ -1,3 +1,4 @@',
      ' unchanged',
      '-removed line',
      '+added line',
      ' also unchanged',
    ].join('\n')

    const result = parseDiff(diff)
    expect(result).toHaveLength(5)
    expect(result[0]).toEqual<DiffLine>({ type: 'header', content: '@@ -1,3 +1,4 @@' })
    expect(result[1]).toEqual<DiffLine>({ type: 'context', content: 'unchanged' })
    expect(result[2]).toEqual<DiffLine>({ type: 'remove', content: 'removed line' })
    expect(result[3]).toEqual<DiffLine>({ type: 'add', content: 'added line' })
    expect(result[4]).toEqual<DiffLine>({ type: 'context', content: 'also unchanged' })
  })

  it('handles empty addition line', () => {
    const result = parseDiff('+')
    expect(result).toEqual<DiffLine[]>([
      { type: 'add', content: '' },
    ])
  })

  it('handles empty removal line', () => {
    const result = parseDiff('-')
    expect(result).toEqual<DiffLine[]>([
      { type: 'remove', content: '' },
    ])
  })
})
