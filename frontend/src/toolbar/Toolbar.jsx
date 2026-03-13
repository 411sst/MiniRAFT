import React, { useEffect } from 'react'

export default function Toolbar({
  tool,
  setTool,
  strokeWidth,
  setStrokeWidth,
  onUndo,
  onRedo,
  userColour,
  connectionStatus,
}) {
  // Register keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
        e.preventDefault()
        if (onUndo) onUndo()
      } else if (
        ((e.ctrlKey || e.metaKey) && e.key === 'y') ||
        ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'z')
      ) {
        e.preventDefault()
        if (onRedo) onRedo()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onUndo, onRedo])

  // Connection status indicator
  const statusDotColor =
    connectionStatus === 'connected'
      ? '#22c55e'
      : connectionStatus === 'reconnecting'
      ? '#f59e0b'
      : connectionStatus === 'disconnected'
      ? '#ef4444'
      : '#6b7280'

  const statusLabel =
    connectionStatus === 'connected'
      ? 'Connected'
      : connectionStatus === 'reconnecting'
      ? 'Reconnecting...'
      : connectionStatus === 'disconnected'
      ? 'Disconnected'
      : 'Connecting...'

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        padding: '8px 16px',
        background: '#1f2937',
        borderBottom: '1px solid #374151',
        flexShrink: 0,
        flexWrap: 'wrap',
        minHeight: '52px',
      }}
    >
      {/* App title */}
      <span
        style={{
          fontWeight: 700,
          fontSize: '15px',
          color: '#f9fafb',
          letterSpacing: '0.02em',
          marginRight: '4px',
        }}
      >
        MiniRAFT
      </span>

      <div style={{ width: '1px', height: '28px', background: '#374151' }} />

      {/* User colour badge */}
      {userColour && (
        <div
          title="Your colour"
          style={{
            width: '22px',
            height: '22px',
            borderRadius: '50%',
            backgroundColor: userColour,
            border: '2px solid #6b7280',
            flexShrink: 0,
          }}
        />
      )}

      {/* Pen button */}
      <button
        onClick={() => setTool('pen')}
        title="Pen (draw)"
        style={{
          padding: '5px 12px',
          borderRadius: '6px',
          border: 'none',
          cursor: 'pointer',
          fontWeight: tool === 'pen' ? 700 : 400,
          fontSize: '13px',
          background: tool === 'pen' ? '#3b82f6' : '#374151',
          color: '#f9fafb',
          transition: 'background 0.15s',
        }}
      >
        ✏️ Pen
      </button>

      {/* Eraser button */}
      <button
        onClick={() => setTool(tool === 'eraser' ? 'pen' : 'eraser')}
        title="Eraser"
        style={{
          padding: '5px 12px',
          borderRadius: '6px',
          border: tool === 'eraser' ? '2px solid #f59e0b' : '2px solid transparent',
          cursor: 'pointer',
          fontWeight: tool === 'eraser' ? 700 : 400,
          fontSize: '13px',
          background: tool === 'eraser' ? '#78350f' : '#374151',
          color: tool === 'eraser' ? '#fde68a' : '#f9fafb',
          transition: 'background 0.15s, border-color 0.15s',
        }}
      >
        🧹 Eraser
      </button>

      <div style={{ width: '1px', height: '28px', background: '#374151' }} />

      {/* Stroke width */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <label style={{ color: '#9ca3af', fontSize: '12px', whiteSpace: 'nowrap' }}>
          Width
        </label>
        <input
          type="range"
          min={1}
          max={20}
          value={strokeWidth}
          onChange={(e) => setStrokeWidth(Number(e.target.value))}
          style={{ width: '80px', accentColor: '#3b82f6', cursor: 'pointer' }}
        />
        <span
          style={{
            color: '#f9fafb',
            fontSize: '12px',
            fontFamily: 'monospace',
            minWidth: '18px',
            textAlign: 'right',
          }}
        >
          {strokeWidth}
        </span>
      </div>

      <div style={{ width: '1px', height: '28px', background: '#374151' }} />

      {/* Undo */}
      <button
        onClick={onUndo}
        title="Undo (Ctrl+Z)"
        style={{
          padding: '5px 10px',
          borderRadius: '6px',
          border: 'none',
          cursor: 'pointer',
          fontSize: '13px',
          background: '#374151',
          color: '#f9fafb',
          transition: 'background 0.15s',
        }}
        onMouseOver={(e) => (e.currentTarget.style.background = '#4b5563')}
        onMouseOut={(e) => (e.currentTarget.style.background = '#374151')}
      >
        ↩ Undo
      </button>

      {/* Redo */}
      <button
        onClick={onRedo}
        title="Redo (Ctrl+Y)"
        style={{
          padding: '5px 10px',
          borderRadius: '6px',
          border: 'none',
          cursor: 'pointer',
          fontSize: '13px',
          background: '#374151',
          color: '#f9fafb',
          transition: 'background 0.15s',
        }}
        onMouseOver={(e) => (e.currentTarget.style.background = '#4b5563')}
        onMouseOut={(e) => (e.currentTarget.style.background = '#374151')}
      >
        ↪ Redo
      </button>

      {/* Spacer */}
      <div style={{ flex: 1 }} />

      {/* Connection status */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
        <div
          style={{
            width: '8px',
            height: '8px',
            borderRadius: '50%',
            backgroundColor: statusDotColor,
            flexShrink: 0,
          }}
        />
        <span style={{ color: '#9ca3af', fontSize: '12px', whiteSpace: 'nowrap' }}>
          {statusLabel}
        </span>
      </div>
    </div>
  )
}
