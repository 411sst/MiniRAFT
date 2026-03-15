import React, { useRef, useEffect, useCallback } from 'react'
import { useDrawingWS } from '../ws/useDrawingWS'
import {
  initCanvas,
  startStroke,
  continueStroke,
  endStroke,
  addCommittedStroke,
  addPendingStroke,
  confirmStroke,
  getPendingStrokes,
  removeStroke,
  clearAllStrokes,
  startRenderLoop,
  stopRenderLoop,
  setDrawingConfig,
} from './drawing'

export default function Canvas({ tool, strokeWidth, userInfo, onUserAssigned, onConnectionStatus, onLeaderInfo }) {
  const canvasRef = useRef(null)
  const isDrawingRef = useRef(false)
  const sendMessageRef = useRef(null)
  const userIdRef = useRef(null)

  // Per-user undo/redo stacks — each entry is { strokeId, data }
  const undoStackRef = useRef([])   // committed strokes this user owns
  const redoStackRef = useRef([])   // popped strokes available for redo

  useEffect(() => {
    setDrawingConfig(userInfo?.colour || '#ffffff', strokeWidth, tool)
  }, [tool, strokeWidth, userInfo])

  const handleMessage = useCallback((type, payload) => {
    if (!payload) return

    if (type === 'STROKE_COMMITTED') {
      const { strokeId, points, colour, width, strokeTool, userId } = payload
      if (strokeId) {
        const data = { points, colour, width, tool: strokeTool || 'pen' }
        // confirmStroke moves the stroke from pending (70% opacity) to committed (100%).
        // For other users' strokes it just adds to committed directly.
        confirmStroke(strokeId, data)
        // Track strokes this user owns for undo.
        if (userId && userId === userIdRef.current) {
          undoStackRef.current.push({ strokeId, data })
          redoStackRef.current = [] // any new commit clears redo
        }
      }
    } else if (type === 'UNDO_COMPENSATION') {
      if (payload.strokeId) {
        removeStroke(payload.strokeId)
      }
    } else if (type === 'STROKE_UNDO') {
      // Also accept STROKE_UNDO (phase 1 compat)
      if (payload.strokeId) {
        removeStroke(payload.strokeId)
      }
    }
  }, [])

  const handleUserAssigned = useCallback((payload) => {
    if (onUserAssigned) onUserAssigned(payload)
    if (payload?.colour) {
      setDrawingConfig(payload.colour, strokeWidth, tool)
    }
    if (payload?.userId) {
      userIdRef.current = payload.userId
    }
  }, [onUserAssigned, strokeWidth, tool])

  const handleCanvasSync = useCallback((entries) => {
    if (!Array.isArray(entries)) return

    // Capture any locally-pending strokes before clearing — we'll re-send those
    // that the server doesn't know about yet (sent but not yet replicated).
    const localPending = getPendingStrokes()

    // Clear existing canvas state before replaying — critical for reconnect correctness.
    clearAllStrokes()
    const syncedIds = new Set()
    entries.forEach((entry) => {
      if (entry.type === 'UNDO_COMPENSATION') {
        if (entry.strokeId) removeStroke(entry.strokeId)
      } else if (entry.strokeId && entry.data) {
        addCommittedStroke(entry.strokeId, entry.data)
        syncedIds.add(entry.strokeId)
      }
    })

    // Re-send any pending strokes not accounted for in the sync snapshot.
    localPending.forEach(([strokeId, data]) => {
      if (!syncedIds.has(strokeId)) {
        addPendingStroke(strokeId, data)
        sendMessageRef.current?.('STROKE_DRAW', {
          strokeId,
          points: data.points,
          colour: data.colour,
          width: data.width,
          strokeTool: data.tool,
        })
      }
    })
  }, [])

  const { sendMessage, connectionStatus, leaderInfo } = useDrawingWS({
    onMessage: handleMessage,
    onUserAssigned: handleUserAssigned,
    onCanvasSync: handleCanvasSync,
  })

  useEffect(() => {
    sendMessageRef.current = sendMessage
  }, [sendMessage])

  useEffect(() => {
    if (onConnectionStatus) onConnectionStatus(connectionStatus)
  }, [connectionStatus, onConnectionStatus])

  useEffect(() => {
    if (onLeaderInfo) onLeaderInfo(leaderInfo)
  }, [leaderInfo, onLeaderInfo])

  // Undo: pop from undoStack, push to redoStack, send STROKE_UNDO
  useEffect(() => {
    const onUndo = () => {
      const entry = undoStackRef.current.pop()
      if (!entry) return
      redoStackRef.current.push(entry)
      // Optimistically remove locally
      removeStroke(entry.strokeId)
      sendMessageRef.current?.('STROKE_UNDO', { strokeId: entry.strokeId })
    }

    // Redo: pop from redoStack, push to undoStack with a NEW strokeId, re-send as STROKE_DRAW.
    // A new strokeId is required because the Raft log already contains an UNDO_COMPENSATION
    // for the original strokeId — reusing it would create a duplicate log entry.
    const onRedo = () => {
      const entry = redoStackRef.current.pop()
      if (!entry) return
      const newStrokeId = crypto.randomUUID()
      undoStackRef.current.push({ strokeId: newStrokeId, data: entry.data })
      // Show at 70% opacity until STROKE_COMMITTED confirms it.
      addPendingStroke(newStrokeId, entry.data)
      sendMessageRef.current?.('STROKE_DRAW', {
        strokeId: newStrokeId,
        points: entry.data.points,
        colour: entry.data.colour,
        width: entry.data.width,
        strokeTool: entry.data.tool,
      })
    }

    window.addEventListener('miniraft:undo', onUndo)
    window.addEventListener('miniraft:redo', onRedo)
    return () => {
      window.removeEventListener('miniraft:undo', onUndo)
      window.removeEventListener('miniraft:redo', onRedo)
    }
  }, [])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const cleanup = initCanvas(canvas)
    startRenderLoop(canvas)

    const handleMouseUp = () => {
      if (!isDrawingRef.current) return
      isDrawingRef.current = false
      const stroke = endStroke()
      if (stroke && stroke.points.length > 0) {
        const strokeId = crypto.randomUUID()
        const data = { points: stroke.points, colour: stroke.colour, width: stroke.width, tool: stroke.tool }
        sendMessageRef.current?.('STROKE_DRAW', {
          strokeId,
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          strokeTool: stroke.tool,
        })
        // Show at 70% opacity until STROKE_COMMITTED confirms it.
        addPendingStroke(strokeId, data)
      }
    }

    const handleMouseDown = () => { isDrawingRef.current = true }

    const handleTouchEnd = () => {
      if (!isDrawingRef.current) return
      isDrawingRef.current = false
      const stroke = endStroke()
      if (stroke && stroke.points.length > 0) {
        const strokeId = crypto.randomUUID()
        const data = { points: stroke.points, colour: stroke.colour, width: stroke.width, tool: stroke.tool }
        sendMessageRef.current?.('STROKE_DRAW', {
          strokeId,
          points: stroke.points,
          colour: stroke.colour,
          width: stroke.width,
          strokeTool: stroke.tool,
        })
        addPendingStroke(strokeId, data)
      }
    }

    const handleTouchStart = () => { isDrawingRef.current = true }

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
