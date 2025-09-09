#!/bin/bash

# QUIC Load Balancer Deployment Script
# This script helps deploy the QUIC/HTTP3 load balancer for Moodle

set -e

# Configuration
INSTALL_DIR="/opt/quic-loadbalancer"
CONFIG_DIR="/etc/quic-loadbalancer"
LOG_DIR="/var/log/quic-loadbalancer"
USER="loadbalancer"
GROUP="loadbalancer"
SERVICE_NAME="quic-loadbalancer"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print functions
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   print_error "This script must be run as root"
   exit 1
fi

print_info "Starting QUIC Load Balancer deployment..."

# Create user and group
if ! id "$USER" &>/dev/null; then
    print_info "Creating user $USER..."
    useradd --system --shell /bin/false --home-dir "$INSTALL_DIR" --create-home "$USER"
else
    print_info "User $USER already exists"
fi

# Create directories
print_info "Creating directories..."
mkdir -p "$INSTALL_DIR"
mkdir -p "$CONFIG_DIR"
mkdir -p "$LOG_DIR"

# Set permissions
chown -R "$USER:$GROUP" "$INSTALL_DIR"
chown -R "$USER:$GROUP" "$LOG_DIR"
chmod 755 "$CONFIG_DIR"

# Copy binary
if [[ -f "./quic-server" ]]; then
    print_info "Installing QUIC server binary..."
    cp ./quic-server "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/quic-server"
    chown "$USER:$GROUP" "$INSTALL_DIR/quic-server"
else
    print_error "quic-server binary not found. Please build it first with: go build -o quic-server ."
    exit 1
fi

# Copy configuration files
if [[ -f "./examples/production-config.json" ]]; then
    print_info "Installing configuration..."
    cp ./examples/production-config.json "$CONFIG_DIR/config.json"
    chown root:root "$CONFIG_DIR/config.json"
    chmod 644 "$CONFIG_DIR/config.json"
else
    print_warning "Production config not found, generating default..."
    "$INSTALL_DIR/quic-server" --generate-config
    mv config.json "$CONFIG_DIR/"
    chown root:root "$CONFIG_DIR/config.json"
    chmod 644 "$CONFIG_DIR/config.json"
fi

# Install systemd service
if [[ -f "./examples/quic-loadbalancer.service" ]]; then
    print_info "Installing systemd service..."
    cp ./examples/quic-loadbalancer.service /etc/systemd/system/
    systemctl daemon-reload
else
    print_error "Service file not found"
    exit 1
fi

# Configure firewall (if ufw is available)
if command -v ufw &> /dev/null; then
    print_info "Configuring firewall..."
    ufw allow 80/tcp comment "HTTP"
    ufw allow 443/tcp comment "HTTPS"
    ufw allow 9443/tcp comment "QUIC HTTPS"
    ufw allow 9443/udp comment "QUIC HTTP/3"
fi

# Optimize system settings
print_info "Optimizing system settings..."

# UDP buffer sizes
cat >> /etc/sysctl.conf << EOF

# QUIC Load Balancer optimizations
net.core.rmem_max = 2500000
net.core.rmem_default = 2500000
net.core.wmem_max = 2500000
net.core.wmem_default = 2500000
net.core.netdev_max_backlog = 5000
EOF

# Apply sysctl settings
sysctl -p

# File descriptor limits
cat >> /etc/security/limits.conf << EOF

# QUIC Load Balancer limits
$USER soft nofile 65536
$USER hard nofile 65536
$USER soft nproc 32768
$USER hard nproc 32768
EOF

# Enable and start service
print_info "Enabling and starting service..."
systemctl enable "$SERVICE_NAME"

# Check configuration before starting
print_info "Validating configuration..."
if sudo -u "$USER" "$INSTALL_DIR/quic-server" -config "$CONFIG_DIR/config.json" -validate 2>/dev/null; then
    print_info "Configuration is valid"
else
    print_warning "Configuration validation failed, but continuing anyway"
fi

# Start the service
if systemctl start "$SERVICE_NAME"; then
    print_info "Service started successfully"
else
    print_error "Failed to start service"
    exit 1
fi

# Check service status
sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
    print_info "Service is running"
    systemctl status "$SERVICE_NAME" --no-pager
else
    print_error "Service failed to start"
    journalctl -u "$SERVICE_NAME" --no-pager -l
    exit 1
fi

print_info "Deployment completed successfully!"
echo
print_info "Next steps:"
echo "1. Edit $CONFIG_DIR/config.json to configure your Moodle backends"
echo "2. Install SSL certificates and update TLS configuration"
echo "3. Configure your Moodle servers to handle proxy headers"
echo "4. Test the load balancer with: curl -k https://localhost:9443/health"
echo "5. Monitor logs with: journalctl -f -u $SERVICE_NAME"
echo
print_info "Documentation available at: $INSTALL_DIR/README.md"