package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config represents the server configuration
type Config struct {
	Server     ServerConfig    `json:"server"`
	LoadBalancer LoadBalancerConfig `json:"loadBalancer"`
	Moodle     MoodleConfig    `json:"moodle"`
	TLS        TLSConfig       `json:"tls"`
	Logging    LoggingConfig   `json:"logging"`
}

// ServerConfig contains basic server settings
type ServerConfig struct {
	HTTPPort      int           `json:"httpPort"`
	HTTPSPort     int           `json:"httpsPort"`
	ReadTimeout   time.Duration `json:"readTimeout"`
	WriteTimeout  time.Duration `json:"writeTimeout"`
	MaxIdleTime   time.Duration `json:"maxIdleTime"`
}

// LoadBalancerConfig contains load balancer settings
type LoadBalancerConfig struct {
	Strategy         string           `json:"strategy"` // round_robin, least_connections, ip_hash
	HealthCheck      HealthCheckConfig `json:"healthCheck"`
	BackendServers   []BackendServer  `json:"backendServers"`
	SessionPersistence bool           `json:"sessionPersistence"`
	ConnectionMigration bool          `json:"connectionMigration"`
}

// HealthCheckConfig contains health check settings
type HealthCheckConfig struct {
	Enabled        bool          `json:"enabled"`
	Interval       time.Duration `json:"interval"`
	Timeout        time.Duration `json:"timeout"`
	HealthyThreshold   int       `json:"healthyThreshold"`
	UnhealthyThreshold int       `json:"unhealthyThreshold"`
	Path           string        `json:"path"`
}

// BackendServer represents a backend Moodle server
type BackendServer struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Weight   int    `json:"weight"`
	Healthy  bool   `json:"healthy"`
	LastCheck time.Time `json:"lastCheck"`
}

// MoodleConfig contains Moodle-specific settings
type MoodleConfig struct {
	DataRoot        string            `json:"dataRoot"`
	WWWRoot         string            `json:"wwwRoot"`
	SessionCookie   string            `json:"sessionCookie"`
	ProxyHeaders    map[string]string `json:"proxyHeaders"`
	PHPFPMSocket    string            `json:"phpFpmSocket"`
	EnableCaching   bool              `json:"enableCaching"`
}

// TLSConfig contains TLS/QUIC settings
type TLSConfig struct {
	CertFile           string   `json:"certFile"`
	KeyFile            string   `json:"keyFile"`
	MinVersion         string   `json:"minVersion"`
	MaxVersion         string   `json:"maxVersion"`
	NextProtos         []string `json:"nextProtos"`
	EnableH3           bool     `json:"enableH3"`
	EnableH2           bool     `json:"enableH2"`
	EnableH1           bool     `json:"enableH1"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level           string `json:"level"`
	Format          string `json:"format"`
	EnableMetrics   bool   `json:"enableMetrics"`
	LogConnections  bool   `json:"logConnections"`
	LogMigrations   bool   `json:"logMigrations"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			HTTPPort:     8080,
			HTTPSPort:    9443,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			MaxIdleTime:  60 * time.Second,
		},
		LoadBalancer: LoadBalancerConfig{
			Strategy: "round_robin",
			HealthCheck: HealthCheckConfig{
				Enabled:            true,
				Interval:           30 * time.Second,
				Timeout:            5 * time.Second,
				HealthyThreshold:   2,
				UnhealthyThreshold: 3,
				Path:               "/admin/cli/healthcheck.php",
			},
			BackendServers: []BackendServer{
				{
					ID:     "moodle-1",
					Host:   "localhost",
					Port:   8081,
					Weight: 1,
					Healthy: true,
				},
			},
			SessionPersistence:  true,
			ConnectionMigration: true,
		},
		Moodle: MoodleConfig{
			DataRoot:      "/var/moodledata",
			WWWRoot:       "/var/www/moodle",
			SessionCookie: "MoodleSession",
			ProxyHeaders: map[string]string{
				"X-Forwarded-For":    "$remote_addr",
				"X-Forwarded-Proto":  "$scheme",
				"X-Forwarded-Host":   "$host",
				"X-Real-IP":          "$remote_addr",
			},
			PHPFPMSocket:  "/run/php/php8.2-fpm.sock",
			EnableCaching: true,
		},
		TLS: TLSConfig{
			CertFile:   "localhost+2.pem",
			KeyFile:    "localhost+2-key.pem",
			MinVersion: "1.2",
			MaxVersion: "1.3",
			NextProtos: []string{"h3", "h2", "http/1.1"},
			EnableH3:   true,
			EnableH2:   true,
			EnableH1:   true,
		},
		Logging: LoggingConfig{
			Level:          "info",
			Format:         "json",
			EnableMetrics:  true,
			LogConnections: true,
			LogMigrations:  true,
		},
	}
}

// LoadConfig loads configuration from a file or returns default config
func LoadConfig(filename string) (*Config, error) {
	config := DefaultConfig()
	
	if filename == "" {
		return config, nil
	}
	
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// If config file doesn't exist, save default config
			if err := config.Save(filename); err != nil {
				return nil, fmt.Errorf("failed to save default config: %w", err)
			}
			return config, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	
	return config, nil
}

// Save saves the configuration to a file
func (c *Config) Save(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	
	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.HTTPPort <= 0 || c.Server.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", c.Server.HTTPPort)
	}
	
	if c.Server.HTTPSPort <= 0 || c.Server.HTTPSPort > 65535 {
		return fmt.Errorf("invalid HTTPS port: %d", c.Server.HTTPSPort)
	}
	
	if len(c.LoadBalancer.BackendServers) == 0 {
		return fmt.Errorf("no backend servers configured")
	}
	
	for _, server := range c.LoadBalancer.BackendServers {
		if server.Host == "" {
			return fmt.Errorf("backend server host cannot be empty")
		}
		if server.Port <= 0 || server.Port > 65535 {
			return fmt.Errorf("invalid backend server port: %d", server.Port)
		}
	}
	
	return nil
}