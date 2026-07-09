// @vitest-environment jsdom
// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { Skeleton, SkeletonRow, SkeletonSessionItem, SkeletonMessage, SkeletonMessages, SkeletonList } from './Skeleton'

describe('Skeleton', () => {
  it('renders a single shimmer block with default props', () => {
    const { container } = render(<Skeleton />)
    const el = container.firstChild as HTMLElement
    expect(el).toBeDefined()
    expect(el.className).toContain('skeleton-shimmer')
  })

  it('applies custom width and height', () => {
    const { container } = render(<Skeleton width={200} height={30} />)
    const el = container.firstChild as HTMLElement
    expect(el.style.width).toBe('200px')
    expect(el.style.height).toBe('30px')
  })

  it('applies string width', () => {
    const { container } = render(<Skeleton width="80%" />)
    const el = container.firstChild as HTMLElement
    expect(el.style.width).toBe('80%')
  })

  it('applies custom borderRadius', () => {
    const { container } = render(<Skeleton borderRadius={8} />)
    const el = container.firstChild as HTMLElement
    expect(el.style.borderRadius).toBe('8px')
  })
})

describe('SkeletonRow', () => {
  it('renders a row with avatar placeholder and two text lines', () => {
    const { container } = render(<SkeletonRow />)
    const shimmerBlocks = container.querySelectorAll('.skeleton-shimmer')
    // avatar + line1 + line2 = 3 blocks
    expect(shimmerBlocks.length).toBe(3)
  })
})

describe('SkeletonSessionItem', () => {
  it('renders a session item with avatar and three text lines', () => {
    const { container } = render(<SkeletonSessionItem />)
    const shimmerBlocks = container.querySelectorAll('.skeleton-shimmer')
    // avatar + line1 + line2 + line3 = 4 blocks
    expect(shimmerBlocks.length).toBe(4)
  })
})

describe('SkeletonMessage', () => {
  it('renders left-aligned message (assistant style)', () => {
    const { container } = render(<SkeletonMessage align="left" />)
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.style.justifyContent).toBe('flex-start')
    const shimmerBlocks = container.querySelectorAll('.skeleton-shimmer')
    expect(shimmerBlocks.length).toBeGreaterThanOrEqual(2)
  })

  it('renders right-aligned message (user style)', () => {
    const { container } = render(<SkeletonMessage align="right" />)
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.style.justifyContent).toBe('flex-end')
  })
})

describe('SkeletonMessages', () => {
  it('renders the specified number of messages', () => {
    const { container } = render(<SkeletonMessages count={3} />)
    // Each message is wrapped in a flex div
    const wrappers = container.children
    expect(wrappers.length).toBe(3)
  })

  it('defaults to 4 messages', () => {
    const { container } = render(<SkeletonMessages />)
    expect(container.children.length).toBe(4)
  })

  it('handles count=0', () => {
    const { container } = render(<SkeletonMessages count={0} />)
    expect(container.children.length).toBe(0)
  })
})

describe('SkeletonList', () => {
  it('renders rows by default', () => {
    const { container } = render(<SkeletonList count={3} />)
    // Each row has 3 shimmer blocks (avatar + 2 lines)
    const shimmerBlocks = container.querySelectorAll('.skeleton-shimmer')
    expect(shimmerBlocks.length).toBe(3 * 3) // 3 rows * 3 blocks each
  })

  it('renders session variant', () => {
    const { container } = render(<SkeletonList count={2} variant="session" />)
    // Each session item has 4 shimmer blocks (avatar + 3 lines)
    const shimmerBlocks = container.querySelectorAll('.skeleton-shimmer')
    expect(shimmerBlocks.length).toBe(2 * 4) // 2 sessions * 4 blocks each
  })
})
