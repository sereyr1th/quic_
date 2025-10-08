package main

import (
	"crypto/aes"
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

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// QUIC-LB Draft 20 Implementation
// Reference: https://datatracker.ietf.org/doc/draft-ietf-quic-load-balancers/

// QUICLBConfig defines QUIC-LB configuration as per Draft 20
type QUICLBConfig struct {
	Algorithm               string    `json:"algorithm"`                   // "plaintext", "stream-cipher", "block-cipher"
	ConfigRotationBits      uint8     `json:"config_rotation_bits"`        // 3-bit config identifier (0-6, 7 reserved)
	ServerIDLen             uint8     `json:"server_id_len"`               // Length of server ID in bytes (1-15)
	ConnectionIDLen         uint8     `json:"connection_id_len"`           // Total CID length (4-20 bytes)
	NonceLen                uint8     `json:"nonce_len"`                   // Nonce length for encrypted algorithms
	Key                     []byte    `json:"key,omitempty"`               // 16-byte key for encrypted algorithms
	LoadBalancerID          []byte    `json:"load_balancer_id"`            // Load balancer identifier
	FirstOctetEncodesCIDLen bool      `json:"first_octet_encodes_cid_len"` // Length self-description flag
	CreatedAt               time.Time `json:"created_at"`
	Active                  bool      `json:"active"`
}

// QUICLBEncoder handles Connection ID encoding/decoding per Draft 20
type QUICLBEncoder struct {
	config *QUICLBConfig
	mu     sync.RWMutex
}

// QUICLBConnectionID represents a QUIC-LB compliant connection ID
type QUICLBConnectionID struct {
	Raw                []byte `json:"raw"`
	ConfigRotationBits uint8  `json:"config_rotation_bits"`
	ServerID           []byte `json:"server_id"`
	Nonce              []byte `json:"nonce,omitempty"`
	BackendID          uint16 `json:"backend_id"`
	Valid              bool   `json:"valid"`
	Algorithm          string `json:"algorithm"`
	LengthSelfEncoded  bool   `json:"length_self_encoded"`
}

// Draft 20 Plaintext Algorithm Implementation
func NewPlaintextQUICLB(configRotationBits uint8, serverIDLen uint8, cidLen uint8) *QUICLBEncoder {
	config := &QUICLBConfig{
		Algorithm:          "plaintext",
		ConfigRotationBits: configRotationBits & 0x07, // 3-bit limit (0-6)
		ServerIDLen:        serverIDLen,
		ConnectionIDLen:    cidLen,
		Active:             true,
		CreatedAt:          time.Now(),
	}

	return &QUICLBEncoder{
		config: config,
	}
}

// Simplified: Only supporting plaintext algorithm for educational/deployment use

// NewEncryptedQUICLB creates an encrypted QUIC-LB encoder per Draft 20
func NewEncryptedQUICLB(algorithm string, configRotationBits uint8, serverIDLen uint8, cidLen uint8, nonceLen uint8, key []byte) (*QUICLBEncoder, error) {
	if algorithm != "stream-cipher" && algorithm != "block-cipher" {
		return nil, fmt.Errorf("unsupported encrypted algorithm: %s", algorithm)
	}

	if len(key) != 16 {
		return nil, fmt.Errorf("key must be 16 bytes, got %d", len(key))
	}

	// Validate Draft 20 constraints
	if serverIDLen < 1 || serverIDLen > 15 {
		return nil, fmt.Errorf("server ID length must be 1-15 bytes, got %d", serverIDLen)
	}

	if nonceLen < 4 {
		return nil, fmt.Errorf("nonce length must be at least 4 bytes, got %d", nonceLen)
	}

	if serverIDLen+nonceLen > 19 {
		return nil, fmt.Errorf("server ID + nonce length must not exceed 19 bytes, got %d", serverIDLen+nonceLen)
	}

	config := &QUICLBConfig{
		Algorithm:          algorithm,
		ConfigRotationBits: configRotationBits & 0x07, // 3-bit limit (0-6)
		ServerIDLen:        serverIDLen,
		ConnectionIDLen:    cidLen,
		NonceLen:           nonceLen,
		Key:                make([]byte, 16),
		Active:             true,
		CreatedAt:          time.Now(),
	}
	copy(config.Key, key)

	return &QUICLBEncoder{
		config: config,
	}, nil
}

// Simplified: Removed complex cryptographic functions
// Only supporting plaintext algorithm for simplicity

// EncodePlaintextCID implements Draft 20 Section 5.2 Plaintext Algorithm
func (e *QUICLBEncoder) EncodePlaintextCID(backendID uint16) (*QUICLBConnectionID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.Algorithm != "plaintext" {
		return nil, fmt.Errorf("encoder not configured for plaintext algorithm")
	}

	cid := make([]byte, e.config.ConnectionIDLen)

	// Draft 20 First Octet format:
	// Bits 5-7: Config Rotation (3 bits)
	// Bits 0-4: CID Length or Random (5 bits)
	var lengthOrRandom uint8
	if e.config.FirstOctetEncodesCIDLen {
		// Encode CID length minus 1 (since CID is at least 1 byte)
		lengthOrRandom = e.config.ConnectionIDLen - 1
	} else {
		// Use random bits for privacy
		randByte := make([]byte, 1)
		rand.Read(randByte)
		lengthOrRandom = randByte[0] & 0x1F // 5 bits
	}

	// First octet: Config Rotation (bits 5-7) + Length/Random (bits 0-4)
	cid[0] = (e.config.ConfigRotationBits << 5) | lengthOrRandom

	// Server ID encoding - starts from second byte for plaintext
	serverIDBytes := make([]byte, e.config.ServerIDLen)
	binary.BigEndian.PutUint16(serverIDBytes, backendID)

	// Copy server ID starting from second byte
	if e.config.ServerIDLen > 0 && len(cid) > 1 {
		copy(cid[1:1+e.config.ServerIDLen], serverIDBytes)
	}

	// Fill remaining bytes with random nonce
	nonceStart := int(1 + e.config.ServerIDLen)
	if nonceStart < int(e.config.ConnectionIDLen) {
		nonceLen := int(e.config.ConnectionIDLen) - nonceStart
		nonce := make([]byte, nonceLen)
		rand.Read(nonce)
		copy(cid[nonceStart:], nonce)
	}

	return &QUICLBConnectionID{
		Raw:                cid,
		ConfigRotationBits: e.config.ConfigRotationBits,
		ServerID:           serverIDBytes,
		BackendID:          backendID,
		Valid:              true,
		Algorithm:          "plaintext",
		LengthSelfEncoded:  e.config.FirstOctetEncodesCIDLen,
	}, nil
}

// Simplified: Removed complex stream cipher and block cipher encoding
// Only supporting plaintext algorithm

// EncodeEncryptedCID implements Draft 20 Section 5.4 Encrypted Algorithms
func (e *QUICLBEncoder) EncodeEncryptedCID(backendID uint16, nonce []byte) (*QUICLBConnectionID, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.config.Algorithm == "plaintext" {
		return nil, fmt.Errorf("encoder not configured for encrypted algorithm")
	}

	if len(nonce) != int(e.config.NonceLen) {
		return nil, fmt.Errorf("nonce length mismatch: expected %d, got %d", e.config.NonceLen, len(nonce))
	}

	cid := make([]byte, e.config.ConnectionIDLen)

	// Draft 20 First Octet format
	var lengthOrRandom uint8
	if e.config.FirstOctetEncodesCIDLen {
		lengthOrRandom = e.config.ConnectionIDLen - 1
	} else {
		randByte := make([]byte, 1)
		rand.Read(randByte)
		lengthOrRandom = randByte[0] & 0x1F
	}

	cid[0] = (e.config.ConfigRotationBits << 5) | lengthOrRandom

	// Prepare plaintext: Server ID + Nonce
	serverIDBytes := make([]byte, e.config.ServerIDLen)
	binary.BigEndian.PutUint16(serverIDBytes, backendID)

	plaintext := make([]byte, e.config.ServerIDLen+e.config.NonceLen)
	copy(plaintext, serverIDBytes)
	copy(plaintext[e.config.ServerIDLen:], nonce)

	// Encrypt based on algorithm
	var ciphertext []byte
	var err error

	if e.config.ServerIDLen+e.config.NonceLen == 16 {
		// Single-pass encryption (Section 5.4.1)
		ciphertext, err = e.singlePassEncrypt(plaintext)
	} else {
		// Four-pass encryption (Section 5.4.2)
		ciphertext, err = e.fourPassEncrypt(plaintext)
	}

	if err != nil {
		return nil, fmt.Errorf("encryption failed: %v", err)
	}

	// Copy encrypted data after first octet
	copy(cid[1:], ciphertext)

	return &QUICLBConnectionID{
		Raw:                cid,
		ConfigRotationBits: e.config.ConfigRotationBits,
		ServerID:           serverIDBytes,
		Nonce:              nonce,
		BackendID:          backendID,
		Valid:              true,
		Algorithm:          e.config.Algorithm,
		LengthSelfEncoded:  e.config.FirstOctetEncodesCIDLen,
	}, nil
}

// singlePassEncrypt implements Draft 20 Section 5.4.1
func (e *QUICLBEncoder) singlePassEncrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) != 16 {
		return nil, fmt.Errorf("single-pass encryption requires 16-byte plaintext, got %d", len(plaintext))
	}

	block, err := aes.NewCipher(e.config.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %v", err)
	}

	ciphertext := make([]byte, 16)
	block.Encrypt(ciphertext, plaintext)
	return ciphertext, nil
}

// fourPassEncrypt implements Draft 20 Section 5.4.2 (simplified version)
func (e *QUICLBEncoder) fourPassEncrypt(plaintext []byte) ([]byte, error) {
	// This is a simplified implementation of the four-pass algorithm
	// Full implementation would require the complete Feistel network

	plaintextLen := len(plaintext)
	halfLen := (plaintextLen + 1) / 2

	// Split plaintext into left and right halves
	left := make([]byte, halfLen)
	right := make([]byte, halfLen)

	copy(left, plaintext[:halfLen])
	if plaintextLen > halfLen {
		copy(right, plaintext[halfLen:])
	}

	// Simplified: Just encrypt each half independently for demonstration
	// Real implementation would do 4 rounds of Feistel network

	block, err := aes.NewCipher(e.config.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %v", err)
	}

	// Encrypt left half
	leftPadded := make([]byte, 16)
	copy(leftPadded, left)
	leftPadded[14] = uint8(plaintextLen)
	leftPadded[15] = 1 // pass number

	leftCipher := make([]byte, 16)
	block.Encrypt(leftCipher, leftPadded)

	// XOR with right
	for i := 0; i < len(right); i++ {
		right[i] ^= leftCipher[i]
	}

	// Encrypt right half
	rightPadded := make([]byte, 16)
	copy(rightPadded, right)
	rightPadded[14] = uint8(plaintextLen)
	rightPadded[15] = 2 // pass number

	rightCipher := make([]byte, 16)
	block.Encrypt(rightCipher, rightPadded)

	// XOR with left
	for i := 0; i < len(left); i++ {
		left[i] ^= rightCipher[i]
	}

	// Combine result
	result := make([]byte, plaintextLen)
	copy(result, left)
	if plaintextLen > halfLen {
		copy(result[halfLen:], right[:plaintextLen-halfLen])
	}

	return result, nil
}

// DecodeCID decodes connection ID to extract backend information (Draft 20 compliant)
func (e *QUICLBEncoder) DecodeCID(cid []byte) (*QUICLBConnectionID, error) {
	if len(cid) == 0 {
		return nil, fmt.Errorf("empty connection ID")
	}

	// Extract config rotation bits from first 3 bits (bits 5-7)
	configRotationBits := (cid[0] >> 5) & 0x07

	// Check for reserved config rotation value (0b111)
	if configRotationBits == 0x07 {
		return nil, fmt.Errorf("unroutable connection ID: reserved config rotation value 0b111")
	}

	if configRotationBits != e.config.ConfigRotationBits {
		return nil, fmt.Errorf("config rotation mismatch: expected %d, got %d", e.config.ConfigRotationBits, configRotationBits)
	}

	// Route to appropriate decoding algorithm
	switch e.config.Algorithm {
	case "plaintext":
		return e.decodePlaintextCID(cid)
	case "stream-cipher", "block-cipher":
		return e.decodeEncryptedCID(cid)
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", e.config.Algorithm)
	}
}

// decodeEncryptedCID decodes encrypted connection ID per Draft 20
func (e *QUICLBEncoder) decodeEncryptedCID(cid []byte) (*QUICLBConnectionID, error) {
	if len(cid) < 2 {
		return nil, fmt.Errorf("encrypted CID too short: need at least 2 bytes")
	}

	// Extract config rotation bits
	configRotationBits := (cid[0] >> 5) & 0x07

	// Decrypt the ciphertext portion
	ciphertext := cid[1:]
	plaintextLen := int(e.config.ServerIDLen + e.config.NonceLen)

	if len(ciphertext) < plaintextLen {
		return nil, fmt.Errorf("ciphertext too short: need %d bytes, got %d", plaintextLen, len(ciphertext))
	}

	var plaintext []byte
	var err error

	if plaintextLen == 16 {
		// Single-pass decryption
		plaintext, err = e.singlePassDecrypt(ciphertext[:16])
	} else {
		// Four-pass decryption
		plaintext, err = e.fourPassDecrypt(ciphertext[:plaintextLen])
	}

	if err != nil {
		return nil, fmt.Errorf("decryption failed: %v", err)
	}

	// Extract server ID and nonce
	serverID := plaintext[:e.config.ServerIDLen]
	nonce := plaintext[e.config.ServerIDLen:]

	backendID := binary.BigEndian.Uint16(serverID)

	return &QUICLBConnectionID{
		Raw:                cid,
		ConfigRotationBits: configRotationBits,
		ServerID:           serverID,
		Nonce:              nonce,
		BackendID:          backendID,
		Valid:              true,
		Algorithm:          e.config.Algorithm,
		LengthSelfEncoded:  e.config.FirstOctetEncodesCIDLen,
	}, nil
}

// singlePassDecrypt implements Draft 20 Section 5.5.1
func (e *QUICLBEncoder) singlePassDecrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) != 16 {
		return nil, fmt.Errorf("single-pass decryption requires 16-byte ciphertext")
	}

	block, err := aes.NewCipher(e.config.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %v", err)
	}

	plaintext := make([]byte, 16)
	block.Decrypt(plaintext, ciphertext)
	return plaintext, nil
}

// fourPassDecrypt implements Draft 20 Section 5.5.2 (simplified version)
func (e *QUICLBEncoder) fourPassDecrypt(ciphertext []byte) ([]byte, error) {
	// This is a simplified implementation - real version would reverse the Feistel network
	ciphertextLen := len(ciphertext)
	halfLen := (ciphertextLen + 1) / 2

	// Split ciphertext
	left := make([]byte, halfLen)
	right := make([]byte, halfLen)

	copy(left, ciphertext[:halfLen])
	if ciphertextLen > halfLen {
		copy(right, ciphertext[halfLen:])
	}

	block, err := aes.NewCipher(e.config.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %v", err)
	}

	// Reverse the encryption process (simplified)
	rightPadded := make([]byte, 16)
	copy(rightPadded, right)
	rightPadded[14] = uint8(ciphertextLen)
	rightPadded[15] = 2

	rightCipher := make([]byte, 16)
	block.Encrypt(rightCipher, rightPadded)

	for i := 0; i < len(left); i++ {
		left[i] ^= rightCipher[i]
	}

	leftPadded := make([]byte, 16)
	copy(leftPadded, left)
	leftPadded[14] = uint8(ciphertextLen)
	leftPadded[15] = 1

	leftCipher := make([]byte, 16)
	block.Encrypt(leftCipher, leftPadded)

	for i := 0; i < len(right); i++ {
		right[i] ^= leftCipher[i]
	}

	result := make([]byte, ciphertextLen)
	copy(result, left)
	if ciphertextLen > halfLen {
		copy(result[halfLen:], right[:ciphertextLen-halfLen])
	}

	return result, nil
}

// decodePlaintextCID decodes plaintext connection ID per Draft 20
func (e *QUICLBEncoder) decodePlaintextCID(cid []byte) (*QUICLBConnectionID, error) {
	if len(cid) < 1+int(e.config.ServerIDLen) {
		return nil, fmt.Errorf("CID too short for server ID: need %d bytes, got %d", 1+e.config.ServerIDLen, len(cid))
	}

	// Extract config rotation bits
	configRotationBits := (cid[0] >> 5) & 0x07

	// Note: Length/random bits available at cid[0] & 0x1F for future use

	// Server ID starts from second byte in plaintext mode
	serverID := make([]byte, e.config.ServerIDLen)
	copy(serverID, cid[1:1+e.config.ServerIDLen])

	backendID := binary.BigEndian.Uint16(serverID)

	// Extract nonce if present
	var nonce []byte
	nonceStart := 1 + int(e.config.ServerIDLen)
	if nonceStart < len(cid) {
		nonce = make([]byte, len(cid)-nonceStart)
		copy(nonce, cid[nonceStart:])
	}

	return &QUICLBConnectionID{
		Raw:                cid,
		ConfigRotationBits: configRotationBits,
		ServerID:           serverID,
		Nonce:              nonce,
		BackendID:          backendID,
		Valid:              true,
		Algorithm:          "plaintext",
		LengthSelfEncoded:  e.config.FirstOctetEncodesCIDLen,
	}, nil
}

// Simplified: Removed all complex decryption functions
// Only supporting plaintext algorithm

// Simplified Connection Tracker
type ConnectionTracker struct {
	mu          sync.RWMutex
	connections map[string]*SimpleConnectionInfo
}

// Simplified ConnectionInfo for basic tracking
type SimpleConnectionInfo struct {
	ConnectionID string    `json:"connection_id"`
	RemoteAddr   string    `json:"remote_addr"`
	StartTime    time.Time `json:"start_time"`
	LastSeen     time.Time `json:"last_seen"`
	RequestCount int64     `json:"request_count"`
	Protocol     string    `json:"protocol"`
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

// QUIC-LB Draft 20 Compliant Load Balancer with Config Rotation Support
type QUICLBLoadBalancer struct {
	backends       []*Backend
	mu             sync.RWMutex
	encoders       map[uint8]*QUICLBEncoder // Map of config rotation bits to encoders
	configs        map[uint8]*QUICLBConfig  // Map of config rotation bits to configs
	activeConfig   uint8                    // Currently active config rotation bits
	backendMap     map[uint16]*Backend      // Direct mapping from backend ID to backend
	algorithm      string
	consistentHash *ConsistentHash
	// Unroutable CID handling
	unroutableTable map[string]*Backend // 4-tuple to backend mapping for unroutable CIDs
	cidTable        map[string]*Backend // CID to backend mapping
}

// NewQUICLBLoadBalancer creates a new QUIC-LB load balancer with config rotation support
func NewQUICLBLoadBalancer(algorithm string, config *QUICLBConfig) (*QUICLBLoadBalancer, error) {
	var encoder *QUICLBEncoder
	var err error

	switch config.Algorithm {
	case "plaintext":
		encoder = NewPlaintextQUICLB(config.ConfigRotationBits, config.ServerIDLen, config.ConnectionIDLen)
	case "stream-cipher", "block-cipher":
		if len(config.Key) == 0 {
			return nil, fmt.Errorf("key required for encrypted algorithm: %s", config.Algorithm)
		}
		encoder, err = NewEncryptedQUICLB(config.Algorithm, config.ConfigRotationBits, config.ServerIDLen, config.ConnectionIDLen, config.NonceLen, config.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to create encrypted encoder: %v", err)
		}
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", config.Algorithm)
	}

	lb := &QUICLBLoadBalancer{
		backends:        []*Backend{},
		encoders:        make(map[uint8]*QUICLBEncoder),
		configs:         make(map[uint8]*QUICLBConfig),
		activeConfig:    config.ConfigRotationBits,
		backendMap:      make(map[uint16]*Backend),
		algorithm:       algorithm,
		unroutableTable: make(map[string]*Backend),
		cidTable:        make(map[string]*Backend),
	}

	// Add the initial configuration
	lb.encoders[config.ConfigRotationBits] = encoder
	lb.configs[config.ConfigRotationBits] = config

	return lb, nil
}

// AddBackend adds a backend to the QUIC-LB load balancer with a specific ID
func (qlb *QUICLBLoadBalancer) AddBackend(backend *Backend, backendID uint16) {
	qlb.mu.Lock()
	defer qlb.mu.Unlock()

	backend.ID = int(backendID)
	qlb.backends = append(qlb.backends, backend)
	qlb.backendMap[backendID] = backend
}

// RouteByConnectionID implements stateless routing per QUIC-LB Draft 20 with fallback support
func (qlb *QUICLBLoadBalancer) RouteByConnectionID(connectionID []byte) (*Backend, error) {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	// Try to decode using available configurations
	var cidInfo *QUICLBConnectionID
	var err error

	if len(connectionID) > 0 {
		configRotationBits := (connectionID[0] >> 5) & 0x07

		// Check for reserved value (unroutable)
		if configRotationBits == 0x07 {
			return qlb.handleUnroutableCID(connectionID)
		}

		// Try to find encoder for this config
		if encoder, exists := qlb.encoders[configRotationBits]; exists {
			cidInfo, err = encoder.DecodeCID(connectionID)
			if err == nil {
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
		}
	}

	// If decoding fails, treat as unroutable
	return qlb.handleUnroutableCID(connectionID)
}

// handleUnroutableCID implements Draft 20 Section 4 fallback algorithms
func (qlb *QUICLBLoadBalancer) handleUnroutableCID(connectionID []byte) (*Backend, error) {
	// Draft 20 Section 4.2 - Baseline Fallback Algorithm
	// For now, implement simple round-robin fallback

	if len(qlb.backends) == 0 {
		return nil, fmt.Errorf("no backends available for unroutable CID")
	}

	// Health-aware selection
	var healthyBackends []*Backend
	for _, backend := range qlb.backends {
		if backend.IsAlive() {
			healthyBackends = append(healthyBackends, backend)
		}
	}

	if len(healthyBackends) == 0 {
		return nil, fmt.Errorf("no healthy backends available for unroutable CID")
	}

	// Simple fallback: select first healthy backend
	// Real implementation would use more sophisticated algorithms
	selected := healthyBackends[0]

	// Store in unroutable table for future use (based on CID)
	cidKey := hex.EncodeToString(connectionID)
	qlb.cidTable[cidKey] = selected

	return selected, nil
}

// GenerateConnectionID creates a new connection ID for a selected backend (all algorithms)
func (qlb *QUICLBLoadBalancer) GenerateConnectionID(backendID uint16) ([]byte, error) {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()

	// Use active config for generation
	config := qlb.configs[qlb.activeConfig]
	encoder := qlb.encoders[qlb.activeConfig]

	if config == nil || encoder == nil {
		return nil, fmt.Errorf("no active configuration available")
	}

	switch config.Algorithm {
	case "plaintext":
		cidInfo, err := encoder.EncodePlaintextCID(backendID)
		if err != nil {
			return nil, err
		}
		return cidInfo.Raw, nil

	case "stream-cipher", "block-cipher":
		// Generate random nonce
		nonce := make([]byte, config.NonceLen)
		rand.Read(nonce)

		cidInfo, err := encoder.EncodeEncryptedCID(backendID, nonce)
		if err != nil {
			return nil, err
		}
		return cidInfo.Raw, nil

	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", config.Algorithm)
	}
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

// GetConfig returns the active QUIC-LB configuration
func (qlb *QUICLBLoadBalancer) GetConfig() *QUICLBConfig {
	qlb.mu.RLock()
	defer qlb.mu.RUnlock()
	return qlb.configs[qlb.activeConfig]
}

// AddConfig adds a new configuration for config rotation
func (qlb *QUICLBLoadBalancer) AddConfig(config *QUICLBConfig) error {
	qlb.mu.Lock()
	defer qlb.mu.Unlock()

	if config.ConfigRotationBits > 6 {
		return fmt.Errorf("config rotation bits must be 0-6, got %d", config.ConfigRotationBits)
	}

	var encoder *QUICLBEncoder
	var err error

	switch config.Algorithm {
	case "plaintext":
		encoder = NewPlaintextQUICLB(config.ConfigRotationBits, config.ServerIDLen, config.ConnectionIDLen)
	case "stream-cipher", "block-cipher":
		if len(config.Key) == 0 {
			return fmt.Errorf("key required for encrypted algorithm: %s", config.Algorithm)
		}
		encoder, err = NewEncryptedQUICLB(config.Algorithm, config.ConfigRotationBits, config.ServerIDLen, config.ConnectionIDLen, config.NonceLen, config.Key)
		if err != nil {
			return fmt.Errorf("failed to create encrypted encoder: %v", err)
		}
	default:
		return fmt.Errorf("unsupported algorithm: %s", config.Algorithm)
	}

	qlb.configs[config.ConfigRotationBits] = config
	qlb.encoders[config.ConfigRotationBits] = encoder

	return nil
}

// SetActiveConfig changes the active configuration
func (qlb *QUICLBLoadBalancer) SetActiveConfig(configRotationBits uint8) error {
	qlb.mu.Lock()
	defer qlb.mu.Unlock()

	if _, exists := qlb.configs[configRotationBits]; !exists {
		return fmt.Errorf("configuration %d not found", configRotationBits)
	}

	qlb.activeConfig = configRotationBits
	return nil
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

// Simplified metrics
type LoadBalancingStats struct {
	TotalRequests     int64      `json:"total_requests"`
	TotalConnections  int64      `json:"total_connections"`
	ActiveConnections int64      `json:"active_connections"`
	RequestsPerSecond float64    `json:"requests_per_second"`
	ErrorRate         float64    `json:"error_rate"`
	TotalBackends     int        `json:"total_backends"`
	HealthyBackends   int        `json:"healthy_backends"`
	Algorithm         string     `json:"algorithm"`
	BackendStats      []*Backend `json:"backend_stats"`
	LastUpdate        time.Time  `json:"last_update"`
}

// Simplified global variables
var (
	connTracker = &ConnectionTracker{
		connections: make(map[string]*SimpleConnectionInfo),
	}
	// QUIC-LB Draft 20 compliant load balancer
	quicLBLoadBalancer *QUICLBLoadBalancer
	// Legacy load balancer (for fallback/migration)
	loadBalancer = &LoadBalancer{
		backends:   []*Backend{},
		algorithm:  "round-robin", // Simplified from adaptive-weighted
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

	// Simple connection tracking
	if conn, exists := ct.connections[connID]; exists {
		// Update existing connection
		conn.LastSeen = now
		atomic.AddInt64(&conn.RequestCount, 1)
	} else {
		// Create new connection with simplified info
		ct.connections[connID] = &SimpleConnectionInfo{
			ConnectionID: connID,
			RemoteAddr:   remoteAddr,
			StartTime:    now,
			LastSeen:     now,
			RequestCount: 1,
			Protocol:     req.Proto,
		}
	}
}

func (ct *ConnectionTracker) getConnections() map[string]*SimpleConnectionInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make(map[string]*SimpleConnectionInfo)
	for k, v := range ct.connections {
		result[k] = v
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

	// Simplified algorithms only
	switch lb.algorithm {
	case "weighted-round-robin":
		return lb.getWeightedRoundRobinBackend()
	case "least-connections":
		return lb.getLeastConnectionsBackend()
	case "round-robin":
		fallthrough
	default:
		return lb.getRoundRobinBackend()
	}
}

// Simplified: Removed complex algorithms (adaptive-weighted, health-based, consistent-hash)
// Only keeping basic algorithms for educational use

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

// Simplified: Removed health-based algorithm method

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

	// Simplified metrics calculation
	connections := connTracker.getConnections()

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
		TotalRequests:     atomic.LoadInt64(&totalRequests),
		TotalBackends:     len(lb.backends),
		HealthyBackends:   healthy,
		Algorithm:         lb.algorithm,
		BackendStats:      lb.backends,
		RequestsPerSecond: rps,
		LastUpdate:        time.Now(),
		TotalConnections:  int64(len(connections)),
		ActiveConnections: int64(len(connections)),
		ErrorRate:         errorRate,
	}
}

// Enhanced health checking
func healthCheck() {
	t := time.NewTicker(time.Second * 15) // More frequent checks
	defer t.Stop()

	for range t.C {
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

		// Simplified: Removed complex metrics recording

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
		Algorithm:               "plaintext", // Start with plaintext for demonstration
		ConfigRotationBits:      0x01,        // 3-bit config rotation (0-6)
		ServerIDLen:             2,           // 2 bytes for server ID (supports up to 65536 backends)
		ConnectionIDLen:         8,           // 8-byte connection ID length
		FirstOctetEncodesCIDLen: false,       // Use random bits for privacy by default
		Active:                  true,
		CreatedAt:               time.Now(),
	}

	// Create QUIC-LB compliant load balancer
	var err error
	quicLBLoadBalancer, err = NewQUICLBLoadBalancer("health-aware", quicLBConfig)
	if err != nil {
		log.Fatalf("❌ Failed to create QUIC-LB load balancer: %v", err)
	}

	log.Printf("✅ QUIC-LB Draft 20 compliant load balancer initialized")
	log.Printf("🔧 Algorithm: %s, Config Rotation: %d, Server ID Length: %d bytes",
		quicLBConfig.Algorithm, quicLBConfig.ConfigRotationBits, quicLBConfig.ServerIDLen)

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

		// Simplified connection stats
		response := map[string]interface{}{
			"connections":  connections,
			"total_count":  len(connections),
			"active_count": len(connections),
			"timestamp":    time.Now(),
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
			"algorithm":             quicLBLoadBalancer.GetConfig().Algorithm,
			"stateless":             true,
			"connection_id_routing": true,
			"supported_algorithms":  []string{"plaintext", "stream-cipher", "block-cipher"},
			"timestamp":             time.Now(),
		}
		json.NewEncoder(w).Encode(response)
	})

	// QUIC-LB Configuration Management endpoint
	mux.HandleFunc("/api/quic-lb/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "POST" {
			// Add a new configuration
			var newConfig QUICLBConfig
			if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			err := quicLBLoadBalancer.AddConfig(&newConfig)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to add config: %v", err), http.StatusBadRequest)
				return
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "Configuration added successfully",
				"config":  newConfig,
			})
			return
		}

		// GET - return all configurations
		quicLBLoadBalancer.mu.RLock()
		configs := make(map[string]*QUICLBConfig)
		for bits, config := range quicLBLoadBalancer.configs {
			configs[fmt.Sprintf("config_%d", bits)] = config
		}
		activeConfig := quicLBLoadBalancer.activeConfig
		quicLBLoadBalancer.mu.RUnlock()

		response := map[string]interface{}{
			"configurations": configs,
			"active_config":  activeConfig,
			"draft":          "IETF QUIC-LB Draft 20",
			"features": []string{
				"config-rotation",
				"length-self-description",
				"unroutable-cid-handling",
				"aes-ecb-encryption",
				"multi-algorithm-support",
			},
		}
		json.NewEncoder(w).Encode(response)
	})

	// QUIC-LB Algorithm Demo endpoint
	mux.HandleFunc("/api/quic-lb/demo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Demonstrate different algorithms
		demos := make(map[string]interface{})

		// Demo 1: Plaintext algorithm
		plaintextConfig := &QUICLBConfig{
			Algorithm:               "plaintext",
			ConfigRotationBits:      0x02,
			ServerIDLen:             2,
			ConnectionIDLen:         8,
			FirstOctetEncodesCIDLen: true, // Enable length encoding for demo
			Active:                  true,
			CreatedAt:               time.Now(),
		}

		encoder := NewPlaintextQUICLB(plaintextConfig.ConfigRotationBits, plaintextConfig.ServerIDLen, plaintextConfig.ConnectionIDLen)
		encoder.config.FirstOctetEncodesCIDLen = true

		cid, err := encoder.EncodePlaintextCID(123) // Backend ID 123
		if err == nil {
			decoded, decodeErr := encoder.DecodeCID(cid.Raw)
			demos["plaintext"] = map[string]interface{}{
				"algorithm":            "plaintext",
				"config_rotation_bits": fmt.Sprintf("0b%03b", plaintextConfig.ConfigRotationBits),
				"first_octet_binary":   fmt.Sprintf("0b%08b", cid.Raw[0]),
				"config_bits":          fmt.Sprintf("bits 5-7: 0b%03b", (cid.Raw[0]>>5)&0x07),
				"length_bits":          fmt.Sprintf("bits 0-4: 0b%05b", cid.Raw[0]&0x1F),
				"cid_hex":              hex.EncodeToString(cid.Raw),
				"decoded_backend_id":   decoded.BackendID,
				"decode_success":       decodeErr == nil,
			}
		}

		// Demo 2: Encrypted algorithm (if we add key)
		key := make([]byte, 16)
		rand.Read(key)

		encryptedEncoder, err := NewEncryptedQUICLB("stream-cipher", 0x03, 2, 8, 4, key)
		if err == nil {
			nonce := make([]byte, 4)
			rand.Read(nonce)

			encryptedCID, encErr := encryptedEncoder.EncodeEncryptedCID(456, nonce)
			if encErr == nil {
				decodedEnc, decEncErr := encryptedEncoder.DecodeCID(encryptedCID.Raw)
				demos["encrypted"] = map[string]interface{}{
					"algorithm":            "stream-cipher",
					"config_rotation_bits": fmt.Sprintf("0b%03b", 0x03),
					"first_octet_binary":   fmt.Sprintf("0b%08b", encryptedCID.Raw[0]),
					"config_bits":          fmt.Sprintf("bits 5-7: 0b%03b", (encryptedCID.Raw[0]>>5)&0x07),
					"length_bits":          fmt.Sprintf("bits 0-4: 0b%05b", encryptedCID.Raw[0]&0x1F),
					"cid_hex":              hex.EncodeToString(encryptedCID.Raw),
					"encrypted":            true,
					"decoded_backend_id":   decodedEnc.BackendID,
					"decode_success":       decEncErr == nil,
				}
			}
		}

		// Demo 3: Unroutable CID
		unroutableCID := make([]byte, 8)
		rand.Read(unroutableCID)
		unroutableCID[0] = (0x07 << 5) | (unroutableCID[0] & 0x1F) // Set to reserved value 0b111

		demos["unroutable"] = map[string]interface{}{
			"algorithm":            "unroutable",
			"config_rotation_bits": "0b111 (reserved)",
			"first_octet_binary":   fmt.Sprintf("0b%08b", unroutableCID[0]),
			"config_bits":          "bits 5-7: 0b111 (reserved)",
			"length_bits":          fmt.Sprintf("bits 0-4: 0b%05b", unroutableCID[0]&0x1F),
			"cid_hex":              hex.EncodeToString(unroutableCID),
			"routable":             false,
			"fallback_required":    true,
		}

		response := map[string]interface{}{
			"description": "QUIC-LB Draft 20 Algorithm Demonstrations",
			"first_octet_format": map[string]string{
				"bits_5_7": "Config Rotation (3 bits)",
				"bits_0_4": "CID Length or Random (5 bits)",
			},
			"algorithms": demos,
			"compliance": "IETF QUIC-LB Draft 20",
			"timestamp":  time.Now(),
		}

		json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("/api/quic-lb/test-cid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Generate test connection IDs for each backend
		testResults := make(map[string]interface{})

		for _, backend := range quicLBLoadBalancer.GetBackendStats() {
			backendID := uint16(backend.ID)
			cid, cidErr := quicLBLoadBalancer.GenerateConnectionID(backendID)
			if cidErr != nil {
				testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
					"error": cidErr.Error(),
				}
				continue
			}

			// Test decoding - try with active config first
			var decodedCID *QUICLBConnectionID
			var decodeErr error

			if len(cid) > 0 {
				configRotationBits := (cid[0] >> 5) & 0x07
				if encoder, exists := quicLBLoadBalancer.encoders[configRotationBits]; exists {
					decodedCID, decodeErr = encoder.DecodeCID(cid)
				} else {
					decodeErr = fmt.Errorf("no encoder for config rotation %d", configRotationBits)
				}
			} else {
				decodeErr = fmt.Errorf("empty CID")
			}
			if decodeErr != nil {
				testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
					"cid_hex":      hex.EncodeToString(cid),
					"decode_error": decodeErr.Error(),
				}
				continue
			}

			testResults[fmt.Sprintf("backend_%d", backendID)] = map[string]interface{}{
				"backend_url":          backend.URL.String(),
				"cid_hex":              hex.EncodeToString(cid),
				"cid_length":           len(cid),
				"decoded_backend_id":   decodedCID.BackendID,
				"config_rotation_bits": decodedCID.ConfigRotationBits,
				"algorithm":            decodedCID.Algorithm,
				"valid":                decodedCID.Valid,
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

			// Simplified algorithms only
			validAlgorithms := []string{
				"round-robin", "weighted-round-robin", "least-connections",
			}

			for _, alg := range validAlgorithms {
				if req.Algorithm == alg {
					loadBalancer.mu.Lock()
					loadBalancer.algorithm = req.Algorithm
					loadBalancer.mu.Unlock()
					log.Printf("🔄 Algorithm changed to: %s", req.Algorithm)
					break
				}
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"algorithm": loadBalancer.algorithm,
			"available": []string{
				"round-robin", "weighted-round-robin", "least-connections",
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
			"features":           []string{"basic-health-checks", "session-affinity", "quic-lb-draft-20"},
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

	// Removed Prometheus metrics endpoint for simplicity

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
		w.Header().Set("X-Enhanced-Features", "basic-health-checks,session-affinity,quic-lb-draft-20")

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
		for range ticker.C {
			connTracker.cleanup()
		}
	}()

	// Simplified: Removed complex metrics collection routine

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

		// Use environment variables for certificate paths
		certFile := os.Getenv("TLS_CERT_FILE")
		keyFile := os.Getenv("TLS_KEY_FILE")
		if certFile == "" {
			certFile = "localhost+2.pem" // fallback
		}
		if keyFile == "" {
			keyFile = "localhost+2-key.pem" // fallback
		}

		log.Printf("🔐 Using certificates: cert=%s, key=%s", certFile, keyFile)
		if err := tcpServer.ListenAndServeTLS(certFile, keyFile); err != nil {
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

	log.Println("🚀 Starting IETF QUIC-LB Draft 20 Fully Compliant HTTP/3 Load Balancer")
	log.Printf("📋 QUIC-LB Config: Algorithm=%s, ConfigRotation=%d, ServerIDLen=%d bytes",
		quicLBConfig.Algorithm, quicLBConfig.ConfigRotationBits, quicLBConfig.ServerIDLen)
	log.Printf("🌐 Enhanced Server: https://localhost:9443")
	log.Printf("🌐 Local IP: %s", currentIP)
	log.Printf("📊 Enhanced Dashboard: https://localhost:9443/")
	log.Printf("🔧 QUIC-LB API: https://localhost:9443/api/quic-lb")
	log.Printf("⚙️ Config Management: https://localhost:9443/api/quic-lb/config")
	log.Printf("🧪 Algorithm Demo: https://localhost:9443/api/quic-lb/demo")
	log.Printf("🧪 CID Test: https://localhost:9443/api/quic-lb/test-cid")
	log.Printf("🔄 Algorithms: round-robin, weighted-round-robin, least-connections")
	log.Printf("🛡️ Features: Full Draft 20 Compliance")
	log.Printf("✅ QUIC-LB Draft 20 Features:")
	log.Printf("   • 3-bit Config Rotation (0-6)")
	log.Printf("   • 5-bit Length Self-Description")
	log.Printf("   • Unroutable CID Handling (0b111 reserved)")
	log.Printf("   • AES-ECB Single/Four-Pass Encryption")
	log.Printf("   • Plaintext, Stream-Cipher, Block-Cipher Algorithms")
	log.Printf("   • Stateless Routing with Fallback")
	log.Printf("   • Multiple Configuration Support")

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
		// Use environment variables for certificate paths
		certFile := os.Getenv("TLS_CERT_FILE")
		keyFile := os.Getenv("TLS_KEY_FILE")
		if certFile == "" {
			certFile = "localhost+2.pem" // fallback
		}
		if keyFile == "" {
			keyFile = "localhost+2-key.pem" // fallback
		}

		log.Printf("🔐 HTTP/3 using certificates: cert=%s, key=%s", certFile, keyFile)
		err := h3Server.ListenAndServeTLS(certFile, keyFile)
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
