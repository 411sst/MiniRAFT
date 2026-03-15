import { useEffect, useState } from 'react'

export function useClusterSSE() {
  const [replicas, setReplicas] = useState([])
  const [lastEvent, setLastEvent] = useState(null)

  useEffect(() => {
    const es = new EventSource('/events/cluster-status')

    es.addEventListener('cluster_status', (e) => {
      try {
        const data = JSON.parse(e.data)
        setReplicas(data.replicas || [])
      } catch {
        console.warn('SSE: failed to parse cluster_status', e.data)
      }
    })

    es.addEventListener('election_started', (e) => {
      try {
        setLastEvent({ type: 'election_started', ...JSON.parse(e.data) })
      } catch {
        setLastEvent({ type: 'election_started' })
      }
    })

    es.addEventListener('leader_elected', (e) => {
      try {
        setLastEvent({ type: 'leader_elected', ...JSON.parse(e.data) })
      } catch {
        setLastEvent({ type: 'leader_elected' })
      }
    })

    es.onerror = () => {
      // EventSource reconnects automatically
    }

    return () => es.close()
  }, [])

  return { replicas, lastEvent }
}
