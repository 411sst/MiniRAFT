import React, { useState, useEffect } from 'react'

export default function ChaosButton() {
  const [target, setTarget] = useState('random')
  const [mode, setMode] = useState('random')
  const [lastResult, setLastResult] = useState(null)
  const [loading, setLoading] = useState(false)
  const [showToast, setShowToast] = useState(false)

  // Auto-hide toast after 4 seconds
  useEffect(() => {
    if (lastResult) {
      setShowToast(true)
      const timer = setTimeout(() => setShowToast(false), 4000)
      return () => clearTimeout(timer)
    }
  }, [lastResult])

  const unleashChaos = async () => {
    setLoading(true)
    try {
      const res = await fetch('/chaos', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target, mode }),
      })
      const data = await res.json()
      setLastResult(data)
    } catch (e) {
      setLastResult({ error: e.message })
    }
    setLoading(false)
  }

  const selectStyle = {
    background: '#374151',
    color: '#f9fafb',
    border: '1px solid #4b5563',
    borderRadius: '6px',
    padding: '4px 6px',
    fontSize: '12px',
    cursor: 'pointer',
    width: '100%',
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
      {/* Target selector */}
      <div>
        <label style={{ color: '#9ca3af', fontSize: '11px', display: 'block', marginBottom: '3px' }}>
          Target
        </label>
        <select
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          style={selectStyle}
        >
          <option value="random">Random</option>
          <option value="replica1">replica1</option>
          <option value="replica2">replica2</option>
          <option value="replica3">replica3</option>
        </select>
      </div>

      {/* Mode selector */}
      <div>
        <label style={{ color: '#9ca3af', fontSize: '11px', display: 'block', marginBottom: '3px' }}>
          Mode
        </label>
        <select
          value={mode}
          onChange={(e) => setMode(e.target.value)}
          style={selectStyle}
        >
          <option value="random">Surprise</option>
          <option value="graceful">Graceful</option>
          <option value="hard">Hard Kill</option>
        </select>
      </div>

      {/* Chaos button */}
      <button
        onClick={unleashChaos}
        disabled={loading}
        style={{
          padding: '8px 12px',
          borderRadius: '6px',
          border: 'none',
          cursor: loading ? 'not-allowed' : 'pointer',
          fontSize: '13px',
          fontWeight: 700,
          background: loading ? '#7f1d1d' : '#dc2626',
          color: '#fef2f2',
          opacity: loading ? 0.7 : 1,
          transition: 'background 0.15s, opacity 0.15s',
          width: '100%',
        }}
        onMouseOver={(e) => { if (!loading) e.currentTarget.style.background = '#b91c1c' }}
        onMouseOut={(e) => { if (!loading) e.currentTarget.style.background = '#dc2626' }}
      >
        {loading ? '⏳ Working...' : '💥 Unleash Chaos'}
      </button>

      {/* Toast notification */}
      {showToast && lastResult && (
        <div
          style={{
            padding: '8px 10px',
            borderRadius: '6px',
            fontSize: '11px',
            border: '1px solid',
            background: lastResult.error ? '#450a0a' : '#14532d',
            borderColor: lastResult.error ? '#991b1b' : '#166534',
            color: lastResult.error ? '#fca5a5' : '#86efac',
            wordBreak: 'break-word',
          }}
        >
          {lastResult.error ? (
            <span>Error: {lastResult.error}</span>
          ) : lastResult.killed ? (
            <span>
              Killed <strong>{lastResult.killed}</strong> ({lastResult.mode === 'graceful' ? 'graceful stop' : 'hard kill'})
            </span>
          ) : (
            <span>{JSON.stringify(lastResult)}</span>
          )}
        </div>
      )}
    </div>
  )
}
