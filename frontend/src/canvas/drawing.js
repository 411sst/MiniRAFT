// ─── Drawing State ───────────────────────────────────────────────────────────
let committedStrokes = new Map() // strokeId -> { points, colour, width, tool }
let pendingStroke = null         // current user's in-progress stroke (optimistic)
let dirty = true

let rafId = null
let isDrawing = false
let currentColour = '#ffffff'
let currentWidth = 4
let currentTool = 'pen'

// Event listener cleanup handles
let _cleanupFns = []

// ─── Canvas Initialisation ───────────────────────────────────────────────────
export function initCanvas(canvas) {
  // Make canvas pixel dimensions match display dimensions
  const resize = () => {
    const { width, height } = canvas.getBoundingClientRect()
    if (canvas.width !== width || canvas.height !== height) {
      canvas.width = width
      canvas.height = height
      dirty = true
    }
  }

  const ro = new ResizeObserver(resize)
  ro.observe(canvas)
  resize()

  // Mouse events
  const onMouseDown = (e) => {
    if (e.button !== 0) return
    const { x, y } = getPos(canvas, e)
    startStroke(canvas, currentColour, currentWidth, currentTool)
    continueStroke(canvas, x, y)
    isDrawing = true
  }

  const onMouseMove = (e) => {
    if (!isDrawing) return
    const { x, y } = getPos(canvas, e)
    continueStroke(canvas, x, y)
  }

  const onMouseUp = (e) => {
    if (!isDrawing) return
    isDrawing = false
    // endStroke is called externally from Canvas.jsx to send WS message
  }

  const onMouseLeave = (e) => {
    if (!isDrawing) return
    isDrawing = false
  }

  // Touch events
  const onTouchStart = (e) => {
    e.preventDefault()
    const touch = e.touches[0]
    const { x, y } = getPos(canvas, touch)
    startStroke(canvas, currentColour, currentWidth, currentTool)
    continueStroke(canvas, x, y)
    isDrawing = true
  }

  const onTouchMove = (e) => {
    e.preventDefault()
    if (!isDrawing) return
    const touch = e.touches[0]
    const { x, y } = getPos(canvas, touch)
    continueStroke(canvas, x, y)
  }

  const onTouchEnd = (e) => {
    e.preventDefault()
    isDrawing = false
  }

  canvas.addEventListener('mousedown', onMouseDown)
  canvas.addEventListener('mousemove', onMouseMove)
  canvas.addEventListener('mouseup', onMouseUp)
  canvas.addEventListener('mouseleave', onMouseLeave)
  canvas.addEventListener('touchstart', onTouchStart, { passive: false })
  canvas.addEventListener('touchmove', onTouchMove, { passive: false })
  canvas.addEventListener('touchend', onTouchEnd, { passive: false })

  _cleanupFns = [
    () => canvas.removeEventListener('mousedown', onMouseDown),
    () => canvas.removeEventListener('mousemove', onMouseMove),
    () => canvas.removeEventListener('mouseup', onMouseUp),
    () => canvas.removeEventListener('mouseleave', onMouseLeave),
    () => canvas.removeEventListener('touchstart', onTouchStart),
    () => canvas.removeEventListener('touchmove', onTouchMove),
    () => canvas.removeEventListener('touchend', onTouchEnd),
    () => ro.disconnect(),
  ]

  return () => {
    _cleanupFns.forEach(fn => fn())
    _cleanupFns = []
  }
}

// ─── Coordinate Helper ───────────────────────────────────────────────────────
function getPos(canvas, e) {
  const rect = canvas.getBoundingClientRect()
  return {
    x: (e.clientX - rect.left) * (canvas.width / rect.width),
    y: (e.clientY - rect.top) * (canvas.height / rect.height),
  }
}

// ─── Stroke API ──────────────────────────────────────────────────────────────
export function startStroke(canvas, colour, width, tool) {
  currentColour = colour || '#ffffff'
  currentWidth = width || 4
  currentTool = tool || 'pen'
  pendingStroke = { points: [], colour: currentColour, width: currentWidth, tool: currentTool }
  dirty = true
}

export function continueStroke(canvas, x, y) {
  if (!pendingStroke) return
  pendingStroke.points.push({ x, y })
  dirty = true
}

export function endStroke() {
  if (!pendingStroke || pendingStroke.points.length === 0) {
    pendingStroke = null
    return null
  }
  const stroke = { ...pendingStroke }
  pendingStroke = null
  dirty = true
  return stroke
}

// ─── Committed Strokes ───────────────────────────────────────────────────────
export function addCommittedStroke(strokeId, data) {
  committedStrokes.set(strokeId, data)
  dirty = true
}

export function removeStroke(strokeId) {
  if (committedStrokes.delete(strokeId)) {
    dirty = true
  }
}

export function clearAllStrokes() {
  committedStrokes.clear()
  pendingStroke = null
  dirty = true
}

// ─── Rendering ───────────────────────────────────────────────────────────────
export function redraw(canvas) {
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  // Clear
  ctx.clearRect(0, 0, canvas.width, canvas.height)

  // Draw committed strokes
  committedStrokes.forEach((stroke) => {
    drawStroke(ctx, stroke)
  })

  // Draw pending (optimistic) stroke on top
  if (pendingStroke && pendingStroke.points.length > 0) {
    drawStroke(ctx, pendingStroke)
  }

  dirty = false
}

function drawStroke(ctx, stroke) {
  const { points, colour, width, tool } = stroke
  if (!points || points.length === 0) return

  ctx.save()

  if (tool === 'eraser') {
    ctx.globalCompositeOperation = 'destination-out'
    ctx.strokeStyle = 'rgba(0,0,0,1)'
  } else {
    ctx.globalCompositeOperation = 'source-over'
    ctx.strokeStyle = colour || '#ffffff'
  }

  ctx.lineWidth = width || 4
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'

  ctx.beginPath()
  if (points.length === 1) {
    // Single dot
    ctx.arc(points[0].x, points[0].y, (width || 4) / 2, 0, Math.PI * 2)
    if (tool === 'eraser') {
      ctx.fillStyle = 'rgba(0,0,0,1)'
    } else {
      ctx.fillStyle = colour || '#ffffff'
    }
    ctx.fill()
  } else {
    ctx.moveTo(points[0].x, points[0].y)
    for (let i = 1; i < points.length; i++) {
      // Smooth curve using quadratic bezier
      if (i < points.length - 1) {
        const midX = (points[i].x + points[i + 1].x) / 2
        const midY = (points[i].y + points[i + 1].y) / 2
        ctx.quadraticCurveTo(points[i].x, points[i].y, midX, midY)
      } else {
        ctx.lineTo(points[i].x, points[i].y)
      }
    }
    ctx.stroke()
  }

  ctx.restore()
}

// ─── RAF Loop ────────────────────────────────────────────────────────────────
export function startRenderLoop(canvas) {
  if (rafId !== null) return // already running

  const loop = () => {
    if (dirty) {
      redraw(canvas)
    }
    rafId = requestAnimationFrame(loop)
  }
  rafId = requestAnimationFrame(loop)
}

export function stopRenderLoop() {
  if (rafId !== null) {
    cancelAnimationFrame(rafId)
    rafId = null
  }
}

// ─── Expose isDrawing state for Canvas.jsx ───────────────────────────────────
export function getIsDrawing() {
  return isDrawing
}

export function setDrawingConfig(colour, width, tool) {
  currentColour = colour || currentColour
  currentWidth = width || currentWidth
  currentTool = tool || currentTool
}
