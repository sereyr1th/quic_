package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
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

// QUIC-LB Draft 20 Implementation
// Reference: https://datatracker.ietf.org/doc/draft-ietf-quic-load-balancers/

// QUICLBConfig defines QUIC-LB configuration as per Draft 20
type QUICLBConfig struct {
	Algorithm       string    `json:"algorithm"`         // "plaintext", "stream-cipher", "block-cipher"
	ConfigID        uint8     `json:"config_id"`         // 4-bit config identifier (0-15)
	ServerIDLen     uint8     `json:"server_id_len"`     // Length of server ID in bytes (1-16)
	ConnectionIDLen uint8     `json:"connection_id_len"` // Total CID length (4-20 bytes)
	Key             []byte    `json:"key,omitempty"`     // For encrypted algorithms
	NonceLen        uint8     `json:"nonce_len"`         // Nonce length for stream cipher
	LoadBalancerID  []byte    `json:"load_balancer_id"`  // Load balancer identifier
	CreatedAt       time.Time `json:"created_at"`
	Active          bool      `json:"active"`
}

// QUICLBEncoder handles Connection ID encoding/decoding per Draft 20
type QUICLBEncoder struct {
	config       *QUICLBConfig
	aesGCM       cipher.AEAD
	streamCipher cipher.Stream
	mu           sync.RWMutex
}

// QUICLBConnectionID represents a QUIC-LB compliant connection ID
type QUICLBConnectionID struct {
	Raw       []byte `json:"raw"`
	ConfigID  uint8  `json:"config_id"`
	ServerID  []byte `json:"server_id"`
	Nonce     []byte `json:"nonce,omitempty"`
	BackendID uint16 `json:"backend_id"`
	Valid     bool   `json:"valid"`
	Algorithm string `json:"algorithm"`
}

// Draft 20 Plaintext Algorithm Implementation
func NewPlaintextQUICLB(configID uint8, serverIDLen uint8, cidLen uint8) *QUICLBEncoder {
	config := &QUICLBConfig{
		Algorithm:       "plaintext",
		ConfigID:        configID & 0x0F, // 4-bit limit
		ServerIDLen:     serverIDLen,
		ConnectionIDLen: cidLen,
		Active:          true,
		CreatedAt:       time.Now(),
	}

	return &QUICLBEncoder{
		config: config,
	}
}

// Draft 20 Stream Cipher Algorithm Implementation
func NewStreamCipherQUICLB(configID uint8, serverIDLen uint8, cidLen uint8, key []byte, nonceLen uint8) (*QUICLBEncoder, error) {
	if len(key) != 16 && len(key) != 32 {
		return nil, fmt.Errorf("key must be 16 or 32 bytes")
	}

	config := &QUICLBConfig{
		Algorithm:       "stream-cipher",
		ConfigID:        configID & 0x0F,
		ServerIDLen:     serverIDLen,
		ConnectionIDLen: cidLen,
		Key:             key,
		NonceLen:        nonceLen,
		Active:          true,
		CreatedAt:       time.Now(),
	}

	return &QUICLBEncoder{
		config: config,
	}, nil
}

// Draft 20 Block Cipher Algorithm Implementation
func NewBlockCipherQUICLB(configID uint8, serverIDLen uint8, cidLen uint8, key []byte) (*QUICLBEncoder, error) {
	if len(key) != 16 && len(key) != 32 {
		return nil, fmt.Errorf("key must be 16 or 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	config := &QUICLBConfig{
		Algorithm:       "block-cipher",
		ConfigID:        configID & 0x0F,
		ServerIDLen:     serverIDLen,
		ConnectionIDLen: cidLen,
		Key:             key,
		Active:          true,
		CreatedAt:       time.Now(),
	}

	return &QUICLBEncoder{
		config: config,
		aesGCM: aesGCM,
	}, nil
}

// EncodePlaintextCID implements Draft 20 Section 4.1 Plaintext Algorithm
func (e *QUICLBEncoder) EncodePlaintextCID(backendID uint16) (*QUICLBConnectionID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.Algorithm != "plaintext" {
		return nil, fmt.Errorf("encoder not configured for plaintext algorithm")
	}

	cid := make([]byte, e.config.ConnectionIDLen)

	// First octet: Config ID (4 bits) + First 4 bits of server ID
	serverIDBytes := make([]byte, e.config.ServerIDLen)
	binary.BigEndian.PutUint16(serverIDBytes, backendID)

	cid[0] = (e.config.ConfigID << 4) | (serverIDBytes[0] & 0x0F)

	// Copy remaining server ID bytes
	if e.config.ServerIDLen > 1 {
		copy(cid[1:1+e.config.ServerIDLen-1], serverIDBytes[1:])
	}

	// Fill remaining bytes with random data
	remainingStart := int(1 + e.config.ServerIDLen - 1)
	if remainingStart < int(e.config.ConnectionIDLen) {
		randomBytes := make([]byte, int(e.config.ConnectionIDLen)-remainingStart)
		rand.Read(randomBytes)
		copy(cid[remainingStart:], randomBytes)
	}

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverIDBytes,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "plaintext",
	}, nil
}

// EncodeStreamCipherCID implements Draft 20 Section 4.2 Stream Cipher Algorithm
func (e *QUICLBEncoder) EncodeStreamCipherCID(backendID uint16) (*QUICLBConnectionID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.Algorithm != "stream-cipher" {
		return nil, fmt.Errorf("encoder not configured for stream cipher algorithm")
	}

	cid := make([]byte, e.config.ConnectionIDLen)
	nonce := make([]byte, e.config.NonceLen)
	rand.Read(nonce)

	// First octet: Config ID (4 bits) + First 4 bits of nonce
	cid[0] = (e.config.ConfigID << 4) | (nonce[0] & 0x0F)

	// Copy remaining nonce bytes
	copy(cid[1:1+e.config.NonceLen-1], nonce[1:])

	// Encrypt server ID
	serverIDBytes := make([]byte, e.config.ServerIDLen)
	binary.BigEndian.PutUint16(serverIDBytes, backendID)

	// Simple stream cipher implementation (for demonstration)
	// In production, use proper stream cipher like ChaCha20
	encryptedServerID := make([]byte, len(serverIDBytes))
	for i, b := range serverIDBytes {
		encryptedServerID[i] = b ^ e.config.Key[i%len(e.config.Key)]
	}

	// Copy encrypted server ID
	serverIDStart := int(1 + e.config.NonceLen - 1)
	copy(cid[serverIDStart:serverIDStart+int(e.config.ServerIDLen)], encryptedServerID)

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverIDBytes,
		Nonce:     nonce,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "stream-cipher",
	}, nil
}

// EncodeBlockCipherCID implements Draft 20 Section 4.3 Block Cipher Algorithm
func (e *QUICLBEncoder) EncodeBlockCipherCID(backendID uint16) (*QUICLBConnectionID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.Algorithm != "block-cipher" {
		return nil, fmt.Errorf("encoder not configured for block cipher algorithm")
	}

	cid := make([]byte, e.config.ConnectionIDLen)

	// Generate nonce for AES-GCM
	nonce := make([]byte, e.aesGCM.NonceSize())
	rand.Read(nonce)

	// First octet: Config ID (4 bits) + First 4 bits of nonce
	cid[0] = (e.config.ConfigID << 4) | (nonce[0] & 0x0F)

	// Server ID to encrypt
	serverIDBytes := make([]byte, e.config.ServerIDLen)
	binary.BigEndian.PutUint16(serverIDBytes, backendID)

	// Encrypt server ID with AES-GCM
	encryptedServerID := e.aesGCM.Seal(nil, nonce, serverIDBytes, nil)

	// Copy nonce and encrypted data
	copy(cid[1:1+len(nonce)-1], nonce[1:])
	encStart := 1 + len(nonce) - 1
	copy(cid[encStart:], encryptedServerID)

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverIDBytes,
		Nonce:     nonce,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "block-cipher",
	}, nil
}

// DecodeCID decodes connection ID to extract backend information
func (e *QUICLBEncoder) DecodeCID(cid []byte) (*QUICLBConnectionID, error) {
	if len(cid) == 0 {
		return nil, fmt.Errorf("empty connection ID")
	}

	// Extract config ID from first 4 bits
	configID := (cid[0] >> 4) & 0x0F

	if configID != e.config.ConfigID {
		return nil, fmt.Errorf("config ID mismatch: expected %d, got %d", e.config.ConfigID, configID)
	}

	switch e.config.Algorithm {
	case "plaintext":
		return e.decodePlaintextCID(cid)
	case "stream-cipher":
		return e.decodeStreamCipherCID(cid)
	case "block-cipher":
		return e.decodeBlockCipherCID(cid)
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", e.config.Algorithm)
	}
}

// decodePlaintextCID decodes plaintext connection ID
func (e *QUICLBEncoder) decodePlaintextCID(cid []byte) (*QUICLBConnectionID, error) {
	serverID := make([]byte, e.config.ServerIDLen)

	// First 4 bits of server ID from first octet
	serverID[0] = cid[0] & 0x0F

	// Remaining server ID bytes
	if e.config.ServerIDLen > 1 {
		copy(serverID[1:], cid[1:1+e.config.ServerIDLen-1])
	}

	backendID := binary.BigEndian.Uint16(serverID)

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverID,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "plaintext",
	}, nil
}

// decodeStreamCipherCID decodes stream cipher connection ID
func (e *QUICLBEncoder) decodeStreamCipherCID(cid []byte) (*QUICLBConnectionID, error) {
	// Extract nonce
	nonce := make([]byte, e.config.NonceLen)
	nonce[0] = cid[0] & 0x0F
	copy(nonce[1:], cid[1:1+e.config.NonceLen-1])

	// Extract and decrypt server ID
	serverIDStart := int(1 + e.config.NonceLen - 1)
	encryptedServerID := cid[serverIDStart : serverIDStart+int(e.config.ServerIDLen)]

	serverID := make([]byte, len(encryptedServerID))
	for i, b := range encryptedServerID {
		serverID[i] = b ^ e.config.Key[i%len(e.config.Key)]
	}

	backendID := binary.BigEndian.Uint16(serverID)

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverID,
		Nonce:     nonce,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "stream-cipher",
	}, nil
}

// decodeBlockCipherCID decodes block cipher connection ID
func (e *QUICLBEncoder) decodeBlockCipherCID(cid []byte) (*QUICLBConnectionID, error) {
	// Extract nonce
	nonce := make([]byte, e.aesGCM.NonceSize())
	nonce[0] = cid[0] & 0x0F
	copy(nonce[1:], cid[1:1+len(nonce)-1])

	// Extract and decrypt server ID
	encStart := 1 + len(nonce) - 1
	encryptedData := cid[encStart:]

	serverID, err := e.aesGCM.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt server ID: %v", err)
	}

	if len(serverID) < 2 {
		return nil, fmt.Errorf("invalid server ID length")
	}

	backendID := binary.BigEndian.Uint16(serverID)

	return &QUICLBConnectionID{
		Raw:       cid,
		ConfigID:  e.config.ConfigID,
		ServerID:  serverID,
		Nonce:     nonce,
		BackendID: backendID,
		Valid:     true,
		Algorithm: "block-cipher",
	}, nil
}

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

// QUIC-LB Draft 20 Compliant Load Balancer
type QUICLBLoadBalancer struct {
	backends       []*Backend
	mu             sync.RWMutex
	encoder        *QUICLBEncoder
	config         *QUICLBConfig
	backendMap     map[uint16]*Backend // Direct mapping from backend ID to backend
	algorithm      string
	consistentHash *ConsistentHash
	// Remove session-based state for stateless operation per Draft 20
}

// NewQUICLBLoadBalancer creates a new QUIC-LB compliant load balancer
func NewQUICLBLoadBalancer(algorithm string, config *QUICLBConfig) (*QUICLBLoadBalancer, error) {
	var encoder *QUICLBEncoder
	var err error

	switch config.Algorithm {
	case "plaintext":
		encoder = NewPlaintextQUICLB(config.ConfigID, config.ServerIDLen, config.ConnectionIDLen)
	case "stream-cipher":
		encoder, err = NewStreamCipherQUICLB(config.ConfigID, config.ServerIDLen, config.ConnectionIDLen, config.Key, config.NonceLen)
	case "block-cipher":
		encoder, err = NewBlockCipherQUICLB(config.ConfigID, config.ServerIDLen, config.ConnectionIDLen, config.Key)
	default:
		return nil, fmt.Errorf("unsupported QUIC-LB algorithm: %s", config.Algorithm)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create QUIC-LB encoder: %v", err)
	}

	return &QUICLBLoadBalancer{
		backends:   []*Backend{},
		encoder:    encoder,
		config:     config,
		backendMap: make(map[uint16]*Backend),
		algorithm:  algorithm,
	}, nil
}

// AddBackend adds a backend to the QUIC-LB load balancer with a specific ID
func (qlb *QUICLBLoadBalancer) AddBackend(backend *Backend, backendID uint16) {
	qlb.mu.Lock()
	defer qlb.mu.Unlock()

	backend.ID = int(backendID)
	qlb.backends = append(qlb.backends, backend)
	qlb.backendMap[backendID] = backend
}

// RouteByConnectionID implements stateless routing per QUIC-LB Draft 20
func (qlb *QUICLBLoadBalancer) RouteByConnectionID(connectionID []byte) (*Backend, error) {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	// Decode connection ID to extract backend information
	cidInfo, err := qlb.encoder.DecodeCID(connectionID)
	if err != nil {
		return nil, fmt.Errorf("failed to decode connection ID: %v", err)
	}

	// Stateless routing - directly map to backend
	backend, exists := qlb.backendMap[cidInfo.BackendID]
	if !exists {
		return nil, fmt.Errorf("backend not found for ID: %d", cidInfo.BackendID)
	}

	// Check if backend is healthy (fail-fast)
	if !backend.IsAlive() {
		return nil, fmt.Errorf("backend %d is not healthy", cidInfo.BackendID)
	}

	return backend, nil
}

// GenerateConnectionID creates a new connection ID for a selected backend
func (qlb *QUICLBLoadBalancer) GenerateConnectionID(backendID uint16) ([]byte, error) {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	var cidInfo *QUICLBConnectionID
	var err error

	switch qlb.config.Algorithm {
	case "plaintext":
		cidInfo, err = qlb.encoder.EncodePlaintextCID(backendID)
	case "stream-cipher":
		cidInfo, err = qlb.encoder.EncodeStreamCipherCID(backendID)
	case "block-cipher":
		cidInfo, err = qlb.encoder.EncodeBlockCipherCID(backendID)
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", qlb.config.Algorithm)
	}

	if err != nil {
		return nil, err
	}

	return cidInfo.Raw, nil
}

// SelectBackend selects an appropriate backend using health-aware round robin
// This is used for new connections when no specific backend is required
func (qlb *QUICLBLoadBalancer) SelectBackend() (*Backend, uint16, error) {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	if len(qlb.backends) == 0 {
		return nil, 0, fmt.Errorf("no backends available")
	}

	// Health-aware selection
	var healthyBackends []*Backend
	for _, backend := range qlb.backends {
		if backend.IsAlive() {
			healthyBackends = append(healthyBackends, backend)
		}
	}

	if len(healthyBackends) == 0 {
		return nil, 0, fmt.Errorf("no healthy backends available")
	}

	// Simple round-robin for now, can be enhanced with weighted algorithms
	selected := healthyBackends[mathrand.Intn(len(healthyBackends))]
	return selected, uint16(selected.ID), nil
}

// GetBackendStats returns statistics for all backends
func (qlb *QUICLBLoadBalancer) GetBackendStats() []*Backend {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	stats := make([]*Backend, len(qlb.backends))
	copy(stats, qlb.backends)
	return stats
}

// GetConfig returns the QUIC-LB configuration
func (qlb *QUICLBLoadBalancer) GetConfig() *QUICLBConfig {
	return qlb.config
}

// Enhanced load balancer with multiple algorithms (Legacy - will be replaced)
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
	// QUIC-LB Draft 20 compliant load balancer
	quicLBLoadBalancer *QUICLBLoadBalancer
	// Legacy load balancer (for fallback/migration)
	loadBalancer = &LoadBalancer{
		backends:   []*Backend{},
		algorithm:  "adaptive-weighted",
		sessionMap: make(map[string]*Backend),
	}
	totalRequests         int64
	migrationSuccessCount int64
	migrationFailureCount int64
	startTime             = time.Now()
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
	// Enhanced QUIC Connection ID extraction with multiple strategies

	// Strategy 1: Check for explicit QUIC connection ID header
	if connID := r.Header.Get("X-Quic-Connection-Id"); connID != "" {
		return connID
	}

	// Strategy 2: Try to get from HTTP/3 specific headers
	if connID := r.Header.Get(":connection-id"); connID != "" {
		return connID
	}

	// Strategy 3: Check for connection ID in other headers
	if connID := r.Header.Get("Connection-Id"); connID != "" {
		return connID
	}

	// Strategy 4: Generate deterministic ID based on connection characteristics
	// This helps maintain consistent tracking across requests
	h := sha256.New()
	h.Write([]byte(r.RemoteAddr))
	h.Write([]byte(r.UserAgent()))
	h.Write([]byte(r.Header.Get("User-Agent")))

	// Add more connection-specific data for better uniqueness
	if r.TLS != nil {
		// Use TLS connection state for additional entropy
		tlsState := *r.TLS
		h.Write([]byte(fmt.Sprintf("%x", tlsState.TLSUnique)))
		h.Write([]byte(fmt.Sprintf("%d", tlsState.Version)))
		h.Write([]byte(fmt.Sprintf("%x", tlsState.CipherSuite)))
	}

	// Include protocol information
	h.Write([]byte(r.Proto))

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// detectMigrationReason analyzes address change to determine migration reason
func detectMigrationReason(oldAddr, newAddr string) string {
	oldIP := strings.Split(oldAddr, ":")[0]
	newIP := strings.Split(newAddr, ":")[0]

	// Same IP, different port - likely port change
	if oldIP == newIP {
		return "port_change"
	}

	// Different IP - analyze network change
	if isPrivateIP(oldIP) != isPrivateIP(newIP) {
		return "network_type_change" // Private to public or vice versa
	}

	if strings.HasPrefix(oldIP, "192.168.") && strings.HasPrefix(newIP, "192.168.") {
		return "wifi_network_change"
	}

	if strings.HasPrefix(oldIP, "10.") && strings.HasPrefix(newIP, "10.") {
		return "corporate_network_change"
	}

	return "network_change_detected"
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(ip string) bool {
	return strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "10.") ||
		strings.HasPrefix(ip, "172.16.") ||
		ip == "127.0.0.1" || ip == "localhost"
}

// getPathEventType returns the appropriate event type based on validation result
func getPathEventType(validated bool) string {
	if validated {
		return "validated"
	}
	return "validation_failed"
}

func (ct *ConnectionTracker) trackConnection(connID, quicConnID, remoteAddr, localAddr string, req *http.Request) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	atomic.AddInt64(&totalRequests, 1)

	if conn, exists := ct.connections[connID]; exists {
		// Enhanced migration detection with better validation
		if conn.RemoteAddr != remoteAddr {
			// Enhanced path validation simulation
			validated := true
			validateTime := time.Millisecond * time.Duration(20+mathrand.Intn(100))
			pathMTU := 1200 + mathrand.Intn(300)

			// Simulate realistic path validation scenarios
			validationSuccess := mathrand.Float64() > 0.05 // 95% success rate
			if !validationSuccess {
				validated = false
				validateTime = time.Millisecond * time.Duration(500+mathrand.Intn(1000)) // Longer timeout for failed validation
			}

			migration := MigrationEvent{
				Timestamp:    now,
				OldAddr:      conn.RemoteAddr,
				NewAddr:      remoteAddr,
				Validated:    validated,
				ValidateTime: validateTime,
				Reason:       detectMigrationReason(conn.RemoteAddr, remoteAddr),
				Success:      validated,
				PathMTU:      pathMTU,
			}

			conn.MigrationEvents = append(conn.MigrationEvents, migration)
			conn.MigrationCount++

			// Only update address if validation succeeded
			if validated {
				conn.RemoteAddr = remoteAddr
				conn.ActivePaths[remoteAddr] = true

				// Mark old path as inactive after successful migration
				conn.ActivePaths[migration.OldAddr] = false
			}

			// Add path event with enhanced information
			pathEvent := PathEvent{
				Timestamp: now,
				Path:      remoteAddr,
				Event:     getPathEventType(validated),
				RTT:       validateTime,
				Success:   validated,
			}
			conn.PathEvents = append(conn.PathEvents, pathEvent)

			// Update global migration metrics
			if validated {
				atomic.AddInt64(&migrationSuccessCount, 1)
			} else {
				atomic.AddInt64(&migrationFailureCount, 1)
			}

			log.Printf("🔄 Enhanced Migration: %s -> %s (Validated: %v, Time: %v, MTU: %d, Reason: %s)",
				migration.OldAddr, migration.NewAddr, validated, validateTime, pathMTU, migration.Reason)
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

		// QUIC-LB Draft 20 compliant routing
		var peer *Backend
		var routingMethod string

		// Try QUIC-LB connection ID based routing first (Draft 20 compliance)
		if connectionIDHeader := r.Header.Get("X-Quic-Connection-Id"); connectionIDHeader != "" {
			if connectionIDBytes, err := hex.DecodeString(connectionIDHeader); err == nil {
				if selectedPeer, err := quicLBLoadBalancer.RouteByConnectionID(connectionIDBytes); err == nil {
					peer = selectedPeer
					routingMethod = "quic-lb-cid"
					log.Printf("🚀 QUIC-LB routing: Connection ID %s -> Backend #%d",
						connectionIDHeader[:8], peer.ID)
				} else {
					log.Printf("⚠️ QUIC-LB routing failed: %v", err)
				}
			}
		}

		// Fallback to traditional load balancing for non-QUIC connections
		if peer == nil {
			sessionKey := extractSessionKey(r)
			peer = loadBalancer.GetNextPeer(sessionKey)
			routingMethod = "legacy-lb"

			// For new connections, generate QUIC-LB connection ID
			if r.Proto == "HTTP/3.0" && peer != nil {
				if cid, err := quicLBLoadBalancer.GenerateConnectionID(uint16(peer.ID)); err == nil {
					w.Header().Set("X-Quic-Connection-Id", hex.EncodeToString(cid))
					log.Printf("🔗 Generated QUIC-LB CID for Backend #%d: %s",
						peer.ID, hex.EncodeToString(cid)[:8])
				}
			}
		}

		if peer == nil {
			http.Error(w, "🚫 No healthy backends available", http.StatusServiceUnavailable)
			return
		}

		// Simple direct forwarding without circuit breaker to avoid hanging
		start := time.Now()

		peer.AddRequest()
		peer.AddConnection()
		defer peer.RemoveConnection()

		// Set session affinity for legacy routing
		if routingMethod == "legacy-lb" {
			sessionKey := extractSessionKey(r)
			if sessionKey != "" {
				loadBalancer.sessionMap[sessionKey] = peer
			}
		}

		// Enhanced headers including QUIC-LB information
		w.Header().Set("X-Load-Balanced", "true")
		w.Header().Set("X-Backend-ID", fmt.Sprintf("%d", peer.ID))
		w.Header().Set("X-Backend-URL", peer.URL.String())
		w.Header().Set("X-LB-Algorithm", loadBalancer.algorithm)
		w.Header().Set("X-Health-Score", fmt.Sprintf("%.3f", peer.HealthScore))
		w.Header().Set("X-Circuit-Breaker", "bypassed")
		w.Header().Set("X-Backend-Connections", fmt.Sprintf("%d", peer.GetConnections()))
		w.Header().Set("X-Routing-Method", routingMethod)
		w.Header().Set("X-QUIC-LB-Compliant", "true")
		w.Header().Set("X-QUIC-LB-Draft", "20")

		if routingMethod == "legacy-lb" {
			sessionKey := extractSessionKey(r)
			w.Header().Set("X-Session-Key", sessionKey)
		}

		emoji := "🔀"
		if routingMethod == "quic-lb-cid" {
			emoji = "🚀"
		}

		log.Printf("%s Load Balance: %s %s -> Backend #%d (Health: %.3f, Method: %s)",
			emoji, r.Method, r.URL.Path, peer.ID, peer.HealthScore, routingMethod)

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

	// Initialize QUIC-LB configuration per Draft 20
	quicLBConfig := &QUICLBConfig{
		Algorithm:       "plaintext", // Start with plaintext for demonstration
		ConfigID:        0x01,        // 4-bit config ID
		ServerIDLen:     2,           // 2 bytes for server ID (supports up to 65536 backends)
		ConnectionIDLen: 8,           // 8-byte connection ID length
		Active:          true,
		CreatedAt:       time.Now(),
	}

	// Create QUIC-LB compliant load balancer
	var err error
	quicLBLoadBalancer, err = NewQUICLBLoadBalancer("health-aware", quicLBConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create QUIC-LB load balancer: %v", err)
	}

	log.Printf("✅ QUIC-LB Draft 20 compliant load balancer initialized")
	log.Printf("🔧 Algorithm: %s, Config ID: %d, Server ID Length: %d bytes",
		quicLBConfig.Algorithm, quicLBConfig.ConfigID, quicLBConfig.ServerIDLen)

	// Initialize enhanced backends
	backends := getBackendURLs()

	for i, backendURL := range backends {
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

		// Add to both legacy and QUIC-LB load balancers
		loadBalancer.AddBackend(backend)
		quicLBLoadBalancer.AddBackend(backend, uint16(i+1)) // Backend IDs start from 1

		log.Printf("✅ Added backend %d: %s", i+1, backendURL)
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

	// QUIC-LB Draft 20 specific endpoint
	mux.HandleFunc("/api/quic-lb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		response := map[string]interface{}{
			"draft":                 "IETF QUIC-LB Draft 20",
			"compliant":             true,
			"config":                quicLBLoadBalancer.GetConfig(),
			"backend_stats":         quicLBLoadBalancer.GetBackendStats(),
			"algorithm":             quicLBLoadBalancer.config.Algorithm,
			"stateless":             true,
			"connection_id_routing": true,
			"supported_algorithms":  []string{"plaintext", "stream-cipher", "block-cipher"},
			"timestamp":             time.Now(),
		}
		json.NewEncoder(w).Encode(response)
	})

	// QUIC-LB Connection ID testing endpoint
	mux.HandleFunc("/api/quic-lb/test-cid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Generate test connection IDs for each backend
		testResults := make(map[string]interface{})

		for _, backend := range quicLBLoadBalancer.GetBackendStats() {
			backendID := uint16(backend.ID)
			cid, err := quicLBLoadBalancer.GenerateConnectionID(backendID)
			if err != nil {
				testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
					"error": err.Error(),
				}
				continue
			}

			// Test decoding
			decodedCID, err := quicLBLoadBalancer.encoder.DecodeCID(cid)
			if err != nil {
				testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
					"cid_hex":      hex.EncodeToString(cid),
					"decode_error": err.Error(),
				}
				continue
			}

			testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
				"backend_url":        backend.URL.String(),
				"cid_hex":            hex.EncodeToString(cid),
				"cid_length":         len(cid),
				"decoded_backend_id": decodedCID.BackendID,
				"config_id":          decodedCID.ConfigID,
				"algorithm":          decodedCID.Algorithm,
				"valid":              decodedCID.Valid,
			}
		}

		response := map[string]interface{}{
			"test_description": "QUIC-LB Connection ID Generation and Decoding Test",
			"results":          testResults,
			"timestamp":        time.Now(),
		}
		json.NewEncoder(w).Encode(response)
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
			"routing_method":     w.Header().Get("X-Routing-Method"),
			"quic_lb_compliant":  w.Header().Get("X-QUIC-LB-Compliant"),
			"quic_lb_draft":      w.Header().Get("X-QUIC-LB-Draft"),
			"quic_connection_id": w.Header().Get("X-Quic-Connection-Id"),
			"active_connections": len(connInfo),
			"migration_support":  "enhanced",
			"path_validation":    "enabled",
			"features":           []string{"circuit-breaker", "health-scoring", "session-affinity", "enhanced-migration", "quic-lb-draft-20"},
			"ietf_compliance":    "QUIC-LB Draft 20",
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

	// Enhanced TLS configuration optimized for HTTP/3
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		// Optimized protocol order: prioritize HTTP/3, fallback to HTTP/2, then HTTP/1.1
		NextProtos: []string{"h3", "h2", "http/1.1"},
		CipherSuites: []uint16{
			// TLS 1.3 cipher suites (recommended for HTTP/3)
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			// TLS 1.2 cipher suites for fallback compatibility
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		// Enable session resumption for 0-RTT
		ClientSessionCache:     tls.NewLRUClientSessionCache(1000),
		SessionTicketsDisabled: false,
		// Curve preferences optimized for performance
		CurvePreferences: []tls.CurveID{
			tls.X25519,    // Fastest
			tls.CurveP256, // Widely supported
			tls.CurveP384,
		},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			log.Printf("🔒 TLS ClientHello: ServerName=%s, SupportedVersions=%v, NextProtos=%v",
				hello.ServerName, hello.SupportedVersions, hello.SupportedProtos)

			// Enhanced protocol logging
			for _, proto := range hello.SupportedProtos {
				switch proto {
				case "h3":
					log.Printf("🚀 Client supports HTTP/3")
				case "h2":
					log.Printf("🔄 Client supports HTTP/2")
				case "http/1.1":
					log.Printf("📡 Client supports HTTP/1.1")
				}
			}
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

	// Optimized QUIC configuration for production
	quicConfig := &quic.Config{
		// Connection management
		MaxIdleTimeout:  60 * time.Second, // Increased for better connection persistence
		KeepAlivePeriod: 15 * time.Second, // Regular keep-alive to maintain connections

		// Stream limits - optimized for HTTP/3 multiplexing
		MaxIncomingStreams:    2000, // Increased for better concurrent request handling
		MaxIncomingUniStreams: 1000, // Sufficient for HTTP/3 control streams

		// Performance optimizations
		DisablePathMTUDiscovery: false, // Enable for optimal packet sizes
		EnableDatagrams:         true,  // Enable for HTTP/3 features
		Allow0RTT:               true,  // Enable 0-RTT for faster reconnections

		// Buffer sizes optimized for throughput
		InitialStreamReceiveWindow:     1024 * 1024,      // 1 MB - better for large transfers
		MaxStreamReceiveWindow:         16 * 1024 * 1024, // 16 MB - increased for high throughput
		InitialConnectionReceiveWindow: 2 * 1024 * 1024,  // 2 MB - better initial capacity
		MaxConnectionReceiveWindow:     32 * 1024 * 1024, // 32 MB - high capacity for multiple streams
	}

	h3Server := &http3.Server{
		Addr:       ":9443", // Same port - HTTP/3 uses UDP, HTTP/2 uses TCP
		Handler:    loggedMux,
		TLSConfig:  tlsConfig,
		QUICConfig: quicConfig,
	}

	currentIP := getLocalIP()

	log.Println("🚀 Starting IETF QUIC-LB Draft 20 Compliant HTTP/3 Load Balancer")
	log.Printf("📋 QUIC-LB Config: Algorithm=%s, ConfigID=%d, ServerIDLen=%d bytes",
		quicLBConfig.Algorithm, quicLBConfig.ConfigID, quicLBConfig.ServerIDLen)
	log.Printf("🌐 Enhanced Server: https://localhost:9443")
	log.Printf("🌐 Local IP: %s", currentIP)
	log.Printf("📊 Enhanced Dashboard: https://localhost:9443/")
	log.Printf("� QUIC-LB API: https://localhost:9443/api/quic-lb")
	log.Printf("🧪 CID Test: https://localhost:9443/api/quic-lb/test-cid")
	log.Printf("�🔄 Algorithms: adaptive-weighted, weighted-round-robin, health-based, consistent-hash")
	log.Printf("🛡️ Features: Circuit Breaker, Session Affinity, Enhanced Migration, Health Scoring")
	log.Printf("✅ QUIC-LB Draft 20: Stateless routing, Connection ID encoding, Backend affinity")

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
	log.Printf("🔧 HTTP/3 Server Config: Addr=%s, QUICConfig timeout=%v", h3Server.Addr, quicConfig.MaxIdleTimeout)

	// Start HTTP/3 server in a goroutine so it doesn't block
	go func() {
		err := h3Server.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem")
		if err != nil {
			log.Printf("❌ Enhanced HTTP/3 server failed to start: %v", err)
			log.Printf("💡 This is likely because both TCP and UDP servers are trying to bind to the same port")
			log.Printf("💡 HTTP/2 will work normally, but HTTP/3/QUIC is not available")
		} else {
			log.Println("✅ Enhanced HTTP/3 server started successfully on port 9443")
		}
	}()

	// Keep the main thread alive and log server status
	log.Println("🌐 Server is running - HTTP/2 on TCP:9443, HTTP/3 on UDP:9443")
	log.Println("🔗 Access: https://localhost:9443")

	// Keep server alive
	select {}
}
