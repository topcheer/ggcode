/**
 * useOnlineStatus - real-time network connectivity detection.
 *
 * Gap fixed: The desktop app had no online/offline indicator. When the
 * network dropped (Wi-Fi disconnect, VPN failure, etc.), the user had no
 * visual feedback -- they'd type a message, hit send, and wait for a
 * confusing timeout error. Claude Desktop, ChatGPT Desktop, and Cursor
 * all show a connectivity indicator.
 *
 * This hook:
 *  - Tracks navigator.onLine (initial + event-driven)
 *  - Listens to 'online' and 'offline' window events
 *  - Exposes isOnline boolean for components to react
 *
 * Usage:
 *   const { isOnline } = useOnlineStatus()
 */

import { useState, useEffect } from 'react'

export interface UseOnlineStatusResult {
  /** Whether the browser reports network connectivity. */
  isOnline: boolean
}

export function useOnlineStatus(): UseOnlineStatusResult {
  const [isOnline, setIsOnline] = useState<boolean>(
    typeof navigator !== 'undefined' ? navigator.onLine : true
  )

  useEffect(() => {
    const handleOnline = () => setIsOnline(true)
    const handleOffline = () => setIsOnline(false)

    window.addEventListener('online', handleOnline)
    window.addEventListener('offline', handleOffline)

    return () => {
      window.removeEventListener('online', handleOnline)
      window.removeEventListener('offline', handleOffline)
    }
  }, [])

  return { isOnline }
}
