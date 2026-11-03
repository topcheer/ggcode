
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

export function formatTokenValue(n: number): string {
  if (!n || n === 0) return ''
  if (n >= 1000000 && n % 1000000 === 0) return `${n / 1000000}m`
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}m`
  if (n >= 1000 && n % 1000 === 0) return `${n / 1000}k`
  return String(n)
}

export function isValidTokenValue(str: string): boolean {
  const s = str.trim().toLowerCase()
  if (s === '' || s === 'auto') return true
  return /^(\d+(?:\.\d+)?)\s*(k|m|g)?$/.test(s)
}
