package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func main() {
	certFile := "/etc/ssl/localcerts/localhost.crt"
	keyFile := "/etc/ssl/localcerts/localhost.key"

	target, _ := url.Parse("http://127.0.0.1:8082")
	rp := httputil.NewSingleHostReverseProxy(target)
	originalDirector := rp.Director
	rp.Director = func(r *http.Request) {
		originalDirector(r)
		// Preserve Host and set proxy headers for Moodle
		r.Host = "localhost:9443"
		r.Header.Set("X-Forwarded-Host", "localhost:9443")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Port", "9443")
		// X-Forwarded-For is set by Go’s reverse proxy automatically, but we can ensure it:
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			r.Header.Set("X-Forwarded-For", ip)
		}
	}

	// Add Alt-Svc header so browsers upgrade to HTTP/3 on next request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", `h3=":9443"; ma=86400`)
		rp.ServeHTTP(w, r)
	})

	// TLS config for TCP (HTTP/1.1 and HTTP/2)
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13, // HTTP/3 requires TLS 1.3
		NextProtos: []string{"h2", "http/1.1"},
	}
	srv := &http.Server{
		Addr:         ":9443",
		Handler:      h,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		TLSConfig:    tlsCfg,
	}

	// Start HTTP/3 server on UDP 9443
	go func() {
		log.Println("Starting HTTP/3 (QUIC) on :9443")
		if err := http3.ListenAndServeTLS(":9443", certFile, keyFile, h); err != nil {
			log.Fatalf("http3 error: %v", err)
		}
	}()

	// Start TCP TLS server (HTTP/1.1 + HTTP/2) on :9443
	log.Println("Starting HTTPS (h2/h1) on :9443")
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
		log.Fatalf("https error: %v", err)
	}
}
