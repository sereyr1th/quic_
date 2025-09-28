#!/bin/bash

# Vultr Deployment Script for QUIC/HTTP3 Load Balancer
# Domain: moodle-itc.duckdns.org

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
DOMAIN="moodle-itc.duckdns.org"
PROJECT_DIR="/opt/quic-lb"
GITHUB_REPO="https://github.com/sereyr1th/quic_.git"

print_banner() {
    echo -e "${BLUE}"
    echo "╔══════════════════════════════════════════════╗"
    echo "║        Vultr QUIC/HTTP3 Deployment          ║"
    echo "║         Domain: moodle-itc.duckdns.org      ║"
    echo "╚══════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_status() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "This script must be run as root"
        exit 1
    fi
    print_status "Running as root"
}

# Update system
update_system() {
    print_info "Updating system packages..."
    apt update && apt upgrade -y
    apt install -y curl wget git htop ufw fail2ban jq
    print_status "System updated"
}

# Configure firewall
setup_firewall() {
    print_info "Configuring firewall..."
    
    # Reset UFW to defaults
    ufw --force reset
    
    # Set default policies
    ufw default deny incoming
    ufw default allow outgoing
    
    # Allow SSH (be careful!)
    ufw allow 22/tcp
    
    # Allow HTTP and HTTPS/QUIC
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw allow 443/udp  # For QUIC
    
    # Allow testing port
    ufw allow 8443/tcp
    ufw allow 8443/udp
    
    # Allow monitoring ports (restrict later if needed)
    ufw allow 3000/tcp  # Grafana
    ufw allow 9090/tcp  # Prometheus
    
    # Enable firewall
    ufw --force enable
    
    print_status "Firewall configured"
}

# Install Docker
install_docker() {
    print_info "Installing Docker..."
    
    if command -v docker >/dev/null 2>&1; then
        print_status "Docker already installed"
        return
    fi
    
    # Install Docker
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
    
    # Install Docker Compose V2
    mkdir -p /usr/local/lib/docker/cli-plugins
    curl -SL "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64" \
         -o /usr/local/lib/docker/cli-plugins/docker-compose
    chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
    
    # Start and enable Docker
    systemctl enable docker
    systemctl start docker
    
    print_status "Docker installed and started"
}

# Install Caddy
install_caddy() {
    print_info "Installing Caddy with HTTP/3 support..."
    
    if command -v caddy >/dev/null 2>&1; then
        print_status "Caddy already installed"
        return
    fi
    
    # Install Caddy
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | \
        gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | \
        tee /etc/apt/sources.list.d/caddy-stable.list
    
    apt update
    apt install -y caddy
    
    # Verify HTTP/3 support
    if caddy version | grep -q "http.handlers.reverse_proxy"; then
        print_status "Caddy with HTTP/3 support installed"
    else
        print_warning "Caddy installed but HTTP/3 support uncertain"
    fi
}

# Install Go
install_go() {
    print_info "Installing Go..."
    
    if command -v go >/dev/null 2>&1; then
        print_status "Go already installed"
        return
    fi
    
    # Download and install Go
    GO_VERSION="1.21.5"
    wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
    tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
    rm "go${GO_VERSION}.linux-amd64.tar.gz"
    
    # Add to PATH
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    export PATH=$PATH:/usr/local/go/bin
    
    print_status "Go installed"
}

# Deploy application
deploy_application() {
    print_info "Deploying QUIC application..."
    
    # Create project directory
    mkdir -p $PROJECT_DIR
    cd $PROJECT_DIR
    
    # Clone repository (you'll need to push your changes first)
    if [ -d ".git" ]; then
        print_info "Updating existing repository..."
        git pull origin main
    else
        print_info "Cloning repository..."
        git clone $GITHUB_REPO .
    fi
    
    # Build the application
    print_info "Building QUIC server..."
    /usr/local/go/bin/go mod tidy
    /usr/local/go/bin/go build -o quic-server main.go metrics.go quic_optimizations.go
    
    # Make scripts executable
    chmod +x *.sh
    
    print_status "Application deployed and built"
}

# Setup systemd services
setup_services() {
    print_info "Setting up systemd services..."
    
    # QUIC Server Instance 1
    cat > /etc/systemd/system/quic-server-1.service << EOF
[Unit]
Description=QUIC Load Balancer Server Instance 1
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$PROJECT_DIR
Environment=PORT=8080
Environment=INSTANCE_ID=1
Environment=LOG_LEVEL=INFO
ExecStart=$PROJECT_DIR/quic-server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    # QUIC Server Instance 2
    cat > /etc/systemd/system/quic-server-2.service << EOF
[Unit]
Description=QUIC Load Balancer Server Instance 2
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$PROJECT_DIR
Environment=PORT=8081
Environment=INSTANCE_ID=2
Environment=LOG_LEVEL=INFO
ExecStart=$PROJECT_DIR/quic-server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    # QUIC Server Instance 3
    cat > /etc/systemd/system/quic-server-3.service << EOF
[Unit]
Description=QUIC Load Balancer Server Instance 3
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$PROJECT_DIR
Environment=PORT=8082
Environment=INSTANCE_ID=3
Environment=LOG_LEVEL=INFO
ExecStart=$PROJECT_DIR/quic-server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    # Reload systemd and enable services
    systemctl daemon-reload
    systemctl enable quic-server-1 quic-server-2 quic-server-3
    
    print_status "Systemd services configured"
}

# Configure Caddy
setup_caddy() {
    print_info "Configuring Caddy..."
    
    # Copy Caddyfile
    cp $PROJECT_DIR/Caddyfile /etc/caddy/Caddyfile
    
    # Create log directory
    mkdir -p /var/log/caddy
    chown caddy:caddy /var/log/caddy
    
    # Test configuration
    if caddy validate --config /etc/caddy/Caddyfile; then
        print_status "Caddy configuration valid"
    else
        print_error "Caddy configuration validation failed"
        exit 1
    fi
    
    # Enable Caddy service
    systemctl enable caddy
    
    print_status "Caddy configured"
}

# Optimize system for QUIC
optimize_system() {
    print_info "Optimizing system for QUIC performance..."
    
    # UDP buffer optimizations
    cat >> /etc/sysctl.conf << EOF

# QUIC/UDP optimizations
net.core.rmem_default = 262144
net.core.rmem_max = 16777216
net.core.wmem_default = 262144
net.core.wmem_max = 16777216
net.core.netdev_max_backlog = 5000
net.ipv4.udp_mem = 102400 873800 16777216
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192

# General network optimizations
net.ipv4.tcp_congestion_control = bbr
net.core.default_qdisc = fq
EOF

    # Apply changes
    sysctl -p
    
    print_status "System optimized for QUIC"
}

# Start services
start_services() {
    print_info "Starting all services..."
    
    # Start QUIC servers
    systemctl start quic-server-1 quic-server-2 quic-server-3
    
    # Wait a moment for servers to start
    sleep 5
    
    # Check QUIC servers
    local running_servers=0
    for i in {1..3}; do
        if systemctl is-active --quiet quic-server-$i; then
            running_servers=$((running_servers + 1))
            print_status "QUIC server $i is running"
        else
            print_error "QUIC server $i failed to start"
            journalctl -u quic-server-$i -n 20 --no-pager
        fi
    done
    
    if [ $running_servers -eq 0 ]; then
        print_error "No QUIC servers started successfully"
        exit 1
    fi
    
    # Start Caddy
    systemctl start caddy
    
    if systemctl is-active --quiet caddy; then
        print_status "Caddy is running"
    else
        print_error "Caddy failed to start"
        journalctl -u caddy -n 20 --no-pager
        exit 1
    fi
    
    print_status "All services started successfully"
}

# Setup monitoring
setup_monitoring() {
    print_info "Setting up monitoring stack..."
    
    cd $PROJECT_DIR
    
    # Start monitoring with Docker Compose
    if [ -f "docker-compose.yml" ]; then
        docker compose up -d prometheus grafana
        sleep 10
        
        if docker compose ps | grep -q "prometheus.*Up" && docker compose ps | grep -q "grafana.*Up"; then
            print_status "Monitoring stack started"
            print_info "Grafana: http://$DOMAIN:3000 (admin/admin)"
            print_info "Prometheus: http://$DOMAIN:9090"
        else
            print_warning "Monitoring stack may have issues"
        fi
    else
        print_warning "docker-compose.yml not found, skipping monitoring setup"
    fi
}

# Test deployment
test_deployment() {
    print_info "Testing deployment..."
    
    # Wait for SSL certificate
    print_info "Waiting for SSL certificate (this may take a minute)..."
    sleep 30
    
    # Test HTTPS connectivity
    if curl -s --connect-timeout 10 "https://$DOMAIN/health" >/dev/null; then
        print_status "HTTPS connectivity working"
    else
        print_warning "HTTPS connectivity failed, checking HTTP..."
        if curl -s --connect-timeout 10 "http://$DOMAIN/health" >/dev/null; then
            print_warning "HTTP working but HTTPS failed - SSL certificate may still be provisioning"
        else
            print_error "Both HTTP and HTTPS failed"
        fi
    fi
    
    # Test load balancing
    print_info "Testing load balancing..."
    local instances=()
    for i in {1..10}; do
        response=$(curl -s --connect-timeout 5 "https://$DOMAIN/api/status" 2>/dev/null || curl -s --connect-timeout 5 "http://$DOMAIN/api/status" 2>/dev/null)
        if [ $? -eq 0 ] && [ -n "$response" ]; then
            instance_id=$(echo "$response" | jq -r '.instance_id // .server_id // "unknown"' 2>/dev/null || echo "unknown")
            instances+=("$instance_id")
        fi
        sleep 0.2
    done
    
    unique_instances=$(printf '%s\n' "${instances[@]}" | sort -u | wc -l)
    if [ "$unique_instances" -gt 1 ]; then
        print_status "Load balancing working: $unique_instances backend instances detected"
    else
        print_warning "Only 1 backend instance detected, load balancing may need time to initialize"
    fi
}

# Show deployment summary
show_summary() {
    echo
    print_info "🚀 Deployment Complete!"
    echo
    echo "🌐 Main endpoint: https://$DOMAIN"
    echo "📊 Health check:  https://$DOMAIN/health"
    echo "📈 Status API:    https://$DOMAIN/api/status"
    echo "🔧 Metrics:       https://$DOMAIN/metrics"
    echo
    echo "📋 Backend instances:"
    echo "   - Instance 1: localhost:8080"
    echo "   - Instance 2: localhost:8081"
    echo "   - Instance 3: localhost:8082"
    echo
    echo "📊 Monitoring:"
    echo "   - Grafana:    http://$DOMAIN:3000 (admin/admin)"
    echo "   - Prometheus: http://$DOMAIN:9090"
    echo
    echo "🔍 Service management:"
    echo "   systemctl status caddy"
    echo "   systemctl status quic-server-{1,2,3}"
    echo
    echo "📝 Logs:"
    echo "   journalctl -u caddy -f"
    echo "   journalctl -u quic-server-1 -f"
    echo
    echo "🧪 Test commands:"
    echo "   curl https://$DOMAIN/health"
    echo "   $PROJECT_DIR/test-connection-migration.sh $DOMAIN"
    echo
    print_status "Your QUIC/HTTP3 load balancer is ready!"
}

# Main deployment function
main() {
    print_banner
    
    check_root
    update_system
    setup_firewall
    install_docker
    install_caddy
    install_go
    deploy_application
    setup_services
    setup_caddy
    optimize_system
    start_services
    setup_monitoring
    test_deployment
    show_summary
}

# Run main function
main "$@"
