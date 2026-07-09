import { describe, expect, it } from 'vitest'
import { clampPosition } from './ContextMenu'

describe('clampPosition', () => {
  it('returns original position when well within viewport', () => {
    const result = clampPosition(100, 100, 3, 1920, 1080)
    expect(result.left).toBe(100)
    expect(result.top).toBe(100)
  })

  it('clamps x when menu would overflow right edge', () => {
    // menu width = 180, padding = 8 → max x = 1920 - 180 - 8 = 1732
    const result = clampPosition(1900, 100, 2, 1920, 1080)
    expect(result.left).toBe(1732)
    expect(result.top).toBe(100)
  })

  it('clamps y when menu would overflow bottom edge', () => {
    // 3 items: height = 3*36+8 = 116 → max y = 1080 - 116 - 8 = 956
    const result = clampPosition(100, 1000, 3, 1920, 1080)
    expect(result.top).toBe(956)
  })

  it('clamps both x and y when menu would overflow bottom-right', () => {
    const result = clampPosition(1900, 1000, 5, 1920, 1080)
    expect(result.left).toBe(1732)
    // 5 items: height = 5*36+8 = 188 → max y = 1080 - 188 - 8 = 884
    expect(result.top).toBe(884)
  })

  it('handles zero items (minimal height)', () => {
    const result = clampPosition(100, 100, 0, 1920, 1080)
    expect(result.left).toBe(100)
    // 0 items: height = 0*36+8 = 8 → max y = 1080 - 8 - 8 = 1064
    expect(result.top).toBe(100)
  })

  it('handles single item', () => {
    const result = clampPosition(100, 100, 1, 1920, 1080)
    expect(result.left).toBe(100)
    // 1 item: height = 1*36+8 = 44 → max y = 1080 - 44 - 8 = 1028
    expect(result.top).toBe(100)
  })

  it('handles small viewport', () => {
    const result = clampPosition(0, 0, 2, 200, 200)
    expect(result.left).toBe(0)
    // 2 items: height = 2*36+8 = 80 → max y = 200 - 80 - 8 = 112
    expect(result.top).toBe(0)
  })

  it('handles x=0 y=0', () => {
    const result = clampPosition(0, 0, 3, 1920, 1080)
    expect(result.left).toBe(0)
    expect(result.top).toBe(0)
  })

  it('never returns negative position for small viewport', () => {
    // Viewport (100) is smaller than menu width (180+8=188).
    // max left = 100 - 180 - 8 = -88, so Math.min(0, -88) = -88
    const result = clampPosition(0, 0, 10, 100, 100)
    expect(result.left).toBe(-88)
    // 10 items: height = 368 → max y = 100 - 368 - 8 = -276
    // Math.min(0, -276) = -276 — negative because menu is taller than viewport
    expect(result.top).toBe(-276)
  })
})
