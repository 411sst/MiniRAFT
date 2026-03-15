import { useEffect, useRef, useCallback, useState } from 'react'

export function useDrawingWS({ onMessage, onUserAssigned, onCanvasSync }) {
  const wsRef = useRef(null)
  const reconnectTimeoutRef = useRef(null)
  const reconnectDelayRef = useRef(500)
  const mountedRef = useRef(true)

  const [connectionStatus, setConnectionStatus] = useState('connecting')
  const [leaderInfo, setLeaderInfo] = useState(null)

  // Store latest callbacks in refs to avoid stale closures
  const onMessageRef = useRef(onMessage)
  const onUserAssignedRef = useRef(onUserAssigned)
  const onCanvasSyncRef = useRef(onCanvasSync)
  useEffect(() => { onMessageRef.current = onMessage }, [onMessage])
  useEffect(() => { onUserAssignedRef.current = onUserAssigned }, [onUserAssigned])
  useEffect(() => { onCanvasSyncRef.current = onCanvasSync }, [onCanvasSync])

  const connect = useCallback(() => {
    if (!mountedRef.current) return

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${window.location.host}/ws`

    setConnectionStatus('connecting')

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      if (!mountedRef.current) { ws.close(); return }
      reconnectDelayRef.current = 500
      setConnectionStatus('connected')
    }

    ws.onmessage = (event) => {
      if (!mountedRef.current) return
      let msg
      try {
        msg = JSON.parse(event.data)
      } catch {
        console.warn('WS: failed to parse message', event.data)
        return
      }

      const { type, payload } = msg

      switch (type) {
        case 'USER_COLOR_ASSIGNED':
          if (onUserAssignedRef.current) onUserAssignedRef.current(payload)
          break
        case 'CANVAS_SYNC':
          if (onCanvasSyncRef.current) onCanvasSyncRef.current(payload.entries || [])
          break
        case 'STROKE_COMMITTED':
          if (onMessageRef.current) onMessageRef.current('STROKE_COMMITTED', payload)
          break
        case 'LEADER_CHANGED':
          if (payload) {
            setLeaderInfo({ leaderId: payload.leaderId, term: payload.term })
          }
          if (onMessageRef.current) onMessageRef.current('LEADER_CHANGED', payload)
          break
        default:
          if (onMessageRef.current) onMessageRef.current(type, payload)
      }
    }

    ws.onclose = () => {
      if (!mountedRef.current) return
      setConnectionStatus('reconnecting')
      wsRef.current = null
      const delay = reconnectDelayRef.current
      reconnectDelayRef.current = Math.min(delay * 2, 16000)
      reconnectTimeoutRef.current = setTimeout(connect, delay)
    }

    ws.onerror = () => {
      // onclose will fire after onerror, handles reconnect
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    connect()
    return () => {
      mountedRef.current = false
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current)
      if (wsRef.current) {
        wsRef.current.onclose = null // prevent reconnect on intentional close
        wsRef.current.close()
      }
    }
  }, [connect])

  const sendMessage = useCallback((type, payload) => {
    const ws = wsRef.current
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type, payload }))
    } else {
      console.warn('WS: cannot send, not connected. type=', type)
    }
  }, [])

  return { sendMessage, connectionStatus, leaderInfo }
}
