package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// NewHealthChecker creates a new health checker
func NewHealthChecker(config *HealthCheckConfig, lb *LoadBalancer) *HealthChecker {
	return &HealthChecker{
		config:       config,
		loadBalancer: lb,
		stopCh:       make(chan struct{}),
	}
}

// Start begins health checking
func (hc *HealthChecker) Start() {
	if !hc.config.Enabled {
		return
	}
	
	hc.wg.Add(1)
	go hc.run()
	log.Printf("Health checker started with %v interval", hc.config.Interval)
}

// Stop stops health checking
func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
	hc.wg.Wait()
	log.Println("Health checker stopped")
}

// run performs the health checking loop
func (hc *HealthChecker) run() {
	defer hc.wg.Done()
	
	ticker := time.NewTicker(hc.config.Interval)
	defer ticker.Stop()
	
	// Initial health check
	hc.checkAllBackends()
	
	for {
		select {
		case <-ticker.C:
			hc.checkAllBackends()
		case <-hc.stopCh:
			return
		}
	}
}

// checkAllBackends checks health of all backends
func (hc *HealthChecker) checkAllBackends() {
	hc.loadBalancer.mu.RLock()
	backends := make([]*Backend, len(hc.loadBalancer.backends))
	copy(backends, hc.loadBalancer.backends)
	hc.loadBalancer.mu.RUnlock()
	
	var wg sync.WaitGroup
	for _, backend := range backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			hc.checkBackend(b)
		}(backend)
	}
	wg.Wait()
}

// checkBackend performs health check on a single backend
func (hc *HealthChecker) checkBackend(backend *Backend) {
	healthy := hc.isBackendHealthy(backend)
	
	backend.mu.Lock()
	previousHealth := backend.Healthy
	backend.LastCheck = time.Now()
	
	// Implement hysteresis to prevent flapping
	if healthy && !previousHealth {
		// Backend is responding, but was previously unhealthy
		// Require multiple successful checks before marking healthy
		backend.consecutiveSuccess++
		if backend.consecutiveSuccess >= hc.config.HealthyThreshold {
			backend.Healthy = true
			backend.consecutiveFailures = 0
			log.Printf("Backend %s marked as healthy after %d consecutive successes", 
				backend.ID, backend.consecutiveSuccess)
		}
	} else if !healthy && previousHealth {
		// Backend is not responding, but was previously healthy
		// Require multiple failures before marking unhealthy
		backend.consecutiveFailures++
		backend.consecutiveSuccess = 0
		if backend.consecutiveFailures >= hc.config.UnhealthyThreshold {
			backend.Healthy = false
			log.Printf("Backend %s marked as unhealthy after %d consecutive failures", 
				backend.ID, backend.consecutiveFailures)
		}
	} else if healthy && previousHealth {
		// Backend remains healthy
		backend.consecutiveSuccess++
		backend.consecutiveFailures = 0
	} else {
		// Backend remains unhealthy
		backend.consecutiveFailures++
		backend.consecutiveSuccess = 0
	}
	
	backend.mu.Unlock()
}

// isBackendHealthy checks if a backend is healthy
func (hc *HealthChecker) isBackendHealthy(backend *Backend) bool {
	url := fmt.Sprintf("http://%s:%d%s", backend.Host, backend.Port, hc.config.Path)
	
	ctx, cancel := context.WithTimeout(context.Background(), hc.config.Timeout)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("Failed to create health check request for %s: %v", backend.ID, err)
		return false
	}
	
	// Add headers that Moodle might expect
	req.Header.Set("User-Agent", "QUIC-HTTP3-LoadBalancer-HealthCheck/1.0")
	req.Header.Set("X-Health-Check", "true")
	
	client := &http.Client{
		Timeout: hc.config.Timeout,
	}
	
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Health check failed for backend %s: %v", backend.ID, err)
		return false
	}
	defer resp.Body.Close()
	
	// Consider 2xx and 3xx status codes as healthy
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	
	if !healthy {
		log.Printf("Backend %s health check returned status %d", backend.ID, resp.StatusCode)
	}
	
	return healthy
}

// Add these fields to Backend struct by updating the loadbalancer.go file
// We need to extend the Backend struct to include consecutive counters
func init() {
	// This will be handled by modifying the Backend struct in loadbalancer.go
}