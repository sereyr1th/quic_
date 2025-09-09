package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancer manages multiple backend servers
type LoadBalancer struct {
	config          *LoadBalancerConfig
	backends        []*Backend
	currentIndex    uint64
	mu              sync.RWMutex
	healthChecker   *HealthChecker
	sessionStore    *SessionStore
	connectionTracker *ConnectionTracker
}

// Backend represents a backend server with health status
type Backend struct {
	*BackendServer
	proxy              *httputil.ReverseProxy
	connections        int64
	lastUsed           time.Time
	consecutiveSuccess int
	consecutiveFailures int
	mu                 sync.RWMutex
}

// HealthChecker manages health checking for backends
type HealthChecker struct {
	config   *HealthCheckConfig
	loadBalancer *LoadBalancer
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// SessionStore manages session persistence
type SessionStore struct {
	sessions map[string]string // sessionID -> backendID
	mu       sync.RWMutex
	ttl      time.Duration
}

// ConnectionTracker tracks QUIC connections for migration support
type ConnectionTracker struct {
	connections map[string]*ConnectionInfo
	mu          sync.RWMutex
}

// ConnectionInfo stores information about a QUIC connection
type ConnectionInfo struct {
	ConnectionID string
	BackendID    string
	ClientAddr   net.Addr
	StartTime    time.Time
	LastActivity time.Time
	Migrated     bool
}

// NewLoadBalancer creates a new load balancer
func NewLoadBalancer(config *LoadBalancerConfig) *LoadBalancer {
	lb := &LoadBalancer{
		config:          config,
		backends:        make([]*Backend, 0, len(config.BackendServers)),
		sessionStore:    NewSessionStore(24 * time.Hour), // 24 hour session TTL
		connectionTracker: NewConnectionTracker(),
	}
	
	// Initialize backends
	for _, serverConfig := range config.BackendServers {
		backend := &Backend{
			BackendServer: &serverConfig,
		}
		
		// Create reverse proxy for this backend
		targetURL := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", serverConfig.Host, serverConfig.Port),
		}
		
		backend.proxy = httputil.NewSingleHostReverseProxy(targetURL)
		backend.proxy.ModifyResponse = lb.modifyResponse
		backend.proxy.ErrorHandler = lb.errorHandler
		
		lb.backends = append(lb.backends, backend)
	}
	
	// Start health checker if enabled
	if config.HealthCheck.Enabled {
		lb.healthChecker = NewHealthChecker(&config.HealthCheck, lb)
		lb.healthChecker.Start()
	}
	
	return lb
}

// NewSessionStore creates a new session store
func NewSessionStore(ttl time.Duration) *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]string),
		ttl:      ttl,
	}
	
	// Start cleanup goroutine
	go store.cleanup()
	
	return store
}

// NewConnectionTracker creates a new connection tracker
func NewConnectionTracker() *ConnectionTracker {
	tracker := &ConnectionTracker{
		connections: make(map[string]*ConnectionInfo),
	}
	
	// Start cleanup goroutine
	go tracker.cleanup()
	
	return tracker
}

// ServeHTTP implements the http.Handler interface
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Track connection for QUIC migration support
	connInfo := lb.trackConnection(r)
	
	// Select backend based on strategy
	backend := lb.selectBackend(r, connInfo)
	if backend == nil {
		http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
		return
	}
	
	// Set up request context for connection tracking
	ctx := context.WithValue(r.Context(), "backend_id", backend.ID)
	ctx = context.WithValue(ctx, "connection_id", connInfo.ConnectionID)
	r = r.WithContext(ctx)
	
	// Add Moodle-specific headers
	lb.addMoodleHeaders(r, backend)
	
	// Update backend usage stats
	atomic.AddInt64(&backend.connections, 1)
	backend.mu.Lock()
	backend.lastUsed = time.Now()
	backend.mu.Unlock()
	
	// Store session mapping if session persistence is enabled
	if lb.config.SessionPersistence {
		if sessionID := lb.extractSessionID(r); sessionID != "" {
			lb.sessionStore.Set(sessionID, backend.ID)
		}
	}
	
	// Serve the request
	backend.proxy.ServeHTTP(w, r)
	
	// Update connection tracking
	connInfo.LastActivity = time.Now()
	connInfo.BackendID = backend.ID
	
	// Decrement connection count
	atomic.AddInt64(&backend.connections, -1)
}

// selectBackend selects a backend based on the configured strategy
func (lb *LoadBalancer) selectBackend(r *http.Request, connInfo *ConnectionInfo) *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	
	// Filter healthy backends
	healthy := make([]*Backend, 0, len(lb.backends))
	for _, backend := range lb.backends {
		if backend.Healthy {
			healthy = append(healthy, backend)
		}
	}
	
	if len(healthy) == 0 {
		return nil
	}
	
	// If connection migration is enabled and we have existing connection info
	if lb.config.ConnectionMigration && connInfo.BackendID != "" {
		for _, backend := range healthy {
			if backend.ID == connInfo.BackendID {
				return backend
			}
		}
		// Original backend is not healthy, log migration
		log.Printf("Connection migration: %s -> selecting new backend", connInfo.ConnectionID)
		connInfo.Migrated = true
	}
	
	// Check for session persistence
	if lb.config.SessionPersistence {
		if sessionID := lb.extractSessionID(r); sessionID != "" {
			if backendID := lb.sessionStore.Get(sessionID); backendID != "" {
				for _, backend := range healthy {
					if backend.ID == backendID {
						return backend
					}
				}
			}
		}
	}
	
	// Apply load balancing strategy
	switch lb.config.Strategy {
	case "least_connections":
		return lb.selectLeastConnections(healthy)
	case "ip_hash":
		return lb.selectIPHash(r, healthy)
	default: // round_robin
		return lb.selectRoundRobin(healthy)
	}
}

// selectRoundRobin implements round-robin selection
func (lb *LoadBalancer) selectRoundRobin(backends []*Backend) *Backend {
	index := atomic.AddUint64(&lb.currentIndex, 1) - 1
	return backends[index%uint64(len(backends))]
}

// selectLeastConnections implements least connections selection
func (lb *LoadBalancer) selectLeastConnections(backends []*Backend) *Backend {
	var selected *Backend
	minConnections := int64(^uint64(0) >> 1) // max int64
	
	for _, backend := range backends {
		connections := atomic.LoadInt64(&backend.connections)
		if connections < minConnections {
			minConnections = connections
			selected = backend
		}
	}
	
	return selected
}

// selectIPHash implements IP hash-based selection
func (lb *LoadBalancer) selectIPHash(r *http.Request, backends []*Backend) *Backend {
	clientIP := lb.getClientIP(r)
	hash := fnv.New32a()
	hash.Write([]byte(clientIP))
	index := hash.Sum32() % uint32(len(backends))
	return backends[index]
}

// trackConnection tracks QUIC connections for migration support
func (lb *LoadBalancer) trackConnection(r *http.Request) *ConnectionInfo {
	// Generate connection ID based on request characteristics
	connID := lb.generateConnectionID(r)
	
	lb.connectionTracker.mu.Lock()
	defer lb.connectionTracker.mu.Unlock()
	
	if info, exists := lb.connectionTracker.connections[connID]; exists {
		info.LastActivity = time.Now()
		return info
	}
	
	// Create new connection info
	info := &ConnectionInfo{
		ConnectionID: connID,
		ClientAddr:   lb.getAddr(r),
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		Migrated:     false,
	}
	
	lb.connectionTracker.connections[connID] = info
	return info
}

// generateConnectionID generates a unique connection ID
func (lb *LoadBalancer) generateConnectionID(r *http.Request) string {
	// Use a combination of client IP, user agent, and other headers
	data := fmt.Sprintf("%s:%s:%s", 
		lb.getClientIP(r), 
		r.Header.Get("User-Agent"),
		r.Header.Get("X-Forwarded-For"))
	
	hash := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", hash)
}

// getClientIP extracts the real client IP
func (lb *LoadBalancer) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	
	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	
	// Fall back to remote address
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// getAddr gets the network address from request
func (lb *LoadBalancer) getAddr(r *http.Request) net.Addr {
	if addr, err := net.ResolveTCPAddr("tcp", r.RemoteAddr); err == nil {
		return addr
	}
	return nil
}

// extractSessionID extracts session ID from request
func (lb *LoadBalancer) extractSessionID(r *http.Request) string {
	// Try to get from cookie first
	if cookie, err := r.Cookie("MoodleSession"); err == nil {
		return cookie.Value
	}
	
	// Try to get from PHPSESSID cookie
	if cookie, err := r.Cookie("PHPSESSID"); err == nil {
		return cookie.Value
	}
	
	// Try to get from query parameter
	return r.URL.Query().Get("sesskey")
}

// addMoodleHeaders adds Moodle-specific proxy headers
func (lb *LoadBalancer) addMoodleHeaders(r *http.Request, backend *Backend) {
	// Add standard proxy headers
	r.Header.Set("X-Forwarded-For", lb.getClientIP(r))
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Real-IP", lb.getClientIP(r))
	
	// Add backend information
	r.Header.Set("X-Backend-Server", backend.ID)
	r.Header.Set("X-Backend-Host", fmt.Sprintf("%s:%d", backend.Host, backend.Port))
	
	// Add protocol information
	r.Header.Set("X-Protocol", r.Proto)
	if r.TLS != nil {
		r.Header.Set("X-TLS-Version", fmt.Sprintf("%.1f", float64(r.TLS.Version)/256.0))
	}
}

// modifyResponse modifies the response from backend
func (lb *LoadBalancer) modifyResponse(r *http.Response) error {
	// Add load balancer headers
	r.Header.Set("X-Load-Balancer", "QUIC-HTTP3-LB")
	r.Header.Set("X-Backend-Server", r.Request.Header.Get("X-Backend-Server"))
	
	// Add Alt-Svc header for HTTP/3 advertisement
	r.Header.Set("Alt-Svc", `h3=":9443"; ma=86400`)
	
	return nil
}

// errorHandler handles backend errors
func (lb *LoadBalancer) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("Backend error for %s: %v", r.URL.Path, err)
	
	// Mark backend as potentially unhealthy
	if backendID := r.Header.Get("X-Backend-Server"); backendID != "" {
		lb.mu.RLock()
		for _, backend := range lb.backends {
			if backend.ID == backendID {
				// Don't immediately mark as unhealthy, let health checker decide
				log.Printf("Backend %s reported error, health checker will verify", backendID)
				break
			}
		}
		lb.mu.RUnlock()
	}
	
	http.Error(w, "Backend server error", http.StatusBadGateway)
}

// Get retrieves a session mapping
func (ss *SessionStore) Get(sessionID string) string {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.sessions[sessionID]
}

// Set stores a session mapping
func (ss *SessionStore) Set(sessionID, backendID string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[sessionID] = backendID
}

// cleanup removes expired sessions
func (ss *SessionStore) cleanup() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	
	for range ticker.C {
		// In a real implementation, you'd track session creation time
		// For now, we'll keep sessions indefinitely
	}
}

// cleanup removes old connection info
func (ct *ConnectionTracker) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		ct.mu.Lock()
		now := time.Now()
		for id, info := range ct.connections {
			if now.Sub(info.LastActivity) > 10*time.Minute {
				delete(ct.connections, id)
			}
		}
		ct.mu.Unlock()
	}
}

// GetStats returns load balancer statistics
func (lb *LoadBalancer) GetStats() map[string]interface{} {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	
	stats := make(map[string]interface{})
	backends := make([]map[string]interface{}, 0, len(lb.backends))
	
	for _, backend := range lb.backends {
		backendStats := map[string]interface{}{
			"id":          backend.ID,
			"host":        backend.Host,
			"port":        backend.Port,
			"healthy":     backend.Healthy,
			"connections": atomic.LoadInt64(&backend.connections),
			"weight":      backend.Weight,
		}
		
		backend.mu.RLock()
		backendStats["lastUsed"] = backend.lastUsed
		backend.mu.RUnlock()
		
		backends = append(backends, backendStats)
	}
	
	stats["backends"] = backends
	stats["strategy"] = lb.config.Strategy
	stats["sessionPersistence"] = lb.config.SessionPersistence
	stats["connectionMigration"] = lb.config.ConnectionMigration
	
	// Add connection tracking stats
	lb.connectionTracker.mu.RLock()
	stats["activeConnections"] = len(lb.connectionTracker.connections)
	migrated := 0
	for _, info := range lb.connectionTracker.connections {
		if info.Migrated {
			migrated++
		}
	}
	stats["migratedConnections"] = migrated
	lb.connectionTracker.mu.RUnlock()
	
	return stats
}

// Close gracefully shuts down the load balancer
func (lb *LoadBalancer) Close() error {
	if lb.healthChecker != nil {
		lb.healthChecker.Stop()
	}
	return nil
}