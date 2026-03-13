import React, { useRef, useEffect, useCallback } from 'react'
import { useDrawingWS } from '../ws/useDrawingWS'
import {
  initCanvas,
  startStroke,
  continueStroke,
  endStroke,
  addCommittedStroke,
  removeStroke,
  startRenderLoop,
  stopRenderLoop,
  setDrawingConfig,
} from './drawing'

export default function Canvas({ tool, strokeWidth, userInfo, onUserAssigned, onConnectionStatus }) {
  const canvasRef = useRef(null)
  const isDrawingRef = useRef(false)
  const sendMessageRef = useRef(null)

  // Keep drawing config in sync with props
  useEffect(() => {
    setDrawingConfig(userInfo?.colour || '#ffffff', strokeWidth, tool)
  }, [tool, strokeWidth, userInfo])

  const handleMessage = useCallback((type, payload) => {
    if (!payload) return
    if (type === 'STROKE_COMMITTED') {
      const { strokeId, points, colour, width, strokeTool } = payload
      if (strokeId) {
        addCommittedStroke(strokeId, { points, colour, width, tool: strokeTool || 'pen' })
      }
    } else if (type === 'UNDO_COMPENSATION' || type === 'STROKE_UNDO') {
      if (payload.strokeId) {
        removeStroke(payload.strokeId)
      }
    } else if (type === 'CANVAS_CLEAR') {
      // handled via onCanvasSync with empty entries
    }
  }, [])

  const handleUserAssigned = useCallback((payload) => {
    if (onUserAssigned) onUserAssigned(payload)
    if (payload?.colour) {
      setDrawingConfig(payload.colour, strokeWidth, tool)
    }
  }, [onUserAssigned, strokeWidth, tool])

  const handleCanvasSync = useCallback((entries) => {
    if (!Array.isArray(entries)) return
    entries.forEach((entry) => {
      if (entry.strokeId && entry.data) {
        addCommittedStroke(entry.strokeId, entry.data)
      }
    })
  }, [])

  const { sendMessage, connectionStatus } = useDrawingWS({
    onMessage: handleMessage,
    onUserAssigned: handleUserAssigned,
    onCanvasSync: handleCanvasSync,
  })

  // Keep sendMessage accessible from event handlers
  useEffect(() => {
    sendMessageRef.current = sendMessage
  }, [sendMessage])

  // Propagate connection status up
  useEffect(() => {
    if (onConnectionStatus) onConnectionStatus(connectionStatus)
  }, [connectionStatus, onConnectionStatus])

  // Handle undo/redo events dispatched from Toolbar
  useEffect(() => {
    const onUndo = () => sendMessageRef.current?.('STROKE_UNDO', {})
    const onRedo = () => sendMessageRef.current?.('STROKE_REDO', {})
    window.addEventListener('miniraft:undo', onUndo)
    window.addEventListener('miniraft:redo', onRedo)
    return () => {
      window.removeEventListener('miniraft:undo', onUndo)
      window.removeEventListener('miniraft:redo', onRedo)
    }
  }, [])

  // Init canvas, render loop, and mouse/touch wiring
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const cleanup = initCanvas(canvas)
    startRenderLoop(canvas)

    // Wire mouseup / touchend to finalize stroke and send to WS
    const handleMouseUp = () => {
      if (!isDrawingRef.current) return
      isDrawingRef.current = false
      const stroke = endStroke()
      if (stroke && stroke.points.length > 0) {
        const strokeId = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
        sendMessageRef.current?.('STROKE_DRAW', {
          strokeId,
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          strokeTool: stroke.tool,
        })
        // Optimistically add as committed
        addCommittedStroke(strokeId, {
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          tool: stroke.tool,
        })
      }
    }

    const handleMouseDown = () => {
      isDrawingRef.current = true
    }

    const handleTouchEnd = () => {
      if (!isDrawingRef.current) return
      isDrawingRef.current = false
      const stroke = endStroke()
      if (stroke && stroke.points.length > 0) {
        const strokeId = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
        sendMessageRef.current?.('STROKE_DRAW', {
          strokeId,
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          strokeTool: stroke.tool,
        })
        addCommittedStroke(strokeId, {
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          tool: stroke.tool,
        })
      }
    }

    const handleTouchStart = () => {
      isDrawingRef.current = true
    }

    canvas.addEventListener('mousedown', handleMouseDown)
    canvas.addEventListener('mouseup', handleMouseUp)
    canvas.addEventListener('touchstart', handleTouchStart, { passive: false })
    canvas.addEventListener('touchend', handleTouchEnd, { passive: false })

    return () => {
      cleanup()
      stopRenderLoop()
      canvas.removeEventListener('mousedown', handleMouseDown)
      canvas.removeEventListener('mouseup', handleMouseUp)
      canvas.removeEventListener('touchstart', handleTouchStart)
      canvas.removeEventListener('touchend', handleTouchEnd)
    }
  }, [])

  return (
    <canvas
      ref={canvasRef}
      style={{
        width: '100%',
        height: '100%',
        display: 'block',
        cursor: tool === 'eraser' ? 'cell' : 'crosshair',
        touchAction: 'none',
      }}
    />
  )
}
