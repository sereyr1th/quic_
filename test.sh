#!/bin/bash

# Quick test script for QUIC Load Balancer
# This script demonstrates the functionality without requiring a full Moodle setup

set -e

echo "🚀 QUIC/HTTP3 Load Balancer Test Script"
echo "======================================="

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

print_step() {
    echo -e "${BLUE}[STEP]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

# Check if binary exists
if [[ ! -f "./quic-server" ]]; then
    print_step "Building QUIC server..."
    go build -o quic-server .
    print_success "Binary built successfully"
fi

# Generate config if it doesn't exist
if [[ ! -f "./config.json" ]]; then
    print_step "Generating configuration..."
    ./quic-server --generate-config
    print_success "Configuration generated"
fi

print_step "Starting load balancer in background..."
./quic-server -config config.json &
SERVER_PID=$!

# Give server time to start
sleep 3

# Test function
test_endpoint() {
    local url=$1
    local description=$2
    
    print_step "Testing: $description"
    if curl -s --max-time 5 -k "$url" > /dev/null; then
        print_success "$description - OK"
        return 0
    else
        echo "❌ $description - FAILED"
        return 1
    fi
}

# Run tests
echo
print_step "Running connectivity tests..."
echo

test_endpoint "https://localhost:9443/health" "Load balancer health check"
test_endpoint "https://localhost:9443/stats" "Load balancer statistics"
test_endpoint "https://localhost:9443/api/test" "API test endpoint"
test_endpoint "http://localhost:8080/api/test" "HTTP/1.1 fallback"

echo
print_step "Testing HTTP/2 protocol..."
if command -v curl > /dev/null && curl --help | grep -q "http2"; then
    HTTP2_RESPONSE=$(curl -s --http2 -k https://localhost:9443/api/test)
    if echo "$HTTP2_RESPONSE" | grep -q "HTTP/2"; then
        print_success "HTTP/2 protocol working"
    else
        echo "❌ HTTP/2 protocol test failed"
    fi
else
    print_info "curl HTTP/2 support not available"
fi

echo
print_step "Fetching load balancer statistics..."
curl -s -k https://localhost:9443/stats | jq . 2>/dev/null || curl -s -k https://localhost:9443/stats

echo
echo
print_step "Fetching health status..."
curl -s -k https://localhost:9443/health | jq . 2>/dev/null || curl -s -k https://localhost:9443/health

echo
echo
print_success "All tests completed!"

echo
print_info "Load balancer is running on:"
echo "  📡 HTTPS/HTTP3: https://localhost:9443"
echo "  🌐 HTTP: http://localhost:8080"
echo "  📊 Statistics: https://localhost:9443/stats"
echo "  🏥 Health: https://localhost:9443/health"

echo
print_info "To configure for Moodle:"
echo "  1. Edit config.json to point to your Moodle servers"
echo "  2. Install the health check script (examples/moodle-healthcheck.php)"
echo "  3. Update Moodle's config.php with proxy settings"
echo "  4. Set up SSL certificates for production"

echo
echo "Press Enter to stop the server..."
read -r

# Clean up
print_step "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true
print_success "Server stopped"

echo
print_success "Test completed successfully! ✨"