#!/bin/bash

# Enhanced QUIC Connection Migration Testing Script
# This script creates various migration scenarios to test your implementation

echo "🧪 QUIC Connection Migration Stress Testing"
echo "=========================================="
echo ""

SERVER_URL="https://localhost:9443"
HTTP_URL="http://localhost:8080"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_test() {
    echo -e "${BLUE}🔧 Test: $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

# Function to test a single endpoint
test_endpoint() {
    local url=$1
    local protocol=$2
    local description=$3
    
    print_test "$description"
    
    if command -v curl &> /dev/null; then
        case $protocol in
            "http3")
                response=$(curl -s -k --max-time 5 --http3-only "$url" 2>/dev/null)
                ;;
            "http2")
                response=$(curl -s -k --max-time 5 --http2 "$url" 2>/dev/null)
                ;;
            "http1")
                response=$(curl -s --max-time 5 "$url" 2>/dev/null)
                ;;
        esac
        
        if [[ $? -eq 0 && -n "$response" ]]; then
            print_success "Response received via $protocol"
            echo "   $(echo "$response" | jq -r '.protocol // .message // .' 2>/dev/null || echo "$response" | head -c 100)"
        else
            print_error "Failed to connect via $protocol"
        fi
    else
        print_error "curl not found"
    fi
    echo ""
}

# Function to simulate multiple concurrent connections
simulate_concurrent_connections() {
    print_test "Simulating concurrent connections"
    
    for i in {1..5}; do
        (
            test_endpoint "$SERVER_URL/api/test?req=$i&client=concurrent" "http3" "Concurrent Request $i"
        ) &
    done
    
    wait
    print_success "Concurrent connections test completed"
    echo ""
}

# Function to simulate rapid requests (migration trigger)
simulate_rapid_requests() {
    print_test "Simulating rapid requests to trigger migration detection"
    
    for i in {1..10}; do
        print_test "Rapid request $i"
        test_endpoint "$SERVER_URL/api/test?req=$i&client=rapid" "http3" "Rapid Request $i"
        sleep 0.1
    done
    
    print_success "Rapid requests test completed"
    echo ""
}

# Function to test migration endpoints
test_migration_endpoints() {
    print_test "Testing migration-specific endpoints"
    
    test_endpoint "$SERVER_URL/api/connections" "http3" "Connection tracking endpoint"
    test_endpoint "$SERVER_URL/api/simulate-migration" "http3" "Migration simulation endpoint"
    
    # Test migration stats if available
    test_endpoint "$SERVER_URL/api/migration-stats" "http3" "Migration statistics endpoint"
    
    echo ""
}

# Function to compare protocols
compare_protocols() {
    print_test "Comparing protocols for the same endpoint"
    
    test_endpoint "$SERVER_URL/api/test?protocol=http3" "http3" "HTTP/3 Test"
    test_endpoint "$SERVER_URL/api/test?protocol=http2" "http2" "HTTP/2 Test"
    test_endpoint "$HTTP_URL/api/test?protocol=http1" "http1" "HTTP/1.1 Test"
    
    echo ""
}

# Function to test network simulation
simulate_network_conditions() {
    print_test "Simulating different network conditions"
    
    # Simulate network latency (if available)
    if command -v tc &> /dev/null; then
        print_warning "Network simulation requires root privileges"
        print_test "Would simulate: Low latency, High latency, Packet loss"
    else
        print_warning "tc (traffic control) not available for network simulation"
    fi
    
    # Instead, test with different request patterns
    print_test "Testing with different request patterns"
    
    # Small requests
    for i in {1..3}; do
        test_endpoint "$SERVER_URL/api/test?size=small&req=$i" "http3" "Small request $i"
    done
    
    # Large requests (simulate by adding query parameters)
    for i in {1..3}; do
        large_query="size=large&data=$(printf 'x%.0s' {1..100})&req=$i"
        test_endpoint "$SERVER_URL/api/test?$large_query" "http3" "Large request $i"
    done
    
    echo ""
}

# Function to test connection persistence
test_connection_persistence() {
    print_test "Testing connection persistence"
    
    print_test "Making multiple requests to test connection reuse"
    for i in {1..5}; do
        test_endpoint "$SERVER_URL/api/test?req=$i&test=persistence" "http3" "Persistence test $i"
        sleep 1
    done
    
    print_success "Connection persistence test completed"
    echo ""
}

# Function to check server status
check_server_status() {
    print_test "Checking server status"
    
    # Test if server is running
    if nc -z localhost 9443 2>/dev/null; then
        print_success "Server is listening on port 9443 (HTTPS/HTTP3)"
    else
        print_error "Server is not running on port 9443"
        echo "Please start the server first: ./quic-moodle"
        exit 1
    fi
    
    if nc -z localhost 8080 2>/dev/null; then
        print_success "Server is listening on port 8080 (HTTP)"
    else
        print_warning "HTTP server not running on port 8080"
    fi
    
    echo ""
}

# Function to analyze results
analyze_results() {
    print_test "Analyzing connection data"
    
    if command -v curl &> /dev/null && command -v jq &> /dev/null; then
        connections_data=$(curl -s -k --http3-only "$SERVER_URL/api/connections" 2>/dev/null)
        
        if [[ $? -eq 0 && -n "$connections_data" ]]; then
            echo "$connections_data" | jq . 2>/dev/null || echo "$connections_data"
            
            # Extract some statistics
            total_connections=$(echo "$connections_data" | jq -r '.total_count // 0' 2>/dev/null)
            print_success "Total tracked connections: $total_connections"
            
            migrations=$(echo "$connections_data" | jq -r '.connections | to_entries[] | .value.migration_count' 2>/dev/null | awk '{sum+=$1} END {print sum+0}')
            print_success "Total migrations detected: $migrations"
        else
            print_error "Could not retrieve connection data"
        fi
    else
        print_warning "jq not available for JSON parsing"
    fi
    
    echo ""
}

# Main execution
main() {
    echo "Starting QUIC Connection Migration Testing..."
    echo "Timestamp: $(date)"
    echo ""
    
    check_server_status
    
    print_test "Running comprehensive migration tests"
    echo ""
    
    compare_protocols
    test_migration_endpoints
    simulate_concurrent_connections
    test_connection_persistence
    simulate_rapid_requests
    simulate_network_conditions
    
    analyze_results
    
    echo "================================="
    echo "🏁 Testing completed!"
    echo ""
    echo "📊 To view detailed connection information:"
    echo "   curl -k --http3-only $SERVER_URL/api/connections | jq ."
    echo ""
    echo "🔄 To simulate a migration:"
    echo "   1. Make some requests via HTTP/3"
    echo "   2. Change your network (WiFi to Ethernet, different WiFi, etc.)"
    echo "   3. Make more requests"
    echo "   4. Check the migration events in the connection data"
    echo ""
    echo "🧪 Real-world migration testing:"
    echo "   - Use mobile device switching between WiFi and cellular"
    echo "   - Use laptop switching between WiFi networks"
    echo "   - Use VPN connection changes"
    echo ""
}

# Run the tests
main
