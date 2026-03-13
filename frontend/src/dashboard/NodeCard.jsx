import React from 'react'

export default function NodeCard({ status }) {
  if (!status) {
    return (
      <div className="bg-gray-800 rounded-lg p-3 border border-gray-700">
        <p className="text-gray-500 text-sm">Connecting...</p>
      </div>
    )
  }

  const {
    replicaId,
    state,
    term,
    logLength,
    commitIndex,
    leaderId,
    lastHeartbeatMs,
    healthy,
  } = status

  const isLeader = state === 'LEADER'
  const isCandidate = state === 'CANDIDATE'
  const isFollower = state === 'FOLLOWER'
  const isUnreachable = !healthy

  // Card border/ring based on state
  let cardClass = 'bg-gray-800 rounded-lg p-3 border transition-all duration-300 '
  if (isLeader) {
    cardClass += 'border-green-500 ring-2 ring-green-400'
  } else if (isCandidate) {
    cardClass += 'border-amber-500 animate-pulse'
  } else {
    cardClass += 'border-gray-700'
  }

  // State badge
  let badgeClass = 'inline-block text-xs font-bold px-2 py-0.5 rounded uppercase tracking-wide '
  if (isLeader) {
    badgeClass += 'bg-green-500 text-white'
  } else if (isCandidate) {
    badgeClass += 'bg-amber-500 text-white animate-pulse'
  } else {
    badgeClass += 'bg-gray-600 text-gray-200'
  }

  return (
    <div className={cardClass}>
      {/* Header */}
      <div className="flex items-center justify-between mb-2">
        <span className="text-white font-semibold text-sm truncate">{replicaId || 'unknown'}</span>
        {isUnreachable && (
          <span className="text-xs font-bold px-2 py-0.5 rounded bg-red-600 text-white uppercase tracking-wide ml-1">
            UNREACHABLE
          </span>
        )}
      </div>

      {/* State badge */}
      <div className="mb-2">
        <span className={badgeClass}>{state || 'UNKNOWN'}</span>
      </div>

      {/* Stats */}
      <div className="space-y-1 text-xs text-gray-400">
        <div className="flex justify-between">
          <span>Term</span>
          <span className="text-gray-200 font-mono">{term ?? '—'}</span>
        </div>
        <div className="flex justify-between">
          <span>Log</span>
          <span className="text-gray-200 font-mono">{logLength ?? '—'} entries</span>
        </div>
        <div className="flex justify-between">
          <span>Committed</span>
          <span className="text-gray-200 font-mono">{commitIndex ?? '—'}</span>
        </div>
        {leaderId && !isLeader && (
          <div className="flex justify-between">
            <span>Leader</span>
            <span className="text-green-400 font-mono truncate max-w-24">{leaderId}</span>
          </div>
        )}
        {lastHeartbeatMs != null && (
          <div className="flex justify-between">
            <span>Last HB</span>
            <span className="text-gray-300 font-mono">{lastHeartbeatMs}ms</span>
          </div>
        )}
      </div>
    </div>
  )
}
