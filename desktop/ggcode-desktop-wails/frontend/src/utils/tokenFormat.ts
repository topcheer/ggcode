/**
 * Parse a human-readable token value into an integer.
 *
 * Supports formats:
 *   "256k" / "256K" → 262144
 *   "1m"   / "1M"   → 1048576
 *   "320000"        → 320000
 *   "" / "auto"     → 0 (auto)
 *
 * Returns 0 for invalid/unparseable input.
 */
export function parseTokenValue(str: string): number {
  const s = str.trim().toLowerCase()
  if (s === '' || s === 'auto' || s === '0') return 0

  const match = s.match(/^(\d+(?:\.\d+)?)\s*(k|m|g)?$/)
  if (!match) return 0

  const num = parseFloat(match[1])
  const suffix = match[2]

  if (suffix === 'k') return Math.round(num * 1000)
  if (suffix === 'm') return Math.round(num * 1000000)
  if (suffix === 'g') return Math.round(num * 1000000000)
  return Math.round(num)
}

/**
 * Format a token count for display in input fields.
 *
 *   262144 → "262k"  (rounds to nearest k for large numbers)
 *   1048576 → "1m"
 *   8192 → "8192"    (small numbers stay raw)
 *   0 → ""
 */
export function formatTokenValue(n: number): string {
  if (!n || n === 0) return ''
  if (n >= 1000000 && n % 1000000 === 0) return `${n / 1000000}m`
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}m`
  if (n >= 1000 && n % 1000 === 0) return `${n / 1000}k`
  return String(n)
}

/**
 * Validate whether a string is a valid token value expression.
 */
export function isValidTokenValue(str: string): boolean {
  const s = str.trim().toLowerCase()
  if (s === '' || s === 'auto') return true
  return /^(\d+(?:\.\d+)?)\s*(k|m|g)?$/.test(s)
}
