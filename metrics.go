package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics for QUIC load balancer
var (
	// Connection metrics
	connectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_connections_total",
			Help: "Total number of QUIC connections established",
		},
		[]string{"protocol", "backend_id"},
	)

	activeConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_active_connections",
			Help: "Current number of active QUIC connections",
		},
		[]string{"protocol", "backend_id"},
	)

	connectionMigrations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_connection_migrations_total",
			Help: "Total number of connection migrations",
		},
		[]string{"reason", "success"},
	)

	migrationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "quic_migration_duration_seconds",
			Help:    "Duration of connection migrations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"reason"},
	)

	// Request metrics
	requestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "status", "protocol", "backend_id"},
	)

	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "quic_request_duration_seconds",
			Help:    "Duration of HTTP requests",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "status", "protocol", "backend_id"},
	)

	// Backend metrics
	backendHealthy = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_backend_healthy",
			Help: "Backend health status (1 = healthy, 0 = unhealthy)",
		},
		[]string{"backend_id", "url"},
	)

	backendResponseTime = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_backend_response_time_seconds",
			Help: "Backend response time in seconds",
		},
		[]string{"backend_id", "url"},
	)

	backendConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_backend_connections",
			Help: "Number of connections to backend",
		},
		[]string{"backend_id", "url"},
	)

	backendWeight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_backend_weight",
			Help: "Backend weight for load balancing",
		},
		[]string{"backend_id", "url"},
	)

	// Circuit breaker metrics
	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_circuit_breaker_state",
			Help: "Circuit breaker state (1 = open, 0 = closed, 0.5 = half-open)",
		},
		[]string{"backend_id", "state"},
	)

	circuitBreakerFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_circuit_breaker_failures_total",
			Help: "Total number of circuit breaker failures",
		},
		[]string{"backend_id"},
	)

	// Network metrics
	packetsSent = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_packets_sent_total",
			Help: "Total number of packets sent",
		},
		[]string{"connection_id"},
	)

	packetsReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_packets_received_total",
			Help: "Total number of packets received",
		},
		[]string{"connection_id"},
	)

	packetsLost = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_packets_lost_total",
			Help: "Total number of packets lost",
		},
		[]string{"connection_id"},
	)

	packetLossRate = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_packet_loss_rate",
			Help: "Current packet loss rate",
		},
		[]string{"connection_id"},
	)

	roundTripTime = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_rtt_seconds",
			Help: "Round trip time in seconds",
		},
		[]string{"connection_id"},
	)

	congestionWindow = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_congestion_window_bytes",
			Help: "Congestion window size in bytes",
		},
		[]string{"connection_id"},
	)

	bandwidth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_bandwidth_bytes_per_second",
			Help: "Estimated bandwidth in bytes per second",
		},
		[]string{"connection_id"},
	)

	// Load balancer metrics
	loadBalancerAlgorithm = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "quic_load_balancer_algorithm",
			Help: "Current load balancing algorithm (encoded as number)",
		},
		[]string{"algorithm"},
	)

	requestsPerSecond = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "quic_requests_per_second",
			Help: "Current requests per second",
		},
	)

	errorRate = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "quic_error_rate",
			Help: "Current error rate percentage",
		},
	)

	// Path validation metrics
	pathValidations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "quic_path_validations_total",
			Help: "Total number of path validations",
		},
		[]string{"path", "result"},
	)

	pathValidationTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "quic_path_validation_duration_seconds",
			Help:    "Duration of path validations",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"path"},
	)
)

// MetricsCollector updates Prometheus metrics from our internal stats
func (tracker *ConnectionTracker) UpdateMetrics() {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	// Update connection metrics
	for _, conn := range tracker.connections {
		activeConnections.WithLabelValues(conn.Protocol, "").Set(1)

		if conn.RTT > 0 {
			roundTripTime.WithLabelValues(conn.ConnectionID).Set(conn.RTT.Seconds())
		}
		if conn.CongestionWindow > 0 {
			congestionWindow.WithLabelValues(conn.ConnectionID).Set(float64(conn.CongestionWindow))
		}
		if conn.Bandwidth > 0 {
			bandwidth.WithLabelValues(conn.ConnectionID).Set(float64(conn.Bandwidth))
		}

		packetsSent.WithLabelValues(conn.ConnectionID).Add(float64(conn.PacketsSent))
		packetsReceived.WithLabelValues(conn.ConnectionID).Add(float64(conn.PacketsReceived))
		packetsLost.WithLabelValues(conn.ConnectionID).Add(float64(conn.PacketsLost))

		// Calculate packet loss rate
		if conn.PacketsSent > 0 {
			lossRate := float64(conn.PacketsLost) / float64(conn.PacketsSent)
			packetLossRate.WithLabelValues(conn.ConnectionID).Set(lossRate)
		}
	}
}

// UpdateBackendMetrics updates backend-related metrics
func (lb *LoadBalancer) UpdateMetrics() {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	for _, backend := range lb.backends {
		backend.mu.RLock()

		// Backend health
		if backend.Alive {
			backendHealthy.WithLabelValues(string(rune(backend.ID)), backend.URL.String()).Set(1)
		} else {
			backendHealthy.WithLabelValues(string(rune(backend.ID)), backend.URL.String()).Set(0)
		}

		// Backend metrics
		backendResponseTime.WithLabelValues(string(rune(backend.ID)), backend.URL.String()).Set(backend.ResponseTime.Seconds())
		backendConnections.WithLabelValues(string(rune(backend.ID)), backend.URL.String()).Set(float64(backend.Connections))
		backendWeight.WithLabelValues(string(rune(backend.ID)), backend.URL.String()).Set(float64(backend.Weight))

		// Circuit breaker metrics
		if backend.CircuitBreaker != nil {
			backend.CircuitBreaker.mu.RLock()
			var stateValue float64
			switch backend.CircuitBreaker.State {
			case "closed":
				stateValue = 0
			case "half-open":
				stateValue = 0.5
			case "open":
				stateValue = 1
			}
			circuitBreakerState.WithLabelValues(string(rune(backend.ID)), backend.CircuitBreaker.State).Set(stateValue)
			circuitBreakerFailures.WithLabelValues(string(rune(backend.ID))).Add(float64(backend.CircuitBreaker.Failures))
			backend.CircuitBreaker.mu.RUnlock()
		}

		backend.mu.RUnlock()
	}
}

// RecordRequest records a request in metrics
func RecordRequest(method, status, protocol, backendID string, duration float64) {
	requestsTotal.WithLabelValues(method, status, protocol, backendID).Inc()
	requestDuration.WithLabelValues(method, status, protocol, backendID).Observe(duration)
}

// RecordMigration records a connection migration
func RecordMigration(reason string, success bool, duration float64) {
	successStr := "false"
	if success {
		successStr = "true"
	}
	connectionMigrations.WithLabelValues(reason, successStr).Inc()
	migrationDuration.WithLabelValues(reason).Observe(duration)
}

// RecordPathValidation records a path validation event
func RecordPathValidation(path, result string, duration float64) {
	pathValidations.WithLabelValues(path, result).Inc()
	pathValidationTime.WithLabelValues(path).Observe(duration)
}
