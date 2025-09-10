package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func main() {
	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir("./static/"))
	mux.Handle("/", fs)

	// Add a simple API endpoint to test HTTP/3
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"protocol": "%s", "method": "%s", "remote_addr": "%s"}`,
			protocol, r.Method, r.RemoteAddr)
	})

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

		mux.ServeHTTP(w, r)
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

	// Create HTTP/3 server (UDP)
	h3Server := &http3.Server{
		Addr:      ":9443",
		Handler:   loggedMux,
		TLSConfig: tlsConfig,
	}

	// Add a goroutine to monitor UDP connections
	go func() {
		for {
			time.Sleep(5 * time.Second)
			log.Println("📡 HTTP/3 server is running and listening for UDP connections...")
		}
	}()

	fmt.Println(" Starting HTTP/3 server (UDP) on https://localhost:9443")
	fmt.Println(" Open https://localhost:9443 in your browser")
	fmt.Println(" Test API endpoint: https://localhost:9443/api/test")
	fmt.Println("✨ Look for the 🚀 emoji in logs to spot HTTP/3 requests!")
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
	fmt.Println("   curl -v --http2 -k https://localhost:9443/api/test")
	fmt.Println("   curl -v http://localhost:8080/api/test")
	fmt.Println("")
	fmt.Println("🔧 Troubleshooting:")
	fmt.Println("   1. Run 'mkcert -install' to trust the CA")
	fmt.Println("   2. Try http://localhost:8080 first (no TLS)")
	fmt.Println("   3. Enable chrome://flags/#allow-insecure-localhost")
	fmt.Println("")

	log.Println("🚀 HTTP/3 server starting...")
	err := h3Server.ListenAndServeTLS("localhost+2.pem", "localhost+2-key.pem")
	if err != nil {
		log.Fatal("HTTP/3 server failed to start:", err)
	}
}
