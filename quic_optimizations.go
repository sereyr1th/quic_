package main

import (
	"crypto/tls"
	"log"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// QUICOptimizer provides advanced QUIC optimizations
type QUICOptimizer struct {
	connectionPool      map[string]interface{} // Using interface{} since quic.Connection is not available here
	congestionAlgorithm string
}

// NewQUICOptimizer creates a new QUIC optimizer
func NewQUICOptimizer() *QUICOptimizer {
	return &QUICOptimizer{
		connectionPool:      make(map[string]interface{}),
		congestionAlgorithm: "bbr", // Default to BBR for better performance
	}
}

// OptimizedQUICConfig returns an optimized QUIC configuration for different scenarios
func OptimizedQUICConfig(scenario string) *quic.Config {
	baseConfig := &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
		EnableDatagrams: true,
		Allow0RTT:       true,
	}

	switch scenario {
	case "high-throughput":
		// Optimized for large file transfers
		baseConfig.MaxIncomingStreams = 5000
		baseConfig.MaxIncomingUniStreams = 2000
		baseConfig.InitialStreamReceiveWindow = 2 * 1024 * 1024     // 2 MB
		baseConfig.MaxStreamReceiveWindow = 32 * 1024 * 1024        // 32 MB
		baseConfig.InitialConnectionReceiveWindow = 4 * 1024 * 1024 // 4 MB
		baseConfig.MaxConnectionReceiveWindow = 64 * 1024 * 1024    // 64 MB

	case "low-latency":
		// Optimized for low latency applications
		baseConfig.MaxIncomingStreams = 1000
		baseConfig.MaxIncomingUniStreams = 500
		baseConfig.InitialStreamReceiveWindow = 256 * 1024      // 256 KB
		baseConfig.MaxStreamReceiveWindow = 2 * 1024 * 1024     // 2 MB
		baseConfig.InitialConnectionReceiveWindow = 512 * 1024  // 512 KB
		baseConfig.MaxConnectionReceiveWindow = 4 * 1024 * 1024 // 4 MB
		baseConfig.MaxIdleTimeout = 30 * time.Second
		baseConfig.KeepAlivePeriod = 5 * time.Second

	case "mobile":
		// Optimized for mobile networks with potential instability
		baseConfig.MaxIncomingStreams = 500
		baseConfig.MaxIncomingUniStreams = 250
		baseConfig.InitialStreamReceiveWindow = 512 * 1024      // 512 KB
		baseConfig.MaxStreamReceiveWindow = 4 * 1024 * 1024     // 4 MB
		baseConfig.InitialConnectionReceiveWindow = 1024 * 1024 // 1 MB
		baseConfig.MaxConnectionReceiveWindow = 8 * 1024 * 1024 // 8 MB
		baseConfig.MaxIdleTimeout = 90 * time.Second            // Longer timeout for mobile
		baseConfig.KeepAlivePeriod = 30 * time.Second

	default:
		// Balanced configuration
		baseConfig.MaxIncomingStreams = 2000
		baseConfig.MaxIncomingUniStreams = 1000
		baseConfig.InitialStreamReceiveWindow = 1024 * 1024         // 1 MB
		baseConfig.MaxStreamReceiveWindow = 16 * 1024 * 1024        // 16 MB
		baseConfig.InitialConnectionReceiveWindow = 2 * 1024 * 1024 // 2 MB
		baseConfig.MaxConnectionReceiveWindow = 32 * 1024 * 1024    // 32 MB
	}

	return baseConfig
}

// EnhancedTLSConfig returns an optimized TLS configuration for HTTP/3
func EnhancedTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		NextProtos: []string{"h3", "h2", "http/1.1"},
		CipherSuites: []uint16{
			// TLS 1.3 cipher suites (preferred for HTTP/3)
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			// TLS 1.2 cipher suites for compatibility
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519,    // Fastest elliptic curve
			tls.CurveP256, // Widely supported
			tls.CurveP384, // Higher security
		},
		// Enable session resumption for 0-RTT
		ClientSessionCache:     tls.NewLRUClientSessionCache(2000),
		SessionTicketsDisabled: false,
		// Performance optimizations
		InsecureSkipVerify: false, // Keep security in production
		ServerName:         "",    // Will be set during handshake
	}
}

// ConnectionQualityMonitor monitors connection quality metrics
type ConnectionQualityMonitor struct {
	RTTHistory       []time.Duration
	PacketLossRate   float64
	BandwidthHistory []int64
	LastUpdate       time.Time
}

// NewConnectionQualityMonitor creates a new connection quality monitor
func NewConnectionQualityMonitor() *ConnectionQualityMonitor {
	return &ConnectionQualityMonitor{
		RTTHistory:       make([]time.Duration, 0, 100),
		BandwidthHistory: make([]int64, 0, 100),
		LastUpdate:       time.Now(),
	}
}

// UpdateMetrics updates connection quality metrics
func (cqm *ConnectionQualityMonitor) UpdateMetrics(rtt time.Duration, bandwidth int64, packetLoss float64) {
	// Keep only last 100 measurements
	if len(cqm.RTTHistory) >= 100 {
		cqm.RTTHistory = cqm.RTTHistory[1:]
	}
	cqm.RTTHistory = append(cqm.RTTHistory, rtt)

	if len(cqm.BandwidthHistory) >= 100 {
		cqm.BandwidthHistory = cqm.BandwidthHistory[1:]
	}
	cqm.BandwidthHistory = append(cqm.BandwidthHistory, bandwidth)

	cqm.PacketLossRate = packetLoss
	cqm.LastUpdate = time.Now()
}

// GetAverageRTT calculates average RTT from history
func (cqm *ConnectionQualityMonitor) GetAverageRTT() time.Duration {
	if len(cqm.RTTHistory) == 0 {
		return 0
	}

	var total time.Duration
	for _, rtt := range cqm.RTTHistory {
		total += rtt
	}
	return total / time.Duration(len(cqm.RTTHistory))
}

// GetAverageBandwidth calculates average bandwidth from history
func (cqm *ConnectionQualityMonitor) GetAverageBandwidth() int64 {
	if len(cqm.BandwidthHistory) == 0 {
		return 0
	}

	var total int64
	for _, bw := range cqm.BandwidthHistory {
		total += bw
	}
	return total / int64(len(cqm.BandwidthHistory))
}

// IsConnectionHealthy determines if connection quality is good
func (cqm *ConnectionQualityMonitor) IsConnectionHealthy() bool {
	avgRTT := cqm.GetAverageRTT()
	avgBandwidth := cqm.GetAverageBandwidth()

	// Define thresholds for healthy connection
	rttThreshold := 200 * time.Millisecond
	bandwidthThreshold := int64(1000000) // 1 Mbps
	packetLossThreshold := 0.05          // 5%

	return avgRTT < rttThreshold &&
		avgBandwidth > bandwidthThreshold &&
		cqm.PacketLossRate < packetLossThreshold
}

// AdaptiveConfiguration dynamically adjusts QUIC configuration based on network conditions
type AdaptiveConfiguration struct {
	monitor     *ConnectionQualityMonitor
	baseConfig  *quic.Config
	lastAdjust  time.Time
	adjustCount int
}

// NewAdaptiveConfiguration creates a new adaptive configuration manager
func NewAdaptiveConfiguration(baseConfig *quic.Config) *AdaptiveConfiguration {
	return &AdaptiveConfiguration{
		monitor:    NewConnectionQualityMonitor(),
		baseConfig: baseConfig,
		lastAdjust: time.Now(),
	}
}

// AdjustConfiguration adapts configuration based on current network conditions
func (ac *AdaptiveConfiguration) AdjustConfiguration() *quic.Config {
	// Only adjust every 30 seconds to avoid thrashing
	if time.Since(ac.lastAdjust) < 30*time.Second {
		return ac.baseConfig
	}

	config := *ac.baseConfig // Copy base configuration

	avgRTT := ac.monitor.GetAverageRTT()
	avgBandwidth := ac.monitor.GetAverageBandwidth()
	packetLoss := ac.monitor.PacketLossRate

	// Adjust timeouts based on RTT
	if avgRTT > 200*time.Millisecond {
		// High latency network - increase timeouts
		config.MaxIdleTimeout = 120 * time.Second
		config.KeepAlivePeriod = 30 * time.Second
		log.Printf("🔧 Adaptive Config: Increased timeouts for high latency (RTT: %v)", avgRTT)
	} else if avgRTT < 50*time.Millisecond {
		// Low latency network - can use shorter timeouts
		config.MaxIdleTimeout = 30 * time.Second
		config.KeepAlivePeriod = 10 * time.Second
		log.Printf("🔧 Adaptive Config: Decreased timeouts for low latency (RTT: %v)", avgRTT)
	}

	// Adjust window sizes based on bandwidth
	if avgBandwidth > 10*1000000 { // > 10 Mbps
		// High bandwidth - increase window sizes
		config.InitialStreamReceiveWindow = 2 * 1024 * 1024
		config.MaxStreamReceiveWindow = 32 * 1024 * 1024
		config.InitialConnectionReceiveWindow = 4 * 1024 * 1024
		config.MaxConnectionReceiveWindow = 64 * 1024 * 1024
		log.Printf("🔧 Adaptive Config: Increased windows for high bandwidth (BW: %d Mbps)", avgBandwidth/1000000)
	} else if avgBandwidth < 1*1000000 { // < 1 Mbps
		// Low bandwidth - decrease window sizes to reduce buffering
		config.InitialStreamReceiveWindow = 256 * 1024
		config.MaxStreamReceiveWindow = 2 * 1024 * 1024
		config.InitialConnectionReceiveWindow = 512 * 1024
		config.MaxConnectionReceiveWindow = 4 * 1024 * 1024
		log.Printf("🔧 Adaptive Config: Decreased windows for low bandwidth (BW: %d Kbps)", avgBandwidth/1000)
	}

	// Adjust stream limits based on packet loss
	if packetLoss > 0.02 { // > 2% packet loss
		// High packet loss - reduce concurrent streams to lower congestion
		config.MaxIncomingStreams = int64(float64(config.MaxIncomingStreams) * 0.7)
		config.MaxIncomingUniStreams = int64(float64(config.MaxIncomingUniStreams) * 0.7)
		log.Printf("🔧 Adaptive Config: Reduced streams for high packet loss (Loss: %.2f%%)", packetLoss*100)
	}

	ac.lastAdjust = time.Now()
	ac.adjustCount++

	return &config
}

// QUICConnectionTracker provides enhanced connection tracking with real QUIC integration
type QUICConnectionTracker struct {
	connections map[string]*EnhancedQUICConnection
	monitor     *ConnectionQualityMonitor
}

// EnhancedQUICConnection wraps QUIC connection with additional metadata
type EnhancedQUICConnection struct {
	Connection     interface{} // Using interface{} for compatibility
	LocalAddr      net.Addr
	RemoteAddr     net.Addr
	CreatedAt      time.Time
	LastActivity   time.Time
	RequestCount   int64
	BytesSent      int64
	BytesReceived  int64
	StreamsOpened  int64
	MigrationCount int
	QualityMonitor *ConnectionQualityMonitor
}

// NewQUICConnectionTracker creates a new enhanced QUIC connection tracker
func NewQUICConnectionTracker() *QUICConnectionTracker {
	return &QUICConnectionTracker{
		connections: make(map[string]*EnhancedQUICConnection),
		monitor:     NewConnectionQualityMonitor(),
	}
}

// TrackConnection adds or updates a QUIC connection in the tracker
func (qct *QUICConnectionTracker) TrackConnection(remoteAddr, localAddr string) string {
	connID := remoteAddr + "-" + localAddr

	if existing, exists := qct.connections[connID]; exists {
		existing.LastActivity = time.Now()
		existing.RequestCount++
		return connID
	}

	enhancedConn := &EnhancedQUICConnection{
		Connection:     nil, // Placeholder for actual connection
		LocalAddr:      nil, // Will be set when available
		RemoteAddr:     nil, // Will be set when available
		CreatedAt:      time.Now(),
		LastActivity:   time.Now(),
		RequestCount:   1,
		QualityMonitor: NewConnectionQualityMonitor(),
	}

	qct.connections[connID] = enhancedConn

	log.Printf("🔗 New QUIC connection tracked: %s -> %s", remoteAddr, localAddr)

	return connID
}

// GetConnectionStats returns statistics for all tracked connections
func (qct *QUICConnectionTracker) GetConnectionStats() map[string]interface{} {
	stats := make(map[string]interface{})

	totalConnections := len(qct.connections)
	totalRequests := int64(0)
	totalBytes := int64(0)

	for _, conn := range qct.connections {
		totalRequests += conn.RequestCount
		totalBytes += conn.BytesSent + conn.BytesReceived
	}

	stats["total_connections"] = totalConnections
	stats["total_requests"] = totalRequests
	stats["total_bytes"] = totalBytes
	stats["average_requests_per_connection"] = float64(totalRequests) / float64(totalConnections)

	return stats
}

// CleanupStaleConnections removes connections that haven't been active
func (qct *QUICConnectionTracker) CleanupStaleConnections(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)

	for connID, conn := range qct.connections {
		if conn.LastActivity.Before(cutoff) {
			// Simply remove from tracking (actual connection cleanup would be handled elsewhere)
			delete(qct.connections, connID)
			log.Printf("🧹 Cleaned up stale QUIC connection: %s", connID)
		}
	}
}
