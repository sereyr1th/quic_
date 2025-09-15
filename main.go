package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// ConnectionTracker tracks QUIC connections and their migrations
type ConnectionTracker struct {
	mu          sync.RWMutex
	connections map[string]*ConnectionInfo
}

// ConnectionInfo holds information about a QUIC connection
type ConnectionInfo struct {
	ConnectionID    string           `json:"connection_id"`
	RemoteAddr      string           `json:"remote_addr"`
	LocalAddr       string           `json:"local_addr"`
	StartTime       time.Time        `json:"start_time"`
	LastSeen        time.Time        `json:"last_seen"`
	RequestCount    int              `json:"request_count"`
	MigrationCount  int              `json:"migration_count"`
	MigrationEvents []MigrationEvent `json:"migration_events"`
}

// MigrationEvent represents a connection migration event
type MigrationEvent struct {
	Timestamp time.Time `json:"timestamp"`
	OldAddr   string    `json:"old_addr"`
	NewAddr   string    `json:"new_addr"`
	Validated bool      `json:"validated"`
}

// Backend represents a backend server
type Backend struct {
	ID           int      `json:"id"`
	URL          *url.URL `json:"url"`
	Alive        bool     `json:"alive"`
	mu           sync.RWMutex
	ReverseProxy *httputil.ReverseProxy `json:"-"`
	Connections  int64                  `json:"connections"`
	RequestCount int64                  `json:"request_count"`
	ErrorCount   int64                  `json:"error_count"`
	LastCheck    time.Time              `json:"last_check"`
	ResponseTime time.Duration          `json:"response_time"`
}

// LoadBalancer manages multiple backends
type LoadBalancer struct {
	backends  []*Backend
	current   uint64
	mu        sync.RWMutex
	algorithm string
}

// LoadBalancingStats holds load balancing statistics
type LoadBalancingStats struct {
	TotalRequests     int64      `json:"total_requests"`
	TotalBackends     int        `json:"total_backends"`
	HealthyBackends   int        `json:"healthy_backends"`
	Algorithm         string     `json:"algorithm"`
	BackendStats      []*Backend `json:"backend_stats"`
	RequestsPerSecond float64    `json:"requests_per_second"`
	LastUpdate        time.Time  `json:"last_update"`
}

var (
	connTracker = &ConnectionTracker{
		connections: make(map[string]*ConnectionInfo),
	}
	loadBalancer = &LoadBalancer{
		backends:  []*Backend{},
		algorithm: "round-robin",
	}
	totalRequests int64
	startTime     = time.Now()
)

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

// trackConnection tracks or updates connection information
func (ct *ConnectionTracker) trackConnection(connID, remoteAddr, localAddr string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()

	if conn, exists := ct.connections[connID]; exists {
		// Check if this is a migration (different remote address)
		if conn.RemoteAddr != remoteAddr {
			migration := MigrationEvent{
				Timestamp: now,
				OldAddr:   conn.RemoteAddr,
				NewAddr:   remoteAddr,
				Validated: true, // In HTTP/3, if we receive a request, the path is validated
			}
			conn.MigrationEvents = append(conn.MigrationEvents, migration)
			conn.MigrationCount++
			conn.RemoteAddr = remoteAddr

			log.Printf("🔄 Connection Migration detected! %s -> %s (Connection ID: %s)",
				migration.OldAddr, migration.NewAddr, connID)
		}
		conn.LastSeen = now
		conn.RequestCount++
	} else {
		// New connection
		ct.connections[connID] = &ConnectionInfo{
			ConnectionID:    connID,
			RemoteAddr:      remoteAddr,
			LocalAddr:       localAddr,
			StartTime:       now,
			LastSeen:        now,
			RequestCount:    1,
			MigrationCount:  0,
			MigrationEvents: []MigrationEvent{},
		}
		log.Printf("🆕 New QUIC connection: %s from %s (Connection ID: %s)",
			localAddr, remoteAddr, connID)
	}
}

// getConnections returns all tracked connections
func (ct *ConnectionTracker) getConnections() map[string]*ConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	// Create a copy to avoid race conditions
	result := make(map[string]*ConnectionInfo)
	for k, v := range ct.connections {
		// Deep copy the connection info
		connCopy := *v
		connCopy.MigrationEvents = make([]MigrationEvent, len(v.MigrationEvents))
		copy(connCopy.MigrationEvents, v.MigrationEvents)
		result[k] = &connCopy
	}
	return result
}

// cleanup removes old connections
func (ct *ConnectionTracker) cleanup() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute) // Remove connections older than 5 minutes
	for id, conn := range ct.connections {
		if conn.LastSeen.Before(cutoff) {
			delete(ct.connections, id)
		}
	}
}

// Backend methods
func (b *Backend) IsAlive() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Alive
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
}

func (b *Backend) AddError() {
	atomic.AddInt64(&b.ErrorCount, 1)
}

func (b *Backend) GetRequestCount() int64 {
	return atomic.LoadInt64(&b.RequestCount)
}

func (b *Backend) GetErrorCount() int64 {
	return atomic.LoadInt64(&b.ErrorCount)
}

// LoadBalancer methods
func (lb *LoadBalancer) AddBackend(backend *Backend) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	backend.ID = len(lb.backends)
	lb.backends = append(lb.backends, backend)
	log.Printf("🏪 Added backend #%d: %s", backend.ID, backend.URL.String())
}

func (lb *LoadBalancer) NextIndex() int {
	return int(atomic.AddUint64(&lb.current, uint64(1)) % uint64(len(lb.backends)))
}

func (lb *LoadBalancer) GetNextPeer() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	switch lb.algorithm {
	case "least-connections":
		return lb.getLeastConnectionsBackend()
	case "round-robin":
		fallthrough
	default:
		return lb.getRoundRobinBackend()
	}
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
	minConnections := int64(^uint64(0) >> 1) // Max int64

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

	return &LoadBalancingStats{
		TotalRequests:     atomic.LoadInt64(&totalRequests),
		TotalBackends:     len(lb.backends),
		HealthyBackends:   healthy,
		Algorithm:         lb.algorithm,
		BackendStats:      lb.backends,
		RequestsPerSecond: rps,
		LastUpdate:        time.Now(),
	}
}

// Health check function
func healthCheck() {
	t := time.NewTicker(time.Second * 30)
	defer t.Stop()

	// Track previous status to only log changes
	previousStatus := make(map[int]bool)

	for {
		select {
		case <-t.C:
			loadBalancer.mu.RLock()
			backends := make([]*Backend, len(loadBalancer.backends))
			copy(backends, loadBalancer.backends)
			loadBalancer.mu.RUnlock()

			for _, backend := range backends {
				start := time.Now()
				isAlive := isBackendAlive(backend.URL)
				responseTime := time.Since(start)

				backend.mu.Lock()
				backend.ResponseTime = responseTime
				backend.mu.Unlock()

				backend.SetAlive(isAlive)

				// Only log status changes
				if prevStatus, exists := previousStatus[backend.ID]; !exists || prevStatus != isAlive {
					status := "❌ DOWN"
					if isAlive {
						status = "✅ UP"
					}
					log.Printf("🏥 Backend #%d %s %s", backend.ID, backend.URL, status)
					previousStatus[backend.ID] = isAlive
				}
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

// LoadBalancerMiddleware handles load balancing
func LoadBalancerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip load balancing for API endpoints (serve directly)
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		// For root path and content, use load balancing
		if r.URL.Path == "/" || r.URL.Path == "/index.html" ||
			r.URL.Path == "/app" || r.URL.Path == "/content" {

			// Get next backend for content requests
			peer := loadBalancer.GetNextPeer()
			if peer == nil {
				http.Error(w, "🚫 No healthy backends available", http.StatusServiceUnavailable)
				return
			}

			// Track request
			atomic.AddInt64(&totalRequests, 1)
			peer.AddRequest()
			peer.AddConnection()
			defer peer.RemoveConnection()

			// Add load balancer headers
			w.Header().Set("X-Load-Balanced", "true")
			w.Header().Set("X-Backend-ID", fmt.Sprintf("%d", peer.ID))
			w.Header().Set("X-Backend-URL", peer.URL.String())
			w.Header().Set("X-LB-Algorithm", loadBalancer.algorithm)

			log.Printf("🔀 Load Balanced: %s %s -> Backend #%d (%s)",
				r.Method, r.URL.Path, peer.ID, peer.URL.String())

			// Forward request to backend
			peer.ReverseProxy.ServeHTTP(w, r)
			return
		}

		// Serve other paths directly
		next.ServeHTTP(w, r)
	})
}

// QuicConnectionMiddleware extracts QUIC connection information
func QuicConnectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract connection ID from context if available
		connID := "unknown"
		remoteAddr := r.RemoteAddr
		localAddr := r.Host

		// Try to get more detailed connection info from the request context
		if r.Context().Value("quic-connection") != nil {
			// This would be populated by a custom HTTP/3 server implementation
		}

		// For HTTP/3, use remote address as stable connection identifier
		// In a real QUIC implementation, we would use the actual QUIC Connection ID
		if r.Proto == "HTTP/3.0" {
			connID = fmt.Sprintf("h3-%s", remoteAddr)
			connTracker.trackConnection(connID, remoteAddr, localAddr)
		}

		// Add connection info to response headers for debugging
		w.Header().Set("X-Connection-ID", connID)
		w.Header().Set("X-Remote-Addr", remoteAddr)
		w.Header().Set("X-Protocol", r.Proto)

		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()

	// Initialize backends - Add your backend servers here!
	backends := []string{
		"http://localhost:8081", // Backend 1
		"http://localhost:8082", // Backend 2
		"http://localhost:8083", // Backend 3
	}

	for _, backendURL := range backends {
		url, err := url.Parse(backendURL)
		if err != nil {
			log.Printf("⚠️ Invalid backend URL %s: %v", backendURL, err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(url)

		// Customize proxy to handle errors
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("❌ Backend error for %s: %v", url.String(), err)
			// Find the backend and increment error count
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

	// Start health checking
	go healthCheck()

	fs := http.FileServer(http.Dir("./static/"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Add connection monitoring endpoints
	mux.HandleFunc("/api/connections", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		connections := connTracker.getConnections()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connections": connections,
			"total_count": len(connections),
			"timestamp":   time.Now(),
		})
	})

	// NEW: Load balancer API endpoints
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

			if req.Algorithm == "round-robin" || req.Algorithm == "least-connections" {
				loadBalancer.mu.Lock()
				loadBalancer.algorithm = req.Algorithm
				loadBalancer.mu.Unlock()
				log.Printf("🔄 Load balancer algorithm changed to: %s", req.Algorithm)
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"algorithm": loadBalancer.algorithm,
			"available": []string{"round-robin", "least-connections"},
		})
	})

	// Add a simple API endpoint to test HTTP/3
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
		}

		// Get connection info from headers set by middleware
		connID := w.Header().Get("X-Connection-ID")
		backendID := w.Header().Get("X-Backend-ID")
		backendURL := w.Header().Get("X-Backend-URL")
		lbAlgorithm := w.Header().Get("X-LB-Algorithm")

		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"protocol":      protocol,
			"method":        r.Method,
			"remote_addr":   r.RemoteAddr,
			"connection_id": connID,
			"backend_id":    backendID,
			"backend_url":   backendURL,
			"lb_algorithm":  lbAlgorithm,
			"load_balanced": w.Header().Get("X-Load-Balanced"),
			"timestamp":     time.Now(),
			"request_id":    r.URL.Query().Get("req"),
		}

		json.NewEncoder(w).Encode(response)
	})

	// Add migration simulation endpoint
	mux.HandleFunc("/api/simulate-migration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get current connection info
		currentConnID := w.Header().Get("X-Connection-ID")
		currentRemoteAddr := r.RemoteAddr

		// Check if we should simulate a migration
		simulateParam := r.URL.Query().Get("simulate")
		if simulateParam == "true" && r.Proto == "HTTP/3.0" {
			// Manually create a migration simulation
			// Use the same connection ID but with a simulated different address
			simulatedNewAddr := "127.0.0.2:12345" // Simulated new address

			// Track this as if it came from the new address
			connTracker.trackConnection(currentConnID, simulatedNewAddr, r.Host)

			log.Printf("🔄 Simulated migration for connection %s: %s -> %s",
				currentConnID, currentRemoteAddr, simulatedNewAddr)
		}

		response := map[string]interface{}{
			"message":            "Migration test endpoint",
			"protocol":           r.Proto,
			"timestamp":          time.Now(),
			"connection_id":      currentConnID,
			"remote_addr":        currentRemoteAddr,
			"instructions":       "Add ?simulate=true to manually create a migration event",
			"real_migration_tip": "For real migration: Change networks (WiFi/cellular) and call again",
		}

		json.NewEncoder(w).Encode(response)
	})

	// Wrap the mux with our middleware chain: LoadBalancer -> QuicConnection -> Logging
	finalHandler := LoadBalancerMiddleware(QuicConnectionMiddleware(mux))

	loggedMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		emoji := ""
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
			emoji = "🚀 "
		}

		// Only log non-static requests and important endpoints to reduce noise
		if !strings.HasPrefix(r.URL.Path, "/static/") &&
			!strings.HasPrefix(r.URL.Path, "/favicon.ico") {
			log.Printf("%s%s %s %s (%s)", emoji, r.RemoteAddr, r.Method, r.URL.Path, protocol)
		}

		// Set Alt-Svc header for HTTP/3 advertisement
		w.Header().Set("Alt-Svc", `h3=":9443"; ma=86400`)

		// Add some debugging headers
		w.Header().Set("X-Server-Protocol", r.Proto)
		w.Header().Set("X-Alt-Svc-Sent", "h3=\":9443\"; ma=86400")

		finalHandler.ServeHTTP(w, r)
	})

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12, // Allow TLS 1.2 for broader compatibility
		MaxVersion: tls.VersionTLS13,
		NextProtos: []string{"h3", "h2", "http/1.1"},
		// Add more detailed logging
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			log.Printf("TLS ClientHello: ServerName=%s, SupportedVersions=%v, NextProtos=%v",
				hello.ServerName, hello.SupportedVersions, hello.SupportedProtos)
			return nil, nil // Return nil to use default certificate loading
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

	// Start HTTP/2 server (TCP)
	go func() {
		tcpServer := &http.Server{
			Addr:         ":9443",
			Handler:      loggedMux,
			TLSConfig:    tlsConfig,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}

		log.Println("🔄 Starting HTTP/1.1 & HTTP/2 server (TCP) on :9443")
		if err := tcpServer.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem"); err != nil {
			log.Printf("TCP server error: %v", err)
		}
	}()

	// Give TCP server time to start
	time.Sleep(1 * time.Second)

	// Create HTTP/3 server (UDP) with enhanced migration support
	quicConfig := &quic.Config{
		MaxIdleTimeout:          30 * time.Second,
		MaxIncomingStreams:      100,
		MaxIncomingUniStreams:   100,
		DisablePathMTUDiscovery: false,
		EnableDatagrams:         true,
		// Connection migration is enabled by default in quic-go
		TokenStore: nil, // Use default
	}

	h3Server := &http3.Server{
		Addr:       ":9443",
		Handler:    loggedMux,
		TLSConfig:  tlsConfig,
		QUICConfig: quicConfig,
	}

	// Removed verbose UDP connection monitoring

	// Get current IP address and public IP
	currentIP := getLocalIP()

	log.Println("🚀 Starting QUIC HTTP/3 Load Balancer")
	log.Printf("� Server: https://localhost:9443")
	log.Printf("🌐 Local IP: %s", currentIP)
	log.Printf("� Dashboard: https://localhost:9443/")

	// Start a simple HTTP server for comparison
	go func() {
		httpServer := &http.Server{
			Addr:    ":8080",
			Handler: loggedMux,
		}
		log.Println("🌐 Starting HTTP/1.1 server (no TLS) on :8080 for testing")
		if err := httpServer.ListenAndServe(); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Println("🚀 HTTP/3 server starting...")
	err := h3Server.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem")
	if err != nil {
		log.Fatal("HTTP/3 server failed to start:", err)
	}
}
