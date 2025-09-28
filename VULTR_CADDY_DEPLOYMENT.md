# Vultr + Caddy QUIC/HTTP3 Deployment Guide

## Overview
This guide sets up a production-ready QUIC/HTTP3 load balancer using Caddy on Vultr VPS, with support for:
- IETF QUIC-LB Draft 20 compliance
- Connection migration
- Multiple backend instances
- Health monitoring
- Performance metrics

## Prerequisites

### 1. Vultr VPS Requirements
- **Instance**: High Frequency Compute (recommended for QUIC performance)
- **CPU**: 2+ vCPUs
- **RAM**: 4GB+ (for multiple backend instances)
- **Storage**: 80GB SSD
- **Network**: IPv4 + IPv6 enabled
- **OS**: Ubuntu 22.04 LTS

### 2. Domain Configuration
- Domain pointing to your Vultr VPS IP
- DNS A record: `your-domain.com` → `VPS_IP`
- DNS AAAA record: `your-domain.com` → `VPS_IPv6` (optional)

## Step 1: Vultr VPS Setup

### 1.1 Create Vultr Instance
```bash
# Choose High Frequency plan for optimal QUIC performance
# Recommended: 2 vCPU, 4GB RAM, 80GB SSD
# Location: Choose closest to your target audience
```

### 1.2 Initial Server Setup
```bash
# Connect to your VPS
ssh root@your-vps-ip

# Update system
apt update && apt upgrade -y

# Install essential packages
apt install -y curl wget git htop ufw fail2ban

# Configure firewall
ufw allow 22    # SSH
ufw allow 80    # HTTP
ufw allow 443   # HTTPS/QUIC
ufw allow 8443  # Testing port
ufw --force enable
```

## Step 2: Install Dependencies

### 2.1 Install Docker
```bash
# Install Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sh get-docker.sh

# Install Docker Compose
curl -L "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
chmod +x /usr/local/bin/docker-compose

# Start Docker
systemctl enable docker
systemctl start docker
```

### 2.2 Install Caddy
```bash
# Install Caddy with QUIC support
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update
apt install caddy
```

### 2.3 Install Go (for building QUIC server)
```bash
# Install Go 1.21+
wget https://go.dev/dl/go1.21.5.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.21.5.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
```

## Step 3: Deploy QUIC Server

### 3.1 Clone and Build
```bash
# Create project directory
mkdir -p /opt/quic-lb
cd /opt/quic-lb

# Clone your project (replace with your repo)
git clone https://github.com/sereyr1th/quic_.git .

# Build the QUIC server
go mod tidy
go build -o quic-server main.go metrics.go quic_optimizations.go
```

### 3.2 Create Multiple Instances
```bash
# Create systemd services for multiple backend instances
# Instance 1 (Port 8080)
cat > /etc/systemd/system/quic-server-1.service << 'EOF'
[Unit]
Description=QUIC Load Balancer Server Instance 1
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/quic-lb
Environment=PORT=8080
Environment=INSTANCE_ID=1
ExecStart=/opt/quic-lb/quic-server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Instance 2 (Port 8081)
cat > /etc/systemd/system/quic-server-2.service << 'EOF'
[Unit]
Description=QUIC Load Balancer Server Instance 2
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/quic-lb
Environment=PORT=8081
Environment=INSTANCE_ID=2
ExecStart=/opt/quic-lb/quic-server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Instance 3 (Port 8082)
cat > /etc/systemd/system/quic-server-3.service << 'EOF'
[Unit]
Description=QUIC Load Balancer Server Instance 3
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/quic-lb
Environment=PORT=8082
Environment=INSTANCE_ID=3
ExecStart=/opt/quic-lb/quic-server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Enable and start services
systemctl daemon-reload
systemctl enable quic-server-1 quic-server-2 quic-server-3
systemctl start quic-server-1 quic-server-2 quic-server-3
```

## Step 4: Configure Caddy

### 4.1 Setup Caddyfile
```bash
# Copy the Caddyfile to Caddy's config directory
cp /opt/quic-lb/Caddyfile /etc/caddy/Caddyfile

# Update domain in Caddyfile
sed -i 's/your-domain.com/YOUR_ACTUAL_DOMAIN/g' /etc/caddy/Caddyfile

# Create log directory
mkdir -p /var/log/caddy
chown caddy:caddy /var/log/caddy
```

### 4.2 Test and Start Caddy
```bash
# Test configuration
caddy validate --config /etc/caddy/Caddyfile

# Start Caddy
systemctl enable caddy
systemctl start caddy
systemctl status caddy
```

## Step 5: Monitoring Setup

### 5.1 Deploy Monitoring Stack
```bash
# Start monitoring services
cd /opt/quic-lb
docker-compose up -d prometheus grafana

# Import Grafana dashboards
# Access: http://your-domain.com:3000
# Default: admin/admin
```

## Step 6: Testing Connection Migration

### 6.1 QUIC Client Testing
```bash
# Test HTTP/3 connectivity
curl --http3 https://your-domain.com/

# Test with multiple requests to verify load balancing
for i in {1..10}; do
  curl --http3 -s https://your-domain.com/api/status | jq .instance_id
done
```

### 6.2 Connection Migration Test
```bash
# Test script for connection migration
cat > test-migration.sh << 'EOF'
#!/bin/bash
echo "Testing QUIC Connection Migration..."

# Start long-running connection
curl --http3 --connect-timeout 30 --max-time 300 \
     -H "Connection: keep-alive" \
     https://your-domain.com/stream &

# Simulate network change (restart one backend)
sleep 10
systemctl restart quic-server-2

# Connection should migrate automatically
wait
echo "Migration test completed"
EOF

chmod +x test-migration.sh
./test-migration.sh
```

## Step 7: Performance Optimization

### 7.1 System Tuning
```bash
# Optimize for QUIC/UDP performance
cat >> /etc/sysctl.conf << 'EOF'
# QUIC/UDP optimizations
net.core.rmem_default = 262144
net.core.rmem_max = 16777216
net.core.wmem_default = 262144
net.core.wmem_max = 16777216
net.core.netdev_max_backlog = 5000
net.ipv4.udp_mem = 102400 873800 16777216
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192
EOF

sysctl -p
```

### 7.2 Caddy Optimization
```bash
# Optimize Caddy for QUIC performance
cat > /etc/caddy/caddy.env << 'EOF'
CADDY_EXPERIMENTAL_HTTP3=1
CADDY_MAX_CONNECTIONS=10000
CADDY_READ_TIMEOUT=30s
CADDY_WRITE_TIMEOUT=30s
EOF
```

## Step 8: SSL/TLS Certificates

### 8.1 Automatic HTTPS with Let's Encrypt
```bash
# Caddy automatically handles SSL certificates
# Verify certificate installation
curl -I https://your-domain.com/

# Check QUIC/HTTP3 support
curl --http3-only -I https://your-domain.com/
```

## Monitoring and Maintenance

### 9.1 Log Monitoring
```bash
# Monitor Caddy logs
tail -f /var/log/caddy/quic-lb.log

# Monitor QUIC server logs
journalctl -u quic-server-1 -f

# System monitoring
htop
```

### 9.2 Performance Metrics
- **Grafana Dashboard**: `http://your-domain.com:3000`
- **Prometheus Metrics**: `http://your-domain.com:9090`
- **QUIC Metrics**: `https://your-domain.com/metrics`

## Troubleshooting

### Common Issues:
1. **QUIC not working**: Check UDP port 443 is open
2. **Connection migration fails**: Verify backend health checks
3. **SSL certificate issues**: Check domain DNS resolution
4. **Performance problems**: Review system resource usage

### Debug Commands:
```bash
# Check service status
systemctl status caddy quic-server-*

# Test QUIC connectivity
curl --http3 -v https://your-domain.com/

# Check logs
journalctl -u caddy -f
```

## Expected Performance

With this setup on Vultr, you should achieve:
- **Latency**: <50ms within same region
- **Throughput**: 1-10 Gbps depending on VPS plan
- **Connection Migration**: <100ms failover time
- **Concurrent Connections**: 10,000+ with proper tuning

## Cost Estimation

**Vultr VPS (High Frequency)**:
- 2 vCPU, 4GB RAM: ~$24/month
- 4 vCPU, 8GB RAM: ~$48/month

**Additional Costs**:
- Domain: ~$10-15/year
- Monitoring (optional): $0 (self-hosted)

## Next Steps

1. Deploy the infrastructure following this guide
2. Run comprehensive load tests
3. Monitor performance metrics
4. Test connection migration scenarios
5. Scale horizontally by adding more backend instances

This setup provides enterprise-grade QUIC/HTTP3 load balancing with excellent performance and reliability on Vultr's infrastructure.
