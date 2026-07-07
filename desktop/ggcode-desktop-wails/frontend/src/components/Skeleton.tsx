import React from 'react'

interface SkeletonBaseProps {
  width?: number | string
  height?: number | string
  borderRadius?: number | string
  style?: React.CSSProperties
}

/** Single shimmer block */
export function Skeleton({ width = '100%', height = 14, borderRadius = 4, style }: SkeletonBaseProps) {
  return (
    <div
      className="skeleton-shimmer"
      style={{
        width,
        height,
        borderRadius,
        ...style,
      }}
    />
  )
}

/** Skeleton row for list items (avatar + 2 lines of text) */
export function SkeletonRow() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px var(--spacing-lg)' }}>
      <Skeleton width={28} height={28} borderRadius={6} />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 4 }}>
        <Skeleton width="60%" height={12} />
        <Skeleton width="40%" height={10} />
      </div>
    </div>
  )
}

/** Skeleton for a session list sidebar item */
export function SkeletonSessionItem() {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '8px var(--spacing-lg)' }}>
      <Skeleton width={32} height={32} borderRadius={6} />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 5 }}>
        <Skeleton width="70%" height={12} />
        <Skeleton width="50%" height={10} />
        <Skeleton width="85%" height={10} />
      </div>
    </div>
  )
}

/** Skeleton for a single chat message bubble (alternating user/assistant widths) */
export function SkeletonMessage({ align = 'left' }: { align?: 'left' | 'right' }) {
  const isRight = align === 'right'
  return (
    <div style={{
      display: 'flex',
      justifyContent: isRight ? 'flex-end' : 'flex-start',
      padding: 'var(--spacing-xs) var(--spacing-lg)',
    }}>
      <div style={{
        maxWidth: '70%',
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        width: isRight ? '45%' : 'auto',
      }}>
        <Skeleton width={isRight ? '100%' : '55%'} height={14} />
        <Skeleton width="90%" height={14} />
        <Skeleton width="75%" height={14} />
      </div>
    </div>
  )
}

/** Render N skeleton chat messages (alternating user/assistant alignment) */
export function SkeletonMessages({ count = 4 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }).map((_, i) => (
        <SkeletonMessage key={i} align={i % 3 === 0 ? 'right' : 'left'} />
      ))}
    </>
  )
}

/** Render N skeleton rows */
export function SkeletonList({ count = 5, variant = 'row' }: { count?: number; variant?: 'row' | 'session' }) {
  const Component = variant === 'session' ? SkeletonSessionItem : SkeletonRow
  return (
    <>
      {Array.from({ length: count }).map((_, i) => (
        <Component key={i} />
      ))}
    </>
  )
}
