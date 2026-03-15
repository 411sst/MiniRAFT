package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// GatewayMetrics holds all Prometheus metrics for the gateway service.
type GatewayMetrics struct {
	WebsocketConnections prometheus.Gauge
	StrokesForwarded     prometheus.Counter
	LeaderChanges        prometheus.Counter
	ChaosEvents          *prometheus.CounterVec
}

// NewGatewayMetrics creates and registers all gateway metrics with Prometheus.
func NewGatewayMetrics() *GatewayMetrics {
	m := &GatewayMetrics{
		WebsocketConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_websocket_connections_total",
			Help: "Current number of active WebSocket connections.",
		}),
		StrokesForwarded: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gateway_strokes_forwarded_total",
			Help: "Total number of strokes forwarded to the leader.",
		}),
		LeaderChanges: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gateway_leader_changes_total",
			Help: "Total number of leader changes observed.",
		}),
		ChaosEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_chaos_events_total",
			Help: "Total number of chaos events triggered, labelled by mode.",
		}, []string{"mode"}),
	}

	prometheus.MustRegister(
		m.WebsocketConnections,
		m.StrokesForwarded,
		m.LeaderChanges,
		m.ChaosEvents,
	)

	return m
}

// IncrConnections increments the WebSocket connections gauge.
func (m *GatewayMetrics) IncrConnections() {
	m.WebsocketConnections.Inc()
}

// DecrConnections decrements the WebSocket connections gauge.
func (m *GatewayMetrics) DecrConnections() {
	m.WebsocketConnections.Dec()
}

// IncrStrokes increments the strokes forwarded counter.
func (m *GatewayMetrics) IncrStrokes() {
	m.StrokesForwarded.Inc()
}

// IncrLeaderChanges increments the leader changes counter.
func (m *GatewayMetrics) IncrLeaderChanges() {
	m.LeaderChanges.Inc()
}

// IncrChaos increments the chaos events counter for the given mode.
func (m *GatewayMetrics) IncrChaos(mode string) {
	m.ChaosEvents.WithLabelValues(mode).Inc()
}
