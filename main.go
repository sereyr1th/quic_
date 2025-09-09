package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func main() {
	var configFile = flag.String("config", "config.json", "Configuration file path")
	var generateConfig = flag.Bool("generate-config", false, "Generate default configuration file and exit")
	flag.Parse()

	// Generate default config if requested
	if *generateConfig {
		config := DefaultConfig()
		if err := config.Save("config.json"); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Println("Default configuration saved to config.json")
		fmt.Println("Please edit the configuration file and restart the server")
		return
	}

	// Load configuration
	config, err := LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	log.Printf("Starting QUIC/HTTP3 Load Balancer for Moodle")
	log.Printf("Configuration loaded from: %s", *configFile)

	// Create load balancer
	loadBalancer := NewLoadBalancer(&config.LoadBalancer)
	defer loadBalancer.Close()

	// Create main HTTP handler
	mux := http.NewServeMux()

	// Static files handler (for testing)
	fs := http.FileServer(http.Dir("./static/"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Health check endpoint for the load balancer itself
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now(),
			"version":   "1.0.0",
		})
	})

	// Load balancer statistics endpoint
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats := loadBalancer.GetStats()
		json.NewEncoder(w).Encode(stats)
	})

	// API test endpoint
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Proto
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
		}

		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"protocol":      protocol,
			"method":       r.Method,
			"remote_addr":  r.RemoteAddr,
			"headers":      r.Header,
			"load_balancer": "active",
			"timestamp":    time.Now(),
		}
		json.NewEncoder(w).Encode(response)
	})

	// All other requests go through the load balancer
	mux.Handle("/", loadBalancer)

	// Create logging middleware
	loggedMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		protocol := r.Proto
		emoji := ""
		if r.Proto == "HTTP/3.0" {
			protocol = "HTTP/3.0 🚀"
			emoji = "🚀 "
		}

		// Set Alt-Svc header for HTTP/3 advertisement
		w.Header().Set("Alt-Svc", `h3=":9443"; ma=86400`)

		// Add load balancer headers
		w.Header().Set("X-Load-Balancer", "QUIC-HTTP3-LB")
		w.Header().Set("X-Server-Protocol", r.Proto)
		w.Header().Set("X-Connection-Migration", strconv.FormatBool(config.LoadBalancer.ConnectionMigration))

		mux.ServeHTTP(w, r)

		duration := time.Since(start)
		log.Printf("%s%s %s %s (Protocol: %s, Duration: %v)", 
			emoji, r.RemoteAddr, r.Method, r.URL.Path, protocol, duration)
	})

	// Configure TLS
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
		NextProtos: config.TLS.NextProtos,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if config.Logging.LogConnections {
				log.Printf("TLS ClientHello: ServerName=%s, SupportedVersions=%v, NextProtos=%v",
					hello.ServerName, hello.SupportedVersions, hello.SupportedProtos)
			}
			return nil, nil // Use default certificate loading
		},
	}

	// Start HTTP/1.1 server for testing (if enabled)
	if config.TLS.EnableH1 && config.Server.HTTPPort != 0 {
		go func() {
			httpServer := &http.Server{
				Addr:    fmt.Sprintf(":%d", config.Server.HTTPPort),
				Handler: loggedMux,
			}
			log.Printf("🌐 Starting HTTP/1.1 server on :%d", config.Server.HTTPPort)
			if err := httpServer.ListenAndServe(); err != nil {
				log.Printf("HTTP/1.1 server error: %v", err)
			}
		}()
	}

	// Start HTTP/2 server (TCP) if enabled
	if config.TLS.EnableH2 {
		go func() {
			tcpServer := &http.Server{
				Addr:         fmt.Sprintf(":%d", config.Server.HTTPSPort),
				Handler:      loggedMux,
				TLSConfig:    tlsConfig,
				ReadTimeout:  config.Server.ReadTimeout,
				WriteTimeout: config.Server.WriteTimeout,
			}

			log.Printf("🔄 Starting HTTP/1.1 & HTTP/2 server (TCP) on :%d", config.Server.HTTPSPort)
			if err := tcpServer.ListenAndServeTLS(config.TLS.CertFile, config.TLS.KeyFile); err != nil {
				log.Printf("TCP server error: %v", err)
			}
		}()
	}

	// Give other servers time to start
	time.Sleep(1 * time.Second)

	// Create and start HTTP/3 server if enabled
	if config.TLS.EnableH3 {
		h3Server := &http3.Server{
			Addr:      fmt.Sprintf(":%d", config.Server.HTTPSPort),
			Handler:   loggedMux,
			TLSConfig: tlsConfig,
		}

		// Monitor connections if logging is enabled
		if config.Logging.LogConnections {
			go func() {
				for {
					time.Sleep(30 * time.Second)
					log.Println("📡 HTTP/3 server running, monitoring QUIC connections...")
					stats := loadBalancer.GetStats()
					log.Printf("📊 Load balancer stats: %d active connections, %d backends", 
						stats["activeConnections"], len(stats["backends"].([]map[string]interface{})))
				}
			}()
		}

		// Setup graceful shutdown
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			log.Println("🛑 Shutting down server...")
			loadBalancer.Close()
			os.Exit(0)
		}()

		// Print startup information
		fmt.Println("\n🚀 QUIC/HTTP3 Load Balancer for Moodle")
		fmt.Println("=====================================")
		fmt.Printf("📡 HTTP/3 server: https://localhost:%d\n", config.Server.HTTPSPort)
		if config.Server.HTTPPort != 0 {
			fmt.Printf("🌐 HTTP/1.1 server: http://localhost:%d\n", config.Server.HTTPPort)
		}
		fmt.Printf("📊 Statistics: https://localhost:%d/stats\n", config.Server.HTTPSPort)
		fmt.Printf("🏥 Health check: https://localhost:%d/health\n", config.Server.HTTPSPort)
		fmt.Printf("🧪 Test endpoint: https://localhost:%d/api/test\n", config.Server.HTTPSPort)
		fmt.Println("\n🔧 Features enabled:")
		fmt.Printf("   ✅ Connection Migration: %v\n", config.LoadBalancer.ConnectionMigration)
		fmt.Printf("   ✅ Session Persistence: %v\n", config.LoadBalancer.SessionPersistence)
		fmt.Printf("   ✅ Health Checks: %v\n", config.LoadBalancer.HealthCheck.Enabled)
		fmt.Printf("   ✅ Load Balancing Strategy: %s\n", config.LoadBalancer.Strategy)
		fmt.Printf("   ✅ Backend Servers: %d configured\n", len(config.LoadBalancer.BackendServers))
		
		fmt.Println("\n🧪 Testing commands:")
		fmt.Printf("   curl -v --http3-only -k https://localhost:%d/api/test\n", config.Server.HTTPSPort)
		fmt.Printf("   curl -v --http2 -k https://localhost:%d/api/test\n", config.Server.HTTPSPort)
		if config.Server.HTTPPort != 0 {
			fmt.Printf("   curl -v http://localhost:%d/api/test\n", config.Server.HTTPPort)
		}
		fmt.Printf("   curl -k https://localhost:%d/stats\n", config.Server.HTTPSPort)
		
		fmt.Println("\n📚 Moodle Integration:")
		fmt.Println("   1. Configure your Moodle servers as backend servers in config.json")
		fmt.Println("   2. Update Moodle's config.php with the load balancer URL")
		fmt.Println("   3. Set up SSL certificates for your domain")
		fmt.Println("   4. Configure health check endpoint in Moodle")
		fmt.Println("")

		log.Printf("🚀 Starting HTTP/3 server on :%d...", config.Server.HTTPSPort)
		err := h3Server.ListenAndServeTLS(config.TLS.CertFile, config.TLS.KeyFile)
		if err != nil {
			log.Fatal("HTTP/3 server failed to start:", err)
		}
	} else {
		log.Println("HTTP/3 is disabled in configuration")
		select {} // Block forever
	}
}
