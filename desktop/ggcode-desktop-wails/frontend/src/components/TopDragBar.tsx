import React from 'react'

// TopDragBar provides a 36px draggable spacer at the top of each page,
// aligned with NavRail's traffic light area. Uses Wails native CSS property.
export function TopDragBar() {
  return (
    <div style={{
      height: 36,
      flexShrink: 0,
      '--wails-draggable': 'drag',
    } as React.CSSProperties} />
  )
}
