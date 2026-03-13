import React, { useState, useCallback } from 'react'
import Toolbar from './toolbar/Toolbar'
import Canvas from './canvas/Canvas'
import ClusterPanel from './dashboard/ClusterPanel'

export default function App() {
  const [selectedTool, setSelectedTool] = useState('pen')
  const [strokeWidth, setStrokeWidth] = useState(4)
  const [userInfo, setUserInfo] = useState(null)
  const [connectionStatus, setConnectionStatus] = useState('connecting')

  const handleUserAssigned = useCallback((payload) => {
    setUserInfo({ userId: payload.userId, colour: payload.colour })
  }, [])

  const handleUndo = useCallback(() => {
    // Undo logic is triggered via keyboard in Toolbar; Canvas listens to sendMessage
    window.dispatchEvent(new CustomEvent('miniraft:undo'))
  }, [])

  const handleRedo = useCallback(() => {
    window.dispatchEvent(new CustomEvent('miniraft:redo'))
  }, [])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', width: '100%', height: '100%', background: '#111827' }}>
      {/* Toolbar */}
      <Toolbar
        tool={selectedTool}
        setTool={setSelectedTool}
        strokeWidth={strokeWidth}
        setStrokeWidth={setStrokeWidth}
        onUndo={handleUndo}
        onRedo={handleRedo}
        userColour={userInfo?.colour}
        connectionStatus={connectionStatus}
      />

      {/* Main content area */}
      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        {/* Canvas area — 75% */}
        <div style={{ flex: 3, position: 'relative', background: '#1f2937', borderRight: '1px solid #374151' }}>
          <Canvas
            tool={selectedTool}
            strokeWidth={strokeWidth}
            userInfo={userInfo}
            onUserAssigned={handleUserAssigned}
            onConnectionStatus={setConnectionStatus}
          />
        </div>

        {/* Sidebar — 25% */}
        <div style={{ flex: 1, overflowY: 'auto', background: '#111827' }}>
          <ClusterPanel />
        </div>
      </div>
    </div>
  )
}
