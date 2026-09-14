import React, { useState, useEffect, useRef } from 'react'
import { Gauge } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import type { wailskit } from '../../wailsjs/go/models'

// #2150 batch 3: status-bar usage badge for the ACTIVE vendor.
// Contract (mirror of the IM surface): ambient information, never an error -
// no data / probe failure / bridge-down all render NOTHING, not a warning.
// Thresholds match the TUI sidebar (80% amber, 95% red). Poll cadence 3min
// (the backend cache TTL - polling faster would only hit the cache).
const WARN_THRESHOLD = 80
const CRIT_THRESHOLD = 95
const POLL_INTERVAL_MS = 3 * 60 * 1000

export function UsageBadge() {
  const [info, setInfo] = useState<wailskit.UsageInfoResult | null>(null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    const poll = async () => {
      try {
        const res = await App.GetUsageInfo()
        if (!mounted.current) return
        setInfo(res ?? null)
      } catch {
        // Ambient surface: probe/bridge failure = render nothing.
        if (mounted.current) setInfo(null)
      }
    }
    void poll()
    const id = window.setInterval(poll, POLL_INTERVAL_MS)
    return () => { mounted.current = false; window.clearInterval(id) }
  }, [])

  // No data or soft error: no badge at all.
  if (!info || info.error) return null

  const main = info.windows?.[0]
  const hasBalance = info.balance !== null && info.balance !== undefined
  if (!main && !hasBalance) return null

  const pct = main ? Math.round(main.usedPercent) : null
  const color =
    pct === null
      ? 'var(--text-secondary)'
      : pct >= CRIT_THRESHOLD
        ? 'var(--color-error, #ef4444)'
        : pct >= WARN_THRESHOLD
          ? 'var(--color-warning)'
          : 'var(--color-success)'

  const titleParts: string[] = [info.vendor]
  if (hasBalance) titleParts.push(`balance ${info.balance!.toFixed(2)}`)
  if (main) {
    titleParts.push(`${main.label} window ${pct}% used`)
    if (main.resetsAt) titleParts.push(`resets ${main.resetsAt}`)
  }

  return (
    <span
      title={titleParts.join(' · ')}
      aria-label={titleParts.join(', ')}
      style={{
        display: 'flex', alignItems: 'center', gap: 3,
        color, fontSize: 10, fontFamily: 'var(--font-mono)',
        fontVariantNumeric: 'tabular-nums',
      }}
    >
      <Gauge size={11} />
      {hasBalance ? info.balance!.toFixed(2) : null}
      {pct !== null ? `${pct}%` : null}
    </span>
  )
}
