import { describe, expect, it } from 'vitest'
import { matchesShortcut, type ShortcutConfig } from './useKeyboardShortcut'

function makeKeyEvent(overrides: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return {
    key: 'k',
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    metaKey: false,
    ...overrides,
  } as KeyboardEvent
}

describe('matchesShortcut', () => {
  it('matches simple key without modifiers', () => {
    const config: ShortcutConfig = { key: 'Escape' }
    expect(matchesShortcut(makeKeyEvent({ key: 'Escape' }), config)).toBe(true)
  })

  it('matches case-insensitively', () => {
    const config: ShortcutConfig = { key: 'K' }
    expect(matchesShortcut(makeKeyEvent({ key: 'k' }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'K' }), config)).toBe(true)
  })

  it('rejects when key does not match', () => {
    const config: ShortcutConfig = { key: 'Enter' }
    expect(matchesShortcut(makeKeyEvent({ key: 'Escape' }), config)).toBe(false)
  })

  it('ctrl matches ctrlKey', () => {
    const config: ShortcutConfig = { key: 'k', ctrl: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', ctrlKey: true }), config)).toBe(true)
  })

  it('ctrl also matches metaKey for cross-platform (macOS Cmd)', () => {
    const config: ShortcutConfig = { key: 'k', ctrl: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', metaKey: true }), config)).toBe(true)
  })

  it('ctrl rejects when neither ctrl nor meta pressed', () => {
    const config: ShortcutConfig = { key: 'k', ctrl: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'k' }), config)).toBe(false)
  })

  it('meta requires metaKey specifically', () => {
    const config: ShortcutConfig = { key: 'k', meta: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', metaKey: true }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'k', ctrlKey: true }), config)).toBe(false)
  })

  it('ctrl+meta requires ctrlKey when both are set', () => {
    const config: ShortcutConfig = { key: 'k', ctrl: true, meta: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', ctrlKey: true, metaKey: true }), config)).toBe(true)
    // Only meta without ctrl should not match when both are explicitly wanted
    expect(matchesShortcut(makeKeyEvent({ key: 'k', metaKey: true }), config)).toBe(false)
  })

  it('shift requires shiftKey', () => {
    const config: ShortcutConfig = { key: 'P', shift: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'p', shiftKey: true }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'p' }), config)).toBe(false)
  })

  it('rejects when shift not wanted but pressed (except for Shift key itself)', () => {
    const config: ShortcutConfig = { key: 'k' }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', shiftKey: true }), config)).toBe(false)
    // Shift key itself should pass
    const shiftConfig: ShortcutConfig = { key: 'Shift' }
    expect(matchesShortcut(makeKeyEvent({ key: 'Shift', shiftKey: true }), shiftConfig)).toBe(true)
  })

  it('rejects when alt not wanted but pressed (except for Alt key itself)', () => {
    const config: ShortcutConfig = { key: 'k' }
    expect(matchesShortcut(makeKeyEvent({ key: 'k', altKey: true }), config)).toBe(false)
    const altConfig: ShortcutConfig = { key: 'Alt' }
    expect(matchesShortcut(makeKeyEvent({ key: 'Alt', altKey: true }), altConfig)).toBe(true)
  })

  it('multi-modifier combo: ctrl+shift', () => {
    const config: ShortcutConfig = { key: 'p', ctrl: true, shift: true }
    expect(matchesShortcut(makeKeyEvent({ key: 'p', ctrlKey: true, shiftKey: true }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'p', metaKey: true, shiftKey: true }), config)).toBe(true) // ctrl cross-platform
    expect(matchesShortcut(makeKeyEvent({ key: 'p', ctrlKey: true }), config)).toBe(false) // missing shift
  })

  it('Enter key with no modifiers', () => {
    const config: ShortcutConfig = { key: 'Enter' }
    expect(matchesShortcut(makeKeyEvent({ key: 'Enter' }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'Enter', shiftKey: true }), config)).toBe(false)
  })

  it('ArrowDown for navigation', () => {
    const config: ShortcutConfig = { key: 'ArrowDown' }
    expect(matchesShortcut(makeKeyEvent({ key: 'ArrowDown' }), config)).toBe(true)
    expect(matchesShortcut(makeKeyEvent({ key: 'arrowdown' }), config)).toBe(true)
  })
})
