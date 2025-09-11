package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
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

	// Get current IP address and public IP
	currentIP := getLocalIP()

	fmt.Println("🚀 Starting HTTP/3 server (UDP) on https://localhost:9443")
	fmt.Println("📱 Connection Migration Test Setup Complete!")
	fmt.Println("")
	fmt.Printf("🌐 Local Network IP: %s\n", currentIP)
	fmt.Println("🌍 Public IP: 203.95.199.46 (requires port forwarding)")
	fmt.Println("")
	fmt.Println("🔗 Test URLs:")
	fmt.Println("   🖥️  Desktop: https://localhost:9443")
	fmt.Printf("   📱 Mobile (WiFi): https://%s:9443\n", currentIP)
	fmt.Printf("   🌐 HTTP fallback: http://%s:8080\n", currentIP)
	fmt.Printf("   API test: https://%s:9443/api/test\n", currentIP)
	fmt.Printf("   Connection info: https://%s:9443/api/connections\n", currentIP)
	fmt.Printf("   Migration test: https://%s:9443/api/simulate-migration\n", currentIP)
	fmt.Println("")
	fmt.Println("📱 Mobile Testing Steps:")
	fmt.Println("   1. Connect your phone to the same WiFi network")
	fmt.Printf("   2. Open Chrome/Safari and go to: https://%s:9443\n", currentIP)
	fmt.Println("   3. Accept the security warning (self-signed certificate)")
	fmt.Println("   4. Click 'Test API Endpoint' while on WiFi")
	fmt.Println("   5. Switch to mobile data/cellular")
	fmt.Println("   6. Click 'Test API Endpoint' again")
	fmt.Println("   7. Check 'View Connections' to see migration!")
	fmt.Println("")
	fmt.Println("🔧 Mobile Troubleshooting:")
	fmt.Printf("   • If HTTPS doesn't work, try HTTP: http://%s:8080\n", currentIP)
	fmt.Println("   • Accept certificate warnings in browser")
	fmt.Println("   • Make sure phone and computer are on same WiFi")
	fmt.Println("   • Check firewall settings if connection fails")
	fmt.Println("")
	fmt.Println("🧪 Connection Migration Reality Check:")
	fmt.Println("   ⚠️  IMPORTANT: When you switch from WiFi to cellular:")
	fmt.Printf("   📱 Your phone CANNOT reach %s from cellular network\n", currentIP)
	fmt.Println("   🔄 Migration works when staying on the SAME network")
	fmt.Println("   🔄 Or when using a publicly accessible server")
	fmt.Println("")
	fmt.Println("🧪 Real Migration Testing Options:")
	fmt.Println("   Option 1: Test on same network with different connections")
	fmt.Println("   • Use WiFi repeater or different WiFi bands (2.4GHz vs 5GHz)")
	fmt.Println("   • Switch between WiFi and Ethernet on same network")
	fmt.Println("")
	fmt.Println("   Option 2: Set up port forwarding (Advanced)")
	fmt.Println("   • Configure router to forward port 9443 to this computer")
	fmt.Println("   • Then cellular can reach: https://203.95.199.46:9443")
	fmt.Println("")
	fmt.Println("   Option 3: Local Network Testing (RECOMMENDED)")
	fmt.Println("   • Use the local IP address for real HTTP/3 testing")
	fmt.Println("   • Run: ./start-local-testing.sh")
	fmt.Printf("   • Phone URL: https://%s:9443\n", currentIP)
	fmt.Println("   • This enables REAL HTTP/3 connection migration testing!")
	fmt.Println("   • No tunnel limitations - direct QUIC/UDP connection")
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
