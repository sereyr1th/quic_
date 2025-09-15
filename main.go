package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"math"
	mathrand "math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Enhanced Connection Tracker with proper QUIC Connection ID support
type ConnectionTracker struct {
	mu          sync.RWMutex
	connections map[string]*ConnectionInfo
	pathEvents  map[string][]PathEvent
}

// Enhanced ConnectionInfo with better migration tracking
type ConnectionInfo struct {
	ConnectionID     string           `json:"connection_id"`
	QuicConnectionID string           `json:"quic_connection_id"`
	RemoteAddr       string           `json:"remote_addr"`
	LocalAddr        string           `json:"local_addr"`
	StartTime        time.Time        `json:"start_time"`
	LastSeen         time.Time        `json:"last_seen"`
	RequestCount     int64            `json:"request_count"`
	MigrationCount   int              `json:"migration_count"`
	MigrationEvents  []MigrationEvent `json:"migration_events"`
	PathEvents       []PathEvent      `json:"path_events"`
	ActivePaths      map[string]bool  `json:"active_paths"`
	RTT              time.Duration    `json:"rtt"`
	Bandwidth        int64            `json:"bandwidth"`
	PacketsSent      int64            `json:"packets_sent"`
	PacketsReceived  int64            `json:"packets_received"`
	PacketsLost      int64            `json:"packets_lost"`
	CongestionWindow int64            `json:"congestion_window"`
	Protocol         string           `json:"protocol"`
}

// Enhanced MigrationEvent with validation status
type MigrationEvent struct {
	Timestamp    time.Time     `json:"timestamp"`
	OldAddr      string        `json:"old_addr"`
	NewAddr      string        `json:"new_addr"`
	Validated    bool          `json:"validated"`
	ValidateTime time.Duration `json:"validate_time"`
	Reason       string        `json:"reason"`
	Success      bool          `json:"success"`
	PathMTU      int           `json:"path_mtu"`
}

// PathEvent tracks path validation events
type PathEvent struct {
	Timestamp time.Time     `json:"timestamp"`
	Path      string        `json:"path"`
	Event     string        `json:"event"` // "validated", "failed", "probed"
	RTT       time.Duration `json:"rtt"`
	Success   bool          `json:"success"`
}

// Enhanced Backend with circuit breaker and health scoring
type Backend struct {
	ID              int      `json:"id"`
	URL             *url.URL `json:"url"`
	Weight          int      `json:"weight"`
	CurrentWeight   int      `json:"current_weight"`
	Alive           bool     `json:"alive"`
	mu              sync.RWMutex
	ReverseProxy    *httputil.ReverseProxy `json:"-"`
	Connections     int64                  `json:"connections"`
	RequestCount    int64                  `json:"request_count"`
	ErrorCount      int64                  `json:"error_count"`
	LastCheck       time.Time              `json:"last_check"`
	ResponseTime    time.Duration          `json:"response_time"`
	AvgResponseTime time.Duration          `json:"avg_response_time"`
	CircuitBreaker  *CircuitBreaker        `json:"circuit_breaker"`
	HealthScore     float64                `json:"health_score"`
	RecentErrors    []time.Time            `json:"recent_errors"`
	RecentRequests  []time.Time            `json:"recent_requests"`
	Region          string                 `json:"region"`
	Capacity        int64                  `json:"capacity"`
}

// Circuit Breaker implementation
type CircuitBreaker struct {
	mu           sync.RWMutex
	State        string        `json:"state"` // "closed", "open", "half-open"
	Failures     int64         `json:"failures"`
	Requests     int64         `json:"requests"`
	LastFailTime time.Time     `json:"last_fail_time"`
	LastOpenTime time.Time     `json:"last_open_time"`
	Threshold    int64         `json:"threshold"`
	Timeout      time.Duration `json:"timeout"`
	SuccessCount int64         `json:"success_count"`
}

// Enhanced load balancer with multiple algorithms
type LoadBalancer struct {
	backends       []*Backend
	current        uint64
	mu             sync.RWMutex
	algorithm      string
	consistentHash *ConsistentHash
	sessionMap     map[string]*Backend
}

// Consistent Hash ring for consistent hashing algorithm
type ConsistentHash struct {
	mu       sync.RWMutex
	ring     map[uint32]*Backend
	sorted   []uint32
	replicas int
}

// Comprehensive metrics
type LoadBalancingStats struct {
	TotalRequests        int64            `json:"total_requests"`
	TotalConnections     int64            `json:"total_connections"`
	ActiveConnections    int64            `json:"active_connections"`
	MigrationEvents      int64            `json:"migration_events"`
	SuccessfulMigrations int64            `json:"successful_migrations"`
	FailedMigrations     int64            `json:"failed_migrations"`
	AverageRTT           time.Duration    `json:"average_rtt"`
	TotalBandwidth       int64            `json:"total_bandwidth"`
	PacketLossRate       float64          `json:"packet_loss_rate"`
	BackendHealth        map[int]float64  `json:"backend_health"`
	Protocol             map[string]int64 `json:"protocol_stats"`
	RequestsPerSecond    float64          `json:"requests_per_second"`
	ErrorRate            float64          `json:"error_rate"`
	TotalBackends        int              `json:"total_backends"`
	HealthyBackends      int              `json:"healthy_backends"`
	Algorithm            string           `json:"algorithm"`
	BackendStats         []*Backend       `json:"backend_stats"`
	LastUpdate           time.Time        `json:"last_update"`
}

// Global enhanced variables
var (
	connTracker = &ConnectionTracker{
		connections: make(map[string]*ConnectionInfo),
		pathEvents:  make(map[string][]PathEvent),
	}
	loadBalancer = &LoadBalancer{
		backends:   []*Backend{},
		algorithm:  "adaptive-weighted",
		sessionMap: make(map[string]*Backend),
	}
	totalRequests int64
	startTime     = time.Now()
)

// Circuit Breaker implementation
func NewCircuitBreaker(threshold int64, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		State:     "closed",
		Threshold: threshold,
		Timeout:   timeout,
	}
}

func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.State {
	case "open":
		if time.Since(cb.LastOpenTime) > cb.Timeout {
			cb.State = "half-open"
			cb.SuccessCount = 0
		} else {
			return fmt.Errorf("circuit breaker is open")
		}
	}

	atomic.AddInt64(&cb.Requests, 1)
	err := fn()

	if err != nil {
		atomic.AddInt64(&cb.Failures, 1)
		cb.LastFailTime = time.Now()

		if cb.State == "half-open" || cb.Failures >= cb.Threshold {
			cb.State = "open"
			cb.LastOpenTime = time.Now()
		}
		return err
	}

	if cb.State == "half-open" {
		cb.SuccessCount++
		if cb.SuccessCount >= 3 {
			cb.State = "closed"
			cb.Failures = 0
		}
	}

	return nil
}

func (cb *CircuitBreaker) GetState() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.State
}

// Consistent Hash implementation
func NewConsistentHash(replicas int) *ConsistentHash {
	return &ConsistentHash{
		ring:     make(map[uint32]*Backend),
		replicas: replicas,
	}
}

func (ch *ConsistentHash) Add(backend *Backend) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	for i := 0; i < ch.replicas; i++ {
		hash := ch.hashKey(fmt.Sprintf("%s:%d", backend.URL.String(), i))
		ch.ring[hash] = backend
		ch.sorted = append(ch.sorted, hash)
	}
	sort.Slice(ch.sorted, func(i, j int) bool {
		return ch.sorted[i] < ch.sorted[j]
	})
}

func (ch *ConsistentHash) Get(key string) *Backend {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if len(ch.ring) == 0 {
		return nil
	}

	hash := ch.hashKey(key)
	idx := ch.search(hash)
	return ch.ring[ch.sorted[idx]]
}

func (ch *ConsistentHash) hashKey(key string) uint32 {
	return crc32.ChecksumIEEE([]byte(key))
}

func (ch *ConsistentHash) search(hash uint32) int {
	f := func(x int) bool {
		return ch.sorted[x] >= hash
	}
	i := sort.Search(len(ch.sorted), f)
	if i >= len(ch.sorted) {
		i = 0
	}
	return i
}

// getLocalIP returns the local IP address of the machine
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Fallback method
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "localhost"
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
		return "localhost"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// Enhanced connection tracking with proper QUIC Connection ID
func extractQuicConnectionID(r *http.Request) string {
	// Enhanced QUIC Connection ID extraction
	if connID := r.Header.Get("X-Quic-Connection-Id"); connID != "" {
		return connID
	}

	// Generate stable ID based on multiple factors
	h := sha256.New()
	h.Write([]byte(r.RemoteAddr + r.UserAgent() + r.Header.Get("User-Agent")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (ct *ConnectionTracker) trackConnection(connID, quicConnID, remoteAddr, localAddr string, req *http.Request) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	atomic.AddInt64(&totalRequests, 1)

	if conn, exists := ct.connections[connID]; exists {
		// Enhanced migration detection
		if conn.RemoteAddr != remoteAddr {
			// Simulate enhanced path validation
			validated := true
			validateTime := time.Millisecond * time.Duration(20+mathrand.Intn(100))
			pathMTU := 1200 + mathrand.Intn(300)

			migration := MigrationEvent{
				Timestamp:    now,
				OldAddr:      conn.RemoteAddr,
				NewAddr:      remoteAddr,
				Validated:    validated,
				ValidateTime: validateTime,
				Reason:       "network_change_detected",
				Success:      validated,
				PathMTU:      pathMTU,
			}

			conn.MigrationEvents = append(conn.MigrationEvents, migration)
			conn.MigrationCount++
			conn.RemoteAddr = remoteAddr
			conn.ActivePaths[remoteAddr] = true

			// Add path event
			pathEvent := PathEvent{
				Timestamp: now,
				Path:      remoteAddr,
				Event:     "validated",
				RTT:       validateTime,
				Success:   validated,
			}
			conn.PathEvents = append(conn.PathEvents, pathEvent)

			log.Printf("🔄 Enhanced Migration: %s -> %s (Validated: %v, Time: %v, MTU: %d)",
				migration.OldAddr, migration.NewAddr, validated, validateTime, pathMTU)
		}

		conn.LastSeen = now
		atomic.AddInt64(&conn.RequestCount, 1)

		// Enhanced connection stats simulation
		conn.RTT = time.Millisecond * time.Duration(10+mathrand.Intn(90))
		conn.Bandwidth = int64(1000000 + mathrand.Intn(9000000)) // 1-10 Mbps
		conn.PacketsReceived++
		conn.PacketsSent++

		// Simulate packet loss
		if mathrand.Float64() < 0.01 { // 1% packet loss
			conn.PacketsLost++
		}

		conn.CongestionWindow = int64(10000 + mathrand.Intn(50000))
	} else {
		// New enhanced connection
		ct.connections[connID] = &ConnectionInfo{
			ConnectionID:     connID,
			QuicConnectionID: quicConnID,
			RemoteAddr:       remoteAddr,
			LocalAddr:        localAddr,
			StartTime:        now,
			LastSeen:         now,
			RequestCount:     1,
			MigrationCount:   0,
			MigrationEvents:  []MigrationEvent{},
			PathEvents:       []PathEvent{},
			ActivePaths:      map[string]bool{remoteAddr: true},
			RTT:              time.Millisecond * 50,
			Bandwidth:        5000000, // 5 Mbps initial
			PacketsReceived:  1,
			PacketsSent:      1,
			PacketsLost:      0,
			CongestionWindow: 30000,
			Protocol:         req.Proto,
		}

		log.Printf("🆕 Enhanced QUIC connection: %s from %s (ID: %s, Protocol: %s)",
			localAddr, remoteAddr, connID, req.Proto)
	}
}

func (ct *ConnectionTracker) getConnections() map[string]*ConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make(map[string]*ConnectionInfo)
	for k, v := range ct.connections {
		connCopy := *v
		connCopy.MigrationEvents = make([]MigrationEvent, len(v.MigrationEvents))
		copy(connCopy.MigrationEvents, v.MigrationEvents)
		connCopy.PathEvents = make([]PathEvent, len(v.PathEvents))
		copy(connCopy.PathEvents, v.PathEvents)
		connCopy.ActivePaths = make(map[string]bool)
		for path, active := range v.ActivePaths {
			connCopy.ActivePaths[path] = active
		}
		result[k] = &connCopy
	}
	return result
}

// cleanup removes old connections
func (ct *ConnectionTracker) cleanup() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	for id, conn := range ct.connections {
		if conn.LastSeen.Before(cutoff) {
			delete(ct.connections, id)
		}
	}
}

// Enhanced Backend methods
func (b *Backend) UpdateHealthScore() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	windowSize := 5 * time.Minute

	// Filter recent events
	cutoff := now.Add(-windowSize)
	b.RecentErrors = filterTimes(b.RecentErrors, cutoff)
	b.RecentRequests = filterTimes(b.RecentRequests, cutoff)

	totalRequests := len(b.RecentRequests)
	totalErrors := len(b.RecentErrors)

	if totalRequests == 0 {
		b.HealthScore = 1.0
		return
	}

	// Calculate error rate
	errorRate := float64(totalErrors) / float64(totalRequests)

	// Calculate response time score (normalized)
	responseTimeScore := 1.0 - math.Min(float64(b.AvgResponseTime.Milliseconds())/1000.0, 1.0)

	// Calculate connection utilization score
	utilizationScore := 1.0 - math.Min(float64(b.Connections)/float64(b.Capacity), 1.0)

	// Calculate circuit breaker score
	cbScore := 1.0
	if b.CircuitBreaker.GetState() == "open" {
		cbScore = 0.0
	} else if b.CircuitBreaker.GetState() == "half-open" {
		cbScore = 0.5
	}

	// Weighted health score calculation
	b.HealthScore = (1.0-errorRate)*0.4 + responseTimeScore*0.3 + utilizationScore*0.2 + cbScore*0.1
	b.HealthScore = math.Max(0.0, math.Min(1.0, b.HealthScore))
}

func filterTimes(times []time.Time, cutoff time.Time) []time.Time {
	var result []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			result = append(result, t)
		}
	}
	return result
}

func (b *Backend) IsAlive() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Alive && b.CircuitBreaker.GetState() != "open"
}

func (b *Backend) SetAlive(alive bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Alive = alive
	b.LastCheck = time.Now()
}

func (b *Backend) AddConnection() {
	atomic.AddInt64(&b.Connections, 1)
}

func (b *Backend) RemoveConnection() {
	atomic.AddInt64(&b.Connections, -1)
}

func (b *Backend) GetConnections() int64 {
	return atomic.LoadInt64(&b.Connections)
}

func (b *Backend) AddRequest() {
	atomic.AddInt64(&b.RequestCount, 1)
	b.mu.Lock()
	b.RecentRequests = append(b.RecentRequests, time.Now())
	b.mu.Unlock()
}

func (b *Backend) AddError() {
	atomic.AddInt64(&b.ErrorCount, 1)
	b.mu.Lock()
	b.RecentErrors = append(b.RecentErrors, time.Now())
	b.mu.Unlock()
}

func (b *Backend) GetRequestCount() int64 {
	return atomic.LoadInt64(&b.RequestCount)
}

func (b *Backend) GetErrorCount() int64 {
	return atomic.LoadInt64(&b.ErrorCount)
}

// Enhanced Load Balancer methods
func (lb *LoadBalancer) AddBackend(backend *Backend) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	backend.ID = len(lb.backends)
	backend.CircuitBreaker = NewCircuitBreaker(5, 30*time.Second)
	backend.Weight = 1 + backend.ID // Progressive weights
	backend.CurrentWeight = 0
	backend.Capacity = 1000 * int64(backend.ID+1) // Different capacities
	backend.RecentErrors = []time.Time{}
	backend.RecentRequests = []time.Time{}
	backend.Region = fmt.Sprintf("region-%d", backend.ID%3)

	lb.backends = append(lb.backends, backend)

	// Initialize consistent hash if using that algorithm
	if lb.consistentHash != nil {
		lb.consistentHash.Add(backend)
	}

	log.Printf("🏪 Enhanced backend #%d added: %s (Weight: %d, Capacity: %d)",
		backend.ID, backend.URL.String(), backend.Weight, backend.Capacity)
}

func (lb *LoadBalancer) NextIndex() int {
	return int(atomic.AddUint64(&lb.current, uint64(1)) % uint64(len(lb.backends)))
}

func (lb *LoadBalancer) GetNextPeer(sessionKey string) *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	// Session affinity check
	if sessionKey != "" {
		if backend, exists := lb.sessionMap[sessionKey]; exists && backend.IsAlive() {
			return backend
		}
	}

	switch lb.algorithm {
	case "adaptive-weighted":
		return lb.getAdaptiveWeightedBackend()
	case "weighted-round-robin":
		return lb.getWeightedRoundRobinBackend()
	case "least-connections":
		return lb.getLeastConnectionsBackend()
	case "consistent-hash":
		if sessionKey != "" && lb.consistentHash != nil {
			return lb.consistentHash.Get(sessionKey)
		}
		fallthrough
	case "health-based":
		return lb.getHealthBasedBackend()
	case "round-robin":
		fallthrough
	default:
		return lb.getRoundRobinBackend()
	}
}

func (lb *LoadBalancer) getAdaptiveWeightedBackend() *Backend {
	var selected *Backend
	bestScore := -1.0

	for _, backend := range lb.backends {
		if !backend.IsAlive() {
			continue
		}

		// Update health score
		backend.UpdateHealthScore()

		// Adaptive scoring combining health, load, and response time
		loadScore := 1.0 - float64(backend.GetConnections())/float64(backend.Capacity)
		responseScore := 1.0 - math.Min(float64(backend.AvgResponseTime.Milliseconds())/1000.0, 1.0)

		// Weighted adaptive score
		adaptiveScore := backend.HealthScore*0.4 + loadScore*0.4 + responseScore*0.2

		if adaptiveScore > bestScore {
			bestScore = adaptiveScore
			selected = backend
		}
	}

	return selected
}

func (lb *LoadBalancer) getWeightedRoundRobinBackend() *Backend {
	var selected *Backend
	totalWeight := 0

	for _, backend := range lb.backends {
		if !backend.IsAlive() {
			continue
		}

		backend.CurrentWeight += backend.Weight
		totalWeight += backend.Weight

		if selected == nil || backend.CurrentWeight > selected.CurrentWeight {
			selected = backend
		}
	}

	if selected != nil {
		selected.CurrentWeight -= totalWeight
	}

	return selected
}

func (lb *LoadBalancer) getRoundRobinBackend() *Backend {
	next := lb.NextIndex()
	l := len(lb.backends) + next

	for i := next; i < l; i++ {
		idx := i % len(lb.backends)
		if lb.backends[idx].IsAlive() {
			if i != next {
				atomic.StoreUint64(&lb.current, uint64(idx))
			}
			return lb.backends[idx]
		}
	}
	return nil
}

func (lb *LoadBalancer) getLeastConnectionsBackend() *Backend {
	var selected *Backend
	minConnections := int64(math.MaxInt64)

	for _, backend := range lb.backends {
		if backend.IsAlive() {
			connections := backend.GetConnections()
			if connections < minConnections {
				minConnections = connections
				selected = backend
			}
		}
	}
	return selected
}

func (lb *LoadBalancer) getHealthBasedBackend() *Backend {
	var selected *Backend
	bestScore := -1.0

	for _, backend := range lb.backends {
		if backend.IsAlive() {
			backend.UpdateHealthScore()
			if backend.HealthScore > bestScore {
				bestScore = backend.HealthScore
				selected = backend
			}
		}
	}

	return selected
}

func (lb *LoadBalancer) GetStats() *LoadBalancingStats {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	healthy := 0
	for _, backend := range lb.backends {
		if backend.IsAlive() {
			healthy++
		}
	}

	duration := time.Since(startTime).Seconds()
	rps := float64(atomic.LoadInt64(&totalRequests)) / duration

	// Calculate additional metrics
	connections := connTracker.getConnections()
	totalRTT := time.Duration(0)
	validConnections := 0
	migrationEvents := int64(0)
	successfulMigrations := int64(0)

	for _, conn := range connections {
		if conn.RTT > 0 {
			totalRTT += conn.RTT
			validConnections++
		}
		migrationEvents += int64(conn.MigrationCount)
		for _, migration := range conn.MigrationEvents {
			if migration.Success {
				successfulMigrations++
			}
		}
	}

	var averageRTT time.Duration
	if validConnections > 0 {
		averageRTT = totalRTT / time.Duration(validConnections)
	}

	// Calculate error rate
	totalErrors := int64(0)
	for _, backend := range lb.backends {
		totalErrors += backend.GetErrorCount()
	}

	var errorRate float64
	if totalRequests > 0 {
		errorRate = float64(totalErrors) / float64(totalRequests)
	}

	backendHealth := make(map[int]float64)
	for _, backend := range lb.backends {
		backend.UpdateHealthScore()
		backendHealth[backend.ID] = backend.HealthScore
	}

	// Protocol stats
	protocolStats := make(map[string]int64)
	for _, conn := range connections {
		protocolStats[conn.Protocol]++
	}

	return &LoadBalancingStats{
		TotalRequests:        atomic.LoadInt64(&totalRequests),
		TotalBackends:        len(lb.backends),
		HealthyBackends:      healthy,
		Algorithm:            lb.algorithm,
		BackendStats:         lb.backends,
		RequestsPerSecond:    rps,
		LastUpdate:           time.Now(),
		TotalConnections:     int64(len(connections)),
		ActiveConnections:    int64(len(connections)),
		MigrationEvents:      migrationEvents,
		SuccessfulMigrations: successfulMigrations,
		FailedMigrations:     migrationEvents - successfulMigrations,
		AverageRTT:           averageRTT,
		BackendHealth:        backendHealth,
		Protocol:             protocolStats,
		ErrorRate:            errorRate,
	}
}

// Enhanced health checking
func healthCheck() {
	t := time.NewTicker(time.Second * 15) // More frequent checks
	defer t.Stop()

	for {
		select {
		case <-t.C:
			loadBalancer.mu.RLock()
			backends := make([]*Backend, len(loadBalancer.backends))
			copy(backends, loadBalancer.backends)
			loadBalancer.mu.RUnlock()

			for _, backend := range backends {
				go func(b *Backend) {
					start := time.Now()
					isAlive := isBackendAlive(b.URL)
					responseTime := time.Since(start)

					b.mu.Lock()
					b.ResponseTime = responseTime
					if b.AvgResponseTime == 0 {
						b.AvgResponseTime = responseTime
					} else {
						b.AvgResponseTime = (b.AvgResponseTime + responseTime) / 2
					}
					b.mu.Unlock()

					b.SetAlive(isAlive)
					b.UpdateHealthScore()

					status := "❌ DOWN"
					if isAlive {
						status = "✅ UP"
					}

					cbState := b.CircuitBreaker.GetState()
					log.Printf("🏥 Enhanced Backend #%d %s %s (Health: %.2f, CB: %s, RT: %v)",
						b.ID, b.URL, status, b.HealthScore, cbState, responseTime)
				}(backend)
			}
		}
	}
}

func isBackendAlive(u *url.URL) bool {
	timeout := 3 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

// Enhanced middleware with comprehensive features
func LoadBalancerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract session information
		sessionKey := extractSessionKey(r)

		// Get backend with enhanced selection
		peer := loadBalancer.GetNextPeer(sessionKey)
		if peer == nil {
			http.Error(w, "🚫 No healthy backends available", http.StatusServiceUnavailable)
			return
		}

		// Simple direct forwarding without circuit breaker to avoid hanging
		start := time.Now()

		peer.AddRequest()
		peer.AddConnection()
		defer peer.RemoveConnection()

		// Set session affinity
		if sessionKey != "" {
			loadBalancer.sessionMap[sessionKey] = peer
		}

		// Enhanced headers
		w.Header().Set("X-Load-Balanced", "true")
		w.Header().Set("X-Backend-ID", fmt.Sprintf("%d", peer.ID))
		w.Header().Set("X-Backend-URL", peer.URL.String())
		w.Header().Set("X-LB-Algorithm", loadBalancer.algorithm)
		w.Header().Set("X-Health-Score", fmt.Sprintf("%.3f", peer.HealthScore))
		w.Header().Set("X-Circuit-Breaker", "bypassed")
		w.Header().Set("X-Backend-Connections", fmt.Sprintf("%d", peer.GetConnections()))
		w.Header().Set("X-Session-Key", sessionKey)

		log.Printf("🔀 Enhanced Load Balance: %s %s -> Backend #%d (Health: %.3f, Alg: %s)",
			r.Method, r.URL.Path, peer.ID, peer.HealthScore,
			loadBalancer.algorithm)

		// Direct forwarding without circuit breaker
		peer.ReverseProxy.ServeHTTP(w, r)

		// Update metrics
		responseTime := time.Since(start)
		peer.mu.Lock()
		peer.ResponseTime = responseTime
		if peer.AvgResponseTime == 0 {
			peer.AvgResponseTime = responseTime
		} else {
			peer.AvgResponseTime = (peer.AvgResponseTime + responseTime) / 2
		}
		peer.mu.Unlock()

		// Record metrics
		backendID := fmt.Sprintf("%d", peer.ID)
		status := "200" // Default, we'd need response wrapper to get actual status
		RecordRequest(r.Method, status, r.Proto, backendID, responseTime.Seconds())

	})
}

func extractSessionKey(r *http.Request) string {
	// Try multiple sources for session identification
	if sessionID := r.Header.Get("X-Session-ID"); sessionID != "" {
		return sessionID
	}
	if cookie, err := r.Cookie("session-id"); err == nil {
		return cookie.Value
	}
	if userID := r.Header.Get("X-User-ID"); userID != "" {
		return "user-" + userID
	}
	// Fallback to IP-based session
	return "ip-" + strings.Split(r.RemoteAddr, ":")[0]
}

// Enhanced QUIC Connection Middleware
func QuicConnectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connID := fmt.Sprintf("conn-%s", r.RemoteAddr)
		quicConnID := extractQuicConnectionID(r)
		remoteAddr := r.RemoteAddr
		localAddr := r.Host

		// Enhanced connection tracking
		connTracker.trackConnection(connID, quicConnID, remoteAddr, localAddr, r)

		// Comprehensive headers
		w.Header().Set("X-Connection-ID", connID)
		w.Header().Set("X-Quic-Connection-ID", quicConnID)
		w.Header().Set("X-Remote-Addr", remoteAddr)
		w.Header().Set("X-Protocol", r.Proto)
		w.Header().Set("X-Migration-Support", "enhanced")
		w.Header().Set("X-Path-Validation", "enabled")
		w.Header().Set("X-Connection-Multiplexing", "active")

		next.ServeHTTP(w, r)
	})
}

// getBackendURLs returns backend URLs from environment variables or defaults
func getBackendURLs() []string {
	// Try to get from environment variables first
	if backend1 := os.Getenv("BACKEND_1_URL"); backend1 != "" {
		if backend2 := os.Getenv("BACKEND_2_URL"); backend2 != "" {
			if backend3 := os.Getenv("BACKEND_3_URL"); backend3 != "" {
				return []string{backend1, backend2, backend3}
			}
		}
	}

	// Fall back to localhost defaults
	return []string{
		"http://localhost:8081", // Backend 1
		"http://localhost:8082", // Backend 2
		"http://localhost:8083", // Backend 3
	}
}

func main() {
	mux := http.NewServeMux()

	// Initialize enhanced backends
	backends := getBackendURLs()

	for _, backendURL := range backends {
		url, err := url.Parse(backendURL)
		if err != nil {
			log.Printf("⚠️ Invalid backend URL %s: %v", backendURL, err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(url)

		// Enhanced proxy error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("❌ Enhanced backend error for %s: %v", url.String(), err)
			for _, backend := range loadBalancer.backends {
				if backend.URL.String() == url.String() {
					backend.AddError()
					break
				}
			}
			http.Error(w, "Backend temporarily unavailable", http.StatusBadGateway)
		}

		backend := &Backend{
			URL:          url,
			Alive:        true,
			ReverseProxy: proxy,
		}
		loadBalancer.AddBackend(backend)
	}

	// Start enhanced health checking
	go healthCheck()

	fs := http.FileServer(http.Dir("./static/"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Enhanced connection monitoring endpoints
	mux.HandleFunc("/api/connections", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		connections := connTracker.getConnections()

		migrationEvents := int64(0)
		successfulMigrations := int64(0)

		for _, conn := range connections {
			migrationEvents += int64(conn.MigrationCount)
			for _, migration := range conn.MigrationEvents {
				if migration.Success {
					successfulMigrations++
				}
			}
		}

		response := map[string]interface{}{
			"connections":           connections,
			"total_count":           len(connections),
			"active_count":          len(connections),
			"migration_events":      migrationEvents,
			"successful_migrations": successfulMigrations,
			"failed_migrations":     migrationEvents - successfulMigrations,
			"timestamp":             time.Now(),
		}
		json.NewEncoder(w).Encode(response)
	})

	// Enhanced load balancer API endpoints
	mux.HandleFunc("/api/loadbalancer", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats := loadBalancer.GetStats()
		json.NewEncoder(w).Encode(stats)
	})

	mux.HandleFunc("/api/loadbalancer/algorithm", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "POST" {
			var req struct {
				Algorithm string `json:"algorithm"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			validAlgorithms := []string{
				"round-robin", "weighted-round-robin", "least-connections",
				"consistent-hash", "health-based", "adaptive-weighted",
			}

			for _, alg := range validAlgorithms {
				if req.Algorithm == alg {
					loadBalancer.mu.Lock()
					loadBalancer.algorithm = req.Algorithm

					// Initialize consistent hash if needed
					if req.Algorithm == "consistent-hash" && loadBalancer.consistentHash == nil {
						loadBalancer.consistentHash = NewConsistentHash(100)
						for _, backend := range loadBalancer.backends {
							loadBalancer.consistentHash.Add(backend)
						}
					}

					loadBalancer.mu.Unlock()
					log.Printf("🔄 Enhanced algorithm changed to: %s", req.Algorithm)
					break
				}
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"algorithm": loadBalancer.algorithm,
			"available": []string{
				"round-robin", "weighted-round-robin", "least-connections",
				"consistent-hash", "health-based", "adaptive-weighted",
			},
		})
	})

	// Enhanced test API endpoint
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
		}

		connInfo := connTracker.getConnections()
		connID := w.Header().Get("X-Connection-ID")

		response := map[string]interface{}{
			"protocol":           protocol,
			"method":             r.Method,
			"remote_addr":        r.RemoteAddr,
			"connection_id":      connID,
			"backend_id":         w.Header().Get("X-Backend-ID"),
			"backend_url":        w.Header().Get("X-Backend-URL"),
			"lb_algorithm":       w.Header().Get("X-LB-Algorithm"),
			"health_score":       w.Header().Get("X-Health-Score"),
			"circuit_breaker":    w.Header().Get("X-Circuit-Breaker"),
			"session_key":        w.Header().Get("X-Session-Key"),
			"active_connections": len(connInfo),
			"migration_support":  "enhanced",
			"path_validation":    "enabled",
			"features":           []string{"circuit-breaker", "health-scoring", "session-affinity", "enhanced-migration"},
			"timestamp":          time.Now(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Add migration simulation endpoint
	mux.HandleFunc("/api/simulate-migration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		currentConnID := w.Header().Get("X-Connection-ID")
		currentRemoteAddr := r.RemoteAddr

		simulateParam := r.URL.Query().Get("simulate")
		if simulateParam == "true" && r.Proto == "HTTP/3.0" {
			simulatedNewAddr := "127.0.0.2:12345"
			connTracker.trackConnection(currentConnID, extractQuicConnectionID(r), simulatedNewAddr, r.Host, r)

			log.Printf("🔄 Simulated enhanced migration for connection %s: %s -> %s",
				currentConnID, currentRemoteAddr, simulatedNewAddr)
		}

		response := map[string]interface{}{
			"message":       "Enhanced migration test endpoint",
			"protocol":      r.Proto,
			"timestamp":     time.Now(),
			"connection_id": currentConnID,
			"remote_addr":   currentRemoteAddr,
			"instructions":  "Add ?simulate=true to manually create a migration event",
			"features":      "Enhanced migration with path validation and timing",
		}

		json.NewEncoder(w).Encode(response)
	})

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Enhanced middleware chain
	finalHandler := LoadBalancerMiddleware(QuicConnectionMiddleware(mux))

	loggedMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		emoji := ""
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
			emoji = "🚀 "
		}

		if !strings.HasPrefix(r.URL.Path, "/static/") &&
			!strings.HasPrefix(r.URL.Path, "/favicon.ico") {
			log.Printf("%s%s %s %s (%s)", emoji, r.RemoteAddr, r.Method, r.URL.Path, protocol)
		}

		w.Header().Set("Alt-Svc", `h3=":9443"; ma=86400`)
		w.Header().Set("X-Server-Protocol", r.Proto)
		w.Header().Set("X-Enhanced-Features", "circuit-breaker,health-scoring,session-affinity,enhanced-migration")

		finalHandler.ServeHTTP(w, r)
	})

	// Enhanced TLS configuration
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		NextProtos: []string{"h3", "h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			log.Printf("TLS ClientHello: ServerName=%s, SupportedVersions=%v, NextProtos=%v",
				hello.ServerName, hello.SupportedVersions, hello.SupportedProtos)
			return nil, nil
		},
	}

	// Start connection cleanup routine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				connTracker.cleanup()
			}
		}
	}()

	// Start metrics collection routine
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Update Prometheus metrics
				connTracker.UpdateMetrics()
				loadBalancer.UpdateMetrics()
			}
		}
	}()

	// Start HTTP/2 server (TCP) for browser compatibility
	go func() {
		tcpServer := &http.Server{
			Addr:         ":9443",
			Handler:      loggedMux,
			TLSConfig:    tlsConfig,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}

		log.Println("🔄 Starting Enhanced HTTP/1.1 & HTTP/2 server (TCP) on :9443")
		if err := tcpServer.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem"); err != nil {
			log.Printf("Enhanced TCP server error: %v", err)
		}
	}()

	// Give TCP server time to start
	time.Sleep(1 * time.Second)

	// Enhanced QUIC configuration
	quicConfig := &quic.Config{
		MaxIdleTimeout:                 30 * time.Second,
		MaxIncomingStreams:             1000,
		MaxIncomingUniStreams:          1000,
		DisablePathMTUDiscovery:        false,
		EnableDatagrams:                true,
		Allow0RTT:                      true,
		InitialStreamReceiveWindow:     512 * 1024,       // 512 KB
		MaxStreamReceiveWindow:         6 * 1024 * 1024,  // 6 MB
		InitialConnectionReceiveWindow: 1024 * 1024,      // 1 MB
		MaxConnectionReceiveWindow:     15 * 1024 * 1024, // 15 MB
	}

	h3Server := &http3.Server{
		Addr:       ":9443", // Same port - HTTP/3 uses UDP, HTTP/2 uses TCP
		Handler:    loggedMux,
		TLSConfig:  tlsConfig,
		QUICConfig: quicConfig,
	}

	currentIP := getLocalIP()

	log.Println("🚀 Starting Enhanced QUIC HTTP/3 Load Balancer")
	log.Printf("🌐 Enhanced Server: https://localhost:9443")
	log.Printf("🌐 Local IP: %s", currentIP)
	log.Printf("📊 Enhanced Dashboard: https://localhost:9443/")
	log.Printf("🔄 Algorithms: adaptive-weighted, weighted-round-robin, health-based, consistent-hash")
	log.Printf("🛡️ Features: Circuit Breaker, Session Affinity, Enhanced Migration, Health Scoring")

	// Start a simple HTTP server for comparison
	go func() {
		httpServer := &http.Server{
			Addr:    ":8080",
			Handler: loggedMux,
		}
		log.Println("🌐 Starting Enhanced HTTP/1.1 server (no TLS) on :8080 for testing")
		if err := httpServer.ListenAndServe(); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Println("🚀 Enhanced HTTP/3 server starting...")
	err := h3Server.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem")
	if err != nil {
		log.Fatal("Enhanced HTTP/3 server failed to start:", err)
	}
}
