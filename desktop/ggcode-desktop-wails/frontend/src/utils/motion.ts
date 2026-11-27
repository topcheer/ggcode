// Shared motion helpers for the desktop frontend. Durations/easings live in
// App.css as CSS variables (--dur-*, --ease-*); this file only exposes the
// JS-side decisions (reduced motion, scroll behaviour).

/** True when the user asked the OS to reduce motion (maps to the CSS rule in style.css). */
export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches
}

/** Scroll behaviour that honours the reduced-motion preference. */
export function scrollBehavior(): ScrollBehavior {
  return prefersReducedMotion() ? 'auto' : 'smooth'
}
