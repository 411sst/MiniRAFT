import React from 'react'
import { useClusterSSE } from './useClusterSSE'
import NodeCard from './NodeCard'
import ChaosButton from '../chaos/ChaosButton'

const REPLICA_IDS = ['replica1', 'replica2', 'replica3']

export default function ClusterPanel() {
  const { replicas, lastEvent } = useClusterSSE()

  // Map replicas array to a lookup by replicaId
  const replicaMap = {}
  replicas.forEach((r) => {
    if (r.replicaId) replicaMap[r.replicaId] = r
  })

  const isEmpty = replicas.length === 0

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        padding: '12px',
        gap: '12px',
        overflowY: 'auto',
      }}
    >
      {/* Panel header */}
      <div>
        <h2 className="text-white font-bold text-base">Cluster Status</h2>
        <p className="text-gray-500 text-xs mt-0.5">RAFT consensus nodes</p>
      </div>

      {/* Connecting message */}
      {isEmpty && (
        <div className="bg-gray-800 rounded-lg p-3 border border-gray-700 text-center">
          <p className="text-gray-400 text-sm">Connecting to cluster...</p>
          <div className="mt-2 flex justify-center">
            <div
              style={{
                width: 20,
                height: 20,
                border: '2px solid #4B5563',
                borderTopColor: '#6B7280',
                borderRadius: '50%',
                animation: 'spin 1s linear infinite',
              }}
            />
          </div>
        </div>
      )}

      {/* Node cards */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
        {REPLICA_IDS.map((id) => (
          <NodeCard key={id} status={isEmpty ? null : (replicaMap[id] || { replicaId: id, healthy: false, state: 'UNKNOWN' })} />
        ))}
      </div>

      {/* Last event notification */}
      {lastEvent && (
        <div
          className={
            'rounded-lg p-3 border text-xs ' +
            (lastEvent.type === 'leader_elected'
              ? 'bg-green-900 border-green-600 text-green-200'
              : 'bg-amber-900 border-amber-600 text-amber-200')
          }
        >
          <div className="font-bold uppercase tracking-wide mb-1">
            {lastEvent.type === 'leader_elected' ? 'Leader Elected' : 'Election Started'}
          </div>
          {lastEvent.type === 'leader_elected' && lastEvent.leaderId && (
            <div>New leader: <span className="font-mono text-green-300">{lastEvent.leaderId}</span></div>
          )}
          {lastEvent.term != null && (
            <div>Term: <span className="font-mono">{lastEvent.term}</span></div>
          )}
        </div>
      )}

      {/* Spacer */}
      <div style={{ flex: 1 }} />

      {/* Chaos controls */}
      <div>
        <h3 className="text-gray-400 text-xs font-semibold uppercase tracking-wide mb-2">Chaos Engineering</h3>
        <ChaosButton />
      </div>

      {/* Spin keyframe injected inline */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}
