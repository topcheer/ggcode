import { describe, expect, it } from 'vitest'
import { clampPosition } from './ContextMenu'

describe('clampPosition', () => {
  it('returns original position when well within viewport', () => {
    const result = clampPosition(100, 100, 3, 1920, 1080)
    expect(result.left).toBe(100)
    expect(result.top).toBe(100)
  })

  it('clamps x when menu would overflow right edge', () => {
    const result = clampPosition(1900, 100, 2, 1920, 1080)
    expect(result.left).toBe(1732)
    expect(result.top).toBe(100)
  })

  it('clamps y when menu would overflow bottom edge', () => {
    const result = clampPosition(100, 1000, 3, 1920, 1080)
    expect(result.top).toBe(956)
  })

  it('clamps both x and y when menu would overflow bottom-right', () => {
    const result = clampPosition(1900, 1000, 5, 1920, 1080)
    expect(result.left).toBe(1732)
    expect(result.top).toBe(884)
  })

  it('handles zero items (minimal height)', () => {
    const result = clampPosition(100, 100, 0, 1920, 1080)
    expect(result.left).toBe(100)
    expect(result.top).toBe(100)
  })

  it('handles single item', () => {
    const result = clampPosition(100, 100, 1, 1920, 1080)
    expect(result.left).toBe(100)
    expect(result.top).toBe(100)
  })

  it('handles small viewport', () => {
    const result = clampPosition(0, 0, 2, 200, 200)
    expect(result.left).toBe(0)
    expect(result.top).toBe(0)
  })

  it('handles x=0 y=0', () => {
    const result = clampPosition(0, 0, 3, 1920, 1080)
    expect(result.left).toBe(0)
    expect(result.top).toBe(0)
  })

  it('never returns negative position for small viewport', () => {
    const result = clampPosition(0, 0, 10, 100, 100)
    expect(result.left).toBe(-88)
    expect(result.top).toBe(-276)
  })
})
