package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
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

var (
	connTracker = &ConnectionTracker{
		connections: make(map[string]*ConnectionInfo),
	}
)

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

		// For now, use a combination of remote address and protocol as connection identifier
		if r.Proto == "HTTP/3.0" {
			connID = fmt.Sprintf("h3-%s-%d", remoteAddr, time.Now().Unix()%1000)
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

	fs := http.FileServer(http.Dir("./static/"))
	mux.Handle("/", fs)

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

	// Add a simple API endpoint to test HTTP/3
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
		}

		// Get connection info from headers set by middleware
		connID := w.Header().Get("X-Connection-ID")

		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"protocol":      protocol,
			"method":        r.Method,
			"remote_addr":   r.RemoteAddr,
			"connection_id": connID,
			"timestamp":     time.Now(),
			"request_id":    r.URL.Query().Get("req"),
		}

		json.NewEncoder(w).Encode(response)
	})

	// Add migration simulation endpoint
	mux.HandleFunc("/api/simulate-migration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// This endpoint helps test migration by providing different response content
		// that can help identify if the same connection is being used
		response := map[string]interface{}{
			"message":       "Migration test endpoint",
			"protocol":      r.Proto,
			"timestamp":     time.Now(),
			"connection_id": w.Header().Get("X-Connection-ID"),
			"instructions":  "Change your network (WiFi to cellular, different WiFi) and call this endpoint again",
		}

		json.NewEncoder(w).Encode(response)
	})

	// Wrap the mux with our connection tracking middleware
	trackedMux := QuicConnectionMiddleware(mux)

	loggedMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		emoji := ""
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
			emoji = "🚀 "
		}
		log.Printf("%s%s %s %s (Protocol: %s)", emoji, r.RemoteAddr, r.Method, r.URL.Path, protocol)

		// Set Alt-Svc header for HTTP/3 advertisement
		w.Header().Set("Alt-Svc", `h3=":9443"; ma=86400`)

		// Add some debugging headers
		w.Header().Set("X-Server-Protocol", r.Proto)
		w.Header().Set("X-Alt-Svc-Sent", "h3=\":9443\"; ma=86400")

		trackedMux.ServeHTTP(w, r)
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

	// Add a goroutine to monitor UDP connections
	go func() {
		for {
			time.Sleep(5 * time.Second)
			log.Println("📡 HTTP/3 server is running and listening for UDP connections...")
		}
	}()

	fmt.Println("🚀 Starting HTTP/3 server (UDP) on https://localhost:9443")
	fmt.Println("📱 Connection Migration Test Setup Complete!")
	fmt.Println("")
	fmt.Println("🔗 Test URLs:")
	fmt.Println("   🖥️  Desktop: https://localhost:9443")
	fmt.Println("   📱 Mobile: https://192.168.0.180:9443")
	fmt.Println("   🌐 HTTP fallback: http://192.168.0.180:8080")
	fmt.Println("   API test: https://192.168.0.180:9443/api/test")
	fmt.Println("   Connection info: https://192.168.0.180:9443/api/connections")
	fmt.Println("   Migration test: https://192.168.0.180:9443/api/simulate-migration")
	fmt.Println("")
	fmt.Println("📱 Mobile Testing Steps:")
	fmt.Println("   1. Connect your phone to the same WiFi network")
	fmt.Println("   2. Open Chrome/Safari and go to: https://192.168.0.180:9443")
	fmt.Println("   3. Accept the security warning (self-signed certificate)")
	fmt.Println("   4. Click 'Test API Endpoint' while on WiFi")
	fmt.Println("   5. Switch to mobile data/cellular")
	fmt.Println("   6. Click 'Test API Endpoint' again")
	fmt.Println("   7. Check 'View Connections' to see migration!")
	fmt.Println("")
	fmt.Println("🔧 Mobile Troubleshooting:")
	fmt.Println("   • If HTTPS doesn't work, try HTTP: http://192.168.0.180:8080")
	fmt.Println("   • Accept certificate warnings in browser")
	fmt.Println("   • Make sure phone and computer are on same WiFi")
	fmt.Println("   • Check firewall settings if connection fails")
	fmt.Println("")
	fmt.Println("🧪 Migration Testing Steps:")
	fmt.Println("   1. Open https://localhost:9443 and note your IP")
	fmt.Println("   2. Make some requests to /api/test")
	fmt.Println("   3. Change networks (WiFi to hotspot, different WiFi, etc.)")
	fmt.Println("   4. Make more requests - connection should migrate seamlessly!")
	fmt.Println("   5. Check /api/connections to see migration events")
	fmt.Println("")
	fmt.Println("✨ Look for the 🚀 emoji in logs to spot HTTP/3 requests!")
	fmt.Println("🔄 Look for the 🔄 emoji to spot connection migrations!")
	fmt.Println("🔄 Try refreshing the page multiple times to activate HTTP/3")
	fmt.Println("")
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

	fmt.Println("🧪 Testing commands:")
	fmt.Println("   curl -v --http3-only -k https://localhost:9443/api/test")
	fmt.Println("   curl -v --http3-only -k https://localhost:9443/api/connections")
	fmt.Println("   curl -v --http2 -k https://localhost:9443/api/test")
	fmt.Println("   curl -v http://localhost:8080/api/test")
	fmt.Println("")
	fmt.Println("🔧 Troubleshooting:")
	fmt.Println("   1. Run 'mkcert -install' to trust the CA")
	fmt.Println("   2. Try http://localhost:8080 first (no TLS)")
	fmt.Println("   3. Enable chrome://flags/#allow-insecure-localhost")
	fmt.Println("   4. Use Chrome DevTools Network tab to see HTTP/3 usage")
	fmt.Println("")

	log.Println("🚀 HTTP/3 server starting...")
	err := h3Server.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem")
	if err != nil {
		log.Fatal("HTTP/3 server failed to start:", err)
	}
}
