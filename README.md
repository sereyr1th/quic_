# QUIC/HTTP3 Load Balancer for Moodle Setup Guide

This guide explains how to set up QUIC/HTTP3 protocol with connection migration and load balancing for Moodle.

## Features

✅ **QUIC/HTTP3 Protocol**: Full HTTP/3 support with automatic fallback to HTTP/2 and HTTP/1.1  
✅ **Connection Migration**: QUIC connections can migrate between networks seamlessly  
✅ **Load Balancing**: Multiple load balancing strategies (round-robin, least connections, IP hash)  
✅ **Health Checks**: Automatic backend server health monitoring  
✅ **Session Persistence**: Maintain user sessions across backend servers  
✅ **High Availability**: Automatic failover and recovery  

## Quick Start

### 1. Generate Configuration
```bash
./quic-server --generate-config
```

This creates a `config.json` file with default settings.

### 2. Configure Backend Servers
Edit `config.json` to point to your Moodle servers:

```json
{
  "loadBalancer": {
    "backendServers": [
      {
        "id": "moodle-1",
        "host": "moodle1.example.com",
        "port": 80,
        "weight": 1,
        "healthy": true
      },
      {
        "id": "moodle-2", 
        "host": "moodle2.example.com",
        "port": 80,
        "weight": 1,
        "healthy": true
      }
    ]
  }
}
```

### 3. Start the Load Balancer
```bash
./quic-server -config config.json
```

## Configuration Options

### Server Settings
- `httpPort`: Port for HTTP/1.1 server (default: 8080)
- `httpsPort`: Port for HTTPS/HTTP2/HTTP3 server (default: 9443)
- `readTimeout`: Request read timeout
- `writeTimeout`: Response write timeout

### Load Balancer Settings
- `strategy`: Load balancing strategy
  - `round_robin`: Distribute requests evenly across backends
  - `least_connections`: Route to backend with fewest active connections
  - `ip_hash`: Consistent routing based on client IP
- `sessionPersistence`: Enable sticky sessions (recommended for Moodle)
- `connectionMigration`: Enable QUIC connection migration

### Health Check Settings
- `enabled`: Enable/disable health checks
- `interval`: How often to check backend health
- `timeout`: Health check request timeout
- `path`: Health check endpoint on Moodle servers
- `healthyThreshold`: Successful checks needed to mark as healthy
- `unhealthyThreshold`: Failed checks needed to mark as unhealthy

### Moodle-Specific Settings
- `dataRoot`: Path to Moodle data directory
- `wwwRoot`: Path to Moodle web directory
- `sessionCookie`: Name of Moodle session cookie
- `proxyHeaders`: Headers to pass to backend servers
- `phpFpmSocket`: PHP-FPM socket path
- `enableCaching`: Enable response caching

## Moodle Integration

### 1. Configure Moodle Health Check
Create `/admin/cli/healthcheck.php` in your Moodle installation:

```php
<?php
define('CLI_SCRIPT', true);
require_once('../../config.php');

// Simple health check
$status = 'healthy';
$checks = [];

// Check database connection
try {
    $DB->get_record_sql('SELECT 1');
    $checks['database'] = 'ok';
} catch (Exception $e) {
    $checks['database'] = 'error';
    $status = 'unhealthy';
}

// Check data directory
if (is_writable($CFG->dataroot)) {
    $checks['dataroot'] = 'ok';
} else {
    $checks['dataroot'] = 'error';
    $status = 'unhealthy';
}

// Return JSON response
header('Content-Type: application/json');
http_response_code($status === 'healthy' ? 200 : 503);
echo json_encode([
    'status' => $status,
    'checks' => $checks,
    'timestamp' => time()
]);
?>
```

### 2. Update Moodle config.php
Add these settings to your Moodle `config.php`:

```php
// Trust the load balancer proxy
$CFG->reverseproxy = true;
$CFG->sslproxy = true;

// Set the correct base URL (load balancer URL)
$CFG->wwwroot = 'https://your-domain.com';

// Configure session handling for load balancing
$CFG->session_handler_class = '\core\session\database';
$CFG->sessioncookiesecure = true;
$CFG->sessioncookiehttponly = true;

// Enable caching for better performance
$CFG->cachejs = true;
$CFG->cachedir = $CFG->dataroot . '/cache';
```

### 3. Configure Web Server (Apache/Nginx)
Make sure your Moodle web servers are configured to handle proxy headers:

#### Apache
```apache
<VirtualHost *:80>
    ServerName moodle1.example.com
    DocumentRoot /var/www/moodle
    
    # Trust proxy headers from load balancer
    RemoteIPHeader X-Forwarded-For
    RemoteIPInternalProxy 127.0.0.1
    
    # PHP configuration
    <FilesMatch \.php$>
        SetHandler "proxy:unix:/run/php/php8.2-fpm.sock|fcgi://localhost"
    </FilesMatch>
</VirtualHost>
```

#### Nginx
```nginx
server {
    listen 80;
    server_name moodle1.example.com;
    root /var/www/moodle;
    
    # Trust proxy headers
    real_ip_header X-Forwarded-For;
    set_real_ip_from 127.0.0.1;
    
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php8.2-fpm.sock;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        
        # Pass proxy headers to PHP
        fastcgi_param HTTP_X_FORWARDED_FOR $http_x_forwarded_for;
        fastcgi_param HTTP_X_FORWARDED_PROTO $http_x_forwarded_proto;
    }
}
```

## SSL/TLS Configuration

### 1. Generate Certificates
For production, obtain SSL certificates from a trusted CA:

```bash
# Using Let's Encrypt
certbot certonly --standalone -d your-domain.com

# Copy certificates
cp /etc/letsencrypt/live/your-domain.com/fullchain.pem ./cert.pem
cp /etc/letsencrypt/live/your-domain.com/privkey.pem ./key.pem
```

### 2. Update Configuration
Update `config.json` with your certificate paths:

```json
{
  "tls": {
    "certFile": "cert.pem",
    "keyFile": "key.pem"
  }
}
```

## Testing

### 1. Test Endpoints
```bash
# Health check
curl -k https://localhost:9443/health

# Load balancer statistics
curl -k https://localhost:9443/stats

# Test API endpoint
curl -k https://localhost:9443/api/test
```

### 2. Test HTTP/3
```bash
# Test HTTP/3 specifically (requires curl with HTTP/3 support)
curl -v --http3-only -k https://localhost:9443/api/test

# Test HTTP/2 fallback
curl -v --http2 -k https://localhost:9443/api/test

# Test HTTP/1.1 fallback
curl -v --http1.1 -k https://localhost:9443/api/test
```

### 3. Test Connection Migration
Connection migration happens automatically when clients change networks (e.g., WiFi to mobile). The QUIC protocol handles this transparently.

## Monitoring

### 1. Load Balancer Statistics
Access real-time statistics at: `https://your-domain.com/stats`

### 2. Health Check Status
Monitor backend health at: `https://your-domain.com/health`

### 3. Log Analysis
The load balancer logs show:
- Protocol used (HTTP/1.1, HTTP/2, HTTP/3 🚀)
- Backend server selection
- Connection migration events
- Health check results

## Troubleshooting

### Common Issues

1. **Backend servers marked as unhealthy**
   - Check if health check endpoint is accessible
   - Verify health check path in configuration
   - Check backend server logs

2. **HTTP/3 not working**
   - Verify UDP port 9443 is open
   - Check client browser supports HTTP/3
   - Review UDP buffer size warnings

3. **Session persistence issues**
   - Verify session cookie configuration
   - Check Moodle session handler settings
   - Review proxy header configuration

4. **Connection migration not working**
   - Ensure `connectionMigration` is enabled
   - Check QUIC connection tracking in logs
   - Verify client supports QUIC migration

### Debug Commands
```bash
# Check if HTTP/3 is working
curl -v --http3-only -k https://localhost:9443/health

# Monitor load balancer logs
tail -f /var/log/quic-loadbalancer.log

# Check backend connectivity
curl -v http://moodle1.example.com/admin/cli/healthcheck.php
```

## Production Deployment

### 1. Systemd Service
Create `/etc/systemd/system/quic-loadbalancer.service`:

```ini
[Unit]
Description=QUIC HTTP/3 Load Balancer for Moodle
After=network.target

[Service]
Type=simple
User=loadbalancer
Group=loadbalancer
WorkingDirectory=/opt/quic-loadbalancer
ExecStart=/opt/quic-loadbalancer/quic-server -config /etc/quic-loadbalancer/config.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 2. Start Service
```bash
sudo systemctl daemon-reload
sudo systemctl enable quic-loadbalancer
sudo systemctl start quic-loadbalancer
```

### 3. Firewall Configuration
```bash
# Allow HTTP/HTTPS traffic
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 9443/tcp
sudo ufw allow 9443/udp  # For HTTP/3
```

## Performance Optimization

### 1. UDP Buffer Sizes
If you see buffer size warnings, increase UDP buffers:

```bash
# Temporary
sudo sysctl -w net.core.rmem_max=2500000
sudo sysctl -w net.core.rmem_default=2500000

# Permanent
echo 'net.core.rmem_max = 2500000' >> /etc/sysctl.conf
echo 'net.core.rmem_default = 2500000' >> /etc/sysctl.conf
```

### 2. Connection Limits
Increase file descriptor limits:

```bash
# /etc/security/limits.conf
loadbalancer soft nofile 65536
loadbalancer hard nofile 65536
```

### 3. CPU Affinity
For high-traffic sites, consider CPU affinity:

```bash
taskset -c 0,1 ./quic-server -config config.json
```

## Security Considerations

1. **Rate Limiting**: Consider implementing rate limiting for production
2. **DDoS Protection**: Use CloudFlare or similar services
3. **Certificate Management**: Automate certificate renewal
4. **Access Control**: Restrict access to management endpoints
5. **Monitoring**: Set up alerting for health check failures

## Support

For issues and questions:
- Check the logs for error messages
- Review the configuration file
- Test individual components (backend servers, health checks)
- Monitor load balancer statistics

This implementation provides enterprise-grade QUIC/HTTP3 load balancing specifically designed for Moodle's requirements.