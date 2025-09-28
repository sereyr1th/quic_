#!/bin/bash

# Quick local testing setup for Caddy + QUIC
# This script sets up a local testing environment before deploying to Vultr

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

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

# Check if Caddy is installed
check_caddy() {
    if ! command -v caddy >/dev/null 2>&1; then
        print_info "Installing Caddy locally..."
        
        # Install Caddy on different systems
        if [[ "$OSTYPE" == "linux-gnu"* ]]; then
            curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
            curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
            sudo apt update && sudo apt install -y caddy
        elif [[ "$OSTYPE" == "darwin"* ]]; then
            if command -v brew >/dev/null 2>&1; then
                brew install caddy
            else
                print_error "Please install Homebrew first or install Caddy manually"
                exit 1
            fi
        else
            print_error "Please install Caddy manually for your system"
            exit 1
        fi
    fi
    
    print_status "Caddy is available"
}

# Build QUIC server instances
build_server() {
    print_info "Building QUIC server..."
    
    if [ ! -f "main.go" ]; then
        print_error "main.go not found in current directory"
        exit 1
    fi
    
    go build -o quic-server main.go metrics.go quic_optimizations.go
    print_status "QUIC server built successfully"
}

# Start multiple QUIC server instances
start_servers() {
    print_info "Starting QUIC server instances..."
    
    # Kill any existing instances
    pkill -f "quic-server" 2>/dev/null || true
    sleep 2
    
    # Start instance 1 on port 8080
    PORT=8080 INSTANCE_ID=1 ./quic-server > /tmp/quic-server-1.log 2>&1 &
    echo $! > /tmp/quic-server-1.pid
    
    # Start instance 2 on port 8081
    PORT=8081 INSTANCE_ID=2 ./quic-server > /tmp/quic-server-2.log 2>&1 &
    echo $! > /tmp/quic-server-2.pid
    
    # Start instance 3 on port 8082
    PORT=8082 INSTANCE_ID=3 ./quic-server > /tmp/quic-server-3.log 2>&1 &
    echo $! > /tmp/quic-server-3.pid
    
    sleep 3
    
    # Check if servers are running
    local running_count=0
    for port in 8080 8081 8082; do
        if curl -s --connect-timeout 2 "http://localhost:$port/health" >/dev/null 2>&1; then
            running_count=$((running_count + 1))
            print_status "QUIC server instance on port $port is running"
        else
            print_warning "QUIC server instance on port $port failed to start"
        fi
    done
    
    if [ $running_count -eq 0 ]; then
        print_error "No QUIC server instances started successfully"
        exit 1
    fi
    
    print_status "$running_count QUIC server instances started"
}

# Start Caddy with local configuration
start_caddy() {
    print_info "Starting Caddy with QUIC/HTTP3 configuration..."
    
    # Kill any existing Caddy instance
    pkill caddy 2>/dev/null || true
    sleep 2
    
    # Start Caddy in background
    caddy run --config Caddyfile > /tmp/caddy.log 2>&1 &
    echo $! > /tmp/caddy.pid
    
    sleep 5
    
    # Check if Caddy started successfully
    if curl -k -s --connect-timeout 5 "https://localhost:8443/health" >/dev/null 2>&1; then
        print_status "Caddy started successfully on https://localhost:8443"
    else
        print_error "Caddy failed to start. Check /tmp/caddy.log for details"
        cat /tmp/caddy.log
        exit 1
    fi
}

# Test the setup
test_setup() {
    print_info "Testing local QUIC/HTTP3 setup..."
    
    # Test basic connectivity
    if curl -k --http2 -s "https://localhost:8443/health" >/dev/null 2>&1; then
        print_status "HTTP/2 connectivity working"
    else
        print_warning "HTTP/2 connectivity failed"
    fi
    
    # Test HTTP/3 if supported
    if curl --help | grep -q "http3" && curl -k --http3 -s "https://localhost:8443/health" >/dev/null 2>&1; then
        print_status "HTTP/3 connectivity working"
    else
        print_warning "HTTP/3 not supported or failed (this is normal for local testing)"
    fi
    
    # Test load balancing
    print_info "Testing load balancing..."
    local instances=()
    for i in {1..10}; do
        response=$(curl -k --http2 -s "https://localhost:8443/api/status" 2>/dev/null)
        if [ $? -eq 0 ] && [ -n "$response" ]; then
            instance_id=$(echo "$response" | jq -r '.instance_id // .server_id // "unknown"' 2>/dev/null || echo "unknown")
            instances+=("$instance_id")
        fi
        sleep 0.1
    done
    
    unique_instances=$(printf '%s\n' "${instances[@]}" | sort -u | wc -l)
    if [ "$unique_instances" -gt 1 ]; then
        print_status "Load balancing working: $unique_instances backend instances detected"
    else
        print_warning "Load balancing may not be working properly"
    fi
}

# Show status and URLs
show_status() {
    echo
    print_info "Local Testing Environment Ready!"
    echo
    echo "🌐 Main endpoint: https://localhost:8443"
    echo "📊 Health check: https://localhost:8443/health"
    echo "📈 Status API:   https://localhost:8443/api/status"
    echo "🔧 Metrics:      https://localhost:8443/metrics"
    echo
    echo "📋 Backend instances:"
    echo "   - Instance 1: http://localhost:8080"
    echo "   - Instance 2: http://localhost:8081" 
    echo "   - Instance 3: http://localhost:8082"
    echo
    echo "📝 Log files:"
    echo "   - Caddy: /tmp/caddy.log"
    echo "   - Server 1: /tmp/quic-server-1.log"
    echo "   - Server 2: /tmp/quic-server-2.log"
    echo "   - Server 3: /tmp/quic-server-3.log"
    echo
    print_info "Test commands:"
    echo "   # Basic test:"
    echo "   curl -k https://localhost:8443/health"
    echo
    echo "   # Load balancing test:"
    echo "   ./test-connection-migration.sh localhost:8443"
    echo
    echo "   # Stop services:"
    echo "   ./local-test-setup.sh stop"
}

# Stop all services
stop_services() {
    print_info "Stopping all services..."
    
    # Stop Caddy
    if [ -f /tmp/caddy.pid ]; then
        kill $(cat /tmp/caddy.pid) 2>/dev/null || true
        rm -f /tmp/caddy.pid
    fi
    pkill caddy 2>/dev/null || true
    
    # Stop QUIC servers
    for i in {1..3}; do
        if [ -f "/tmp/quic-server-$i.pid" ]; then
            kill $(cat "/tmp/quic-server-$i.pid") 2>/dev/null || true
            rm -f "/tmp/quic-server-$i.pid"
        fi
    done
    pkill -f "quic-server" 2>/dev/null || true
    
    # Clean up log files
    rm -f /tmp/caddy.log /tmp/quic-server-*.log
    
    print_status "All services stopped"
}

# Main function
main() {
    case "${1:-start}" in
        "start")
            check_caddy
            build_server
            start_servers
            start_caddy
            test_setup
            show_status
            ;;
        "stop")
            stop_services
            ;;
        "restart")
            stop_services
            sleep 2
            main start
            ;;
        "status")
            if pgrep -f "caddy" >/dev/null && pgrep -f "quic-server" >/dev/null; then
                print_status "Services are running"
                show_status
            else
                print_warning "Services are not running"
            fi
            ;;
        *)
            echo "Usage: $0 {start|stop|restart|status}"
            exit 1
            ;;
    esac
}

# Trap for cleanup on exit
trap 'stop_services' EXIT

main "$@"
